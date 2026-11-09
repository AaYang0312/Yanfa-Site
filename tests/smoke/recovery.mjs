// V06 owns its tagged binary and four independently initialized instances. No
// fault route exists; private filesystem barriers synchronize actual SIGKILL.
import assert from 'node:assert/strict';
import {readFile,writeFile,mkdir,mkdtemp,rm,lstat} from 'node:fs/promises';
import {resolve,join,dirname} from 'node:path';
import {parseArgs} from 'node:util';
import {setTimeout as delay} from 'node:timers/promises';
import {randomBytes} from 'node:crypto';
import sharp from 'sharp';
import {passwordFile,initialize,command,startServer,stopServer,live,client,editable,hash,snapshotFiles,assertOldBaselineRejected} from './publication.mjs';

const {values}=parseArgs({options:{go:{type:'string'},root:{type:'string'},'task-number':{type:'string'},out:{type:'string'}}});
assert.ok(values.go&&values.root&&values['task-number']&&values.out,'--go --root --task-number --out required');
const root=resolve(values.root),out=resolve(values.out),taskNumber=Number(values['task-number']);assert.equal(process.cwd(),root,'cwd must be the dispatched checkout');assert.ok(Number.isInteger(taskNumber)&&taskNumber>0);
await mkdir(out,{recursive:true});await mkdir(join(root,'.work/bin'),{recursive:true});
const password=await passwordFile(),binary=join(root,'.work/bin/yuanfa-integration'),base='http://127.0.0.1:'+(19000+taskNumber);
const report={success:false,commands:[],scenarios:[]};
const env={...process.env,GOTOOLCHAIN:'local',GOPROXY:'https://goproxy.cn',GOSUMDB:'sum.golang.org'};for(const key of ['HTTP_PROXY','HTTPS_PROXY','ALL_PROXY','http_proxy','https_proxy','all_proxy','npm_config_proxy','npm_config_https_proxy'])delete env[key];
let owned,privateRoot;
let rpcID=1;
async function draftTool(secret,name,arguments_){
 const id=rpcID++,response=await fetch(base+'/mcp',{method:'POST',headers:{Authorization:'Bearer '+secret,'Content-Type':'application/json',Accept:'application/json, text/event-stream','MCP-Protocol-Version':'2025-03-26'},body:JSON.stringify({jsonrpc:'2.0',id,method:'tools/call',params:{name,arguments:arguments_}})});
 assert.equal(response.status,200,'isolated draft MCP transport');const text=await response.text();const message=response.headers.get('content-type')?.includes('text/event-stream')?text.split(/\r?\n/).filter(line=>line.startsWith('data:')).map(line=>JSON.parse(line.slice(5))).find(item=>item.id===id):JSON.parse(text);
 assert.ok(message&&!message.error,'isolated draft MCP protocol');assert.ok(!message.result.isError,JSON.stringify(message.result.structuredContent));assert.ok(message.result.structuredContent);return message.result.structuredContent;
}
try{
 assert.equal((await command(values.go,['build','-tags=integration','-o','.work/bin/yuanfa-integration','./backend/cmd/yuanfa'],env,report)).exit_code,0,'build actual tagged binary');
 const roleRoot=dirname(dirname(resolve(process.env.DATA_DIR)));const integration=join(roleRoot,'integration');await mkdir(integration,{recursive:true,mode:0o700});
 for(const [index,barrier]of ['before-active-temp','after-temp-fsync-before-rename','after-rename-before-memory/DB','after-memory-before-DB'].entries()){
  privateRoot=await mkdtemp(join(integration,'v06-'+index+'-'));const data=join(privateRoot,'data'),control=join(privateRoot,'control');await mkdir(data,{mode:0o700});await mkdir(control,{mode:0o700});
  const evidence=join(out,'boundary-'+index);await mkdir(evidence,{recursive:true});const scenario={barrier,commands:[],http:[],checks:[],data_mode:((await lstat(data)).mode&0o777).toString(8),control_mode:((await lstat(control)).mode&0o777).toString(8)};report.scenarios.push(scenario);
  const childEnv={...env,DATA_DIR:data,HTTP_ADDR:new URL(base).host,SITE_URL:base,QA_URL:base,TEMPLATE_DIR:root,ADMIN_STATIC_DIR:join(root,'admin/dist'),SITE_CONFIG_PATH:join(root,'src/generated/site-config.json'),YUANFA_INTEGRATION_DIR:control};
  await writeFile(join(control,'scenario.json'),'{}',{mode:0o600});await initialize(binary,childEnv,scenario);
  owned=startServer(binary,childEnv,scenario);await live(base,owned);let api=client(base,password,scenario);await api.login();const baseline=await api.waitCandidate((await api.freeze()).id);assert.equal(baseline.state,'ready');await api.publish(baseline);
  const originalAuthority=JSON.parse(await readFile(join(data,'state/active.json'),'utf8'));
  const oldManifest=JSON.parse(await readFile(join(data,'artifacts',baseline.id,'manifest.json'),'utf8'));const oldImagePath='/'+oldManifest.resources.find(r=>r.path.startsWith('media/')).path;const oldImage=await api.expect(oldImagePath,{anonymous:true},200,'baseline successful image');
  const tag=randomBytes(6).toString('hex');const bytes=await sharp({create:{width:32,height:24,channels:3,background:'#'+tag.slice(0,6)}}).png().toBuffer();const form=new FormData();form.append('file',new Blob([bytes]),'crash-boundary.png');const image=(await api.expect('/api/admin/media',{method:'POST',raw:form},201,'upload new exclusive image')).json;
  // This scenario tests explicit publication recovery. Browser writes now
  // enqueue automatic updates; use real MCP drafts so no unrelated automatic
  // activation competes with the exact crash boundary under test.
  const issued=(await api.expect('/api/admin/mcp-tokens',{method:'POST',body:{name:'isolated recovery drafts',expires_at:new Date(Date.now()+3600000).toISOString(),scopes:['products:read','products:write']}},201,'issue isolated draft credential')).json;
  const seed=(await api.request('/api/admin/products/p_f30m')).json.product;const draft={slug:'v06-crash-'+tag,...editable(seed),model:'CRASH-'+tag,name:'Crash boundary fixture',visibility:'unlisted',images:[{media_id:image.media_id,label:'boundary',description:''}],cover_media_id:image.media_id};let p=(await draftTool(issued.secret,'create_product',draft)).product;const edit=editable(p);edit.visibility='listed';p=(await draftTool(issued.secret,'update_product',{product_id:p.id,expected_version:p.version,edit})).product;
  await api.expect('/api/admin/mcp-tokens/'+issued.token.id,{method:'DELETE'},204,'revoke isolated draft credential');
  assert.equal((await api.expect('/api/admin/site-updates',{},200,'draft has no automatic confirmation')).json.items.find(item=>item.product_id===p.id)?.state,'draft');
  const candidate=await api.waitCandidate((await api.freeze()).id);assert.equal(candidate.state,'ready');const pending=await api.intent(candidate);const request=await api.intent(candidate);const confirmationKey='crash-confirm-'+tag;
  await writeFile(join(control,'scenario.json'),JSON.stringify({barrier}),{mode:0o600});await api.confirm(request,confirmationKey);
  const originalConfirmationAPI=api;
  let reached;for(let n=0;n<600;n++){assert.ok(owned.exitCode===null&&owned.signalCode===null);try{reached=JSON.parse(await readFile(join(control,'reached.json'),'utf8'));if(reached.barrier===barrier)break}catch{}await delay(20)}assert.equal(reached?.barrier,barrier,'exact filesystem barrier reached');assert.equal(reached.pid,owned.pid);
  const atBarrier=await readFile(join(data,'state/active.json'));const expected=index<2?baseline.id:candidate.id;assert.equal(JSON.parse(atBarrier).release_id,expected,'durable pointer at crash boundary');
  await writeFile(join(evidence,'reached.json'),JSON.stringify(reached,null,2));await writeFile(join(evidence,'active-at-crash.json'),atBarrier);
  await stopServer(owned,'SIGKILL');owned=undefined;await writeFile(join(control,'scenario.json'),'{}',{mode:0o600});
  // A second independently started serve obtains the OS-released lifetime lock.
  owned=startServer(binary,childEnv,scenario);await live(base,owned);api=client(base,password,scenario);await api.login();
  const recovered=(await api.expect('/api/admin/publication-requests/'+request.id,{},200,'recovered publication record')).json;assert.equal(recovered.state,index<2?'failed':'succeeded');if(index<2){assert.equal(recovered.error.code,'INTERRUPTED');assert.equal(recovered.result_release_id,null)}else assert.equal(recovered.result_release_id,candidate.id);
  const health=(await api.expect('/health/ready',{anonymous:true},200,'verified active restored')).json;assert.equal(health.release_id,expected);const page=await api.expect('/',{anonymous:true},200,'restored active homepage');assert.ok(page.bytes.includes(Buffer.from('name="release-id" content="'+expected+'"')));assert.ok((await api.expect(oldImagePath,{anonymous:true},200,'historical hash remains public')).bytes.equals(oldImage.bytes));
  const newImage=await api.expect('/media/'+image.sha256+'.png',{anonymous:true},index<2?404:200,'unswitched artifact remains private; switched artifact public');if(index>=2)assert.ok(newImage.bytes.equals(bytes));
  await api.expect('/products/'+p.slug+'/',{anonymous:true},index<2?404:200,'new route follows durable pointer');assert.deepEqual((await api.request('/api/admin/products/'+p.id)).json.product,p,'workspace preserved through crash');
  const after=await readFile(join(data,'state/active.json'));assert.ok(after.equals(atBarrier),'recovery must reconcile DB, never re-activate or rewrite pointer');
  await delay(250);assert.equal((await api.request('/api/admin/publication-requests/'+request.id)).json.state,recovered.state,'interrupted operation never automatically retried');
  const before=await snapshotFiles(data);const locked=await command(binary,['migrate'],childEnv,scenario);assert.equal(locked.exit_code,73);assert.deepEqual(await snapshotFiles(data),before);scenario.checks.push('lifecycle flock excludes other writer');
  scenario.release_id=expected;scenario.baseline_release_id=baseline.id;scenario.candidate_id=candidate.id;scenario.request_id=request.id;scenario.recovered_state=recovered.state;scenario.active_sha256=hash(after);scenario.snapshot_sha256=JSON.parse(after).snapshot_sha256;scenario.old_image_sha256=hash(oldImage.bytes);scenario.new_image_status=newImage.status;scenario.checks.push('active/task/page/new route/private artifact/old hash/workspace agree after SIGKILL and restart');
  await writeFile(join(evidence,'active-after-recovery.json'),after);await writeFile(join(evidence,'recovered-request.json'),JSON.stringify(recovered,null,2));
  const pendingAfterRecovery=(await api.expect('/api/admin/publication-requests/'+pending.id,{},200,'pending baseline after crash recovery')).json;assert.equal(pendingAfterRecovery.state,index<2?'awaiting_confirmation':'stale');
  let successful=recovered,successfulKey=confirmationKey,successfulAPI=index<2?api:originalConfirmationAPI;
  if(index<2){const replacement=await api.intent(candidate);successfulKey='replacement-confirm-'+tag;await api.confirm(replacement,successfulKey);successful=await api.waitRequest(replacement.id);assert.equal(successful.state,'succeeded')}
  await api.publish(baseline,'rollback');const roundTripBytes=await readFile(join(data,'state/active.json'));const roundTrip=JSON.parse(roundTripBytes);assert.equal(roundTrip.release_id,baseline.id);assert.notEqual(roundTrip.operation_id,originalAuthority.operation_id);
  scenario.aba={pending_state_after_recovery:pendingAfterRecovery.state,original_operation_id:originalAuthority.operation_id,round_trip_operation_id:roundTrip.operation_id,before_restart:await assertOldBaselineRejected(api,candidate,pending,successful,successfulKey,'boundary-'+index+'-before-restart',successfulAPI)};
  assert.ok((await readFile(join(data,'state/active.json'))).equals(roundTripBytes));await stopServer(owned);owned=undefined;
  owned=startServer(binary,childEnv,scenario);await live(base,owned);await api.session();scenario.aba.after_restart=await assertOldBaselineRejected(api,candidate,pending,successful,successfulKey,'boundary-'+index+'-after-restart',successfulAPI);assert.ok((await readFile(join(data,'state/active.json'))).equals(roundTripBytes));scenario.checks.push('ABA baseline never revives after crash recovery or normal restart; successful same-key replay preserves authority');
  await writeFile(join(evidence,'active-after-aba.json'),roundTripBytes);await stopServer(owned);owned=undefined;await rm(privateRoot,{recursive:true,force:true});privateRoot=undefined;scenario.cleaned=true;console.log('PASS '+barrier+' -> '+recovered.state+' / '+expected+'; ABA rejected across restart');
 }
 report.success=true;
}catch(error){report.error=error.stack;throw error}finally{try{await stopServer(owned)}finally{try{if(privateRoot)await rm(privateRoot,{recursive:true,force:true,maxRetries:5,retryDelay:100})}finally{await writeFile(join(out,'recovery-smoke.json'),JSON.stringify(report,null,2))}}}
