// V06 uses real production HTTP, T05 builds, protected browser preview and
// independently initialized ordinary failure instances. Credentials stay in RAM.
import assert from 'node:assert/strict';
import {readFile,writeFile,mkdir,mkdtemp,lstat,readdir,readlink,rm,copyFile} from 'node:fs/promises';
import {resolve,join,dirname} from 'node:path';
import {pathToFileURL} from 'node:url';
import {spawn} from 'node:child_process';
import {createHash,randomBytes} from 'node:crypto';
import {setTimeout as delay} from 'node:timers/promises';
import {parseArgs} from 'node:util';
import sharp from 'sharp';
import {copyTemplate} from '../../scripts/build-candidate.mjs';

export const hash=bytes=>createHash('sha256').update(bytes).digest('hex');
export async function passwordFile(){const name=process.env.SMOKE_ADMIN_PASSWORD_FILE;assert.ok(name,'SMOKE_ADMIN_PASSWORD_FILE required');assert.equal((await lstat(name)).mode&0o077,0,'password file must be private');return (await readFile(name,'utf8')).replace(/\r?\n$/,'');}
export async function command(binary,args,env,report){
 const child=spawn(binary,args,{env,stdio:['ignore','pipe','pipe']});let output='';child.stdout.on('data',b=>output+=b);child.stderr.on('data',b=>output+=b);
 const result=await new Promise((res,rej)=>{child.once('error',rej);child.once('exit',(exit_code,signal)=>res({exit_code,signal}))});report.commands??=[];report.commands.push({binary,args,...result,output});return {...result,output};
}
// Production init-admin still reads a real TTY with echo disabled. This helper
// creates the private PTY and never passes the password via argv/env/stdout.
export async function initialize(binary,env,report){
 assert.equal((await command(binary,['migrate'],env,report)).exit_code,0);
 const python=String.raw`import os,pty,select,subprocess,sys,termios,time
with open(os.environ['SMOKE_ADMIN_PASSWORD_FILE'],'rb') as f: password=f.read().rstrip(b'\r\n')
m,s=pty.openpty();p=subprocess.Popen([sys.argv[1],'init-admin','--username','admin'],stdin=s,stdout=s,stderr=s);os.close(s)
data=b'';sent=0;deadline=time.monotonic()+30
try:
 while p.poll() is None:
  if time.monotonic()>deadline: raise RuntimeError('TTY initialization timeout')
  if select.select([m],[],[],0.1)[0]:
   try:data+=os.read(m,4096)
   except OSError:break
   expected=b'Password: ' if sent==0 else b'Confirm password: '
   if sent<2 and expected in data:
    if termios.tcgetattr(m)[3]&termios.ECHO:raise RuntimeError('TTY echo enabled')
    os.write(m,password+b'\n');sent+=1
 p.wait(timeout=5)
 while select.select([m],[],[],0)[0]:
  try:data+=os.read(m,4096)
  except OSError:break
 if password in data:raise RuntimeError('password echoed')
 if p.returncode!=0 or b'ADMIN_INITIALIZED' not in data:raise RuntimeError('TTY initialization failed')
 print('ADMIN_INITIALIZED; TTY echo disabled')
finally:
 if p.poll() is None:p.kill();p.wait()
 os.close(m)
`;
 const tty=await command('python3',['-c',python,binary],env,report);assert.equal(tty.exit_code,0,'production TTY init');
 assert.equal((await command(binary,['import-seed','--seed','seed/initial-catalog.json','--media-root','src/assets/products'],env,report)).exit_code,0);
}
export function startServer(binary,env,report){
 const child=spawn(binary,['serve'],{env,detached:true,stdio:['ignore','pipe','pipe']});let output='';child.stdout.on('data',b=>output+=b);child.stderr.on('data',b=>output+=b);
 child.completed=new Promise((res,rej)=>{child.once('error',rej);child.once('exit',(exit_code,signal)=>{const result={args:['serve'],pid:child.pid,exit_code,signal,output};report.commands??=[];report.commands.push(result);res(result)})});return child;
}
export async function stopServer(child,signal='SIGTERM'){
 if(!child)return;if(child.exitCode===null&&child.signalCode===null)process.kill(-child.pid,signal);
 const timer=setTimeout(()=>{try{process.kill(-child.pid,'SIGKILL')}catch{}},20000);
 try{const result=await child.completed;if(signal==='SIGTERM')assert.equal(result.exit_code,0,'serve graceful shutdown');else assert.equal(result.signal,signal);return result}finally{clearTimeout(timer)}
}
export async function live(base,child){for(let n=0;n<300;n++){if(child)assert.ok(child.exitCode===null&&child.signalCode===null,'owned server exited');try{if((await fetch(base+'/health/live')).status===200)return}catch{}await delay(50)}throw new Error('server did not become live')}
export function client(base,password,report={http:[]}){
 let cookies=new Map(),csrf;report.http??=[];
 const request=async(path,{method='GET',body,raw,anonymous=false,headers={}}={})=>{
  const h={...headers};if(!anonymous&&cookies.size)h.Cookie=[...cookies].map(([k,v])=>k+'='+v).join('; ');if(!['GET','HEAD'].includes(method)){h.Origin=base;if(csrf&&!anonymous)h['X-CSRF-Token']=csrf}if(body!==undefined){h['Content-Type']='application/json';body=JSON.stringify(body)}
  const response=await fetch(base+path,{method,headers:h,body:raw??body,redirect:'manual',signal:AbortSignal.timeout(20000)});const bytes=Buffer.from(await response.arrayBuffer());let json;try{json=JSON.parse(bytes.toString())}catch{}
  if(!anonymous)for(const cookie of response.headers.getSetCookie()){const [pair]=cookie.split(';');const split=pair.indexOf('=');if(/Max-Age=0|Max-Age=-1/i.test(cookie))cookies.delete(pair.slice(0,split));else cookies.set(pair.slice(0,split),pair.slice(split+1))}
  return {status:response.status,headers:response.headers,bytes,json};
 };
 const expect=async(path,options,status,label)=>{const r=await request(path,options);report.http.push({method:options?.method??'GET',path,status:r.status,expected_status:status,label});assert.equal(r.status,status,label+': '+r.json?.error?.code);return r};
 const login=async()=>{const r=await expect('/api/admin/login',{method:'POST',body:{username:'admin',password}},200,'login');csrf=r.json.csrf_token;return r.json};
 const session=async()=> (await expect('/api/admin/session',{},200,'current state')).json;
 const waitCandidate=async id=>{for(let n=0;n<3400;n++){const c=(await request('/api/admin/candidates/'+id)).json;assert.ok(c?.id,'candidate readable');if(c.state==='ready'||c.state==='failed')return c;await delay(100)}throw new Error('candidate wait timeout')};
 const freeze=async(key=randomBytes(12).toString('hex'))=>{const state=await session();return (await expect('/api/admin/candidates',{method:'POST',headers:{'Idempotency-Key':key},body:{expected_workspace_revision:state.workspace_revision}},202,'freeze entire workspace')).json};
 const intent=async(c,kind='publish',key=randomBytes(12).toString('hex'))=>{const state=await session();return (await expect('/api/admin/publication-requests',{method:'POST',headers:{'Idempotency-Key':key},body:{kind,candidate_id:kind==='publish'?c.id:null,release_id:kind==='rollback'?c.id:null,snapshot_sha256:c.snapshot_sha256,expected_active_release_id:state.active_release_id,expected_workspace_revision:state.workspace_revision}},201,kind+' intent')).json};
 const confirmation=p=>({snapshot_sha256:p.snapshot_sha256,expected_active_release_id:p.expected_active_release_id,expected_workspace_revision:p.expected_workspace_revision});
 const confirm=async(p,key=randomBytes(12).toString('hex'))=>(await expect('/api/admin/publication-requests/'+p.id+'/confirm',{method:'POST',headers:{'Idempotency-Key':key},body:confirmation(p)},202,'browser confirmation')).json;
 const waitRequest=async id=>{for(let n=0;n<300;n++){const r=await request('/api/admin/publication-requests/'+id);assert.equal(r.status,200);if(['succeeded','failed','stale','expired','cancelled'].includes(r.json.state))return r.json;await delay(50)}throw new Error('publication wait timeout')};
 const publish=async(c,kind='publish')=>{const p=await intent(c,kind);await confirm(p);const done=await waitRequest(p.id);assert.equal(done.state,'succeeded',done.error?.code);assert.equal(done.result_release_id,c.id);assert.ok(done.completed_at&&done.site_url);return done};
 return {base,request,expect,login,session,freeze,waitCandidate,intent,confirmation,confirm,waitRequest,publish,cookies:()=>cookies,csrf:()=>csrf};
}
export function editable(product){const {id,slug,version,updated_at,...edit}=structuredClone(product);delete edit.guide_profile.reviews;return edit}
export function incomplete(slug){return {slug,kind:'tool',name:'',model:'',category_id:'straight',series:'',legacy_series_ids:[],summary:'',applications:[],features:[],notes:[],scene_ids:[],nail_type:null,fastener_text:'',long_nose:false,blower:false,images:[],cover_media_id:null,visibility:'unlisted',sort_order:0,guide_profile:{scene_ids:[],materials:[],substrates:[],excluded_materials:[],excluded_substrates:[],power_source:null,nail_type:null,fastener_length_mm:null,load_bearing:null,long_nose:null,blower:null,limitations:[],consultation_notes:[],required_requirements:[],sources:[]},purchase_options:[]}}
export async function snapshotFiles(directory){const out={};async function walk(relative){for(const name of(await readdir(join(directory,relative))).sort()){const key=join(relative,name),stat=await lstat(join(directory,key));if(stat.isDirectory())await walk(key);else if(stat.isSymbolicLink()){out[key]={kind:'symlink',target:await readlink(join(directory,key)),inode:stat.ino,mtime_ms:stat.mtimeMs}}else{assert.ok(stat.isFile());out[key]={sha256:hash(await readFile(join(directory,key))),bytes:stat.size,inode:stat.ino,mtime_ms:stat.mtimeMs}}}}await walk('');return out}
async function descendants(pid){const children=new Set();let threads;try{threads=await readdir('/proc/'+pid+'/task')}catch(error){if(error.code==='ENOENT')return [];throw error}for(const thread of threads){try{for(const child of (await readFile('/proc/'+pid+'/task/'+thread+'/children','utf8')).trim().split(/\s+/).filter(Boolean))children.add(Number(child))}catch(error){if(error.code!=='ENOENT')throw error}}const found=[...children];for(const child of children)found.push(...await descendants(child));return found}

