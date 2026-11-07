// V07 exercises production HTTP against real seed imports and real T05 builds.
// The .test commerce addresses are safety fixtures; no external shop is visited.
import assert from 'node:assert/strict';
import {readFile,writeFile,mkdir} from 'node:fs/promises';
import {resolve,join} from 'node:path';
import {parseArgs} from 'node:util';
import {randomBytes} from 'node:crypto';
import {setTimeout as delay} from 'node:timers/promises';
import {client,editable,incomplete,hash,passwordFile,startServer,stopServer,live} from './publication.mjs';

const {values}=parseArgs({options:{base:{type:'string'},out:{type:'string'}}});
assert.ok(values.base&&values.out,'--base and --out required');
const base=values.base,out=resolve(values.out),root=process.cwd();await mkdir(out,{recursive:true});
const report={success:false,checks:[],http:[],commands:[],scope:'Local production catalog and deterministic rules; fixture hosts are not customer commerce evidence.'};
const pass=label=>{report.checks.push(label);console.log('PASS '+label)};
let owned,browser;
const seed=JSON.parse(await readFile('seed/initial-catalog.json','utf8'));
const scenario=JSON.parse(await readFile('tests/fixtures/public-catalog/scenario.json','utf8'));
const secret=await passwordFile();
const api=client(base,secret,report);
const anonymous=async(path,status=200,label=path)=>{
 const r=await api.expect(path,{anonymous:true},status,label);assert.equal(r.headers.get('cache-control'),'no-store',label+' cache');return r;
};
const product=async id=>(await api.expect('/api/admin/products/'+id,{},200,'workspace product')).json.product;
const save=async(p,e)=>(await api.expect('/api/admin/products/'+p.id,{method:'PUT',headers:{'If-Match':'"'+p.version+'"'},body:e},200,'save workspace without public mutation')).json.product;
const build=async()=>{const c=await api.waitCandidate((await api.freeze()).id);assert.equal(c.state,'ready',c.error?.code);return c};
const checkExport=(r,releaseID)=>{
 const e=r.json;
 assert.equal(e.schema_version,1);if(releaseID)assert.equal(e.release_id,releaseID);
 assert.deepEqual(e.categories,seed.categories);assert.deepEqual(e.scenes,seed.scenes);assert.deepEqual(e.nail_types,seed.nail_types);
 assert.ok(e.products.every(p=>p.release_id===e.release_id&&p.schema_version===1));
 assert.equal(new Set(e.products.map(p=>p.product_id)).size,e.products.length);
 for(const forbidden of ['workspace_revision','snapshot_sha256','reviewed_by','reviewed_at','fact_sha256','source_sha256s','original_filename','T07_PRIVATE_'])assert.ok(!r.bytes.includes(Buffer.from(forbidden)),forbidden+' leaked');
 return e;
};
const compareExport=async(releaseID)=>checkExport(await anonymous('/api/catalog/export'+(releaseID?'?release_id='+releaseID:'')),releaseID);
const concurrentExport=async(action,label)=>{
 let done=false;const ids=[];
 const changing=(async()=>{try{await action()}finally{done=true}})();
 do{const r=await api.request('/api/catalog/export',{anonymous:true});report.http.push({method:'GET',path:'/api/catalog/export',status:r.status,label:'export while '+label});assert.equal(r.headers.get('cache-control'),'no-store');if(r.status===409){assert.equal(r.json.error.code,'CATALOG_CHANGED')}else{assert.equal(r.status,200);ids.push(checkExport(r).release_id)}await delay(10)}while(!done);
 await changing;ids.push((await compareExport()).release_id);report[label]={observed_release_ids:[...new Set(ids)],consistent_exports:ids.length};
};
try{
 for(const key of ['MODEL_PROVIDER','MODEL_BASE_URL','MODEL_MODEL','MODEL_API_KEY'])assert.ok(!process.env[key],'V07 requires no model configuration: '+key);
 if(process.env.SMOKE_MANAGE_SERVER==='1'){owned=startServer(resolve('.work/bin/yuanfa'),{...process.env,SITE_URL:base,HTTP_ADDR:new URL(base).host},report);await live(base,owned)}else await live(base);
 await anonymous('/api/catalog/release',503);await anonymous('/api/catalog/export',503);await api.login();
 const suffix=randomBytes(6).toString('hex');
 const form=new FormData();form.append('file',new Blob([await readFile('tests/fixtures/media/valid.png')]),'private.png');
 const privateImage=(await api.expect('/api/admin/media',{method:'POST',raw:form},201,'private draft image')).json;
 const draft=incomplete('t07-private-'+suffix);draft.name='T07_PRIVATE_DRAFT';draft.guide_profile.materials=['T07_PRIVATE_MATERIAL'];draft.images=[{media_id:privateImage.media_id,label:'T07_PRIVATE_IMAGE',description:''}];draft.cover_media_id=privateImage.media_id;
 const privateProduct=(await api.expect('/api/admin/products',{method:'POST',body:draft},201,'unpublished draft')).json.product;
 const a=await build();await api.publish(a);
 const initial=await compareExport(a.id);const expected=seed.products.filter(p=>p.visibility==='listed').sort((a,b)=>a.sort_order-b.sort_order||a.id.localeCompare(b.id));
 assert.deepEqual(initial.products.map(p=>p.product_id),expected.map(p=>p.id));
 for(const p of initial.products){const original=expected.find(x=>x.id===p.product_id);assert.equal(p.model,original.model);assert.equal(p.summary,original.summary);assert.equal(p.match_level,'needs_confirmation');assert.deepEqual(p.guide_profile.materials,[]);assert.equal(p.guide_profile.power_source,null);assert.deepEqual(p.purchase_options,[]);assert.deepEqual(p.guide_profile.sources,[])}
 const release=(await anonymous('/api/catalog/release')).json;assert.equal(release.release_id,a.id);assert.equal((await anonymous('/health/ready')).json.model,'unconfigured');
 const first=await anonymous('/api/catalog/products?limit=3');let page=first.json,all=[...page.items];
 while(page.next_cursor){page=(await anonymous('/api/catalog/products?limit=3&release_id='+a.id+'&cursor='+encodeURIComponent(page.next_cursor))).json;all.push(...page.items)}
 assert.deepEqual(all.map(p=>p.product_id),expected.map(p=>p.id));
 for(const p of initial.products){const detail=(await anonymous('/api/catalog/products/'+p.product_id+'?release_id='+a.id)).json;assert.deepEqual(detail.product,p);assert.equal(detail.release_id,a.id)}
 const chosen=expected.find(p=>p.id==='p_f30m'),other=expected.find(p=>p.kind==='tool'&&p.id!==chosen.id);assert.ok(chosen&&other);
 assert.ok((await anonymous('/api/catalog/products?q=F_3-0%20M')).json.items.some(p=>p.product_id===chosen.id));
 assert.equal((await anonymous('/api/catalog/products?q=F_3-0%20M&include_unknown=false')).json.items[0].match_level,'needs_confirmation');
 assert.deepEqual((await anonymous('/api/catalog/products?q=F30M&material='+encodeURIComponent('木材'))).json.items,[]);
 const unknown=(await anonymous('/api/catalog/products?q=F30M&material='+encodeURIComponent('木材')+'&include_unknown=true')).json.items.find(p=>p.product_id===chosen.id);assert.ok(unknown.missing_fields.includes('guide_profile.materials'));
 await anonymous('/api/catalog/products/'+privateProduct.id,404);await anonymous('/media/'+privateImage.sha256+'.png',404);
 const noMapping=(await anonymous('/api/catalog/products/'+chosen.id+'/purchase-options?release_id='+a.id)).json;assert.equal(noMapping.purchase_status,'unavailable');assert.deepEqual(noMapping.purchase_options,[]);
 await anonymous('/api/catalog/products/'+chosen.id+'/purchase-options',422);
 for(const query of ['limit=0','limit=101','limit=1&limit=2','scene=unknown','category=unknown','nail_type=unknown','power_source=unknown','fastener_length_mm=NaN','fastener_length_mm=0','fastener_length_mm=1e309','include_unknown=1','url=https://evil.test','material=%20%20'])await anonymous('/api/catalog/products?'+query,422);
 await anonymous('/api/catalog/products?limit=3&category=steel&cursor='+encodeURIComponent(first.json.next_cursor),409);
 pass('P13/G11: no-model anonymous seed browse, normalized keyword, full stable paging, detail/export identity and strict queries; unreviewed products remain browsable');
 let p=await product(chosen.id),e=editable(p);e.summary+=' T07 draft-only edit';e.guide_profile=structuredClone(scenario.profile);e.guide_profile.sources=[{id:'t07_source',kind:'product_text',reference:'/summary',sha256:hash(JSON.stringify(e.summary)),public_label:'隔离审核来源',public_url:'https://docs.example.test/public/t07'}];e.purchase_options=structuredClone(scenario.options);p=await save(p,e);
 const facts=scenario.reviewed_fields.map(field=>({field,fact_sha256:hash(JSON.stringify(p.guide_profile[field])),source_sha256s:[p.guide_profile.sources[0].sha256]}));
 p=(await api.expect('/api/admin/products/'+p.id+'/guide-review',{method:'POST',headers:{'If-Match':'"'+p.version+'"'},body:{version:p.version,facts}},200,'review exact current fixture facts')).json.product;
 let second=await product(other.id),secondEdit=editable(second);secondEdit.purchase_options=[{...structuredClone(scenario.options[0]),url:'https://item.example.test/product/t07-other',store_name:'另一个商品渠道'}];second=await save(second,secondEdit);
 const old=await compareExport(a.id);assert.equal(old.products.find(x=>x.product_id===p.id).summary,chosen.summary);
 const b=await build();await api.expect('/api/admin/preview-session',{method:'POST',body:{candidate_id:b.id}},200,'bind real candidate preview');
 for(const path of ['/api/catalog/export','/api/catalog/products','/api/catalog/products/'+p.id,'/api/catalog/products/'+p.id+'/purchase-options?release_id='+a.id]){const r=await api.expect(path,{},200,'admin+preview still active public projection');assert.equal(r.json.release_id,a.id);assert.ok(!r.bytes.includes(Buffer.from('T07 draft-only edit')));assert.ok(!r.bytes.includes(Buffer.from('t07-a')))}
 assert.equal((await api.expect('/api/catalog/products/'+privateProduct.id,{},404,'preview cannot expose draft')).status,404);
 if(process.env.BROWSER_PATH&&process.env.PLAYWRIGHT_MODULE){
  const {chromium}=await import(process.env.PLAYWRIGHT_MODULE);await mkdir('.work/browser-tmp',{recursive:true});browser=await chromium.launch({executablePath:process.env.BROWSER_PATH,headless:true,args:['--no-sandbox'],env:{...process.env,TMPDIR:'/proc/self/cwd/.work/browser-tmp'}});
  const context=await browser.newContext({viewport:{width:390,height:844}});await context.addCookies([...api.cookies()].map(([name,value])=>({name,value,url:base,httpOnly:true,sameSite:'Strict'})));
  const page=await context.newPage();await page.goto(base+'/products/'+p.slug+'/');await page.locator('#yuanfa-preview').waitFor({state:'visible'});assert.equal(await page.locator('meta[name=release-id]').getAttribute('content'),b.id);
  const current=await page.evaluate(async id=>{const r=await fetch('/api/catalog/products/'+id);return {status:r.status,data:await r.json()}},p.id);assert.equal(current.status,200);assert.equal(current.data.release_id,a.id);assert.equal(current.data.product.summary,chosen.summary);
  await page.screenshot({path:join(out,'preview-active-public-api-390.png'),fullPage:true});await context.close();report.browser={preview_release:b.id,public_api_release:a.id,viewport:{width:390,height:844}};
 }
 await api.expect('/api/admin/preview-session',{method:'DELETE'},200,'leave preview');
 await concurrentExport(()=>api.publish(b),'publish_exports');
 await anonymous('/api/catalog/products?limit=3&cursor='+encodeURIComponent(first.json.next_cursor),409);
 for(const path of ['/api/catalog/products/'+p.id,'/api/catalog/export','/api/catalog/products/'+p.id+'/purchase-options']){const r=await anonymous(path+'?release_id='+a.id,409);assert.equal(r.json.error.code,'CATALOG_CHANGED')}
 const published=await compareExport(b.id),publicProduct=published.products.find(x=>x.product_id===p.id);assert.deepEqual(publicProduct.guide_profile.materials,['木材']);assert.equal(publicProduct.guide_profile.power_source,'pneumatic');assert.deepEqual(publicProduct.guide_profile.consultation_notes,[]);assert.ok(publicProduct.guide_profile.unknown_fields.includes('consultation_notes'));assert.ok(publicProduct.guide_profile.sources.every(s=>Object.keys(s).sort().join(',')==='field,public_label,public_url'));
 for(const length of [10,30]){const result=(await anonymous('/api/catalog/products?q=F30M&material='+encodeURIComponent('木材')+'&power_source=pneumatic&nail_type=brad&fastener_length_mm='+length)).json;assert.equal(result.items[0].product_id,p.id);assert.ok(result.items[0].missing_fields.includes('air_supply'));assert.equal(result.items[0].match_level,'needs_confirmation');assert.ok(result.items[0].matched_facts.some(f=>f.field==='fastener_length_mm'))}
 for(const filter of ['fastener_length_mm=9.999','fastener_length_mm=30.001','material='+encodeURIComponent('钢板'),'power_source=electric','nail_type=steel-nail','scene=wall-mesh'])assert.deepEqual((await anonymous('/api/catalog/products?q=F30M&include_unknown=true&'+filter)).json.items,[]);
 assert.deepEqual((await anonymous('/api/catalog/products?q=F30M&material='+encodeURIComponent('实木'))).json.items,[]);assert.equal((await anonymous('/api/catalog/products?q=F30M&material='+encodeURIComponent('实木')+'&include_unknown=true')).json.items[0].match_level,'needs_confirmation');
 const options=(await anonymous('/api/catalog/products/'+p.id+'/purchase-options?release_id='+b.id)).json;assert.equal(options.purchase_status,'available');assert.deepEqual(options.purchase_options.map(x=>x.purchase_option_id),p.purchase_options.filter(x=>x.enabled).map(x=>x.id));assert.equal(options.purchase_options[1].match_scope,'listing_with_variants');assert.equal(options.purchase_options[1].sku_hint,scenario.options[1].sku_hint);assert.deepEqual(options.purchase_options,publicProduct.purchase_options);
 for(const [i,option]of options.purchase_options.entries()){const response=await anonymous(option.handoff_url,303);assert.equal(response.headers.get('location'),scenario.options[i].url)}
 const activeLink=options.purchase_options[0].handoff_url;
 for(const [path,status]of [[activeLink+'&url=https://evil.test',422],['/go/products/'+other.id+'/'+p.purchase_options[0].id+'?release_id='+b.id,404],['/go/products/'+privateProduct.id+'/'+p.purchase_options[0].id+'?release_id='+b.id,404],['/go/products/'+p.id+'/'+p.purchase_options[2].id+'?release_id='+b.id,404]]){const response=await anonymous(path,status);assert.equal(response.headers.get('location'),null)}
 for(const url of ['http://item.example.test/product/a','https://item.example.test.evil/product/a','https://user:pass@item.example.test/product/a','https://short.example.test/a','https://item.example.test/redirect?url=https://evil.test','https://item.example.test/product/a?target=https://evil.test','https://item.example.test:444/product/a','https://item.example.test/','javascript:alert(1)']){const e=editable(p);e.purchase_options[0].url=url;await api.expect('/api/admin/products/'+p.id,{method:'PUT',headers:{'If-Match':'"'+p.version+'"'},body:e},422,'bad destination rejected before publication')}
 pass('G02/G03/G05/G12/G13 component boundaries: reviewed facts only, closed nail-length bounds, exact names, conflicts exclude even with unknowns, air gap remains; no invented product or URL');
 pass('G09/G10: ordered multi-store/variant hints, absent/disabled/cross-product/private mapping rejection, exact-host HTTPS targets and no arbitrary redirect parameter');
 const pageB=(await anonymous('/api/catalog/products?limit=3')).json;
 p=await product(p.id);e=editable(p);e.purchase_options=[e.purchase_options[0]];e.purchase_options[0].enabled=false;await save(p,e);
 second=await product(other.id);secondEdit=editable(second);secondEdit.visibility='unlisted';await save(second,secondEdit);
 const c=await build();await api.publish(c);
 const noOptions=(await anonymous('/api/catalog/products/'+p.id+'/purchase-options?release_id='+c.id)).json;assert.equal(noOptions.purchase_status,'unavailable');assert.deepEqual(noOptions.purchase_options,[]);
 for(const option of options.purchase_options){const response=await anonymous(option.handoff_url,409);assert.equal(response.headers.get('location'),null);assert.ok(response.bytes.includes(Buffer.from('重新查看')));const now=option.handoff_url.replace(b.id,c.id);assert.equal((await anonymous(now,404)).headers.get('location'),null)}
 await anonymous('/api/catalog/products/'+other.id,404);await anonymous('/go/products/'+other.id+'/'+second.purchase_options[0].id+'?release_id='+c.id,404);
 assert.ok(!(await compareExport(c.id)).products.some(x=>x.product_id===other.id));
 if(browser){const context=await browser.newContext({viewport:{width:320,height:844}});const page=await context.newPage();const stale=await page.goto(base+activeLink);assert.equal(stale.status(),409);await page.locator('a[href="/products/"]').focus();await page.keyboard.press('Enter');await page.waitForURL(base+'/products/');assert.equal(await page.locator('meta[name=release-id]').getAttribute('content'),c.id);await page.goto(base+activeLink);await page.screenshot({path:join(out,'changed-handoff-320.png'),fullPage:true});const missing=await page.goto(base+activeLink.replace(b.id,c.id));assert.equal(missing.status(),404);assert.ok(await page.locator('a[href^="tel:"]').isVisible());await page.screenshot({path:join(out,'disabled-handoff-320.png'),fullPage:true});report.browser.handoff={old_status:409,disabled_status:404,keyboard_current_catalog_release:c.id};await context.close()}
 await concurrentExport(()=>api.publish(a,'rollback'),'rollback_exports');
 const reverted=await compareExport(a.id);assert.deepEqual(reverted.products,initial.products);
 for(const path of ['/api/catalog/products?limit=3&cursor='+encodeURIComponent(pageB.next_cursor),activeLink]){const response=await anonymous(path,409);assert.equal(response.headers.get('location'),null)}
 const workspace=await product(p.id);assert.equal(workspace.purchase_options[0].enabled,false);assert.equal((await product(other.id)).visibility,'unlisted');
 pass('P14/G09/G10/G11: publish/rollback exports stay one release, old detail/cursor/handoff returns 409, current revoked/withdrawn returns 404 and rollback preserves drafts');
 report.releases={initial:a.id,reviewed:b.id,revoked:c.id,final:a.id};report.private_product_id=privateProduct.id;report.snapshot_sha256=a.snapshot_sha256;report.schema_version=1;report.success=true;
} catch(error){report.error=error.stack;throw error}finally{
 try{if(browser)await browser.close();await stopServer(owned)}finally{await writeFile(join(out,'catalog-smoke.json'),JSON.stringify(report,null,2))}
}
