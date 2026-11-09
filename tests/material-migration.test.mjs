import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { buildManifest } from '../scripts/prepare-materials.mjs';
import { editableProduct, hashValue, planExisting, runMigration, browserAdminAPI } from '../scripts/migrate-materials.mjs';
import { previewSnapshot } from '../scripts/material-preview.mjs';

const fixture = JSON.parse(await readFile(new URL('../scripts/material-manifest.json', import.meta.url)));
test('approved material mapping contains 68 details, 4 deleted generic records and only 56 accepted photos', () => {
  assert.equal(fixture.records.length, 68);
  assert.equal(fixture.records.filter(r => r.action === 'preserve').length, 40);
  assert.equal(fixture.records.filter(r => r.action === 'reuse').length, 3);
  assert.equal(fixture.hubs.length, 4);
  assert.ok(fixture.hubs.every(h=>h.action==='delete-hub'));
  assert.deepEqual(fixture.counts, { tools: 50, parts: 5, nails: 13 });
  assert.equal(fixture.assets.length, 56);
  assert.equal(fixture.held_images.length, 5);
  assert.equal(new Set(fixture.assets.map(a => a.sha256)).size, 56);
  assert.equal(fixture.records.find(r => r.slug === 'nail-j10').images.length, 14);
  assert.equal(fixture.records.find(r => r.slug === 'nail-brad').images.length, 10);
  assert.equal(fixture.records.find(r => r.slug === 'nail-n').images[0].label, 'N9 规格图');
  assert.equal(fixture.records.find(r => r.slug === '422j-lithium').product.features.length, 0);
  assert.equal(fixture.records.filter(r => r.action !== 'preserve' && !r.images.length).length, 18);
  assert.equal(fixture.records.filter(r => r.conflict_fields?.length).length, 5);
  assert.doesNotMatch(JSON.stringify(fixture), /[A-Z]:[\\/]|Wechat|\.xlsx|\.zip|"local"/i);
});

test('packaged images match their sanitized paths and do not include held photos', async () => {
  for (const asset of fixture.assets) {
    assert.match(asset.file, /^material-assets\/[a-f0-9]{64}\.jpg$/);
    const bytes = await readFile(new URL(`../scripts/${asset.file}`, import.meta.url));
    assert.equal(hashValue(bytes), asset.sha256);
    assert.ok(!fixture.held_images.some(held => held.sha256 === asset.sha256));
  }
});

test('N9 cover uses the photographed N9 file, not the reversed source filename order',()=>{
  const images=fixture.records.find(r=>r.slug==='nail-n').images;
  assert.equal(images[0].sha256,'7ef8810593b1d63169b9e08e2d86307037a75f2d0550d924115dc21951e1a8fa');
  assert.deepEqual(images.map(i=>i.label),['N9 规格图','N11 规格图','N13 规格图','N15 规格图','N17 规格图','N19 规格图','N21 规格图']);
});

test('existing edits are never overwritten, including unrelated hidden and commerce fields', () => {
  const baseline = { summary: 'old', features: [], visibility: 'listed', purchase_options: [] };
  const record = { baseline: Object.fromEntries(Object.entries(baseline).map(([k,v]) => [k,hashValue(v)])), product: { summary: 'new', features: ['material'] } };
  assert.deepEqual(planExisting(baseline, record), { status: 'update', product: { ...baseline, ...record.product }, conflicts: [] });
  for (const changed of [{ summary: 'user copy' }, { visibility: 'unlisted' }, { purchase_options: [{ url: 'https://shop.example/item' }] }]) {
    const current = { ...baseline, ...changed };
    const plan = planExisting(current, record);
    assert.equal(plan.status, 'conflict');
    assert.deepEqual(current, { ...baseline, ...changed });
    assert.ok(plan.conflicts.includes(Object.keys(changed)[0]));
  }
  assert.equal(planExisting({ ...baseline, ...record.product }, record).status, 'unchanged');
});

test('snapshot media metadata and review records are excluded from editable PUT payloads', () => {
  const edited = editableProduct({ id: 'p', slug: 'x', version: 4, images: [{ media_id: 'm', label: 'photo', description: '', sha256: 'private' }], guide_profile: { sources: [], reviews: [{ reviewed_by: 'admin' }] } });
  assert.deepEqual(edited.images, [{ media_id: 'm', label: 'photo', description: '' }]);
  assert.deepEqual(edited.guide_profile, { sources: [] });
  assert.ok(!('id' in edited));
});