// Compare consumers against the current activation after a real A-B-A cutover.
// Every negative request uses a new key; successful confirmation uses its original key.
export async function assertOldBaselineRejected(api,candidate,pending,successful,confirmationKey,label,replayAPI=api){
 const state=await api.session();const suffix=label+'-'+randomBytes(6).toString('hex');
 const rejected=await api.expect('/api/admin/publication-requests',{method:'POST',headers:{'Idempotency-Key':'aba-candidate-'+suffix},body:{kind:'publish',candidate_id:candidate.id,release_id:null,snapshot_sha256:candidate.snapshot_sha256,expected_active_release_id:state.active_release_id,expected_workspace_revision:state.workspace_revision}},409,'ABA rejects new intent from old candidate '+label);
 const old=await api.expect('/api/admin/publication-requests/'+pending.id+'/confirm',{method:'POST',headers:{'Idempotency-Key':'aba-pending-'+suffix},body:api.confirmation(pending)},409,'ABA rejects old pending confirmation '+label);
 const replay=await replayAPI.confirm(successful,confirmationKey);assert.equal(replay.id,successful.id);assert.equal(replay.state,'succeeded');assert.equal(replay.result_release_id,candidate.id);assert.equal(replay.completed_at,successful.completed_at);assert.equal((await api.session()).active_release_id,state.active_release_id);
 return {new_intent_status:rejected.status,pending_confirm_status:old.status,replayed_request_id:replay.id,replayed_release_id:replay.result_release_id,active_release_id:state.active_release_id};
}

