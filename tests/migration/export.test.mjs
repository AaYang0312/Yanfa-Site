import test from 'node:test';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { access, cp, mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { validateContract } from '../../src/generated/validate-contract.mjs';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..');
const exporter = resolve(root, 'scripts/export-initial-catalog.mjs');
const hash = bytes => createHash('sha256').update(bytes).digest('hex');
const readJSON = async file => JSON.parse(await readFile(file, 'utf8'));
const writeJSON = (file, value) => writeFile(file, `${JSON.stringify(value, null, 2)}\n`);
const outputNames = ['initial-catalog.json', 'media-manifest.json', 'provenance.json'];

async function fixture(t) {
  await mkdir(resolve(root, '.work'), { recursive: true });
  const directory = await mkdtemp(resolve(root, '.work/migration-test-'));
  t.after(() => rm(directory, { recursive: true, force: true }));
  await cp(resolve(root, 'seed/provenance'), resolve(directory, 'seed/provenance'), { recursive: true });
  await cp(resolve(root, 'src/assets/products'), resolve(directory, 'src/assets/products'), { recursive: true });
  await mkdir(resolve(directory, 'src/data'), { recursive: true });
  await writeFile(resolve(directory, 'src/data/catalog.ts'), "throw new Error('The live catalog must not be evaluated');\n");
  await writeJSON(resolve(directory, 'src/data/product-source.json'), { models: [] });
  const inputPath = resolve(directory, 'seed/provenance/product-source.json');
  const recordsPath = resolve(directory, 'seed/provenance/archive-records.json');
  const updateInput = async update => {
    const input = await readJSON(inputPath);
    await update(input);
    await writeJSON(inputPath, input);
    const records = await readJSON(recordsPath);
    const record = records.archives.find(item => item.id === 'product_source');
    record.source_sha256 = record.archive_sha256 = hash(await readFile(inputPath));
    await writeJSON(recordsPath, records);
    return input;
  };
  const run = (out = 'out') => spawnSync(process.execPath, [
    '--experimental-strip-types', exporter, '--baseline', 'seed/provenance/legacy-catalog.ts', '--out', out,
  ], { cwd: directory, encoding: 'utf8' });
  return { directory, inputPath, recordsPath, updateInput, run };
}
function succeeded(result) {
  assert.equal(result.status, 0, `${result.stdout}\n${result.stderr}`);
}
async function rejected(f, pattern) {
  const result = f.run();
  assert.equal(result.status, 1, result.stdout);
  assert.match(result.stderr, pattern);
  await assert.rejects(access(resolve(f.directory, 'out')), { code: 'ENOENT' });
}

test('the frozen module consumes its adjacent source archive and emits consumer-ready content', async t => {
  const f = await fixture(t);
  const input = await f.updateInput(input => {
    const row = input.models[0];
    row.summary = '独立归档输入';
    row.applications = '1.第一项\n接续段\n2.第二项';
    row.features = ' 第一项特点 \n\n 第二项特点 ';
    row.notes = '1.说明\n续行\n2.另一说明';
    row.images[0].label = '归档图片标签';
  });
  succeeded(f.run());
  const catalog = await readJSON(resolve(f.directory, 'out/initial-catalog.json'));
  const product = catalog.products.find(item => item.model === input.models[0].model);
  assert.equal(product.summary, '独立归档输入');
  assert.deepEqual(product.applications, ['1.第一项接续段', '2.第二项']);
  assert.deepEqual(product.features, ['第一项特点', '第二项特点']);
  assert.deepEqual(product.notes, ['1.说明续行', '2.另一说明']);
  const ordered = [...input.models[0].images].sort((a, b) => Number(b.role === 'detail') - Number(a.role === 'detail'));
  assert.deepEqual(product.images.map(image => image.media_id), ordered.map(image => `m_${image.sha256}`));
  assert.equal(product.cover_media_id, `m_${ordered[0].sha256}`);
  assert.equal(product.images.find(image => image.sha256 === input.models[0].images[0].sha256).label, '归档图片标签');
  assert.deepEqual(product.purchase_options, []);
  assert.deepEqual(product.guide_profile.reviews, []);
  for (const field of ['power_source', 'nail_type', 'fastener_length_mm', 'load_bearing', 'long_nose', 'blower']) {
    assert.equal(product.guide_profile[field], null, field);
  }
  const textSource = product.guide_profile.sources.find(source => source.reference === '/summary');
  assert.equal(textSource.sha256, hash(JSON.stringify(product.summary)));
  assert.equal(textSource.public_url, null);
  validateContract('catalog-snapshot.schema.json#/$defs/PublishableSnapshot', catalog);
  const memberSeries = catalog.legacy_series.filter(series => series.product_ids.includes(product.id));
  assert.deepEqual(product.legacy_series_ids, memberSeries.map(series => series.id));
  const provenance = await readJSON(resolve(f.directory, 'out/provenance.json'));
  const origin = provenance.products.find(item => item.product_id === product.id);
  assert.equal(origin.source_row, input.models[0].sourceRow);
  assert.equal(origin.source_row_sha256, hash(JSON.stringify(input.models[0])));
});

test('repeated exports have identical bytes and media identities across output directories', async t => {
  const f = await fixture(t);
  succeeded(f.run('first'));
  succeeded(f.run('second'));
  for (const name of outputNames) {
    assert.deepEqual(await readFile(resolve(f.directory, 'first', name)), await readFile(resolve(f.directory, 'second', name)), name);
  }
  const manifest = await readJSON(resolve(f.directory, 'first/media-manifest.json'));
  for (const media of manifest.media) {
    assert.equal(media.media_id, `m_${hash(await readFile(resolve(f.directory, 'src/assets/products', media.path)))}`);
  }
});

test('changed source or executable archives fail before any output is created', async t => {
  for (const filename of ['product-source.json', 'legacy-catalog.ts']) {
    await t.test(filename, async t => {
      const f = await fixture(t);
      await writeFile(resolve(f.directory, 'seed/provenance', filename), '\n', { flag: 'a' });
      await rejected(f, /Archive SHA256 mismatch/);
    });
  }
});

test('changed media bytes cannot retain the archived identity', async t => {
  const f = await fixture(t);
  const input = await readJSON(f.inputPath);
  await writeFile(resolve(f.directory, 'src/assets/products', input.models[0].images[0].file), 'changed image');
  await rejected(f, /Media SHA256 mismatch/);
});

test('a matching checksum does not make undecodable bytes valid media', async t => {
  const f = await fixture(t);
  const bytes = Buffer.from('not a decodable image');
  await f.updateInput(async input => {
    const image = input.models[0].images[0];
    await writeFile(resolve(f.directory, 'src/assets/products', image.file), bytes);
    image.sha256 = hash(bytes);
  });
  await rejected(f, /unsupported image format/i);
});

test('a product cannot take a reserved legacy series route', async t => {
  const f = await fixture(t);
  await f.updateInput(input => {
    input.models.push({ ...input.models[0], model: 'F30' });
  });
  await rejected(f, /VALIDATION_FAILED[\s\S]*slug/);
});
