import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile, mkdir, mkdtemp, writeFile, rm } from 'node:fs/promises';
import { resolve } from 'node:path';
import { readSnapshot, publicImages } from '../scripts/audit-build.mjs';

process.env.CATALOG_SNAPSHOT = resolve('tests/fixtures/catalog/snapshot.json');
const { createCatalog, imagesOf, nailOf, scenesOf, products, productBySlug, productsIn, representativeProduct } = await import('../src/data/catalog.ts');
const fixture = JSON.parse(await readFile(process.env.CATALOG_SNAPSHOT, 'utf8'));

test('public queries exclude incomplete unlisted drafts and retain historical tombstones', () => {
  assert.equal(productBySlug('hidden-draft'), undefined);
  assert.deepEqual(products.map(product => product.id), ['p_entry', 'p_example']);
  assert.deepEqual(productsIn('straight').map(product => product.id), ['p_entry', 'p_example']);
  assert.equal(representativeProduct('straight').id, 'p_entry');
  const snapshot = structuredClone(fixture);
  snapshot.products.forEach(product => { product.visibility = 'unlisted'; });
  const view = createCatalog(snapshot);
  assert.deepEqual(view.products, []);
  assert.deepEqual(view.legacySeries[0].items, []);
  assert.deepEqual(view.withdrawnRoutes, [{ product_id: 'p_removed', slug: 'removed-tool' }]);
});

test('explicit scene and nail fields never inherit category or model guesses', () => {
  const snapshot = structuredClone(fixture);
  const item = snapshot.products.find(product => product.id === 'p_example');
  Object.assign(item, { scene_ids: [], nail_type: null, long_nose: false, blower: false, summary: '加长枪嘴 吹尘' });
  const product = createCatalog(snapshot).products.find(product => product.id === item.id);
  assert.deepEqual(scenesOf(product), []);
  assert.equal(nailOf(product), undefined);
  assert.equal(product.longNose, false);
  assert.equal(product.blower, false);
});

test('changing cover moves only that image first and preserves the remaining edited order', () => {
  const snapshot = structuredClone(fixture);
  const item = snapshot.products.find(product => product.id === 'p_example');
  const extra = { ...item.images[0], media_id: 'm_extra' };
  item.images = [extra, ...item.images];
  item.cover_media_id = item.images.at(-1).media_id;
  const product = createCatalog(snapshot).products.find(product => product.id === item.id);
  assert.deepEqual(imagesOf(product).map(image => image.media_id), [item.cover_media_id, extra.media_id, item.images[1].media_id]);
});

test('draft exclusive media cannot enlarge public resource authorization', () => {
  const snapshot = structuredClone(fixture);
  const image = { ...snapshot.products[0].images[0], media_id: 'm_secret', sha256: 'f'.repeat(64), path: 'private.png' };
  snapshot.products.find(product => product.id === 'p_hidden').images = [image];
  assert.equal(publicImages(snapshot).some(item => item.media_id === image.media_id), false);
  snapshot.products.find(product => product.id === 'p_hidden').visibility = 'listed';
  assert.equal(publicImages(snapshot).some(item => item.media_id === image.media_id), true);
});

test('snapshot inputs fail closed for missing files, unsupported schema and incomplete listed tools', async t => {
  await mkdir('.work', { recursive: true });
  const directory = await mkdtemp(resolve('.work/catalog-test-'));
  t.after(() => rm(directory, { recursive: true, force: true }));
  assert.throws(() => readSnapshot(undefined), /CATALOG_SNAPSHOT/);
  assert.throws(() => readSnapshot(resolve(directory, 'missing.json')), /ENOENT/);
  for (const change of [snapshot => { snapshot.schema_version = 999; }, snapshot => { snapshot.products[0].images = []; snapshot.products[0].cover_media_id = null; }]) {
    const snapshot = structuredClone(fixture); change(snapshot);
    const path = resolve(directory, 'invalid.json');
    await writeFile(path, JSON.stringify(snapshot));
    assert.throws(() => readSnapshot(path), error => error.status === 422);
  }
});