async function failures({binary,root,password,out,report,taskNumber}){
 const parent=dirname(resolve(process.env.DATA_DIR||process.env.TMPDIR));await mkdir(parent,{recursive:true});const privateRoot=await mkdtemp(join(parent,'.v06-failures-'));
 let owned;const base='http://127.0.0.1:'+(22000+taskNumber);const env={...process.env,DATA_DIR:join(privateRoot,'data'),HTTP_ADDR:new URL(base).host,SITE_URL:base,TEMPLATE_DIR:root};
 try{
  await initialize(binary,env,report);owned=startServer(binary,env,report);await live(base,owned);let api=client(base,password,report);await api.login();const c=await api.waitCandidate((await api.freeze()).id);assert.equal(c.state,'ready');await api.publish(c);
  const page=await api.request('/',{anonymous:true}),oldImagePath='/'+JSON.parse(await readFile(join(env.DATA_DIR,'artifacts',c.id,'manifest.json'),'utf8')).resources.find(r=>r.path.startsWith('media/')).path;const oldImage=await api.request(oldImagePath,{anonymous:true});
  // A missing actual template input causes the real copier/build to fail.
  await stopServer(owned);owned=undefined;
  const template=join(privateRoot,'template');await copyTemplate(root,template);await copyFile(join(root,'scripts/build-candidate.mjs'),join(template,'scripts/build-candidate.mjs'));
  const configuration=join(template,'astro.config.mjs'),original=await readFile(configuration,'utf8');await writeFile(configuration,'throw new Error("isolated V06 template failure");\n'+original);
  const negativeEnv={...env,TEMPLATE_DIR:template};owned=startServer(binary,negativeEnv,report);await live(base,owned);api=client(base,password,report);await api.login();const failed=await api.waitCandidate((await api.freeze()).id);assert.equal(failed.state,'failed');assert.equal(failed.error.code,'BUILD_FAILED');assert.ok((await api.request('/',{anonymous:true})).bytes.equals(page.bytes));assert.ok((await api.request(oldImagePath,{anonymous:true})).bytes.equals(oldImage.bytes));assert.equal((await api.session()).active_release_id,c.id);
  report.checks.push('real template/build failure preserves active HTML and original image');
  await stopServer(owned);owned=undefined;
  await writeFile(configuration,'await new Promise(resolve=>setTimeout(resolve,360000));\n'+original);
  // Default production maximum is exercised for the complete 300-second window.
  const timeoutEnv={...negativeEnv};delete timeoutEnv.BUILD_TIMEOUT;
  owned=startServer(binary,timeoutEnv,report);await live(base,owned);api=client(base,password,report);await api.login();const started=Date.now();const timeoutCandidate=await api.freeze();const observed=new Set();for(let n=0;n<30;n++){for(const pid of await descendants(owned.pid))observed.add(pid);await delay(200)}assert.ok(observed.size>0,'real build subprocesses observed');const timed=await api.waitCandidate(timeoutCandidate.id);const elapsed=Date.now()-started;
  assert.equal(timed.state,'failed');assert.equal(timed.error.code,'BUILD_TIMEOUT');assert.ok(elapsed>=299000&&elapsed<330000,'real 300s timeout window');assert.ok((await api.request('/',{anonymous:true})).bytes.equals(page.bytes));assert.ok((await api.request(oldImagePath,{anonymous:true})).bytes.equals(oldImage.bytes));assert.equal((await api.session()).active_release_id,c.id);
  const states=[];for(const pid of observed){let state='absent';try{const stat=await readFile('/proc/'+pid+'/stat','utf8');state=stat.slice(stat.lastIndexOf(')')+2).split(' ')[0]}catch(error){if(error.code!=='ENOENT')throw error}assert.ok(state==='absent'||state==='Z','timed-out build process still running: '+pid);states.push({pid,state})}
  report.timeout={elapsed_ms:elapsed,configured_seconds:300,state:timed.state,error:timed.error.code,observed_child_states:states};report.checks.push('real 300s timeout kills build descendants and preserves active HTML/image');
  const before=await snapshotFiles(env.DATA_DIR);for(const argv of [['serve'],['migrate'],['init-admin','--username','admin'],['import-seed','--seed','seed/initial-catalog.json','--media-root','src/assets/products']]){const locked=await command(binary,argv,env,report);assert.equal(locked.exit_code,73);assert.equal(locked.output.trim(),'DATA_DIR_LOCKED')};assert.deepEqual(await snapshotFiles(env.DATA_DIR),before);report.checks.push('T03 excludes every second write process without changing DB/files');
  await writeFile(join(out,'ordinary-failure-state.json'),JSON.stringify({release_id:c.id,failed_candidate:failed.id,timeout_candidate:timed.id,active:JSON.parse(await readFile(join(env.DATA_DIR,'state/active.json'),'utf8')),timeout:report.timeout},null,2));
 }finally{
  await stopServer(owned);
  const logs=join(out,'ordinary-build-logs');await mkdir(logs,{recursive:true});let candidates=[];try{candidates=await readdir(join(env.DATA_DIR,'work'))}catch(error){if(error.code!=='ENOENT')throw error}
  for(const id of candidates){try{await writeFile(join(logs,id+'.log'),await readFile(join(env.DATA_DIR,'work',id,'build.log')))}catch(error){if(error.code!=='ENOENT')throw error}}
  await rm(privateRoot,{recursive:true,force:true});report.failure_instance_cleaned=true
 }
}

