// Incremental browser-admin API migration. Dry-run by default. No seed import or live invocation.
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { resolve, dirname } from 'node:path';
import { pathToFileURL, fileURLToPath } from 'node:url';
import { parseArgs } from 'node:util';

const editFields=['kind','name','model','category_id','series','legacy_series_ids','summary','applications','features','notes','scene_ids','nail_type','fastener_text','long_nose','blower','images','cover_media_id','visibility','sort_order','guide_profile','purchase_options'];
function canonical(value) {
  if(Array.isArray(value)) return value.map(canonical);
  if(value && typeof value==='object') return Object.fromEntries(Object.keys(value).sort().map(k=>[k,canonical(value[k])]));
  return value;
}
export const hashValue=value=>createHash('sha256').update(Buffer.isBuffer(value)?value:JSON.stringify(canonical(value))).digest('hex');
export function fieldHash(field,value) {
  if(field==='guide_profile' && value) {
    value={...value,sources:value.sources?.filter(source=>source.kind!=='product_text')};
  }
  return hashValue(value);
}
export function editableProduct(product) {
  const result=Object.fromEntries(editFields.filter(k=>k in product).map(k=>[k,structuredClone(product[k])]));
  if(result.images) result.images=result.images.map(({media_id,label,description})=>({media_id,label,description}));
  if(result.guide_profile) delete result.guide_profile.reviews;
  return result;
}
// A changed record is left entirely untouched. Never silently publish a mixture
// of reviewed material and later user/MCP edits, including fields absent in the form.
export function planExisting(current, record) {
  const edit=editableProduct(current); const desired={...edit,...record.product};
  const conflicts=Object.entries(record.baseline||{}).filter(([field,hash])=>{
    if(fieldHash(field,edit[field])===hash) return false;
    return !(field in record.product && hashValue(edit[field])===hashValue(record.product[field]));
  }).map(([field])=>field);
  if(conflicts.length) return {status:'conflict',conflicts};
  return {status:hashValue(edit)===hashValue(desired)?'unchanged':'update',product:desired,conflicts:[]};
}
function productMatches(current, desired) {
  const edit=editableProduct(current);
  return Object.entries(desired).every(([field,value])=>{
    // The service refreshes text source digests; they are not an imported safety review.
    if(field==='guide_profile') {
      const a={...edit[field]}, b={...value}; delete a.sources; delete b.sources;
      return hashValue(a)===hashValue(b);
    }
    return hashValue(edit[field])===hashValue(value);
  });
}
const statusOf=error=>error.status;
export async function runMigration({manifest,api,apply=false,readAsset,onEvent=()=>{}}) {
  const report={dry_run:!apply,preserved:[],created:[],updated:[],deleted:[],unchanged:[],planned:[],conflicts:[]};
  const index=await api.listProducts(); const bySlug=new Map(index.map(p=>[p.slug,p]));
  const media=new Map((await api.listMedia()).map(m=>[m.sha256,m]));
  const available=new Set(index.filter(p=>p.visibility==='listed').map(p=>p.slug));
  const key=(record,operation,body)=>`${manifest.migration_id}:${record.slug}:${operation}:${hashValue(body).slice(0,24)}`;
  const recordConflict=(record,fields,reason)=>{const conflict={slug:record.slug,fields,reason};report.conflicts.push(conflict);onEvent({status:'conflict',...conflict});};
  async function readHub(id) {
    try {return await api.getProduct(id);} catch(error) {if(statusOf(error)===404) return null;throw error;}
  }
  async function attach(record,upload) {
    const result=structuredClone(record.product);
    if(!record.images?.length) return result;
    const attached=[];
    for(const image of record.images) {
      let uploaded=media.get(image.sha256);
      if(!uploaded && upload) {
        const asset=manifest.assets.find(a=>a.sha256===image.sha256); assert.ok(asset,'Missing approved asset');
        assert.match(asset.file,/^material-assets\/[a-f0-9]{64}\.jpg$/);
        const bytes=await readAsset(asset); assert.equal(hashValue(bytes),image.sha256,'Asset SHA mismatch');
        uploaded=await api.uploadMedia(bytes,`${image.sha256}.jpg`);
        assert.equal(uploaded.sha256,image.sha256,'Media API changed image digest'); media.set(image.sha256,uploaded);
      }
      attached.push({media_id:uploaded?.media_id || `m_${image.sha256}`,label:image.label,description:image.description});
    }
    result.images=attached; result.cover_media_id=attached[0].media_id;
    return result;
  }
  for(const record of [...manifest.records,...manifest.hubs]) {
    if(record.action==='preserve') { report.preserved.push(record.slug);continue; }
    try {
      if(record.action==='delete-hub') {
        // Only the four explicitly mapped original IDs may be business-deleted.
        assert.ok(manifest.hubs.some(hub=>hub.id===record.id && hub.slug===record.slug));
        let latest=await readHub(record.id);
        if(!latest) {report.unchanged.push(record.slug);continue;}
        if(latest.product.slug!==record.slug) {recordConflict(record,['slug'],'Permanent route ownership mismatch');continue;}
        let plan=planExisting(latest.product,{...record,product:{}});
        if(plan.status==='conflict') {recordConflict(record,plan.conflicts,'Changed generic record retained');continue;}
        const members=apply?new Set((await api.listProducts()).filter(p=>p.visibility==='listed').map(p=>p.slug)):available;
        if(!record.members.every(slug=>members.has(slug) && !report.conflicts.some(c=>c.slug===slug))) {
          recordConflict(record,[],'Family details are not all listed; original hub retained');continue;
        }
        if(!apply) {report.planned.push({slug:record.slug,action:'delete-hub'});continue;}
        latest=await readHub(record.id);
        if(!latest) {report.unchanged.push(record.slug);continue;}
        if(latest.product.slug!==record.slug) {recordConflict(record,['slug'],'Permanent route ownership mismatch');continue;}
        plan=planExisting(latest.product,{...record,product:{}});
        if(plan.status==='conflict') {recordConflict(record,plan.conflicts,'Concurrent generic edits retained');continue;}
        try {await api.deleteProduct(record.id,latest.etag,key(record,'delete',{id:record.id,version:latest.version}));}
        catch(error) {if(statusOf(error)===404) {report.unchanged.push(record.slug);continue;}throw error;}
        report.deleted.push(record.slug);
      } else if(record.action==='create') {
        const known=bySlug.get(record.slug);
        if(known) {
          const latest=await api.getProduct(known.id); const desired=await attach(record,false);
          if(productMatches(latest.product,desired)) {report.unchanged.push(record.slug);available.add(record.slug);continue;}
        }
        if(!apply) {report.planned.push({slug:record.slug,action:known?'check-create-ownership':'create'});available.add(record.slug);continue;}
        const desired=await attach(record,true);
        // Replaying this exact key recovers the original create response after a
        // lost connection. A same-slug pre-existing product must fail, not be adopted.
        const create={slug:record.slug,...desired};
        const created=await api.createProduct(create,key(record,'create',create));
        const latest=await api.getProduct(created.product.id);
        if(latest.product.slug!==record.slug) throw Error('Created product route mismatch');
        if(!productMatches(latest.product,desired)) {
          recordConflict(record,[],'Created record changed after creation; current edits preserved');continue;
        }
        report.created.push(record.slug);available.add(record.slug);
      } else {
        let latest=await api.getProduct(record.id);
        if(latest.product.slug!==record.slug) {recordConflict(record,['slug'],'Permanent route ownership mismatch');continue;}
        let desired=await attach(record,false);
        let plan=planExisting(latest.product,{...record,product:desired});
        if(plan.status==='conflict') {recordConflict(record,plan.conflicts,'Current fields differ from reviewed baseline');continue;}
        if(plan.status==='unchanged') {report.unchanged.push(record.slug);continue;}
        if(!apply) {report.planned.push({slug:record.slug,action:record.action});continue;}
        desired=await attach(record,true);
        // Uploads can take time. Read and compare again immediately before PUT.
        latest=await api.getProduct(record.id);
        if(latest.product.slug!==record.slug) {recordConflict(record,['slug'],'Permanent route ownership mismatch');continue;}
        plan=planExisting(latest.product,{...record,product:desired});
        if(plan.status==='conflict') {recordConflict(record,plan.conflicts,'Concurrent edits preserved');continue;}
        if(plan.status==='unchanged') {report.unchanged.push(record.slug);continue;}
        await api.replaceProduct(record.id,plan.product,latest.etag,key(record,'replace',{version:latest.version,product:plan.product}));
        report.updated.push(record.slug);
      }
      onEvent({slug:record.slug,status:apply?'saved':'planned'});
    } catch(error) {
      if([404,409,412,422].includes(statusOf(error))) {recordConflict(record,[],`API ${error.status}: ${error.message}`);continue;}
      throw error; // Unknown/network failure stops; rerun with the same keys to recover.
    }
  }
  return report;
}