function migrationAPI() {
  const products = new Map(); const media = new Map(); const calls = []; const creates = new Map();
  let nextID = 1;
  const envelope = p => ({ product: structuredClone(p), version: p.version, etag: `"${p.version}"` });
  return { products, media, calls,
    async listProducts() { return [...products.values()]; },
    async getProduct(id) { calls.push(['read', id]); if(!products.has(id)) throw Object.assign(new Error('deleted'),{status:404}); return envelope(products.get(id)); },
    async listMedia() { return [...media.values()]; },
    async uploadMedia(bytes) { const sha = hashValue(bytes); calls.push(['upload',sha]); const m = {media_id: `m_${sha}`, sha256: sha}; media.set(sha,m); return m; },
    async createProduct(body, key) {
      calls.push(['create',key,body]);
      if (creates.has(key)) return structuredClone(creates.get(key));
      if ([...products.values()].some(p => p.slug === body.slug)) throw Object.assign(new Error('slug used'), { status: 409 });
      const p = { ...body, id: `new_${nextID++}`, version: 1 }; products.set(p.id,p);
      const result = envelope(p); creates.set(key,result); return result;
    },
    async replaceProduct(id, body, etag, key) {
      calls.push(['replace',id,etag,key,body]); const p = products.get(id);
      assert.equal(etag, `"${p.version}"`);
      const updated = {...p,...body,version:p.version+1}; products.set(id,updated); return envelope(updated);
    },
    async deleteProduct(id,etag,key) {
      calls.push(['delete',id,etag,key]);const p=products.get(id);
      if(!p) throw Object.assign(new Error('deleted'),{status:404});
      assert.equal(etag,`"${p.version}"`);products.delete(id);return {deleted_id:id,workspace_revision:1};
    },
  };
}
const newRecord = () => ({ action: 'create', row: 42, slug: 'yf1206', images: [], product: {kind:'tool', name:'YF1206', model:'YF1206', category_id:'other', summary:'6H直柄风批', applications:['source purpose'], features:[], notes:[], visibility:'listed', images:[], cover_media_id:null, guide_profile:{sources:[]} } });
const smallManifest = r => ({ migration_id:'material-test-v1', records:[r], hubs:[], assets:[] });

test('dry run performs reads only; applying then rerunning creates one detail and preserves later user edits', async () => {
  const api = migrationAPI(); const manifest = smallManifest(newRecord());
  await runMigration({ manifest, api }); assert.equal(api.calls.length,0);
  await runMigration({ manifest, api, apply:true });
  assert.equal(api.products.size,1); assert.equal([...api.products.values()][0].visibility,'listed');
  assert.deepEqual(api.calls.map(c=>c[0]),['create','read']);
  api.calls.length = 0;
  await runMigration({ manifest, api, apply:true });
  assert.ok(!api.calls.some(c=>['replace','upload'].includes(c[0])));
  const p = [...api.products.values()][0]; p.summary='user copy'; p.visibility='unlisted'; p.version++;
  const report = await runMigration({ manifest, api, apply:true });
  assert.equal(p.summary,'user copy'); assert.equal(p.visibility,'unlisted');
  assert.equal(report.conflicts.length,1);
});

test('stale optimistic versions stop instead of retrying over a concurrent edit', async () => {
  const api = migrationAPI(); api.products.set('old',{id:'old',slug:'nail-steel',summary:'old',visibility:'listed',version:8});
  const record = {action:'reuse',id:'old',slug:'nail-steel',images:[],baseline:{summary:hashValue('old'),visibility:hashValue('listed')},product:{summary:'new'}};
  api.replaceProduct = async () => { throw Object.assign(new Error('conflict'), {status:412}); };
  const report = await runMigration({manifest:smallManifest(record),api,apply:true});
  assert.equal(report.conflicts.length,1); assert.equal(api.products.get('old').summary,'old');
});

test('uploads deduplicate by hash and reuse an uploaded image after interrupted product creation', async () => {
  const bytes = Buffer.from('media bytes'); const hash=hashValue(bytes); const record=newRecord();
  record.images=[{sha256:hash,label:'source',description:''}];
  const manifest=smallManifest(record); manifest.assets=[{sha256:hash,file:`material-assets/${hash}.jpg`}];
  const api=migrationAPI(); api.media.set(hash,{media_id:'existing_media',sha256:hash});
  await runMigration({manifest,api,apply:true,readAsset:async()=>bytes});
  assert.ok(!api.calls.some(c=>c[0]==='upload'));
  assert.equal([...api.products.values()][0].cover_media_id,'existing_media');
});