async function main(){
 const {values}=parseArgs({options:{base:{type:'string'},out:{type:'string'}}});assert.ok(values.base&&values.out,'--base and --out required');const base=values.base,out=resolve(values.out);await mkdir(out,{recursive:true});
 const password=await passwordFile(),root=process.cwd(),binary=resolve(process.env.SMOKE_YUANFA_BINARY||'.work/bin/yuanfa');const report={checks:[],http:[],commands:[],success:false};const managed=process.env.SMOKE_MANAGE_SERVER==='1';let owned;
 const pass=label=>{report.checks.push(label);console.log('PASS '+label)};const env={...process.env,SITE_URL:base,HTTP_ADDR:new URL(base).host};
 try{
  if(managed){owned=startServer(binary,env,report);await live(base,owned)}else await live(base);
  const api=client(base,password,report);await api.expect('/',{anonymous:true},503,'first publication absent');await api.login();await api.expect('/api/admin/candidates/unknown',{anonymous:true},401,'anonymous candidate denied');
  await api.expect('/api/admin/products',{method:'POST',body:incomplete('f30')},409,'old series slug cannot be reassigned');
  const tag=randomBytes(6).toString('hex');const privateBytes=await sharp({create:{width:24,height:18,channels:3,background:'#'+tag.slice(0,6)}}).png().toBuffer();const publicBytes=await sharp({create:{width:36,height:24,channels:3,background:'#'+tag.slice(6,12)}}).png().toBuffer();
  const upload=async bytes=>{const form=new FormData();form.append('file',new Blob([bytes]),'boundary.png');return (await api.expect('/api/admin/media',{method:'POST',raw:form},201,'private upload')).json};
  const privateImage=await upload(privateBytes),publicImage=await upload(publicBytes);const draft=incomplete('v06-private-'+tag);draft.images=[{media_id:privateImage.media_id,label:'private',description:'private-only'}];draft.cover_media_id=privateImage.media_id;
  const privateProduct=(await api.expect('/api/admin/products',{method:'POST',body:draft},201,'save incomplete private draft')).json.product;
  const c0=await api.waitCandidate((await api.freeze('baseline-'+tag)).id);assert.equal(c0.state,'ready',c0.error?.code);await api.publish(c0);assert.equal((await api.expect('/health/ready',{},200,'verified ready')).json.release_id,c0.id);pass('real T05 build and first confirmed release');
  const originalAuthority=JSON.parse(await readFile(join(process.env.DATA_DIR,'state/active.json'),'utf8'));
  await api.expect('/media/'+privateImage.sha256+'.png',{anonymous:true},404,'frozen draft-exclusive image not public');await api.expect('/products/'+privateProduct.slug+'/',{anonymous:true},404,'never-published draft not public');
  const seed=(await api.expect('/api/admin/products/p_f30m',{},200,'seed source')).json.product;
  const create={slug:'v06-listed-'+tag,...editable(seed),model:'V06-'+tag,name:'V06 boundary product',visibility:'unlisted',legacy_series_ids:['s_f30'],images:[{media_id:publicImage.media_id,label:'public',description:'V06 listed image'}],cover_media_id:publicImage.media_id};
  // editable includes existing channel IDs only when present; seed has none.
  let p=(await api.expect('/api/admin/products',{method:'POST',body:create},201,'new complete draft')).json.product;let edit=editable(p);edit.visibility='listed';p=(await api.expect('/api/admin/products/'+p.id,{method:'PUT',headers:{'If-Match':'"'+p.version+'"'},body:edit},200,'target listing')).json.product;
  const stale=await api.freeze('stale-'+tag);const changed=editable(seed);changed.summary+=' V06 later workspace edit';const updatedSeed=(await api.expect('/api/admin/products/'+seed.id,{method:'PUT',headers:{'If-Match':'"'+seed.version+'"'},body:changed},200,'edit while candidate building')).json.product;const builtStale=await api.waitCandidate(stale.id);assert.equal(builtStale.state,'ready');assert.equal(builtStale.workspace_changed,true);
  const state=await api.session();await api.expect('/api/admin/publication-requests',{method:'POST',headers:{'Idempotency-Key':'stale-request-'+tag},body:{kind:'publish',candidate_id:stale.id,release_id:null,snapshot_sha256:stale.snapshot_sha256,expected_active_release_id:state.active_release_id,expected_workspace_revision:state.workspace_revision}},409,'stale candidate cannot request activation');pass('edits during build freeze old bytes and prevent stale publication');
  const c1=await api.waitCandidate((await api.freeze('new-'+tag)).id);assert.equal(c1.state,'ready');assert.ok(c1.diff.changes.some(x=>x.product_id===p.id));
  await api.expect('/api/admin/preview-session',{method:'POST',body:{candidate_id:c1.id}},200,'bind browser preview');const preview=await api.expect('/products/'+p.slug+'/',{},200,'preview candidate HTML');assert.ok(preview.bytes.includes(Buffer.from(c1.id)));assert.equal(preview.headers.get('cache-control'),'private, no-store');assert.equal(preview.headers.get('x-robots-tag'),'noindex');assert.ok((await api.expect('/media/'+publicImage.sha256+'.png',{},200,'preview listed image')).bytes.equals(publicBytes));await api.expect('/media/'+privateImage.sha256+'.png',{},404,'preview never authorizes draft-exclusive media');await api.expect('/media/'+publicImage.sha256+'.png',{anonymous:true},404,'anonymous cannot fetch candidate-exclusive image');assert.ok((await api.expect('/',{anonymous:true},200,'anonymous stays on active release')).bytes.includes(Buffer.from(c0.id)));
  if(process.env.BROWSER_PATH&&process.env.PLAYWRIGHT_MODULE){
   await mkdir('.work/browser-tmp',{recursive:true});const {chromium}=await import(process.env.PLAYWRIGHT_MODULE);const browser=await chromium.launch({executablePath:process.env.BROWSER_PATH,headless:true,args:['--no-sandbox'],env:{...process.env,TMPDIR:'/proc/self/cwd/.work/browser-tmp'}});
   try{
    const context=await browser.newContext({viewport:{width:1440,height:1000}});
    await context.addCookies([...api.cookies()].map(([name,value])=>({name,value,url:base,httpOnly:true,sameSite:'Strict'})));
    const page=await context.newPage();await page.goto(base+'/products/'+p.slug+'/');await page.locator('#yuanfa-preview').waitFor({state:'visible'});
    const visibleImage=page.locator('[data-gallery-panel]:not([hidden]) img').first();await visibleImage.scrollIntoViewIfNeeded();await visibleImage.evaluate(async image=>{await image.decode()});assert.ok(await visibleImage.evaluate(image=>image.naturalWidth>0));
    assert.equal(await page.locator('meta[name=release-id]').getAttribute('content'),c1.id);await page.screenshot({path:join(out,'preview-desktop.png'),fullPage:true});
    await page.goto(base+'/products/f30m/');const thumbs=page.locator('[data-gallery-thumb]');assert.ok(await thumbs.count()>1,'seed gallery has multiple real images');await thumbs.nth(1).click();assert.equal(await thumbs.nth(1).getAttribute('aria-pressed'),'true');
    const selected=await page.locator('[data-gallery-panel]:not([hidden])').getAttribute('data-media-id');await page.keyboard.press('ArrowRight');assert.notEqual(await page.locator('[data-gallery-panel]:not([hidden])').getAttribute('data-media-id'),selected);await page.locator('[data-gallery-panel]:not([hidden]) img').evaluate(async image=>{await image.decode()});
    const sizes=[];for(const width of [1440,390,320]){await page.setViewportSize({width,height:width===1440?1000:844});sizes.push(await page.evaluate(()=>({width:innerWidth,height:innerHeight})));if(width!==1440)await page.screenshot({path:join(out,'preview-'+width+'.png'),fullPage:true})}
    await page.locator('#yuanfa-preview-exit').click();await page.waitForURL(base+'/');assert.equal(await page.locator('#yuanfa-preview').count(),0);assert.equal(await page.locator('meta[name=release-id]').getAttribute('content'),c0.id);await context.close();
    const anonymous=await browser.newContext();const anonymousPage=await anonymous.newPage();assert.equal((await anonymousPage.goto(base+'/products/'+p.slug+'/')).status(),404);assert.equal((await anonymousPage.goto(base+'/media/'+publicImage.sha256+'.png')).status(),404);await anonymous.close();
    report.browser={actual_viewports:sizes,preview_release:c1.id,exit_release:c0.id,gallery_arrow_changed_media:true,anonymous_candidate_status:404};
   }finally{await browser.close()}
  }
  await api.expect('/api/admin/preview-session',{method:'DELETE'},200,'exit HTTP preview');pass('protected preview navigation/images/browser exit; private frozen media excluded');
  const cancelled=await api.intent(c1);const cancelledResult=await api.expect('/api/admin/publication-requests/'+cancelled.id+'/cancel',{method:'POST'},200,'cancel pending intent');assert.equal(cancelledResult.json.state,'cancelled');await api.expect('/api/admin/publication-requests/'+cancelled.id+'/confirm',{method:'POST',headers:{'Idempotency-Key':'cancelled-'+tag},body:api.confirmation(cancelled)},409,'cancelled intent cannot activate');
  const replayKey='intent-replay-'+tag;const intentReplay=await api.intent(c1,'publish',replayKey);assert.equal((await api.intent(c1,'publish',replayKey)).id,intentReplay.id);await api.expect('/api/admin/publication-requests/'+intentReplay.id+'/cancel',{method:'POST'},200,'cancel replay fixture');
  const history=(await api.expect('/api/admin/publications?limit=1',{},200,'publication history first page')).json;assert.ok(history.next_cursor);const older=(await api.expect('/api/admin/publications?limit=1&cursor='+encodeURIComponent(history.next_cursor),{},200,'publication history next page')).json;assert.notEqual(older.items[0].id,history.items[0].id);pass('pending cancellation, request replay and history pagination');
  const abaPending=await api.intent(c1);
  const first=await api.intent(c1),second=await api.intent(c1);const key='confirmation-'+tag;await api.confirm(first,key);const published=await api.waitRequest(first.id);assert.equal(published.state,'succeeded');const replay=await api.confirm(first,key);assert.equal(replay.id,first.id);await api.expect('/api/admin/publication-requests/'+second.id+'/confirm',{method:'POST',headers:{'Idempotency-Key':'second-'+tag},body:api.confirmation(second)},409,'another publication made old intent stale');assert.equal((await api.request('/api/admin/publication-requests/'+second.id)).json.state,'stale');pass('confirmed activation, idempotent lost-response replay, competing intent rejected');
  const liveProduct=await api.expect('/products/'+p.slug+'/',{anonymous:true},200,'new product detail active');assert.ok(liveProduct.bytes.includes(Buffer.from(c1.id)));const productLink=Buffer.from('href="/products/'+p.slug+'/"');assert.ok((await api.request('/products/f30/',{anonymous:true})).bytes.includes(productLink),'new product link belongs to original series');const catalog=await api.request('/products/',{anonymous:true});assert.ok(catalog.bytes.includes(productLink),'new product link belongs to public catalog');const manifest=JSON.parse(await readFile(join(process.env.DATA_DIR,'artifacts',c1.id,'manifest.json'),'utf8'));assert.ok(!manifest.resources.some(x=>x.path.includes(privateImage.sha256)));
  for(const path of ['/artifacts/'+c1.id+'/catalog.json','/catalog.json','/manifest.json','/db/catalog.sqlite','/work/'+c1.id+'/catalog.json'])await api.expect(path,{anonymous:true},404,'private storage not mounted');
  // No workspace edit occurs between these activations: the equal release ID
  // after rollback must not restore the previous candidate/intent baseline.
  await api.publish(c0,'rollback');const roundTripBytes=await readFile(join(process.env.DATA_DIR,'state/active.json'));const roundTrip=JSON.parse(roundTripBytes);assert.equal(roundTrip.release_id,originalAuthority.release_id);assert.notEqual(roundTrip.operation_id,originalAuthority.operation_id);
  report.aba={original_operation_id:originalAuthority.operation_id,round_trip_operation_id:roundTrip.operation_id,pending_request_id:abaPending.id,before_restart:await assertOldBaselineRejected(api,c1,abaPending,published,key,'ordinary-before-restart')};
  assert.ok((await readFile(join(process.env.DATA_DIR,'state/active.json'))).equals(roundTripBytes),'successful replay must not rewrite the round-trip authority');
  if(managed){await stopServer(owned);owned=undefined;owned=startServer(binary,env,report);await live(base,owned);await api.session();report.aba.after_restart=await assertOldBaselineRejected(api,c1,abaPending,published,key,'ordinary-after-restart');assert.ok((await readFile(join(process.env.DATA_DIR,'state/active.json'))).equals(roundTripBytes))}
  const rebuilt=await api.waitCandidate((await api.freeze('after-aba-'+tag)).id);assert.equal(rebuilt.state,'ready');await api.publish(rebuilt);report.aba.fresh_candidate_id=rebuilt.id;pass('A-B-A permanently rejects both old publication paths across restart; successful same-key replay is unchanged; freshly rebuilt candidate publishes');
  // Withdraw the promoted models and the complete straight category in one
  // frozen workspace, then verify real homepage/category/series empty states.
  const all=(await api.request('/api/admin/products?limit=100')).json.items;const promoted=new Set(['p_f30m','p_st64ml','p_st64gl','p_f30c','p_f30cl']);
  for(const item of all){const doc=(await api.request('/api/admin/products/'+item.id)).json.product;if(doc.visibility==='listed'&&(doc.category_id==='straight'||promoted.has(doc.id))){const e=editable(doc);e.visibility='unlisted';await api.expect('/api/admin/products/'+doc.id,{method:'PUT',headers:{'If-Match':'"'+doc.version+'"'},body:e},200,'withdraw category/promoted member')}}
  const c2=await api.waitCandidate((await api.freeze()).id);assert.equal(c2.state,'ready');await api.publish(c2);assert.equal((await api.expect('/',{anonymous:true},200,'homepage after withdrawals')).status,200);assert.ok(!(await api.request('/products/',{anonymous:true})).bytes.includes(Buffer.from(p.model)));await api.expect('/products/f30/',{anonymous:true},200,'old series empty route preserved');pass('withdraw promoted products and complete category without broken homepage/series');
  const workspaceBefore=(await api.request('/api/admin/products/'+p.id)).json;await api.publish(c0,'rollback');const withdrawn=await api.expect('/products/'+p.slug+'/',{anonymous:true},200,'rollback before first publication retains withdrawn URL');assert.equal(withdrawn.headers.get('x-robots-tag'),'noindex');assert.ok(withdrawn.bytes.includes(Buffer.from(c0.id)));assert.deepEqual((await api.request('/api/admin/products/'+p.id)).json,workspaceBefore);assert.ok((await api.expect('/media/'+publicImage.sha256+'.png',{anonymous:true},200,'old successful hash survives rollback')).bytes.equals(publicBytes));await api.expect('/api/admin/media/'+publicImage.media_id,{method:'DELETE'},409,'successful/candidate references retain media');assert.equal((await api.session()).active_release_id,c0.id);pass('rollback retains whole workspace, historical URL and old successful image');
  const before=await snapshotFiles(process.env.DATA_DIR);for(const argv of [['serve'],['migrate'],['init-admin','--username','admin'],['import-seed','--seed','seed/initial-catalog.json','--media-root','src/assets/products']]){const locked=await command(binary,argv,env,report);assert.equal(locked.exit_code,73);assert.equal(locked.output.trim(),'DATA_DIR_LOCKED')};assert.deepEqual(await snapshotFiles(process.env.DATA_DIR),before);pass('normal publication instance excludes other write processes');
  report.releases={baseline:c0.id,added:c1.id,withdrawn:c2.id,stale:stale.id,final:c0.id};
  if(managed){await stopServer(owned);owned=undefined}
  await failures({binary,root,password,out,report,taskNumber:Number(process.env.TASK_NUMBER||6)});report.success=true;
 }catch(error){report.error=error.message;throw error}finally{try{await stopServer(owned)}finally{await writeFile(join(out,'publication-smoke.json'),JSON.stringify(report,null,2))}}
}
if(process.argv[1]&&import.meta.url===pathToFileURL(resolve(process.argv[1])).href)await main();
