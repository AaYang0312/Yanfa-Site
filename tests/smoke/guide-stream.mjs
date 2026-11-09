// V12 executes actual Go/Eino/public-tool/HTTP encoders with isolated tagged
// adapters. Customer model, proxy and useChat UI acceptance remain T19/T20/T14.
import assert from 'node:assert/strict';
import {mkdir,mkdtemp,readFile,writeFile,rm} from 'node:fs/promises';
import {join,resolve} from 'node:path';
import {parseArgs} from 'node:util';
import {createGuideHarness,setupGuideFixture,waitGuide} from '../integration/guide/harness.mjs';
import {decodeContract} from '../../src/generated/validate-contract.mjs';
import {DefaultChatTransport,readUIMessageStream} from 'ai';
import {editable,command,initialize,startServer,stopServer,live} from './publication.mjs';

const {values}=parseArgs({options:{go:{type:'string'},root:{type:'string'},'task-number':{type:'string'},out:{type:'string'},scenario:{type:'string',multiple:true}}});
assert.ok(values.go&&values.root&&values['task-number']&&values.out,'--go --root --task-number --out required');
const selectedScenarios=new Set(values.scenario??[]);
const out=resolve(values.out);await mkdir(out,{recursive:true});
const report={success:false,scope:'Integration-only controlled Eino execution, not a customer provider',scenarios:[],results:[],checks:[]};
const harness=await createGuideHarness({go:values.go,root:values.root,taskNumber:Number(values['task-number']),out:join(out,'processes'),withAdmin:false});
const endpoint='/api/guide/messages';
const pass=label=>{report.checks.push(label);console.log('PASS '+label)};
const configure=async(ctx,scenario)=>writeFile(join(ctx.controlDir,'scenario.json'),JSON.stringify(scenario),{mode:0o600});
const proposal=(requirements,{intent='consult',ids=['p_f30m'],id=null,reference=null}={})=>({intent,requirements,candidate_product_ids:ids,selected_product_id:id,selection_reference:reference});
async function request(ctx,body,{stream=false,status=200,signal,headers={}}={}){
 const response=await fetch(ctx.baseURL+endpoint,{method:'POST',headers:{'Content-Type':'application/json',Accept:stream?'text/event-stream':'application/json',...headers},body:JSON.stringify(body),signal});
 const raw=await response.text();assert.equal(response.status,status,raw);assert.equal(response.headers.get('cache-control'),'no-store');
 if(status!==200||!stream||body.selected_product_id!==undefined){assert.match(response.headers.get('content-type'),/^application\/json/);const result=status===200||status===503?decodeContract('guide.schema.json#/$defs/GuideResult',raw):JSON.parse(raw);report.results.push({status,stream:false,raw,result});return {result,raw,response}}
 assert.match(response.headers.get('content-type'),/^text\/event-stream/);assert.equal(response.headers.get('x-vercel-ai-ui-message-stream'),'v1');
 const frames=raw.trim().split('\n\n').map(frame=>{assert.ok(frame.startsWith('data: '));const value=frame.slice(6);return value==='[DONE]'?value:JSON.parse(value)});
 const data=frames.filter(f=>f.type==='data-guide-result');
 let result;if(data.length===1){result=decodeContract('guide.schema.json#/$defs/GuideResult',JSON.stringify(data[0].data));if(result.state!=='no_match')assert.deepEqual(result.conflicts,[])}
 report.results.push({status,stream:true,raw,result});
 return {result,raw,frames,response};
}
function complete(stream){
 assert.deepEqual(stream.frames.map(f=>f==='[DONE]'?f:f.type),['start','text-start','text-delta','text-end','data-guide-result','finish','[DONE]']);
 assert.ok(stream.frames[0].messageId);assert.equal(stream.frames[1].id,stream.frames[2].id);assert.equal(stream.frames[2].id,stream.frames[3].id);
 if(stream.result.state==='no_match')for(const reason of stream.result.conflicts)assert.ok(stream.frames[2].delta.includes(reason),'visible text must include deterministic cause');
}
async function both(ctx,body){const json=await request(ctx,body);const stream=await request(ctx,body,{stream:true});complete(stream);assert.deepEqual(stream.result,json.result,'same controlled Go business result must survive JSON and SSE');return stream.result}
async function scenario(name,callback){
 if(selectedScenarios.size&&!selectedScenarios.has(name))return;
 const entry={name,http:[]};report.scenarios.push(entry);
 await harness.runScenario(name,async ctx=>{
  const fixture=await setupGuideFixture(ctx.baseURL,entry);
  if(name==='deadline-45-seconds'||name==='cancel-block'||name==='cancel-tool-block'){
   // Guide-review creates a newer draft that hides the older confirmation's
   // pending state. Confirm this final reviewed version, then await that exact
   // version; merely checking for no pending items can race a publication.
   fixture.product=await fixture.save(fixture.product,editable(fixture.product));
   await waitGuide(async()=>{
    const updates=(await fixture.api.expect('/api/admin/site-updates',{},200,'wait for fixture publication')).json.items;
    const update=updates.find(item=>item.product_id===fixture.product.id);
    assert.ok(update?.state!=='failed','fixture publication failed');
    return update?.version===fixture.product.version&&update.state==='succeeded';
   },'fixture automatic publication settled',180000);
   fixture.candidate.id=(await fixture.api.expect('/api/catalog/release',{anonymous:true},200,'capture settled fixture release')).json.release_id;
   entry.settled_release_id=fixture.candidate.id;
  }
  await callback(ctx,fixture,entry);entry.stats=await ctx.readStats();
 });
}
async function reached(ctx,name){return waitGuide(async()=>{try{const s=JSON.parse(await readFile(join(ctx.controlDir,'reached.json'),'utf8'));return s.barrier===name&&s}catch(error){if(error.code!=='ENOENT')throw error}},name)}
async function productionNoModel(){
 const root=resolve(values.root),binary=join(root,'.work/bin/yuanfa'),entry={commands:[],http:[]};report.production=entry;
 const env={...process.env,GOTOOLCHAIN:'local',GOPROXY:'https://goproxy.cn',GOSUMDB:'sum.golang.org'};
 for(const key of Object.keys(env))if(/^MODEL_/i.test(key)||['HTTP_PROXY','HTTPS_PROXY','ALL_PROXY','http_proxy','https_proxy','all_proxy','npm_config_proxy','npm_config_https_proxy'].includes(key))delete env[key];
 assert.equal((await command(values.go,['build','-o',binary,'./backend/cmd/yuanfa'],env,entry)).exit_code,0);
 const work=await mkdtemp(join(root,'.work',process.env.RUN_ROLE||'worker','normal-v12-')),data=join(work,'data'),control=join(work,'control');
 await mkdir(data,{mode:0o700});await mkdir(control,{mode:0o700});await writeFile(join(control,'scenario.json'),JSON.stringify({model_error:'loop'}),{mode:0o600});
 const baseURL='http://127.0.0.1:'+(18000+Number(values['task-number']));
 const childEnv={...env,DATA_DIR:data,HTTP_ADDR:new URL(baseURL).host,SITE_URL:baseURL,QA_URL:baseURL,TEMPLATE_DIR:root,SITE_CONFIG_PATH:join(root,'src/generated/site-config.json'),YUANFA_INTEGRATION_DIR:control};
 let owned;
 try{
  await initialize(binary,childEnv,entry);owned=startServer(binary,childEnv,entry);await live(baseURL,owned);
  const f=await setupGuideFixture(baseURL,entry),ctx={baseURL};
  const failed=await request(ctx,{release_id:f.candidate.id,message:'生产没有客户模型仍需明确不可用'},{stream:true,status:503});assert.equal(failed.result.state,'unavailable');assert.deepEqual(failed.result.conflicts,[]);
  const selected=await request(ctx,{release_id:f.candidate.id,selected_product_id:'p_f30m',requirements:f.fixture.requirements},{stream:true});assert.equal(selected.result.state,'selected');assert.equal(selected.result.selection.purchase_status,'available');
  entry.model_configuration='all MODEL_* absent';entry.integration_control_ignored=true;entry.release_id=f.candidate.id;entry.success=true;
 }finally{try{await stopServer(owned)}finally{await rm(work,{recursive:true,force:true});entry.cleaned=true}}
 pass('actual production build ignores integration scenario; absent customer model returns503 while deterministic selection remains available');
}
const noPurchase=result=>{assert.equal(result.selection,null);assert.deepEqual(result.recommendations,[]);assert.equal(result.question,null)};
try{
 await scenario('five-choice-boundaries',async(ctx,f)=>{
  const body={release_id:f.candidate.id,requirements:f.fixture.requirements,message:'我就选F30M'};
  await configure(ctx,{model_proposal:proposal(body.requirements,{intent:'select',id:'p_f30m',reference:'F30M',ids:[]}),get_product_id:'p_f30m'});
  const selected=await both(ctx,body);assert.equal(selected.state,'selected');assert.equal(selected.selection.match_level,'needs_confirmation');assert.deepEqual(selected.missing_fields,['air_supply']);assert.deepEqual(selected.selection.missing_fields,selected.missing_fields);assert.equal(selected.selection.purchase_status,'available');assert.equal(selected.confirmed_requirements.air_supply,null);
  const publicProduct=(await f.api.expect('/api/catalog/products/p_f30m?release_id='+body.release_id,{anonymous:true},200,'same public detail')).json.product;assert.deepEqual(publicProduct.purchase_options,selected.selection.purchase_options);assert.equal(selected.selection.model,publicProduct.model);
  assert.ok(selected.selection.matched_facts.some(fact=>fact.sources.some(source=>source.public_label.includes('\n第二行 {data}'))),'Chinese multiline source survives Go JSON');
  let stats=await ctx.readStats();assert.equal(stats.agent_runs,2);assert.equal(stats.model_calls,4);assert.equal(stats.tool_calls,4);assert.equal(stats.select_calls,2);
  await configure(ctx,{model_proposal:proposal(body.requirements),get_product_id:'p_f30m'});
  const inquiry=await both(ctx,{...body,message:'F30M适合什么材料？'});assert.equal(inquiry.selection,null);assert.ok(inquiry.recommendations.some(r=>r.product_id==='p_f30m'));assert.deepEqual(inquiry.conflicts,[]);
  await configure(ctx,{model_proposal:proposal(body.requirements,{intent:'select',id:'p_f30m',reference:'第二款',ids:[]})});
  const ambiguous=await both(ctx,{...body,message:'就第二款',context_product_id:'p_f30m'});assert.equal(ambiguous.state,'clarifying');assert.equal(ambiguous.selection,null);assert.equal(ambiguous.question.field,null);assert.equal(ambiguous.question.text,'请点击要选择的商品卡片');
  const merged={...body.requirements,air_supply:false};await configure(ctx,{model_proposal:proposal(merged,{intent:'select',id:'p_f30m',reference:'F30M',ids:[]})});
  const corrected=await both(ctx,{...body,message:'我就选F30M，刚才说错了，现场没有气源'});assert.equal(corrected.state,'no_match');assert.deepEqual(corrected.confirmed_requirements,merged);noPurchase(corrected);assert.ok(corrected.conflicts.some(x=>x.includes('压缩空气')&&x.includes('没有可用气源')));
  stats=await ctx.readStats();assert.equal(stats.agent_runs,8);assert.equal(stats.model_calls,16);assert.equal(stats.select_calls,4);assert.equal(stats.active_model_calls,0);
  pass('explicit model reference, inquiry, ordinal ambiguity and same-turn air=false correction; one Agent run per text request');
 });
 await scenario('no-model-and-sdk',async(ctx,f)=>{
  const body={release_id:f.candidate.id,message:'请帮忙选工具',requirements:f.fixture.requirements};
  const unavailable=await request(ctx,body,{stream:true,status:503});assert.equal(unavailable.result.state,'unavailable');assert.deepEqual(unavailable.result.conflicts,[]);
  const id=await request(ctx,{release_id:body.release_id,selected_product_id:'p_f30m',requirements:body.requirements},{stream:true});assert.equal(id.result.state,'selected');assert.deepEqual(await ctx.readStats(),{agent_runs:0,model_calls:0,tool_calls:0,select_calls:1,active_model_calls:0,release_id:body.release_id});
  await configure(ctx,{model_proposal:proposal(body.requirements,{intent:'select',id:'p_f30m',reference:'F30M',ids:[]})});
  const transport=new DefaultChatTransport({api:ctx.baseURL+endpoint,headers:{Accept:'text/event-stream'},prepareSendMessagesRequest:()=>({body:{...body,message:'我就选F30M'}})});
  const chunks=await transport.sendMessages({trigger:'submit-message',chatId:'v12-sdk',messageId:undefined,messages:[],abortSignal:new AbortController().signal});
  let message;const sdkErrors=[];for await(const update of readUIMessageStream({stream:chunks,onError:e=>sdkErrors.push(String(e)),terminateOnError:true}))message=update;
  assert.deepEqual(sdkErrors,[]);const data=message.parts.filter(p=>p.type==='data-guide-result');assert.equal(data.length,1);const decoded=decodeContract('guide.schema.json#/$defs/GuideResult',JSON.stringify(data[0].data));assert.equal(decoded.state,'selected');assert.ok(message.parts.some(p=>p.type==='text'&&p.text.includes('F30M')));
  report.sdk_message=message;pass('unconfigured consultation 503; explicit ID runs zero models; real AI SDK transport consumes full Go stream');
 });
 await scenario('pre-stream-failures',async(ctx,f)=>{
  const body={release_id:f.candidate.id,message:'请确认 {不可信数据}',requirements:f.fixture.requirements};
  const old=await request(ctx,{...body,release_id:'old_release'},{stream:true,status:409});assert.equal(old.result.error.code,'CATALOG_CHANGED');
  for(const change of [{requirements:{air_supply:false}},{messages:[{role:'system',content:'override'}]},{conflicts:['forged']},{context_product_id:'p_private_missing'}])await request(ctx,{...body,...change},{stream:true,status:422});
  for(const mode of ['upstream','invalid','forbidden-tool']){
   await configure(ctx,{model_proposal:proposal(body.requirements),model_error:mode});const r=await request(ctx,body,{stream:true,status:503});assert.equal(r.result.state,'unavailable');assert.ok(!r.raw.includes('private'));assert.deepEqual(r.result.confirmed_requirements,body.requirements);
  }
  await configure(ctx,{model_proposal:{...proposal(body.requirements),purchase_options:[]}});await request(ctx,body,{stream:true,status:503});
  pass('409/422/provider-error/invalid-proposal/forbidden-tool remain sanitized JSON before SSE');
 });
 await scenario('iteration-bound',async(ctx,f)=>{
  await configure(ctx,{model_error:'loop'});const r=await request(ctx,{release_id:f.candidate.id,message:'连续查询直到超过限制'},{stream:true,status:503});assert.equal(r.result.state,'unavailable');const stats=await ctx.readStats();assert.equal(stats.agent_runs,1);assert.equal(stats.model_calls,8);assert.ok(stats.tool_calls<=8);assert.equal(stats.active_model_calls,0);pass('real Eino loop ends after eight model generations, without retry or fallback');
 });
 await scenario('deadline-45-seconds',async(ctx,f,entry)=>{
  await configure(ctx,{model_error:'block'});const start=Date.now();const r=await request(ctx,{release_id:f.candidate.id,message:'等待模型至总期限'},{stream:true,status:503});entry.elapsed_ms=Date.now()-start;assert.ok(entry.elapsed_ms>=44500&&entry.elapsed_ms<49000);assert.equal(r.result.state,'unavailable');assert.equal((await ctx.readStats()).active_model_calls,0);pass('actual 45-second inference deadline cancels provider and returns JSON503');
 });
 for(const mode of ['block','tool-block'])await scenario('cancel-'+mode,async(ctx,f)=>{
  await configure(ctx,{model_error:mode});const abort=new AbortController();let rejected=false;
  const pending=request(ctx,{release_id:f.candidate.id,message:'取消正在运行的咨询'},{stream:true,signal:abort.signal}).catch(e=>{rejected=true;assert.ok(e.name==='AbortError'||e.name==='TypeError')});
  await reached(ctx,mode==='block'?'model':'tool');abort.abort();await pending;assert.ok(rejected);
  await waitGuide(async()=>(await ctx.readStats()).active_model_calls===0,'cancelled model drained');
  await configure(ctx,{model_proposal:proposal(f.fixture.requirements)});const r=await request(ctx,{release_id:f.candidate.id,message:'取消后重新发起',requirements:f.fixture.requirements},{stream:true});complete(r);assert.equal((await ctx.readStats()).agent_runs,2);pass(mode+' cancellation reaches work and permits a fresh inference');
 });
 for(const mode of ['block','tool-block'])await scenario('concurrency-four-'+mode,async(ctx,f)=>{
  await configure(ctx,{model_error:mode});const aborts=Array.from({length:4},()=>new AbortController());
  const pending=aborts.map(a=>request(ctx,{release_id:f.candidate.id,message:'占用一个推理名额'},{stream:true,signal:a.signal}).catch(e=>{assert.ok(e.name==='AbortError'||e.name==='TypeError')}));
  try{
   await waitGuide(async()=>{const s=await ctx.readStats();return mode==='block'?s.active_model_calls===4:s.tool_calls===4},'four active model/tool executions');
   await request(ctx,{release_id:f.candidate.id,message:'第五次请求'},{stream:true,status:429});
   const selected=await request(ctx,{release_id:f.candidate.id,selected_product_id:'p_f30m',requirements:f.fixture.requirements});assert.equal(selected.result.state,'selected');assert.equal((await ctx.readStats()).model_calls,4);
  }finally{aborts.forEach(a=>a.abort());await Promise.all(pending)}
  await waitGuide(async()=>(await ctx.readStats()).active_model_calls===0,'cancelled model calls drained');
  await configure(ctx,{model_error:'block'});const probes=Array.from({length:4},()=>new AbortController());
  const active=probes.map(a=>request(ctx,{release_id:f.candidate.id,message:'验证所有四个名额均可重新占用'},{stream:true,signal:a.signal}).catch(e=>{assert.ok(e.name==='AbortError'||e.name==='TypeError')}));
  try{await waitGuide(async()=>(await ctx.readStats()).active_model_calls===4,'all four cancelled slots reusable')}finally{probes.forEach(a=>a.abort());await Promise.all(active)}
  await waitGuide(async()=>(await ctx.readStats()).active_model_calls===0,'four capacity probes drained');
  assert.equal((await ctx.readStats()).agent_runs,8);pass('four '+mode+' executions, fifth JSON429, model-free explicit selection and all four cancelled slots reusable');
 });
 await scenario('source-ip-budget',async(ctx,f)=>{
  const body={release_id:f.candidate.id,selected_product_id:'p_f30m',requirements:f.fixture.requirements};
  for(let i=0;i<10;i++)assert.equal((await request(ctx,body,{headers:{'X-Forwarded-For':'192.0.2.'+(i+1)}})).result.state,'selected');
  const denied=await request(ctx,body,{stream:true,status:429,headers:{'X-Forwarded-For':'203.0.113.1'}});assert.equal(denied.result.error.code,'RATE_LIMITED');assert.equal((await ctx.readStats()).model_calls,0);pass('ten per source IP; untrusted forwarded addresses cannot evade budget');
 });
 for(const fault of ['error','disconnect','missing-finish','missing-done'])await scenario('stream-'+fault,async(ctx,f)=>{
  await configure(ctx,{model_proposal:proposal(f.fixture.requirements),stream_fault:fault});const stream=await request(ctx,{release_id:f.candidate.id,message:'输出完整商品资料',requirements:f.fixture.requirements},{stream:true});
  assert.equal(stream.frames.filter(e=>e.type==='data-guide-result').length,1);assert.ok(!stream.raw.includes('private'));assert.ok(!stream.frames.includes('[DONE]'));
  if(fault==='error'){assert.ok(stream.frames.some(e=>e.type==='error'));assert.ok(!stream.frames.some(e=>e.type==='finish'))}else if(fault==='missing-done')assert.ok(stream.frames.some(e=>e.type==='finish'));else assert.ok(!stream.frames.some(e=>e.type==='finish'));
  pass('stream '+fault+' stays incomplete and never produces a complete commit sequence');
 });
 await scenario('release-before-response',async(ctx,f)=>{
  let p=await f.get('p_f30m'),edit=editable(p);edit.summary+=' T12 release switch';await f.save(p,edit);const next=await f.api.waitCandidate((await f.api.freeze()).id);assert.equal(next.state,'ready');
  await configure(ctx,{model_proposal:proposal(f.fixture.requirements,{intent:'select',id:'p_f30m',reference:'F30M',ids:[]}),pause_before_result:true});
  let received=false;const pending=request(ctx,{release_id:f.candidate.id,message:'我就选F30M',requirements:f.fixture.requirements},{stream:true,status:409}).then(r=>{received=true;return r});
  await reached(ctx,'before-result');assert.equal(received,false,'no early HTTP200/tokens');await f.api.publish(next);await ctx.releaseBarrier();const stale=await pending;assert.equal(stale.result.error.code,'CATALOG_CHANGED');assert.notEqual(next.id,f.candidate.id);pass('release activation during parsed selection proceeds without inference lock; BeginResponse rejects old result409');
 });
 await scenario('release-during-stream',async(ctx,f)=>{
  let p=await f.get('p_f30m'),edit=editable(p);edit.summary+=' T12 stream switch';await f.save(p,edit);const next=await f.api.waitCandidate((await f.api.freeze()).id);assert.equal(next.state,'ready');
  await configure(ctx,{model_proposal:proposal(f.fixture.requirements),stream_fault:'pause'});
  const pending=request(ctx,{release_id:f.candidate.id,message:'完整回复与版本检查',requirements:f.fixture.requirements},{stream:true});await reached(ctx,'stream');
  await f.api.publish(next);await ctx.releaseBarrier();const stream=await pending;complete(stream);assert.equal(stream.result.release_id,f.candidate.id);const current=(await f.api.expect('/api/catalog/release',{anonymous:true},200,'current release after complete stream')).json;assert.equal(current.release_id,next.id);assert.notEqual(current.release_id,stream.result.release_id);pass('stream does not hold activation lock and retains original release; current-release consumer check rejects old cards');
 });
 await scenario('catalogue-evidence',async(ctx,f)=>{
  // Remove all other public products through management HTTP, never through a
  // copied database, fake export or modified shared matching implementation.
  const products=(await f.api.expect('/api/admin/products?limit=100',{},200,'all workspace products')).json.items;
  for(const item of products)if(item.id!=='p_f30m'&&item.visibility==='listed'){const p=await f.get(item.id),edit=editable(p);edit.visibility='unlisted';await f.save(p,edit)}
  let p=await f.get('p_f30m'),edit=editable(p);edit.guide_profile.excluded_substrates=['钢板'];p=await f.review(await f.save(p,edit));
  let release=await f.publish();const req={...f.fixture.requirements,material:'钢板',substrate:'钢板',air_supply:false};const body=()=>({release_id:release.id,message:'确认全部公开目录',requirements:req});
  await configure(ctx,{model_proposal:proposal(req,{ids:[]})});let result=await both(ctx,body());assert.equal(result.state,'no_match');assert.deepEqual(result.conflicts,['已审核资料明确不适用：钢板','该工具需要压缩空气，现场明确没有可用气源。']);noPurchase(result);
  // A second public, unreviewed product prevents full-directory absence even
  // when the proposed product is excluded and the filtered search page is empty.
  let unknown=await f.get('p_f30cl'),ue=editable(unknown);ue.visibility='listed';ue.guide_profile=structuredClone(f.fixture.profile);ue.guide_profile.power_source=null;ue.guide_profile.materials=['钢板'];ue.guide_profile.substrates=['钢板'];ue.guide_profile.excluded_materials=[];ue.guide_profile.excluded_substrates=[];ue.guide_profile.sources=[{id:'t12_unknown',kind:'product_text',reference:'/summary',sha256:(await import('node:crypto')).createHash('sha256').update(JSON.stringify(unknown.summary)).digest('hex'),public_label:'隔离未知动力资料',public_url:null}];unknown=await f.review(await f.save(unknown,ue));release=await f.publish();
  await configure(ctx,{model_proposal:proposal(req,{ids:[]}),search_query:{power_source:'manual'}});result=await both(ctx,body());assert.equal(result.state,'clarifying');assert.ok(result.missing_fields.includes('guide_profile.power_source'));assert.deepEqual(result.conflicts,[]);
  const filtered=(await f.api.expect('/api/catalog/products?release_id='+release.id+'&power_source=manual',{anonymous:true},200,'unknown filtered empty page')).json;assert.deepEqual(filtered.items,[]);
  p=await f.get('p_f30m');edit=editable(p);edit.visibility='unlisted';await f.save(p,edit);release=await f.publish();
  await configure(ctx,{model_proposal:proposal(req,{ids:['p_f30cl']})});result=await both(ctx,body());assert.equal(result.state,'recommendations');assert.equal(result.recommendations[0].match_level,'needs_confirmation');assert.ok(result.missing_fields.includes('guide_profile.power_source'));assert.deepEqual(result.conflicts,[]);
  unknown=await f.get('p_f30cl');ue=editable(unknown);ue.guide_profile.power_source='manual';unknown=await f.review(await f.save(unknown,ue));release=await f.publish();await configure(ctx,{model_proposal:proposal(req,{ids:[]})});const omitted=await request(ctx,body(),{stream:true,status:503});assert.equal(omitted.result.state,'unavailable');assert.deepEqual(omitted.result.recommendations,[]);
  unknown=await f.get('p_f30cl');ue=editable(unknown);ue.visibility='unlisted';await f.save(unknown,ue);release=await f.publish();await configure(ctx,{model_proposal:proposal(req,{ids:[]})});result=await both(ctx,body());assert.equal(result.state,'no_match');assert.deepEqual(result.conflicts,['当前已发布目录暂无可供导购的上架商品，请联系咨询。']);
  pass('actual Export/Evaluate empty catalogue, all conflicts with stable deduplication, mixed unknown, filtered empty page, unreviewed power and omitted matched proposal; Go JSON/SSE decoded by shared JS validator');
 });
 if(!selectedScenarios.size||selectedScenarios.has('production-no-model'))await productionNoModel();
 for(const name of selectedScenarios)assert.ok(name==='production-no-model'?report.production?.success:report.scenarios.some(entry=>entry.name===name),'unknown scenario: '+name);
 report.success=true;
} catch(error){report.error=error.stack;throw error}finally{try{await harness.close()}finally{await writeFile(join(out,'guide-stream-smoke.json'),JSON.stringify(report,null,2))}}
