import test from 'node:test';
import assert from 'node:assert/strict';
import { editable, mergeEdits, siteUpdateState, newProduct, newSlug, moveImage } from '../admin/src/lib/product-state.ts';

test('a previous success never acknowledges a newer save or a machine draft', () => {
  const published = { product_id: 'p1', version: 4, state: 'succeeded', message: '', public_url: '/products/a/' };
  assert.equal(siteUpdateState(published, 5), 'pending');
  assert.equal(siteUpdateState(published, 4), 'succeeded');
  assert.equal(siteUpdateState({ ...published, version: 6, state: 'draft' }, 5), 'draft');
  assert.equal(siteUpdateState(undefined, 5), 'pending');
});

test('editing strips response metadata and preserves invisible business fields', () => {
  const base = newProduct();
  base.purchase_options = [{ id: 'shop', platform: '商城', store_name: '店铺', url: 'https://example.com', match_scope: 'exact_model', sku_hint: '', enabled: true }];
  base.legacy_series_ids = ['original'];
  const doc = { ...base, id: 'p1', slug: 'original', version: 3, updated_at: '', guide_profile: { ...base.guide_profile, reviews: [{ field: 'nail_type' }] } };
  const edit = editable(doc);
  assert.deepEqual(edit, base);
  edit.purchase_options[0].store_name = 'changed';
  assert.equal(doc.purchase_options[0].store_name, '店铺');
});

test('conflict merge preserves remote hidden changes and requires a choice for overlapping visible changes', () => {
  const base = newProduct(); base.name = '原名称'; base.summary = '原简介';
  const local = structuredClone(base); local.name = '本地名称';
  const remote = structuredClone(base); remote.name = '新名称'; remote.summary = '新简介'; remote.guide_profile.consultation_notes = ['新内部资料']; remote.legacy_series_ids = ['new'];
  const result = mergeEdits(base, local, remote);
  assert.deepEqual(result.conflicts, ['name']);
  assert.equal(result.edit.summary, '新简介');
  assert.deepEqual(result.edit.guide_profile.consultation_notes, ['新内部资料']);
  assert.deepEqual(result.edit.legacy_series_ids, ['new']);
  assert.equal(mergeEdits(base, local, remote, { name: 'local' }).edit.name, '本地名称');
  assert.equal(mergeEdits(base, local, remote, { name: 'remote' }).edit.name, '新名称');
});

test('image order and cover are merged together without truncating a 20 image gallery', () => {
  const base = newProduct();
  base.images = Array.from({ length: 20 }, (_, i) => ({ media_id: `m${i}`, label: '', description: '' })); base.cover_media_id = 'm0';
  const local = structuredClone(base); moveImage(local.images, 19, 0); local.cover_media_id = 'm19';
  const remote = structuredClone(base); remote.images[0].description = 'new';
  assert.deepEqual(mergeEdits(base, local, remote).conflicts, ['images']);
  const merged = mergeEdits(base, local, remote, { images: 'local' }).edit;
  assert.equal(merged.images.length, 20); assert.equal(merged.images[0].media_id, 'm19'); assert.equal(merged.cover_media_id, 'm19');
  moveImage(merged.images, -1, 3); assert.equal(merged.images[0].media_id, 'm19');
});

test('new products get a unique valid invisible path', () => {
  const first = newSlug(); const second = newSlug();
  assert.match(first, /^[a-z0-9][a-z0-9-]{0,79}$/); assert.notEqual(first, second);
});