test('all 47 baseline records migrate to 68 details and rerun without any duplicate or overwrite', async () => {
  const base=JSON.parse(await readFile(new URL('../seed/initial-catalog.json',import.meta.url)));
  const api=migrationAPI();
  for(const p of base.products) api.products.set(p.id,structuredClone(p));
  const original=structuredClone(base.products.filter(p=>p.kind==='tool'));
  const readAsset=a=>readFile(new URL(`../scripts/${a.file}`,import.meta.url));
  const report=await runMigration({manifest:fixture,api,apply:true,readAsset});
  assert.deepEqual(report.conflicts,[]);
  assert.equal(report.created.length,25);assert.equal(report.updated.length,3);assert.equal(report.deleted.length,4);
  assert.equal([...api.products.values()].filter(p=>p.visibility==='listed').length,68);
  for(const p of original) assert.deepEqual(api.products.get(p.id),p);
  api.calls.length=0;
  const rerun=await runMigration({manifest:fixture,api,apply:true,readAsset});
  assert.deepEqual(rerun.conflicts,[]);
  assert.equal(api.calls.filter(c=>c[0]!=='read').length,0);
  assert.equal(api.products.size,68);
  const preview=previewSnapshot(base,fixture);
  assert.equal(preview.products.length,68);
  assert.equal(preview.products.filter(p=>p.visibility==='listed').length,68);
  for(const p of original) assert.deepEqual(preview.products.find(v=>v.id===p.id),p);
});

test('mapped generic hubs delete only after all detailed members exist; changed hubs and stale versions are preserved',async()=>{
  const hub={action:'delete-hub',id:'p_parts',slug:'parts',product:{},images:[],members:['quick-coupler'],baseline:{summary:hashValue('original'),visibility:hashValue('listed')}};
  const manifest={migration_id:'delete-test',records:[],hubs:[hub],assets:[]};
  const api=migrationAPI();api.products.set(hub.id,{id:hub.id,slug:hub.slug,summary:'original',visibility:'listed',version:4});
  let report=await runMigration({manifest,api,apply:true});assert.equal(report.conflicts.length,1);assert.equal(api.calls.filter(c=>c[0]==='delete').length,0);
  api.products.set('member',{id:'member',slug:'quick-coupler',visibility:'listed',version:1});
  api.products.get(hub.id).summary='user change';
  report=await runMigration({manifest,api,apply:true});assert.deepEqual(report.conflicts[0].fields,['summary']);assert.ok(api.products.has(hub.id));
  api.products.get(hub.id).summary='original';
  const deleting=api.deleteProduct;api.deleteProduct=async()=>{throw Object.assign(new Error('stale'),{status:412});};
  report=await runMigration({manifest,api,apply:true});assert.equal(report.conflicts.length,1);assert.ok(api.products.has(hub.id));
  api.deleteProduct=deleting;
  report=await runMigration({manifest,api,apply:true});assert.deepEqual(report.deleted,['parts']);assert.ok(!api.products.has(hub.id));
  const deletion=api.calls.find(c=>c[0]==='delete');assert.equal(deletion[2],'"4"');assert.match(deletion[3],/^delete-test:parts:delete:/);
  report=await runMigration({manifest,api,apply:true});assert.deepEqual(report.unchanged,['parts']);assert.equal(report.conflicts.length,0);
  report=await runMigration({manifest:smallManifest({...hub,action:'reuse',slug:'missing-detail'}),api,apply:true});assert.equal(report.conflicts.length,1);assert.deepEqual(report.unchanged,[]);
});

test('browser DELETE sends the current strong version, CSRF, Origin and stable operation key without a JSON body',async()=>{
  const requests=[];
  const api=browserAdminAPI('http://127.0.0.1:18081','yuanfa_session=test-only',{fetch:async(url,init)=>{
    requests.push({url:String(url),...init});
    return new Response(JSON.stringify(String(url).endsWith('/session')?{csrf_token:'csrf-test'}:{deleted_id:'p_parts',workspace_revision:2}),{status:200,headers:{'Content-Type':'application/json'}});
  }});
  await api.deleteProduct('p_parts','"9"','stable-key');
  const request=requests[1];assert.equal(request.url,'http://127.0.0.1:18081/api/admin/products/p_parts');assert.equal(request.method,'DELETE');
  for(const [header,value] of Object.entries({'If-Match':'"9"','X-CSRF-Token':'csrf-test',Origin:'http://127.0.0.1:18081','Idempotency-Key':'stable-key'})) assert.equal(request.headers.get(header),value);
  assert.equal(request.body,undefined);assert.equal(request.redirect,'error');
});
