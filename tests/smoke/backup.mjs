// V15 exercises real Go processes, HTTP publication, SQLite WAL and the actual
// offline orchestration script. Docker/1Panel checks require separate authority.
import assert from 'node:assert/strict';
import {readFile,writeFile,mkdir,mkdtemp,lstat,readdir,rename,cp,rm,stat} from 'node:fs/promises';
import {resolve,join,dirname} from 'node:path';
import {spawn} from 'node:child_process';
import {parseArgs} from 'node:util';
import {randomBytes} from 'node:crypto';
import {setTimeout as delay} from 'node:timers/promises';
import {DatabaseSync} from 'node:sqlite';
import sharp from 'sharp';
import {passwordFile,initialize,command,startServer,stopServer,live,client,editable,incomplete,hash,snapshotFiles} from './publication.mjs';

const {values}=parseArgs({options:Object.fromEntries(['go','root','task-number','binary','server','out'].map(k=>[k,{type:'string'}]))});
for(const k of ['go','root','task-number','binary','server','out'])assert.ok(values[k],'--'+k+' required');
const root=resolve(values.root),out=resolve(values.out),binary=resolve(values.binary),server=resolve(values.server),task=Number(values['task-number']);
assert.equal(process.cwd(),root);assert.ok(Number.isInteger(task)&&task>0);
await mkdir(out,{recursive:true});
const role=process.env.RUN_ROLE||'worker';const parent=join(root,'.work',role,'backup');await mkdir(parent,{recursive:true,mode:0o700});
const work=await mkdtemp(join(parent,'v15-')),data=join(work,'source-data'),restored=join(work,'restored-data'),control=join(work,'control');
await mkdir(control,{mode:0o700});
const sourceBase='http://127.0.0.1:'+(18000+task),restoreBase='http://127.0.0.1:'+(22000+task);
const runtime=join(root,'.work/runtime'),tagged=join(root,'.work/bin/yuanfa-backup-integration');
const env={...process.env,GOTOOLCHAIN:'local',GOPROXY:'https://goproxy.cn',GOSUMDB:'sum.golang.org',DATA_DIR:data,HTTP_ADDR:new URL(sourceBase).host,SITE_URL:sourceBase,QA_URL:sourceBase,TEMPLATE_DIR:join(runtime,'template'),ADMIN_STATIC_DIR:join(runtime,'admin'),SITE_CONFIG_PATH:join(runtime,'site-config.json')};
for(const key of ['HTTP_PROXY','HTTPS_PROXY','ALL_PROXY','http_proxy','https_proxy','all_proxy','npm_config_proxy','npm_config_https_proxy','MODEL_API_KEY','MODEL_BASE_URL','MODEL_MODEL','YUANFA_INTEGRATION_DIR'])delete env[key];
const report={success:false,work,commands:[],http:[],checks:[],external_unverified:['Docker image build/run','1Panel/OpenResty/TLS/SSE','real Eino/customer model and client']};
const pass=text=>{report.checks.push(text);console.log('PASS '+text)};
const backupPath=join(work,'sealed'),normalPath=join(work,'offline-script-success');
const password=await passwordFile();let owned,externalPID,paused;
const readDB=name=>{const db=new DatabaseSync(name,{readOnly:true});try{const tables=db.prepare("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name").all();return Object.fromEntries(tables.map(({name})=>[name,db.prepare('SELECT * FROM "'+name+'" ORDER BY rowid').all()]))}finally{db.close()}};
async function pidState(pid){try{return (await readFile('/proc/'+pid+'/stat','utf8')).split(') ').at(-1).split(' ')[0]}catch(e){if(e.code==='ENOENT')return 'absent';throw e}}
async function stopExternal(){if(!externalPID)return;const pid=externalPID;if(!['absent','Z'].includes(await pidState(pid)))process.kill(pid,'SIGTERM');for(let n=0;n<400;n++){if(['absent','Z'].includes(await pidState(pid))){report.commands.push({pid,operation:'stop local-script restarted Go',observed_state:await pidState(pid)});externalPID=undefined;return}await delay(50)}throw Error('local restarted Go did not stop')}
async function offline(output,expected){
 const pidFile=join(control,'source.pid');assert.ok(owned||externalPID);
 await writeFile(pidFile,String(owned?.pid??externalPID)+'\n',{mode:0o600});
 const original=owned;const result=await command('bash',['deploy/backup-offline.sh','--data',data,'--output',output,'--health-url',sourceBase,'--local-server',server,'--local-pid-file',pidFile,'--binary',binary],{...env,BACKUP_CONTROL_DIR:join(control,'orchestration')},report);
 if(original){await original.completed;owned=undefined}
 externalPID=Number((await readFile(pidFile,'utf8')).trim());assert.ok(externalPID>1);assert.equal(result.exit_code,expected);await live(sourceBase);assert.equal((await fetch(sourceBase+'/health/ready')).status,200);
 return result;
}
async function writersLocked(targetEnv){
 const before=await snapshotFiles(targetEnv.DATA_DIR);
 for(const [bin,args]of [[server,['serve']],[server,['migrate']],[server,['init-admin','--username','admin']],[server,['import-seed','--seed','seed/initial-catalog.json','--media-root','src/assets/products']],[binary,['create','--data',targetEnv.DATA_DIR,'--output',join(work,'must-not-create-'+randomBytes(4).toString('hex'))]],[binary,['restore','--backup',normalPath,'--data',targetEnv.DATA_DIR]]]){
  const result=await command(bin,args,targetEnv,report);assert.equal(result.exit_code,73);assert.equal(result.output.trim(),'DATA_DIR_LOCKED');
 }
 assert.deepEqual(await snapshotFiles(targetEnv.DATA_DIR),before,'rejected writers leave every file/inode/mtime unchanged');
}
async function waitBarrier(child,name){for(let n=0;n<2000;n++){assert.ok(child.exitCode===null&&child.signalCode===null,'barrier process exited');try{const reached=JSON.parse(await readFile(join(control,'reached.json'),'utf8'));if(reached.barrier===name&&reached.pid===child.pid)return reached}catch{}await delay(20)}throw Error('backup barrier not reached')}
function pausedCLI(args){const child=spawn(tagged,args,{env:{...env,YUANFA_INTEGRATION_DIR:control},stdio:['ignore','pipe','pipe']});let output='';child.stdout.on('data',b=>output+=b);child.stderr.on('data',b=>output+=b);child.completed=new Promise((res,rej)=>{child.once('error',rej);child.once('close',(exit_code,signal)=>{const result={binary:tagged,args,pid:child.pid,exit_code,signal,output};report.commands.push(result);res(result)})});return child}
async function negativeCopy(name,mutate){const directory=join(work,name);await cp(backupPath,directory,{recursive:true});await mutate(directory);const before=await snapshotFiles(directory);const r=await command(binary,['verify','--backup',directory],env,report);assert.notEqual(r.exit_code,0);assert.deepEqual(await snapshotFiles(directory),before,'verify is read-only');return directory}
async function publishInBrowser(api,candidate){
 assert.ok(process.env.BROWSER_PATH&&process.env.PLAYWRIGHT_MODULE,'V15 browser executable/module required');
 await mkdir(join(root,'.work/browser-tmp'),{recursive:true});
 const {chromium}=await import(process.env.PLAYWRIGHT_MODULE);
 const browserServer=await chromium.launchServer({executablePath:process.env.BROWSER_PATH,headless:true,args:['--no-sandbox'],env:{...process.env,TMPDIR:'/proc/self/cwd/.work/browser-tmp'}});
 const browser=await chromium.connect(browserServer.wsEndpoint());const errors=[];
 try{
  const context=await browser.newContext({viewport:{width:390,height:900}}),page=await context.newPage();page.setDefaultTimeout(20000);page.on('pageerror',e=>errors.push(e.message));
  await page.goto(restoreBase+'/admin/login/?next='+encodeURIComponent('/admin/'));
  await page.getByLabel('用户名',{exact:true}).fill('admin');await page.getByLabel('密码',{exact:true}).fill(password);
  await Promise.all([page.waitForURL(restoreBase+'/admin/'),page.getByRole('button',{name:'登录',exact:true}).click()]);
  await page.locator('#session-state').filter({hasText:'admin'}).waitFor();
  await page.goto(new URL(candidate.review_url,restoreBase).href);
  await Promise.all([page.waitForResponse(r=>new URL(r.url()).pathname==='/api/admin/publication-requests'&&r.request().method()==='POST'&&r.status()===201),page.locator('#request-publish').click()]);
  await page.waitForURL(url=>url.pathname==='/admin/publication-requests/'&&Boolean(url.searchParams.get('id')));
  const intentID=new URL(page.url()).searchParams.get('id');
  const intent=(await api.expect('/api/admin/publication-requests/'+encodeURIComponent(intentID),{},200,'browser-created reviewed intent')).json;
  await page.locator('#request-state').filter({hasText:'待人工确认'}).waitFor();await page.locator('#reviewed-workspace').check();
  await Promise.all([page.waitForResponse(r=>new URL(r.url()).pathname==='/api/admin/publication-requests/'+intent.id+'/confirm'&&r.status()===202),page.locator('#confirm-publication').click()]);
  const published=await api.waitRequest(intent.id);assert.equal(published.state,'succeeded');await page.locator('#publication-result').filter({hasText:candidate.id}).waitFor();
  await page.screenshot({path:join(out,'restored-browser-publication.png'),fullPage:true});
  await page.goto(restoreBase+'/products/f30m/');assert.equal(await page.locator('meta[name=release-id]').getAttribute('content'),candidate.id);
  const image=page.locator('[data-gallery-panel]:not([hidden]) img').first();await image.scrollIntoViewIfNeeded();await image.evaluate(async img=>img.decode());
  await page.screenshot({path:join(out,'restored-public-product.png'),fullPage:true});assert.deepEqual(errors,[]);
  report.browser={pid:browserServer.process().pid,publication_id:published.id,release_id:candidate.id,viewport:{width:390,height:900},page_errors:errors,screenshots:['restored-browser-publication.png','restored-public-product.png']};await context.close();
 }finally{await browser.close();await browserServer.close()}
}
try{
 await initialize(server,env,report);owned=startServer(server,env,report);await live(sourceBase,owned);let api=client(sourceBase,password,report);await api.login();
 await api.expect('/',{anonymous:true},503,'empty active has no fake site');
 const upload=async bytes=>{const form=new FormData();form.append('file',new Blob([bytes]),'retained.png');return (await api.expect('/api/admin/media',{method:'POST',raw:form},201,'private retained upload')).json};
 const images=[];for(const colour of ['#314159','#271828','#161803']){const bytes=await sharp({create:{width:28,height:20,channels:3,background:colour}}).png().toBuffer();images.push({bytes,record:await upload(bytes)})}
 const draftEdit=incomplete('v15-private-'+randomBytes(6).toString('hex'));draftEdit.images=[{media_id:images[0].record.media_id,label:'private draft',description:''}];draftEdit.cover_media_id=images[0].record.media_id;
 const draft=(await api.expect('/api/admin/products',{method:'POST',body:draftEdit},201,'retain incomplete draft-exclusive media')).json.product;
 const c0=await api.waitCandidate((await api.freeze()).id);assert.equal(c0.state,'ready');await api.publish(c0);
 const seed=(await api.expect('/api/admin/products/p_f30m',{},200,'source product')).json.product;
 const newDoc={slug:'v15-public-'+randomBytes(6).toString('hex'),...editable(seed),name:'V15 recovery product',model:'V15-'+randomBytes(6).toString('hex'),visibility:'unlisted',images:[{media_id:images[2].record.media_id,label:'historical public image',description:''}],cover_media_id:images[2].record.media_id};
 let listed=(await api.expect('/api/admin/products',{method:'POST',body:newDoc},201,'create future public product')).json.product;const edit=editable(listed);edit.visibility='listed';listed=(await api.expect('/api/admin/products/'+listed.id,{method:'PUT',body:edit,headers:{'If-Match':'"'+listed.version+'"'}},200,'list historical product')).json.product;
 const c1=await api.waitCandidate((await api.freeze()).id);assert.equal(c1.state,'ready');await api.publish(c1);await api.publish(c0,'rollback');
 const candidate=await api.waitCandidate((await api.freeze()).id);assert.equal(candidate.state,'ready');const pending=await api.intent(candidate);
 const activeBytes=await readFile(join(data,'state/active.json')),active=JSON.parse(activeBytes);const lockBefore=await stat(join(data,'.yuanfa-data.lock'));
 const before=await snapshotFiles(data);const online=await command(binary,['create','--data',data,'--output',join(work,'online-rejected')],env,report);assert.equal(online.exit_code,73);assert.deepEqual(await snapshotFiles(data),before);pass('running serve excludes backup before any source/output change');
 await offline(normalPath,0);pass('real offline script stops Go, creates and verifies backup, restarts originally running service');
 const successfulBefore=await snapshotFiles(normalPath);
 for(const [kind,broken]of [['missing',undefined],['checksum',Buffer.from('corrupt original media')]]){
  const name=join(data,'media',images[1].record.sha256+'.png'),saved=join(control,'saved-'+kind+'.png');await rename(name,saved);if(broken)await writeFile(name,broken,{mode:0o600});
  try{const failed=join(work,'failed-'+kind);await offline(failed,1);await assert.rejects(lstat(join(failed,'manifest.json')),{code:'ENOENT'});assert.ok(JSON.parse(await readFile(join(failed,'failure.json'),'utf8')).error);assert.deepEqual(await snapshotFiles(normalPath),successfulBefore,'previous sealed backup unchanged');assert.ok((await readFile(join(data,'state/active.json'))).equals(activeBytes));}
  finally{if(broken)await rm(name);await rename(saved,name)}
 }
 pass('missing unassociated media and checksum failures preserve diagnostics and previous backup; original service recovers');
 await stopExternal();
 assert.equal((await command(values.go,['build','-tags=integration','-o',tagged,'./backend/cmd/yuanfa-backup'],env,report)).exit_code,0);
 report.backup_barriers=[];
 for(const barrier of ['backup-locked','backup-before-seal']){
  await writeFile(join(control,'scenario.json'),JSON.stringify({barrier}),{mode:0o600});paused=pausedCLI(['create','--data',data,'--output',join(work,barrier)]);report.backup_barriers.push(await waitBarrier(paused,barrier));await writersLocked(env);await writeFile(join(control,'continue'),'go',{mode:0o600});assert.equal((await paused.completed).exit_code,0);paused=undefined;await rm(join(control,'continue'));await rm(join(control,'reached.json'));
 }
 pass('backup lock held before SQLite and after full verification: serve/migrate/init-admin/import-seed/create/restore exit73 without changing source files');
 owned=startServer(server,env,report);await live(sourceBase,owned);api=client(sourceBase,password,report);await api.login();
 const walBytes=await sharp({create:{width:29,height:21,channels:3,background:'#424242'}}).png().toBuffer();const walMedia=await upload(walBytes);
 await stopServer(owned,'SIGKILL');owned=undefined;const wal=await stat(join(data,'db/catalog.sqlite-wal'));assert.ok(wal.size>32,'real killed serve left committed WAL pages');
 await cp(join(data,'db/catalog.sqlite'),join(control,'main-file-only.sqlite'));const immutable=readDB(join(control,'main-file-only.sqlite'));assert.ok(!immutable.media.some(x=>x.id===walMedia.media_id),'last committed media has not reached main SQLite file');
 assert.equal((await command(binary,['create','--data',data,'--output',backupPath],env,report)).exit_code,0);
 const sealed=readDB(join(backupPath,'data/db/catalog.sqlite'));assert.ok(sealed.media.some(x=>x.id===walMedia.media_id),'backup API retained committed WAL row');
 report.wal={bytes:wal.size,media_id:walMedia.media_id,main_file_had_row:false,backup_has_row:true};
 const snapCandidate=sealed.candidates.find(x=>x.id===candidate.id),privateMeta=JSON.parse(snapCandidate.validation_json);assert.equal(privateMeta.baseline_operation_id,active.operation_id);assert.equal(JSON.parse(sealed.candidates.find(x=>x.id===active.release_id).validation_json).reconciled_operation_id,active.operation_id);
 assert.equal((await command(binary,['verify','--backup',backupPath],env,report)).exit_code,0);pass('ordinary SQLite backup API includes real committed WAL, full private candidate metadata and every retained media row');
 const manifest=JSON.parse(await readFile(join(backupPath,'manifest.json'),'utf8'));assert.ok(!manifest.files.some(x=>x.path.includes('node_modules')||x.path.endsWith('-wal')||x.path.endsWith('-shm')));
 await negativeCopy('bad-checksum',async dir=>{const name=join(dir,'data/media',images[0].record.sha256+'.png');const b=await readFile(name);b[0]^=1;await writeFile(name,b)});
 await negativeCopy('missing-image',dir=>rm(join(dir,'data/media',images[1].record.sha256+'.png')));
 await negativeCopy('bad-format-schema',async dir=>{const name=join(dir,'manifest.json'),m=JSON.parse(await readFile(name,'utf8'));m.schema_version=2;await writeFile(name,JSON.stringify(m))});
 await negativeCopy('bad-database-schema',async dir=>{const dbName=join(dir,'data/db/catalog.sqlite'),db=new DatabaseSync(dbName);db.exec("UPDATE schema_migrations SET sha256=printf('%064d',0)");db.close();const name=join(dir,'manifest.json'),m=JSON.parse(await readFile(name,'utf8')),f=m.files.find(x=>x.path==='db/catalog.sqlite'),bytes=await readFile(dbName);f.sha256=hash(bytes);f.bytes=bytes.length;await writeFile(name,JSON.stringify(m))});
 const nonempty=join(work,'nonempty');await mkdir(nonempty);await writeFile(join(nonempty,'keep'),'preserve');assert.notEqual((await command(binary,['restore','--backup',backupPath,'--data',nonempty],env,report)).exit_code,0);assert.equal(await readFile(join(nonempty,'keep'),'utf8'),'preserve');pass('read-only verify rejects changed/missing payload and unsupported schema; restore never overwrites nonempty data');
 const interrupted=join(work,'interrupted');await writeFile(join(control,'scenario.json'),JSON.stringify({barrier:'restore-locked'}),{mode:0o600});paused=pausedCLI(['restore','--backup',backupPath,'--data',interrupted]);report.restore_barrier=await waitBarrier(paused,'restore-locked');await writersLocked({...env,DATA_DIR:interrupted});paused.kill('SIGTERM');assert.notEqual((await paused.completed).exit_code,0);paused=undefined;assert.ok(await lstat(join(interrupted,'.restore-incomplete')));const refused=await command(server,['serve'],{...env,DATA_DIR:interrupted},report);assert.equal(refused.exit_code,1);assert.match(refused.output,/RESTORE_INCOMPLETE/);pass('interrupted restore retains startup guard and holds target lock through copy/verification window');
 assert.equal((await command(binary,['restore','--backup',backupPath,'--data',restored],env,report)).exit_code,0);assert.deepEqual(readDB(join(restored,'db/catalog.sqlite')),sealed,'whole SQLite schema/rows/private metadata preserved');
 assert.ok((await readFile(join(restored,'state/active.json'))).equals(activeBytes));assert.equal((await stat(join(data,'.yuanfa-data.lock'))).ino,lockBefore.ino);
 const retired=join(work,'retired-source-data');await rename(data,retired);report.retired_source=retired;
 const restoredEnv={...env,DATA_DIR:restored,HTTP_ADDR:new URL(restoreBase).host,SITE_URL:restoreBase,QA_URL:restoreBase};owned=startServer(server,restoredEnv,report);await live(restoreBase,owned);api=client(restoreBase,password,report);await api.login();
 const ready=(await api.expect('/health/ready',{anonymous:true},200,'restored active ready without model')).json;assert.equal(ready.release_id,active.release_id);assert.equal(ready.model,'unconfigured');assert.deepEqual((await api.expect('/api/admin/products/'+draft.id,{},200,'restored incomplete draft')).json.product,draft);
 for(const image of [...images,{bytes:walBytes,record:walMedia}])assert.ok((await api.expect('/api/admin/media/'+image.record.media_id+'/file',{},200,'restored retained private media')).bytes.equals(image.bytes));
 await api.expect('/media/'+images[0].record.sha256+'.png',{anonymous:true},404,'draft-exclusive image stays private');await api.expect('/media/'+images[1].record.sha256+'.png',{anonymous:true},404,'unassociated image stays private');assert.ok((await api.expect('/media/'+images[2].record.sha256+'.png',{anonymous:true},200,'successful historical original survives rollback/recovery')).bytes.equals(images[2].bytes));
 const page=await api.expect('/',{anonymous:true},200,'recovered active homepage');assert.ok(page.bytes.includes(Buffer.from(active.release_id)));await api.expect('/products/'+listed.slug+'/',{anonymous:true},200,'ever-published route remains withdrawal page');
 await api.confirm(pending);assert.equal((await api.waitRequest(pending.id)).state,'succeeded','valid baseline intent survives full backup/restore');
 const current=(await api.expect('/api/admin/products/p_f30m',{},200,'edit restored workspace')).json.product;const next=editable(current);next.summary+=' V15 restored publication';await api.expect('/api/admin/products/'+current.id,{method:'PUT',body:next,headers:{'If-Match':'"'+current.version+'"'}},200,'save restored workspace');
 const nextCandidate=await api.waitCandidate((await api.freeze()).id);assert.equal(nextCandidate.state,'ready',nextCandidate.error?.code);await publishInBrowser(api,nextCandidate);const finalPage=await api.expect('/',{anonymous:true},200,'fresh publication after independent restore');assert.ok(finalPage.bytes.includes(Buffer.from(nextCandidate.id)));assert.ok((await api.expect('/media/'+images[2].record.sha256+'.png',{anonymous:true},200,'old public hash retained after fresh build')).bytes.equals(images[2].bytes));
 report.releases={baseline:c0.id,historical:c1.id,retained_candidate:candidate.id,restored_active:active.release_id,restored_operation_id:active.operation_id,fresh:nextCandidate.id};report.snapshot_path=join(restored,'artifacts',nextCandidate.id,'catalog.json');
 const children=await readdir('/proc/'+owned.pid+'/task');const descendantIDs=new Set();for(const tid of children){try{for(const pid of (await readFile('/proc/'+owned.pid+'/task/'+tid+'/children','utf8')).trim().split(/\s+/).filter(Boolean))descendantIDs.add(pid)}catch(e){if(e.code!=='ENOENT')throw e}}assert.equal(descendantIDs.size,0,'no persistent Node after actual candidate builds');report.process={server_pid:owned.pid,children_after_build:[...descendantIDs],template:restoredEnv.TEMPLATE_DIR};pass('empty-directory restore without original data starts exact active; private draft/orphan/history survive; saved intent and newly built publication both succeed; only Go remains');
 await writeFile(join(out,'backup-manifest.json'),await readFile(join(backupPath,'manifest.json')));await writeFile(join(out,'active-before-backup.json'),activeBytes);await writeFile(join(out,'active-after-fresh-publish.json'),await readFile(join(restored,'state/active.json')));report.success=true;
}catch(error){report.error=error.stack;throw error}finally{
 if(paused){paused.kill('SIGTERM');await paused.completed}
 await stopServer(owned);await stopExternal();report.processes_stopped=true;
 await writeFile(join(out,'backup-smoke.json'),JSON.stringify(report,null,2));
}
