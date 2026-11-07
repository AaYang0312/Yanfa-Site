// T12 owns this process harness. T14 imports only this public API. Every scenario
// has fresh data/control, production migrations/TTY init/import, real management
// HTTP fixture reviews/build/confirmation, and finally a joined owned process.
// Controlled model/fault scenarios are integration-only, never customer evidence.
import assert from 'node:assert/strict';
import {mkdir,mkdtemp,readFile,writeFile,rm,lstat} from 'node:fs/promises';
import {join,resolve} from 'node:path';
import {setTimeout as delay} from 'node:timers/promises';
import {command,initialize,passwordFile,startServer,stopServer,live,client,editable,hash} from '../../smoke/publication.mjs';

export async function createGuideHarness({go,root,taskNumber,out,withAdmin=false}) {
// callback receives {baseURL,qaURL,dataDir,controlDir,readStats,releaseBarrier}.
// Before a request, write a 0600 controlDir/scenario.json with model_proposal
// (the full shared GuideModelProposal), optional search_query/get_product_id,
// model_error (upstream|invalid|loop|block|tool-block|forbidden-tool),
// pause_before_result, and stream_fault
// (error|disconnect|missing-finish|missing-done|pause). A barrier writes
// reached.json with before-result|model|tool|stream. releaseBarrier creates only
// continue. An absent proposal/error leaves the customer model unconfigured.
// stats.json contains only the six counters/ID specified by the approved plan.
// configure and consume inside callback; runScenario always stops the process.

 root=resolve(root);out=resolve(out);
 assert.equal(process.cwd(),root,'harness cwd must be the assigned checkout');
 assert.ok(Number.isInteger(taskNumber)&&taskNumber>0&&taskNumber<1000);
 const role=process.env.RUN_ROLE||'worker',directory=join(root,'.work',role,'integration');
 await mkdir(directory,{recursive:true,mode:0o700});await mkdir(join(root,'.work/bin'),{recursive:true});await mkdir(out,{recursive:true});
 const env={...process.env,GOTOOLCHAIN:'local',GOPROXY:'https://goproxy.cn',GOSUMDB:'sum.golang.org'};
 for(const key of Object.keys(env))if(/^MODEL_/i.test(key)||['HTTP_PROXY','HTTPS_PROXY','ALL_PROXY','http_proxy','https_proxy','all_proxy','npm_config_proxy','npm_config_https_proxy'].includes(key))delete env[key];
 for(const key of ['TMPDIR','GOTMPDIR'])assert.ok(env[key]?.startsWith(root+'/.work/'),'private absolute '+key+' required');
 if(withAdmin)assert.ok((await lstat(join(root,'admin/dist'))).isDirectory(),'build:admin required for withAdmin');
 const report={scope:'Integration-only Eino adapter; not Q04/T19/T20',commands:[],scenarios:[]};
 const binary=join(root,'.work/bin/yuanfa-integration');
 assert.equal((await command(go,['build','-tags=integration','-o',binary,'./backend/cmd/yuanfa'],env,report)).exit_code,0,'build tagged binary');
 let running=false,closed=false,owned;
 const close=async()=>{closed=true;await stopServer(owned);owned=undefined;await writeFile(join(out,'harness.json'),JSON.stringify(report,null,2))};
 return {
  async runScenario(name,callback){
   assert.ok(!closed&&!running,'scenarios are isolated and sequential');assert.match(name,/^[a-z0-9_-]+$/i);running=true;
   const work=await mkdtemp(join(directory,name+'-')),dataDir=join(work,'data'),controlDir=join(work,'control');
   const evidence=join(out,name);await mkdir(evidence,{recursive:true});await mkdir(dataDir,{mode:0o700});await mkdir(controlDir,{mode:0o700});
   const baseURL='http://127.0.0.1:'+(19000+taskNumber),entry={name,commands:[],http:[],dataDir,controlDir,cleaned:false};report.scenarios.push(entry);
   const childEnv={...env,DATA_DIR:dataDir,HTTP_ADDR:new URL(baseURL).host,SITE_URL:baseURL,QA_URL:baseURL,TEMPLATE_DIR:root,SITE_CONFIG_PATH:join(root,'src/generated/site-config.json'),ADMIN_STATIC_DIR:join(root,'admin/dist'),YUANFA_INTEGRATION_DIR:controlDir};
   try{
    await writeFile(join(controlDir,'scenario.json'),'{}',{mode:0o600});
    await initialize(binary,childEnv,entry);
    owned=startServer(binary,childEnv,entry);await live(baseURL,owned);
    await callback({baseURL,qaURL:baseURL,dataDir,controlDir,
     async readStats(){try{return JSON.parse(await readFile(join(controlDir,'stats.json'),'utf8'))}catch(error){if(error.code!=='ENOENT')throw error;return {agent_runs:0,model_calls:0,tool_calls:0,select_calls:0,active_model_calls:0,release_id:''}}},
     async releaseBarrier(){await writeFile(join(controlDir,'continue'),'continue',{mode:0o600})},
    });
   }catch(error){entry.error=error.stack;throw error}finally{
    try{await stopServer(owned);owned=undefined}finally{
     for(const file of ['stats.json','reached.json','scenario.json'])try{await writeFile(join(evidence,file),await readFile(join(controlDir,file)),{mode:0o600})}catch(error){if(error.code!=='ENOENT')throw error}
     await rm(work,{recursive:true,force:true});entry.cleaned=true;running=false;
     await writeFile(join(out,'harness.json'),JSON.stringify(report,null,2));
    }
   }
  },close,
 };
}

