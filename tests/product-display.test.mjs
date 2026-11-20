import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';
process.env.CATALOG_SNAPSHOT=resolve('tests/fixtures/catalog/snapshot.json');
const { createCatalog, catalogCounts, searchTextOf }=await import('../src/data/catalog.ts');
const source=JSON.parse(await readFile(process.env.CATALOG_SNAPSHOT,'utf8'));

test('historical family routes use only listed members and keep original route ownership',()=>{
  const snapshot=structuredClone(source);const base=snapshot.products[0];
  snapshot.products=[{...base,id:'p_lithium',slug:'lithium',kind:'entry',visibility:'unlisted'},
    {...base,id:'new-f30',slug:'f30-lithium',kind:'tool'}, {...base,id:'hidden-j4',slug:'422j-lithium',visibility:'unlisted'}];
  snapshot.previously_published_routes=[{product_id:'p_lithium',slug:'lithium'}];
  const view=createCatalog(snapshot);
  assert.equal(view.products.length,1);
  assert.equal(view.familyHubs[0].slug,'lithium');
  assert.deepEqual(view.familyHubs[0].items.map(p=>p.id),['new-f30']);
  assert.equal(view.withdrawnRoutes.length,0);
  snapshot.previously_published_routes[0].product_id='unrelated';
  assert.equal(createCatalog(snapshot).familyHubs.length,0);
});

test('deleted never-published seed hubs retain explicit family paths without appearing in the admin snapshot',()=>{
  const snapshot=structuredClone(source);const base=snapshot.products[0];
  snapshot.products=['f30-lithium','422j-lithium','1022j-lithium'].map((slug,i)=>({...base,id:`new${i}`,slug,kind:'tool',visibility:'listed'}));
  snapshot.previously_published_routes=[];
  assert.equal(createCatalog(snapshot).familyHubs.find(f=>f.slug==='lithium').items.length,3);
  snapshot.products.push({...base,id:'different-owner',slug:'lithium',visibility:'unlisted'});
  assert.equal(createCatalog(snapshot).familyHubs.length,0);
  snapshot.products.pop();snapshot.previously_published_routes=[{product_id:'different-owner',slug:'lithium'}];
  assert.equal(createCatalog(snapshot).familyHubs.length,0);
});

test('product totals distinguish tools, accessory entries and consumables',()=>{
  assert.deepEqual(catalogCounts([{kind:'tool'},{kind:'entry'},{kind:'consumable'},{kind:'tool'}]),{tools:2,parts:1,nails:1});
});

test('SR40C has one listed short URL and retains both addresses after withdrawal', () => {
  const snapshot = structuredClone(source);
  const item = {
    ...snapshot.products[0],
    id: 'p_7cfc5ab6236e2a90458d68b24dde0423',
    slug: 'product-78ef705cdffb4cfe82e40e4ff642dd5d',
    visibility: 'listed',
  };
  snapshot.products = [item];
  snapshot.previously_published_routes = [{ product_id: item.id, slug: item.slug }];
  assert.deepEqual(createCatalog(snapshot).products.map(p => p.slug), ['sr40c']);
  assert.deepEqual(createCatalog(snapshot).shortLink, { from: item.slug, to: 'sr40c' });
  item.visibility = 'unlisted';
  assert.deepEqual(createCatalog(snapshot).withdrawnRoutes.map(r => r.slug), [item.slug, 'sr40c']);
  snapshot.previously_published_routes = [];
  assert.deepEqual(createCatalog(snapshot).withdrawnRoutes, []);
  item.visibility = 'listed';
  snapshot.products.push({ ...item, id: 'another-owner', slug: 'sr40c' });
  assert.equal(createCatalog(snapshot).shortLink, undefined);
  assert.deepEqual(createCatalog(snapshot).products.map(p => p.slug).sort(), [item.slug, 'sr40c'].sort());
});

test('search finds applications and notes on consumables and accessories',()=>{
  const product=createCatalog(source).products[0];
  product.kind='consumable';product.applications=['适用钉枪：目录外FST型号'];product.notes=['操作前确认作业条件'];
  assert.match(searchTextOf(product),/目录外FST型号/);
  assert.match(searchTextOf(product),/操作前确认作业条件/);
});
