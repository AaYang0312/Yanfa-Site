// Rebuild the reviewed, private-source-free migration bundle. Never imports a seed.
import assert from 'node:assert/strict';
import { readFile, writeFile, mkdir } from 'node:fs/promises';
import { resolve, basename } from 'node:path';
import { pathToFileURL } from 'node:url';
import { parseArgs } from 'node:util';
import { editableProduct, hashValue, fieldHash } from './migrate-materials.mjs';

const definitions = [
  [42,'yf1206','tool'],[43,'yf1208','tool'],[44,'yf1210','tool'],[45,'yf1206q','tool'],[46,'yf1210hl','tool'],
  [47,'f30-lithium','tool','brad'],[48,'422j-lithium','tool','staple'],[49,'1022j-lithium','tool','staple'],
  [50,'yf32','tool'],[51,'yf9000','tool'],[52,'quick-coupler','entry'],[53,'spider-workbench','entry'],
  [54,'pu-air-hose','entry'],[55,'repair-kit','entry'],[56,'original-driver-blade','entry'],
  [57,'nail-special-gas','consumable','gas-nail'],[58,'nail-high-carbon-n','consumable','staple'],
  [59,'nail-coil','consumable','coil-nail'],[60,'nail-special-k4','consumable','staple','K4系列特种码钉'],
  [61,'nail-n','consumable','staple','N系列码钉'],[62,'nail-brad','consumable','brad','F系列直钉','p_nail-brad'],
  [63,'nail-j4','consumable','staple','J4系列码钉'],[64,'nail-j10','consumable','staple','10J系列码钉'],
  [65,'nail-10f','consumable','staple','10F系列码钉'],[66,'nail-pin','consumable','pin','蚊钉','p_nail-pin'],
  [67,'nail-k','consumable','staple','K4系列码钉'],[68,'nail-steel','consumable','steel-nail','钢排钉','p_nail-steel'],
  [69,'nail-fst','consumable','steel-nail','FST无头钢排钉'],
];
const held = new Map([
  ['F系列直钉/型号图5.jpg','T38 family unresolved'],['F系列直钉/型号图6.jpg','T50 family unresolved'],
  ['F系列直钉/型号图8.jpg','F12 sales range unresolved'],['F系列直钉/型号图11.jpg','F25 sales range unresolved'],
  ['FST无头钢排钉/型号图.jpg','FST additional lengths unresolved'],
]);
const conflicts = { ST38LG:['notes'], P625G:['summary'], P635G:['summary'], SR40A:['features'], SR40B:['features'] };
const lines = text => String(text || '').replaceAll('\r\n','\n').split('\n').map(s=>s.trim()).filter(Boolean);
const hashes = p => Object.fromEntries(Object.entries(editableProduct(p)).map(([key,value])=>[key,fieldHash(key,value)]));
const blankGuide = () => ({scene_ids:[],materials:[],substrates:[],excluded_materials:[],excluded_substrates:[],power_source:null,nail_type:null,fastener_length_mm:null,load_bearing:null,long_nose:null,blower:null,limitations:[],consultation_notes:[],required_requirements:[],sources:[]});
function imageOrder(image) {
  const file=image.name.split('/')[1];
  if(file==='主图.jpg') return 0;
  if(file==='422j.jpg') return 3.5;
  const n=Number(file.match(/型号图(\d+)/)?.[1] || 0);
  // Source filenames run N21, N19, ... N9; the actual spec is the sort key.
  return image.name.startsWith('N系列码钉/')?23-n*2:n;
}
function imageLabel(image) {
  const [folder,file]=image.name.split('/');
  if(file==='主图.jpg') return '产品主图';
  if(file==='422j.jpg') return '422J 中标规格图';
  if(folder==='N系列码钉') return `N${imageOrder(image)} 规格图`;
  const n=Number(file.match(/型号图(\d+)/)?.[1]);
  const labels={
    '10J系列码钉':['1006J 中标','1008J 中标','1010J 中标','1013J 中标','1016J 中标','1022J 中标','1006J 304不锈钢','1008J 304不锈钢','1010J 304不锈钢','1013J 304不锈钢','1016J 304不锈钢','1019J 304不锈钢','1022J 304不锈钢'],
    'J4系列码钉':['413J 中标','416J 中标','419J 中标','413J 304不锈钢','419J 304不锈钢','422J 304不锈钢'],
    'K4系列码钉':['425K','432K','438K'],
    '钢排钉':['ST18','ST25','ST32','ST38','ST45','ST50','ST57','ST64'],
    'F系列直钉':['F15 中标','F20 中标','F25 中标','F30 中标',null,null,'F10 高碳钢黑色',null,'F15 高碳钢黑色','F20 高碳钢黑色',null,'F20 304不锈钢','F30 304不锈钢'],
  };
  const label=labels[folder]?.[n-1];assert.ok(label,`Unreviewed image label: ${image.name}`);
  return `${label}规格图`;
}
export function buildManifest({ comparison, workbook, products, images }) {
  assert.equal(comparison.rows.length,68); assert.equal(products.length,47); assert.equal(images.length,61);
  const sheet=workbook.find(s=>s.name==='产品中心'); assert.ok(sheet);
  const rows=new Map(comparison.rows.map(r=>[r.row,r]));
  for(const row of rows.values()) {
    const cells=sheet.rows.find(r=>r.row===row.row)?.cells;
    assert.equal(cells?.[`B${row.row}`],row.model,`Row ${row.row} model mismatch`);
    for(const [field,column] of Object.entries({series:'C',summary:'D',applications:'E',fastener:'F',features:'G',notes:'H'}))
      assert.equal((cells[`${column}${row.row}`]||'').trim(),(row.values[field]||'').trim(),`Row ${row.row} ${field} mismatch`);
  }
  const records=[];
  for(let row=2;row<=41;row++) {
    const source=rows.get(row); const product=products.find(p=>p.kind==='tool' && p.model===source.model); assert.ok(product);
    records.push({row,action:'preserve',id:product.id,slug:product.slug,kind:product.kind,baseline:hashes(product),conflict_fields:conflicts[product.model]||[]});
  }
  assert.equal(products.filter(p=>p.kind==='tool').flatMap(p=>p.images).length,67);
  const assets=[];
  for(const [row,slug,kind,nail,folder,id] of definitions) {
    const source=rows.get(row); const v=source.values; const old=id?products.find(p=>p.id===id):undefined;
    if(id) assert.equal(old?.slug,slug);
    const photos=images.filter(i=>i.name.split('/')[0]===folder && !held.has(i.name)).sort((a,b)=>imageOrder(a)-imageOrder(b));
    const attached=photos.map(i=>{
      const label=imageLabel(i);
      assets.push({sha256:i.sha256,file:`material-assets/${i.sha256}.jpg`,bytes:i.size,width:i.width,height:i.height,format:'jpeg'});
      return {sha256:i.sha256,label,description:''};
    });
    const product={kind,name:source.model,model:kind==='tool'?source.model:'',category_id:row<=56?'other':'nails',series:v.series,
      summary:v.summary,applications:lines(v.applications),features:lines(v.features),notes:lines(v.notes),fastener_text:v.fastener||'',nail_type:nail||null};
    if(!old) Object.assign(product,{legacy_series_ids:[],scene_ids:[],long_nose:false,blower:false,images:[],cover_media_id:null,visibility:'listed',sort_order:row-2,guide_profile:blankGuide(),purchase_options:[]});
    records.push({row,action:old?'reuse':'create',...(old?{id,baseline:hashes(old)}:{}),slug,product,images:attached});
  }
  const hubDefinitions=[
    ['p_nail-staple','nail-staple',['nail-high-carbon-n','nail-special-k4','nail-n','nail-j4','nail-j10','nail-10f','nail-k']],
    ['p_lithium','lithium',['f30-lithium','422j-lithium','1022j-lithium']],
    ['p_air-screwdriver','air-screwdriver',['yf1206','yf1208','yf1210','yf1206q','yf1210hl']],
    ['p_parts','parts',['quick-coupler','spider-workbench','pu-air-hose','repair-kit','original-driver-blade']],
  ];
  const hubs=hubDefinitions.map(([id,slug,members])=>{const p=products.find(p=>p.id===id);assert.equal(p?.slug,slug);return {id,slug,action:'delete-hub',baseline:hashes(p),product:{},images:[],members};});
  assert.equal(assets.length,56);
  assert.equal(held.size,5);
  const manifest={schema_version:1,migration_id:'yuanfa-materials-2026-11-09-v1',counts:{tools:50,parts:5,nails:13},records,hubs,assets,
    held_images:[...held].map(([name,reason])=>{const i=images.find(i=>i.name===name);assert.ok(i);return {sha256:i.sha256,reason};})};
  return manifest;
}
export async function prepareMaterials(source, output) {
  const read=async name=>JSON.parse(await readFile(resolve(source,name),'utf8'));
  const [comparison,workbook,products,images]=await Promise.all(['comparison.json','workbook.json','workspace-products.json','images.json'].map(read));
  const manifest=buildManifest({comparison,workbook,products,images});
  await mkdir(resolve(output,'material-assets'),{recursive:true});
  for(const asset of manifest.assets) {
    const image=images.find(i=>i.sha256===asset.sha256);
    // Resolve only the reviewed numeric extraction name, never a source absolute path.
    const local=basename(image.local.replaceAll('\\','/'));
    assert.match(local,/^\d+\.jpeg$/);
    const bytes=await readFile(resolve(source,'images',local));
    assert.equal(hashValue(bytes),asset.sha256); assert.equal(bytes.length,asset.bytes);
    await writeFile(resolve(output,asset.file),bytes);
  }
  await writeFile(resolve(output,'material-manifest.json'),JSON.stringify(manifest,null,2)+'\n');
  return {records:manifest.records.length,assets:manifest.assets.length,hubs:manifest.hubs.length};
}
if(process.argv[1] && import.meta.url===pathToFileURL(resolve(process.argv[1])).href) {
  const {values}=parseArgs({options:{source:{type:'string'},out:{type:'string',default:'scripts'}}});
  assert.ok(values.source,'--source is required');
  console.log(JSON.stringify(await prepareMaterials(values.source,values.out)));
}
