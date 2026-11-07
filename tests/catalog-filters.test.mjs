import test from 'node:test';
import assert from 'node:assert/strict';
import { matchesCatalogItem, readCatalogState, writeCatalogState } from '../src/scripts/catalog-filters.js';

test('multiple types form a union while scene and nail constrain the results', () => {
  const state = { type: ['straight', 'steel'], scene: ['水电安装'], nail: ['steel-nail'], q: '' };
  assert.equal(matchesCatalogItem({ type: 'steel', scene: ['吊顶龙骨', '水电安装'], nail: 'steel-nail', search: 'ST38LG' }, state), true);
  assert.equal(matchesCatalogItem({ type: 'straight', scene: ['家装木工'], nail: 'brad', search: 'F30M' }, state), false);
  assert.equal(matchesCatalogItem({ type: 'gas', scene: ['水电安装'], nail: 'gas-nail', search: 'SR40A' }, state), false);
});

test('model search tolerates case, whitespace and a hyphen in a model name', () => {
  const item = { type: 'straight', scene: [], nail: 'brad', search: 'F30M F30系列 加长枪嘴' };
  for (const q of ['f30m', ' F 30 M ', 'F30-M', '加长枪嘴']) {
    assert.equal(matchesCatalogItem(item, { type: [], scene: [], nail: [], q }), true);
  }
  assert.equal(matchesCatalogItem(item, { type: [], scene: [], nail: [], q: 'CN90' }), false);
});

test('shared catalog links preserve all selected values and unrelated URL parameters', () => {
  const state = { type: ['straight', 'steel'], scene: ['吊顶龙骨'], nail: ['steel-nail'], q: 'ST38' };
  const params = writeCatalogState(state, '?ref=home&type=coil');
  assert.deepEqual(params.getAll('type'), ['straight', 'steel']);
  assert.equal(params.get('ref'), 'home');
  assert.deepEqual(readCatalogState(params), state);
  const reset = writeCatalogState({ type: [], scene: [], nail: [], q: '' }, params);
  assert.equal(reset.toString(), 'ref=home');
});
