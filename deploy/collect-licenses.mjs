// Regenerate notices with the existing npm/Go locks; never resolves new versions.
import assert from 'node:assert/strict';
import {readFile,writeFile,readdir,mkdir,stat} from 'node:fs/promises';
import {resolve,join} from 'node:path';
import {createHash} from 'node:crypto';
import {spawnSync} from 'node:child_process';
const root=process.cwd(),out=join(root,'licenses/dependencies');
await mkdir(join(out,'texts'),{recursive:true});
const sha=bytes=>createHash('sha256').update(bytes).digest('hex');
async function notices(dir){
 const result=[];
 for(const name of (await readdir(dir)).sort()){
  if(!/^(license|licence|copying|notice)(\b|[._-])/i.test(name)||!(await stat(join(dir,name))).isFile())continue;
  const raw=await readFile(join(dir,name)),digest=sha(raw),target='texts/'+digest+'.txt';
  await writeFile(join(out,target),raw);result.push({name,file:target,sha256:digest});
 }
 return result;
}
const lock=JSON.parse(await readFile('package-lock.json','utf8')),npm=[];
for(const [path,entry]of Object.entries(lock.packages)){
 if(!path)continue;
 let pkg;try{pkg=JSON.parse(await readFile(join(root,path,'package.json'),'utf8'))}catch(e){if(e.code==='ENOENT'&&entry.optional)continue;throw e}
 assert.equal(pkg.version,entry.version,'Installed version differs from lock: '+path);
 npm.push({name:pkg.name,version:pkg.version,path,license:pkg.license??entry.license??null,resolved:entry.resolved,integrity:entry.integrity,notices:await notices(join(root,path))});
}
const env={...process.env,GOTOOLCHAIN:'local',GOPROXY:'https://goproxy.cn',GOSUMDB:'sum.golang.org'};
for(const key of ['HTTP_PROXY','HTTPS_PROXY','ALL_PROXY','http_proxy','https_proxy','all_proxy','npm_config_proxy','npm_config_https_proxy'])delete env[key];
const result=spawnSync(process.env.GO_BIN,['mod','download','-json'],{env,encoding:'utf8',maxBuffer:32*1024*1024});
process.stderr.write(result.stderr??'');assert.equal(result.status,0,'go mod download');
// Go emits a stream of independent objects.
const modules=JSON.parse('['+result.stdout.trim().replace(/}\s*\n\s*{/g,'},{')+']');
const go=[];
for(const m of modules){assert.ok(!m.Error,m.Error);go.push({path:m.Path,version:m.Version,sum:m.Sum,go_mod_sum:m.GoModSum,notices:await notices(m.Dir)})}
let supplements=[];
try{supplements=JSON.parse(await readFile(join(out,'supplements.json'),'utf8'))}catch(error){if(error.code!=='ENOENT')throw error}
for(const [kind,packages]of [['npm',npm],['go',go]])for(const pkg of packages){
 const key=kind+':'+(pkg.name??pkg.path)+'@'+pkg.version;
 pkg.notices.push(...supplements.filter(x=>x.key===key).map(({key,...notice})=>notice));
}
const inventory={npm_lock_sha256:sha(await readFile('package-lock.json')),go_mod_sha256:sha(await readFile('go.mod')),go_sum_sha256:sha(await readFile('go.sum')),npm,go,native_licenses:supplements.filter(x=>x.key==='native-common-license')};
await writeFile(join(out,'inventory.json'),JSON.stringify(inventory,null,2)+'\n');
console.log(JSON.stringify({npm_packages:npm.length,go_modules:go.length,inventory:'licenses/dependencies/inventory.json',missing_notice:npm.filter(x=>!x.notices.length).map(x=>({name:x.name,version:x.version,license:x.license})),go_missing_notice:go.filter(x=>!x.notices.length).map(x=>({path:x.path,version:x.version}))},null,2));
