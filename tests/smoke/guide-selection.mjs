// V11 runs the real production selection consumer with MODEL_* absent. Facts
// and .test commerce mappings are isolated fixtures, not customer acceptance.
import assert from 'node:assert/strict';
import {readFile, writeFile, mkdir} from 'node:fs/promises';
import {resolve, join} from 'node:path';
import {parseArgs} from 'node:util';
import {randomBytes} from 'node:crypto';
import {client, editable, incomplete, hash, passwordFile, startServer, stopServer, live} from './publication.mjs';
import {decodeContract} from '../../src/generated/validate-contract.mjs';

const {values} = parseArgs({options:{base:{type:'string'},out:{type:'string'}}});
assert.ok(values.base && values.out, '--base and --out required');
const base = values.base, out = resolve(values.out);
await mkdir(out, {recursive:true});
const report = {success:false, checks:[], http:[], commands:[], results:{}, guide_results:[], scope:'Production no-model selection; synthetic reviewed facts and commerce hosts are local rule fixtures.'};
const scenario = JSON.parse(await readFile('tests/fixtures/guide/selection.json', 'utf8'));
const api = client(base, await passwordFile(), report);
const pass = label => {report.checks.push(label); console.log('PASS ' + label)};
const endpoint = '/api/guide/messages';
let owned;
const get = async id => (await api.expect('/api/admin/products/'+id, {}, 200, 'read workspace')).json.product;
const save = async (p, edit) => (await api.expect('/api/admin/products/'+p.id, {method:'PUT', headers:{'If-Match':'"'+p.version+'"'}, body:edit}, 200, 'save fixture')).json.product;
const publish = async () => {const candidate = await api.waitCandidate((await api.freeze()).id); assert.equal(candidate.state, 'ready', candidate.error?.code); await api.publish(candidate); return candidate};
const review = async (doc, label) => {
 const facts=scenario.reviewed_fields.map(field=>({field,fact_sha256:hash(JSON.stringify(doc.guide_profile[field])),source_sha256s:[doc.guide_profile.sources[0].sha256]}));
 return (await api.expect('/api/admin/products/'+doc.id+'/guide-review',{method:'POST',headers:{'If-Match':'"'+doc.version+'"'},body:{version:doc.version,facts}},200,label)).json.product;
};
const guideResponse = async (body, status, label, headers={}) => {
 const r=await api.expect(endpoint,{method:'POST',anonymous:true,headers,body},status,label);
 assert.equal(r.headers.get('cache-control'),'no-store');
 assert.match(r.headers.get('content-type'),/^application\/json/);
 if(r.status===200||r.status===503) {
  r.json=decodeContract('guide.schema.json#/$defs/GuideResult',r.bytes.toString('utf8'));
  if(r.json.state==='no_match') {
   assert.equal(r.json.selection,null);assert.equal(r.json.question,null);assert.deepEqual(r.json.recommendations,[]);
  } else assert.deepEqual(r.json.conflicts,[],'non-no_match must have explicit empty conflicts');
  report.guide_results.push({label,status:r.status,raw:r.bytes.toString('utf8'),decoded:r.json});
 }
 return r;
};
const supplyConflict = result => assert.ok(result.conflicts.some(reason=>reason.includes('压缩空气')&&reason.includes('没有可用气源')),'actual absence of compressed air must be explained');
const select = async (releaseID, productID, requirements, status=200, label='select') => {
 const r = await guideResponse({release_id:releaseID, selected_product_id:productID, requirements},status,label,{Accept:'text/event-stream'});
 assert.equal(r.headers.get('cache-control'), 'no-store');
 assert.match(r.headers.get('content-type'), /^application\/json/);
 if(status===200) {
  assert.equal(r.json.release_id,releaseID); assert.equal(r.json.contact_href,site.contact_href);
  assert.deepEqual(Object.keys(r.json.confirmed_requirements).sort(),Object.keys(scenario.requirements).sort());
  assert.deepEqual(r.json.recommendations,[]);
  if(r.json.state==='selected') assert.deepEqual(r.json.missing_fields,r.json.selection.missing_fields);
  for(const forbidden of ['reviewed_by','reviewed_at','fact_sha256','source_sha256s','private_reference','T11_PRIVATE']) assert.ok(!r.bytes.includes(Buffer.from(forbidden)),forbidden+' leaked');
 }
 return r.json;
};
const site = JSON.parse(await readFile('src/generated/site-config.json','utf8'));
try {
 for(const key of Object.keys(process.env).filter(key=>/^MODEL_/i.test(key))) assert.ok(!process.env[key], 'V11 requires absent '+key);
 if(process.env.SMOKE_MANAGE_SERVER==='1') {owned=startServer(resolve(process.env.SMOKE_YUANFA_BINARY||'.work/bin/yuanfa'),{...process.env,SITE_URL:base,HTTP_ADDR:new URL(base).host},report); await live(base,owned)} else await live(base);
 await api.login();
 const draft=incomplete('t11-private-'+randomBytes(6).toString('hex')); draft.name='T11_PRIVATE_PRODUCT';
 const privateProduct=(await api.expect('/api/admin/products',{method:'POST',body:draft},201,'private draft')).json.product;
 let p=await get('p_f30m'), edit=editable(p);
 edit.guide_profile=structuredClone(scenario.profile);
 edit.guide_profile.sources=[{id:'t11_source',kind:'product_text',reference:'/summary',sha256:hash(JSON.stringify(p.summary)),public_label:'隔离选型规则来源',public_url:'https://docs.example.test/public/t11'}];
 edit.purchase_options=structuredClone(scenario.options);
 p=await save(p,edit);
 p=await review(p,'browser-authorized exact fact review');
 // A separate reviewed product exercises duplicate reasons without changing the
 // original profile, required conditions or purchase mappings used by V11.
 const tag=randomBytes(6).toString('hex');
 const duplicateEdit={slug:'t11-conflicts-'+tag,...editable(p),name:'隔离重复原因夹具',model:'T11-C-'+tag,visibility:'unlisted',legacy_series_ids:[],purchase_options:[],summary:'隔离规则：钢板既不可作为材料，也不可作为基材；气动工具需要现场气源。'};
 duplicateEdit.guide_profile=structuredClone(scenario.profile);
 duplicateEdit.guide_profile.excluded_substrates=['钢板'];
 duplicateEdit.guide_profile.sources=[{id:'t11_duplicate_source',kind:'product_text',reference:'/summary',sha256:hash(JSON.stringify(duplicateEdit.summary)),public_label:'隔离重复原因规则来源',public_url:null}];
 let duplicate=(await api.expect('/api/admin/products',{method:'POST',body:duplicateEdit},201,'create independent duplicate-reason rule fixture')).json.product;
 const listedDuplicate=editable(duplicate);listedDuplicate.visibility='listed';duplicate=await save(duplicate,listedDuplicate);
 duplicate=await review(duplicate,'review duplicate material/substrate exclusion facts');
 const otherID='p_f30cl'; let other=await get(otherID), otherEdit=editable(other);
 otherEdit.purchase_options=[{...structuredClone(scenario.options[0]),url:'https://item.example.test/product/t11-other',store_name:'另一型号规则店'}]; other=await save(other,otherEdit);
 const a=await publish();
 assert.equal((await api.expect('/health/ready',{anonymous:true},200,'ready without model')).json.model,'unconfigured');
 const req=structuredClone(scenario.requirements);
 const unknown=await select(a.id,p.id,req,200,'G12 air=null');
 assert.equal(unknown.state,'selected'); assert.equal(unknown.selection.match_level,'needs_confirmation');
 assert.equal(unknown.confirmed_requirements.air_supply,null);
 assert.deepEqual(unknown.missing_fields,['air_supply']); assert.deepEqual(unknown.selection.missing_fields,['air_supply']);
 assert.equal(unknown.question.field,'air_supply'); assert.match(unknown.question.text,/气源|压缩空气/,'missing air supply is asked in the actionable question');
 assert.equal(unknown.selection.purchase_status,'available');
 assert.deepEqual(unknown.selection.purchase_options.map(x=>x.purchase_option_id),p.purchase_options.filter(x=>x.enabled).map(x=>x.id));
 assert.equal(unknown.selection.purchase_options[1].match_scope,'listing_with_variants'); assert.equal(unknown.selection.purchase_options[1].sku_hint,scenario.options[1].sku_hint);
 assert.equal(unknown.selection.model,p.model); assert.equal(unknown.selection.name,p.name);
 assert.ok(unknown.selection.image?.url.startsWith('/media/'));
 const detail=(await api.expect('/api/catalog/products/'+p.id+'?release_id='+a.id,{anonymous:true},200,'same public detail')).json.product;
 assert.deepEqual(unknown.selection.purchase_options,detail.purchase_options);
 assert.ok(unknown.selection.matched_facts.some(x=>x.field==='materials'&&x.sources.some(s=>s.public_label==='隔离选型规则来源')));
 assert.ok((await api.expect(unknown.selection.detail_url,{anonymous:true},200,'model-independent detail')).bytes.includes(Buffer.from(a.id)));
 await api.expect(unknown.selection.image.url,{anonymous:true},200,'selected published image');
 report.results.air_null=unknown;
 const denied=await select(a.id,p.id,{...req,air_supply:false},200,'G13 air=false');
 assert.equal(denied.state,'no_match'); assert.equal(denied.selection,null); assert.deepEqual(denied.recommendations,[]); assert.equal(denied.confirmed_requirements.air_supply,false);
 supplyConflict(denied);
 report.results.air_false=denied;
 const mixed=await select(a.id,p.id,{...req,air_supply:false,material:null},200,'conflict outranks missing material');
 assert.equal(mixed.state,'no_match'); assert.equal(mixed.selection,null); assert.deepEqual(mixed.missing_fields,['material']);
 supplyConflict(mixed);report.results.air_false_material_null=mixed;
 const independent=await select(a.id,p.id,{...req,air_supply:true,material:null},200,'G13 true only clears air gap');
 assert.equal(independent.state,'selected'); assert.equal(independent.selection.match_level,'needs_confirmation'); assert.deepEqual(independent.missing_fields,['material']);
 assert.equal(independent.question.field,'material'); assert.equal(independent.selection.purchase_status,'available');
 report.results.air_true_material_null=independent;
 const unknownMaterial=await select(a.id,p.id,{...req,air_supply:true,material:'实木'},200,'unknown exact material remains profile gap');
 assert.equal(unknownMaterial.selection.match_level,'needs_confirmation'); assert.deepEqual(unknownMaterial.missing_fields,['guide_profile.materials']);
 report.results.air_true_profile_gap=unknownMaterial;
 const matched=await select(a.id,p.id,{...req,air_supply:true,material:' 木材 '},200,'normalized exact material');
 assert.equal(matched.selection.match_level,'matched'); assert.equal(matched.confirmed_requirements.material,'木材'); assert.deepEqual(matched.missing_fields,[]); assert.equal(matched.question,null);
 const booleans=await select(a.id,p.id,{...req,load_bearing:null,long_nose:null,blower:null},200,'unknown optional booleans stay null');
 for(const field of ['air_supply','load_bearing','long_nose','blower']) assert.equal(booleans.confirmed_requirements[field],null);
 pass('G12/G13: no-model air null/false/true, independent visitor/profile gaps, full requirements and server purchase channels');
 const multiple=await select(a.id,p.id,{...req,material:'钢板',substrate:'混凝土',nail_type:null,air_supply:false},200,'material/substrate/supply conflicts retain unknown nail type');
 supplyConflict(multiple);
 assert.deepEqual(multiple.missing_fields,['nail_type']);
 assert.ok(multiple.conflicts[0].includes('钢板'));assert.ok(multiple.conflicts[1].includes('混凝土'));assert.ok(multiple.conflicts[2].includes('压缩空气'));
 report.results.multiple_conflicts=multiple;
 const duplicateRequirements={...req,material:'钢板',substrate:'钢板',air_supply:false};
 const repeated=await select(a.id,duplicate.id,duplicateRequirements,200,'duplicate exclusions preserve both causes before supply');
 supplyConflict(repeated);
 assert.deepEqual(repeated.conflicts.map(reason=>reason.includes('钢板')?'excluded':reason.includes('压缩空气')?'supply':reason),['excluded','excluded','supply']);
 assert.equal(repeated.conflicts[0],repeated.conflicts[1]);assert.deepEqual(repeated.missing_fields,[]);
 const repeatedAgain=await select(a.id,duplicate.id,duplicateRequirements,200,'same release and requirements keep duplicate order');
 assert.deepEqual(repeatedAgain,repeated);report.results.duplicate_conflicts=repeated;
 pass('G13 conflicts: concrete supply/material reasons, stable order and duplicate exclusions survive without altering missing fields');
 for(const length of [10,30]) assert.equal((await select(a.id,p.id,{...req,air_supply:true,fastener_length_mm:length},200,'closed length boundary '+length)).selection.match_level,'matched');
 for(const change of [{fastener_length_mm:9.999},{fastener_length_mm:30.001},{material:'钢板'},{substrate:'混凝土'},{nail_type:'steel-nail'},{scene_ids:['wall-mesh']},{load_bearing:true},{blower:true}]) {
  const r=await select(a.id,p.id,{...req,air_supply:true,...change},200,'hard conflict '+JSON.stringify(change)); assert.equal(r.state,'no_match'); assert.equal(r.selection,null);
 }
 pass('G02: reviewed material/substrate/power/feature conflicts and closed length boundaries exclude purchase selections');
 const valid={release_id:a.id,selected_product_id:p.id,requirements:req};
 const invalid=async (body,field,label) => {const r=await api.expect(endpoint,{method:'POST',anonymous:true,body},422,label); assert.equal(r.json.error.code,'VALIDATION_FAILED');if(field)assert.ok(r.json.error.fields.some(path=>path===field||path.startsWith(field+'.')),JSON.stringify(r.json));};
 for(const field of Object.keys(req)) {const v=structuredClone(valid);delete v.requirements[field];await invalid(v,'requirements.'+field,'missing '+field)}
 for(const field of ['release_id','selected_product_id','requirements']) {const v=structuredClone(valid);delete v[field];await invalid(v,field,'missing envelope '+field)}
 for(const [field,value] of [['scene_ids',null],['scene_ids',['home-woodworking','home-woodworking']],['scene_ids',['invented']],['material','   '],['material','木'.repeat(101)],['substrate',true],['nail_type','invented'],['fastener_length_mm',0],['fastener_length_mm',-1],['fastener_length_mm','20'],['air_supply',0],['load_bearing','false'],['long_nose',[]],['blower',{}],['match_level','matched'],['missing_fields',[]],['purchase_options',[]],['sources',[]],['conflicts',['forged']]]) await invalid({...valid,requirements:{...req,[field]:value}},'requirements.'+field,'invalid requirement '+field);
 for(const field of ['message','messages','context_product_id','match_level','missing_fields','url','purchase_options','model','conflicts']) await invalid({...valid,[field]:'forged'},field,'reject derived/consult field '+field);
 for(const number of ['1e309','1e-9999']) {const raw=JSON.stringify(valid).replace('"fastener_length_mm":20','"fastener_length_mm":'+number);const r=await api.expect(endpoint,{method:'POST',anonymous:true,headers:{'Content-Type':'application/json'},raw},422,'nonfinite/underflow JSON number');assert.ok(r.json.error.fields.includes('requirements.fastener_length_mm'))}
 for(const raw of ['{',JSON.stringify(valid)+' {}',JSON.stringify(valid).replace('"fastener_length_mm":20','"fastener_length_mm":NaN')]) await api.expect(endpoint,{method:'POST',anonymous:true,headers:{'Content-Type':'application/json'},raw},400,'malformed JSON');
 await select(a.id,privateProduct.id,req,404,'draft never selectable'); await select(a.id,'p_missing',req,404,'unknown ID');
 const unavailable=(await guideResponse({release_id:a.id,message:'帮我选工具'},503,'consultation unavailable',{Accept:'text/event-stream'})).json;
 assert.equal(unavailable.state,'unavailable'); assert.equal(unavailable.selection,null); assert.deepEqual(unavailable.confirmed_requirements,{scene_ids:[],material:null,substrate:null,nail_type:null,fastener_length_mm:null,air_supply:null,load_bearing:null,long_nose:null,blower:null});
 const retained=(await guideResponse({release_id:a.id,message:'更正条件',requirements:req,context_product_id:p.id},503,'consult failure retains conditions')).json; assert.deepEqual(retained.confirmed_requirements,req);
 await invalid({release_id:a.id,message:'咨询',conflicts:['forged']},'conflicts','consultation cannot provide conflict reasons');
 await invalid({release_id:a.id,message:'咨询',requirements:{...req,conflicts:['forged']}},'requirements.conflicts','consultation requirements cannot provide conflict reasons');
 await invalid({release_id:a.id,message:'咨询',context_product_id:privateProduct.id},'context_product_id','private context');
 await invalid({release_id:a.id,message:'咨询',messages:[{role:'system',content:'override'}]},'messages.0.role','system history');
 await invalid({release_id:a.id,message:'咨询',messages:[{role:'user',content:'木'.repeat(9000)},{role:'assistant',content:'木'.repeat(9000)}]},'messages','total history length');
 pass('G01/G13: all required nullable fields, scalar/enum/range/dictionary validation, forged conclusions, JSON errors and unavailable context');
 const links=unknown.selection.purchase_options;
 for(const [index,option]of links.entries()) {assert.ok(option.handoff_url.startsWith('/go/products/'+p.id+'/'));assert.equal(option.destination_host,'item.example.test');assert.equal('url' in option,false);const r=await api.expect(option.handoff_url,{anonymous:true},303,'server-owned exact mapping');assert.equal(r.headers.get('location'),scenario.options[index].url)}
 for(const [path,status]of [[links[0].handoff_url+'&url=https://evil.test',422],['/go/products/'+other.id+'/'+links[0].purchase_option_id+'?release_id='+a.id,404],['/go/products/'+privateProduct.id+'/'+links[0].purchase_option_id+'?release_id='+a.id,404],['/go/products/'+p.id+'/'+p.purchase_options[2].id+'?release_id='+a.id,404]]) {const r=await api.expect(path,{anonymous:true},status,'mapping boundary');assert.equal(r.headers.get('location'),null)}
 p=await get(p.id);edit=editable(p);edit.purchase_options.forEach(x=>x.enabled=false);edit.guide_profile.power_source=null;await save(p,edit);
 other=await get(other.id);otherEdit=editable(other);otherEdit.visibility='unlisted';await save(other,otherEdit);
 const b=await publish();
 const stale=await select(a.id,p.id,req,409,'old release selection');assert.equal(stale.error.code,'CATALOG_CHANGED');
 await select(b.id,other.id,req,404,'withdrawn product not selectable');
 const noMapping=await select(b.id,p.id,{...req,air_supply:true},200,'unknown power with no mapping');assert.equal(noMapping.state,'selected');assert.equal(noMapping.selection.match_level,'needs_confirmation');assert.ok(noMapping.missing_fields.includes('guide_profile.power_source'));assert.equal(noMapping.selection.purchase_status,'unavailable');assert.deepEqual(noMapping.selection.purchase_options,[]);
 const unknownPowerAbsentAir=await select(b.id,p.id,{...req,air_supply:false},200,'unknown power and absent air remain uncertainty');
 assert.equal(unknownPowerAbsentAir.state,'selected');assert.equal(unknownPowerAbsentAir.selection.match_level,'needs_confirmation');assert.deepEqual(unknownPowerAbsentAir.conflicts,[]);assert.deepEqual(unknownPowerAbsentAir.missing_fields,['guide_profile.power_source']);assert.equal(unknownPowerAbsentAir.confirmed_requirements.air_supply,false);
 report.results.unknown_power_air_false=unknownPowerAbsentAir;
 const unknownPowerExcludedMaterial=await select(b.id,p.id,{...req,material:'钢板',air_supply:false},200,'known material conflict retains unknown power gap');
 assert.equal(unknownPowerExcludedMaterial.state,'no_match');assert.deepEqual(unknownPowerExcludedMaterial.missing_fields,['guide_profile.power_source']);assert.ok(unknownPowerExcludedMaterial.conflicts.every(reason=>reason.includes('钢板')));
 report.results.unknown_power_material_conflict=unknownPowerExcludedMaterial;
 for(const option of links) {assert.equal((await api.expect(option.handoff_url,{anonymous:true},409,'old mapping requires refresh')).headers.get('location'),null);assert.equal((await api.expect(option.handoff_url.replace(a.id,b.id),{anonymous:true},404,'disabled current mapping')).headers.get('location'),null)}
 report.results.current_unknown_power=noMapping;report.releases={reviewed:a.id,changed:b.id};report.snapshot_sha256=b.snapshot_sha256;report.schema_version=1;
 pass('G10/release: exact current mappings only, draft/hidden/foreign/disabled targets denied, old release409 and missing mappings remain selected');
 report.success=true;
} catch(error) {report.error=error.stack;throw error} finally {try {await stopServer(owned)} finally {await writeFile(join(out,'guide-selection-smoke.json'),JSON.stringify(report,null,2))}}