export function browserAdminAPI(baseURL,cookie,{fetch:fetcher=fetch}={}) {
  const origin=new URL(baseURL);
  assert.ok(origin.protocol==='https:' || (origin.protocol==='http:' && ['localhost','127.0.0.1','[::1]'].includes(origin.hostname)),'Use HTTPS or a loopback local instance');
  assert.ok(!origin.username && !origin.password && origin.pathname==='/' && !origin.search && !origin.hash,'--base-url must be an origin');
  assert.ok(cookie && !/[\r\n]/.test(cookie),'MATERIAL_ADMIN_COOKIE is required');
  let csrf;
  async function request(path,{method='GET',body,headers={}}={}) {
    const h=new Headers({Cookie:cookie,...headers});
    if(method!=='GET') {if(!csrf) csrf=(await request('/api/admin/session')).data.csrf_token; h.set('X-CSRF-Token',csrf);h.set('Origin',origin.origin);}
    if(body && !(body instanceof FormData)) {h.set('Content-Type','application/json');body=JSON.stringify(body);}
    const response=await fetcher(new URL(path,origin),{method,headers:h,body,redirect:'error',cache:'no-store'});
    const data=response.status===204?null:await response.json();
    if(!response.ok) throw Object.assign(new Error(data?.error?.message||`Request failed (${response.status})`),{status:response.status});
    return {data,etag:response.headers.get('etag')};
  }
  async function list(path) {
    const items=[];let cursor;const seen=new Set();
    do {const q=new URLSearchParams({limit:'100',...(cursor?{cursor}:{})});const {data}=await request(`${path}?${q}`);items.push(...data.items);cursor=data.next_cursor;
      if(cursor){assert.ok(!seen.has(cursor),'Repeated pagination cursor');seen.add(cursor);}
    }while(cursor);return items;
  }
  const envelope=({data,etag})=>{assert.equal(etag,`"${data.version}"`,'Missing or inconsistent strong ETag');return {...data,etag};};
  return {
    listProducts:()=>list('/api/admin/products'),listMedia:()=>list('/api/admin/media'),
    getProduct:async id=>envelope(await request(`/api/admin/products/${encodeURIComponent(id)}`)),
    createProduct:async(body,key)=>envelope(await request('/api/admin/products',{method:'POST',body,headers:{'Idempotency-Key':key}})),
    replaceProduct:async(id,body,etag,key)=>envelope(await request(`/api/admin/products/${encodeURIComponent(id)}`,{method:'PUT',body,headers:{'If-Match':etag,'Idempotency-Key':key}})),
    deleteProduct:async(id,etag,key)=>(await request(`/api/admin/products/${encodeURIComponent(id)}`,{method:'DELETE',headers:{'If-Match':etag,'Idempotency-Key':key}})).data,
    uploadMedia:async(bytes,name)=>{const body=new FormData();body.set('file',new Blob([bytes],{type:'image/jpeg'}),name);return (await request('/api/admin/media',{method:'POST',body})).data;},
  };
}
if(process.argv[1] && import.meta.url===pathToFileURL(resolve(process.argv[1])).href) {
  const {values}=parseArgs({options:{'base-url':{type:'string'},manifest:{type:'string',default:fileURLToPath(new URL('./material-manifest.json',import.meta.url))},apply:{type:'boolean',default:false}}});
  assert.ok(values['base-url'],'--base-url is required');
  const manifest=JSON.parse(await readFile(values.manifest,'utf8'));
  const report=await runMigration({manifest,api:browserAdminAPI(values['base-url'],process.env.MATERIAL_ADMIN_COOKIE),apply:values.apply,
    readAsset:asset=>readFile(resolve(dirname(values.manifest),asset.file)),onEvent:event=>console.error(JSON.stringify(event))});
  console.log(JSON.stringify(report,null,2));
  if(report.conflicts.length) process.exitCode=2;
}
