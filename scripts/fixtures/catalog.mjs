import assert from 'node:assert/strict';
import { readFile, mkdir, realpath, copyFile, writeFile, lstat } from 'node:fs/promises';
import { resolve, dirname, sep } from 'node:path';
import { parseArgs } from 'node:util';
import { createRequire } from 'node:module';
import { readSnapshot, sha256 } from '../audit-build.mjs';
import { validateContract } from '../../src/generated/validate-contract.mjs';

const { values } = parseArgs({ options: { seed: { type: 'string' }, media: { type: 'string' }, out: { type: 'string' } } });
assert.ok(values.seed && values.media && values.out, 'Usage: node scripts/fixtures/catalog.mjs --seed <snapshot> --media <root> --out <new-directory>');
const seed = readSnapshot(values.seed);
const mediaRoot = await realpath(values.media);
const out = resolve(values.out);
try { await lstat(out); assert.fail('Fixture output must not exist'); } catch (error) { if (error.code !== 'ENOENT') throw error; }
const tool = seed.products.find(product => product.kind === 'tool' && product.images.length >= 3);
assert.ok(tool, 'Fixture seed needs a tool with at least three images');
const sharp = createRequire(new URL('../../package.json', import.meta.url))('sharp');
const privateBytes = await sharp({ create: { width: 93, height: 71, channels: 3, background: '#174d32' } }).png().toBuffer();
const digest = sha256(privateBytes);
const privateImage = { media_id: `m_${digest}`, sha256: digest, format: 'png', bytes: privateBytes.length, width: 93, height: 71, path: 'draft-only.png', label: '私有草稿图片', description: '' };
const draft = { ...structuredClone(tool), id: 'p_private_draft', slug: 'private-draft', name: '', model: '', visibility: 'unlisted', summary: '', applications: [], features: [], notes: [], legacy_series_ids: [], images: [privateImage], cover_media_id: privateImage.media_id };
draft.guide_profile.sources = []; draft.guide_profile.reviews = [];
const base = structuredClone(seed);
base.products.push(draft);
base.previously_published_routes = seed.products.filter(product => product.visibility === 'listed').map(product => ({ product_id: product.id, slug: product.slug }));
base.previously_published_routes.push({ product_id: 'p_retired_fixture', slug: 'retired-fixture' });
const cases = new Map(['base', 'added-product', 'all-promotions-unlisted', 'empty-category', 'cover-reordered'].map(name => [name, structuredClone(base)]));
const added = { ...structuredClone(tool), id: 'p_added_fixture', slug: 'added-fixture', model: 'QA-ADDED', name: 'QA 新增工具', long_nose: true, blower: true, sort_order: 0, features: [], notes: [], version: 1 };
added.guide_profile.sources = []; added.guide_profile.reviews = [];
cases.get('added-product').products.push(added);
for (const series of cases.get('added-product').legacy_series) if (added.legacy_series_ids.includes(series.id)) series.product_ids.push(added.id);
const entry = { ...structuredClone(added), id: 'p_entry_fixture', slug: 'entry-fixture', kind: 'entry', name: '有图配套入口', model: 'ENTRY-IMAGE', legacy_series_ids: [], sort_order: 1 };
cases.get('added-product').products.push(entry);
const promotionIDs = new Set(['p_f30m', 'p_st64ml', 'p_st64gl', 'p_f30c', 'p_f30cl']);
for (const product of cases.get('all-promotions-unlisted').products) if (promotionIDs.has(product.id)) product.visibility = 'unlisted';
for (const product of cases.get('empty-category').products) if (product.category_id === tool.category_id) product.visibility = 'unlisted';
const reordered = cases.get('cover-reordered').products.find(product => product.id === tool.id);
reordered.cover_media_id = reordered.images.at(-1).media_id;
reordered.images.reverse();
for (const [name, snapshot] of cases) {
  snapshot.release_id = `fixture_${name.replaceAll('-', '_')}`;
  snapshot.workspace_revision++;
  validateContract('catalog-snapshot.schema.json#/$defs/PublishableSnapshot', snapshot);
}
for (const [name, snapshot] of cases) {
  const directory = resolve(out, name);
  await mkdir(resolve(directory, 'media'), { recursive: true });
  const copied = new Set();
  for (const product of snapshot.products) for (const image of product.images) {
    if (copied.has(image.path)) continue;
    copied.add(image.path);
    const target = resolve(directory, 'media', image.path);
    assert.ok(target.startsWith(`${resolve(directory, 'media')}${sep}`), 'Unsafe fixture media path');
    await mkdir(dirname(target), { recursive: true });
    if (image.media_id === privateImage.media_id) await writeFile(target, privateBytes);
    else {
      const source = await realpath(resolve(mediaRoot, image.path));
      assert.ok(source.startsWith(`${mediaRoot}${sep}`), 'Unsafe source media path');
      assert.equal(sha256(await readFile(source)), image.sha256, 'Fixture source media changed');
      await copyFile(source, target);
    }
  }
  await writeFile(resolve(directory, 'catalog.json'), `${JSON.stringify(snapshot, null, 2)}\n`, { flag: 'wx', mode: 0o600 });
}
console.log(JSON.stringify({ cases: [...cases.keys()], output: out, private_draft_sha256: digest, cover_product_id: tool.id, empty_category_id: tool.category_id }, null, 2));