// Test consumers use the real browser-authorized API to populate an isolated
// reviewed F30M fixture. Nothing is inserted directly into SQLite or artifacts.
export async function setupGuideFixture(baseURL,report={http:[]}) {
 const api=client(baseURL,await passwordFile(),report);await api.login();
 const fixture=JSON.parse(await readFile('tests/fixtures/guide/selection.json','utf8'));
 const get=async id=>(await api.expect('/api/admin/products/'+id,{},200,'read fixture product')).json.product;
 const save=async(p,edit)=>(await api.expect('/api/admin/products/'+p.id,{method:'PUT',headers:{'If-Match':'"'+p.version+'"'},body:edit},200,'save isolated product')).json.product;
 const review=async doc=>{
  const facts=fixture.reviewed_fields.filter(field=>doc.guide_profile[field]!==null).map(field=>({field,fact_sha256:hash(JSON.stringify(doc.guide_profile[field])),source_sha256s:[doc.guide_profile.sources[0].sha256]}));
  return (await api.expect('/api/admin/products/'+doc.id+'/guide-review',{method:'POST',headers:{'If-Match':'"'+doc.version+'"'},body:{version:doc.version,facts}},200,'review actual fixture facts')).json.product;
 };
 let product=await get('p_f30m'),edit=editable(product);
 edit.guide_profile=structuredClone(fixture.profile);
 edit.guide_profile.sources=[{id:'t12_public_source',kind:'product_text',reference:'/summary',sha256:hash(JSON.stringify(product.summary)),public_label:'隔离公开规则：中文“引号”\n第二行 {data}',public_url:null}];
 edit.purchase_options=structuredClone(fixture.options);
 product=await review(await save(product,edit));
 const publish=async()=>{const c=await api.waitCandidate((await api.freeze()).id);assert.equal(c.state,'ready',JSON.stringify(c.error));await api.publish(c);return c};
 const candidate=await publish();
 return {api,fixture,product,candidate,get,save,review,publish,report};
}

// A bounded observation loop for filesystem/stat changes; no fault HTTP routes.
export async function waitGuide(predicate,label='guide observation',timeout=10000){
 const end=Date.now()+timeout;
 while(Date.now()<end){const value=await predicate();if(value)return value;await delay(20)}
 throw new Error('Timed out: '+label);
}
