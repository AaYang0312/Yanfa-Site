import { createHash } from 'node:crypto';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { parseArgs } from 'node:util';
import sharp from 'sharp';
import { validateContract } from '../src/generated/validate-contract.mjs';

const sha256 = value => createHash('sha256').update(value).digest('hex');
const json = value => `${JSON.stringify(value, null, 2)}\n`;
const digestValue = value => sha256(JSON.stringify(value));
const sceneIDs = new Map([
  ['家装木工', 'home-woodworking'], ['吊顶龙骨', 'ceiling-framing'],
  ['包装托盘', 'packaging-pallets'], ['家具木方', 'furniture-timber'],
  ['板材钉合', 'panel-fastening'], ['退钉回收', 'nail-removal'],
  ['墙面保温', 'wall-insulation'], ['水电安装', 'electrical-plumbing'],
  ['墙面挂网', 'wall-mesh'], ['软包藤艺', 'upholstery-rattan'],
]);
function scenes(names) {
  return names.map(name => {
    if (!sceneIDs.has(name)) throw new Error(`Unknown archived scene: ${name}`);
    return sceneIDs.get(name);
  });
}
function source(id, kind, reference, sha) {
  return { id, kind, reference, sha256: sha, public_label: '', public_url: null };
}
function unknownGuide(sources) {
  return {
    scene_ids: [], materials: [], substrates: [], excluded_materials: [], excluded_substrates: [],
    power_source: null, nail_type: null, fastener_length_mm: null, load_bearing: null,
    long_nose: null, blower: null, limitations: [], consultation_notes: [],
    required_requirements: [], sources, reviews: [],
  };
}

async function main() {
  const { values } = parseArgs({ options: { baseline: { type: 'string' }, out: { type: 'string' } } });
  if (!values.baseline || !values.out) {
    throw new Error('Usage: npm run seed:export -- --baseline seed/provenance/legacy-catalog.ts --out <directory>');
  }
  const root = process.cwd();
  const baselinePath = resolve(root, values.baseline);
  const records = JSON.parse(await readFile(resolve(dirname(baselinePath), 'archive-records.json'), 'utf8'));
  const catalogRecord = records.archives.find(record => record.id === 'legacy_catalog');
  const inputRecord = records.archives.find(record => record.id === 'product_source');
  if (records.schema_version !== 1 || !catalogRecord || !inputRecord ||
      baselinePath !== resolve(root, catalogRecord.archive_path) ||
      resolve(dirname(baselinePath), 'product-source.json') !== resolve(root, inputRecord.archive_path)) {
    throw new Error('Baseline must be the recorded catalog archive with its adjacent JSON archive');
  }
  for (const record of records.archives) {
    const actual = sha256(await readFile(resolve(root, record.archive_path)));
    if (actual !== record.archive_sha256) throw new Error(`Archive SHA256 mismatch: ${record.archive_path}`);
  }
  // Only the frozen module and its adjacent JSON are evaluated; the live catalog may be removed.
  const baseline = await import(pathToFileURL(baselinePath).href);
  const input = JSON.parse(await readFile(resolve(root, inputRecord.archive_path), 'utf8'));
  const rows = new Map(input.models.map((row, index) => [row.model.toLowerCase(), { row, index }]));
  const toolSlugs = new Set(baseline.toolProducts.map(product => product.slug));
  if (rows.size !== input.models.length || rows.size !== toolSlugs.size || [...rows.keys()].some(slug => !toolSlugs.has(slug))) {
    throw new Error('Archived source models and displayed tools must have a one-to-one mapping');
  }
  const legacySeries = baseline.legacySeries.map(series => ({
    id: `s_${series.slug}`, slug: series.slug, name: series.name,
    product_ids: series.items.map(product => `p_${product.slug}`),
  }));
  const media = new Map();
  const provenanceProducts = [];
  const products = [];
  for (const [sortOrder, product] of baseline.products.entries()) {
    const id = `p_${product.slug}`;
    const raw = rows.get(product.slug);
    const ordered = [...product.images].sort((a, b) => Number(b.role === 'detail') - Number(a.role === 'detail'));
    const images = [];
    const origins = [];
    for (const [displayIndex, image] of ordered.entries()) {
      if (!/^[A-Za-z0-9_-]+\.(?:jpg|jpeg|png|webp)$/.test(image.file)) throw new Error(`Unsafe media filename: ${image.file}`);
      const bytes = await readFile(resolve(root, 'src/assets/products', image.file));
      const hash = sha256(bytes);
      if (hash !== image.sha256) throw new Error(`Media SHA256 mismatch: ${image.file}`);
      let record = media.get(hash);
      if (!record) {
        const decoder = sharp(bytes, { limitInputPixels: 40000000, failOn: 'warning' });
        const metadata = await decoder.metadata();
        const extensionFormat = /\.jpe?g$/.test(image.file) ? 'jpeg' : image.file.split('.').at(-1);
        if (metadata.format !== extensionFormat || (metadata.pages ?? 1) !== 1) throw new Error(`Invalid media format: ${image.file}`);
        // A metadata read alone does not prove the image can be decoded.
        await decoder.raw().toBuffer();
        const info = {
          media_id: `m_${hash}`, sha256: hash, format: metadata.format, bytes: bytes.length,
          width: metadata.width, height: metadata.height, path: image.file,
        };
        validateContract('catalog-snapshot.schema.json#/$defs/SnapshotMedia', info);
        record = { ...info, origins: [] };
        media.set(hash, record);
      }
      const origin = {
        product_id: id, file: image.file, role: image.role, source: image.source, sha256: hash,
        source_index: product.images.indexOf(image), display_index: displayIndex,
      };
      record.origins.push(origin);
      origins.push(origin);
      const { origins: unusedOrigins, ...info } = record;
      images.push({ ...info, label: image.label, description: image.description ?? '' });
    }
    const document = {
      id, slug: product.slug, kind: toolSlugs.has(product.slug) ? 'tool' : 'entry',
      name: product.name, model: product.model ?? '', category_id: product.category, series: product.series ?? '',
      legacy_series_ids: legacySeries.filter(series => series.product_ids.includes(id)).map(series => series.id),
      summary: product.summary, applications: product.applications, features: product.features, notes: product.notes,
      scene_ids: scenes(baseline.scenesOf(product)), nail_type: baseline.nailOf(product) ?? null,
      fastener_text: product.fastener ?? '', long_nose: product.longNose ?? false, blower: product.blower ?? false,
      images, cover_media_id: images[0]?.media_id ?? null, visibility: 'listed', sort_order: sortOrder,
      guide_profile: unknownGuide([]), purchase_options: [], version: 1, updated_at: records.frozen_at,
    };
    const sources = [
      source('legacy_catalog', 'document', catalogRecord.id, catalogRecord.archive_sha256),
      ...(raw ? [source('product_source', 'document', inputRecord.id, inputRecord.archive_sha256)] : []),
      ...['summary', 'applications', 'features', 'notes', 'fastener_text', 'scene_ids', 'nail_type', 'long_nose', 'blower']
        .map(field => source(`text_${field}`, 'product_text', `/${field}`, digestValue(document[field]))),
      ...images.map(image => source(image.media_id, 'media', image.media_id, image.sha256)),
    ];
    document.guide_profile = unknownGuide(sources);
    products.push(document);
    provenanceProducts.push({
      product_id: id, slug: product.slug,
      source_row: raw?.row.sourceRow ?? null,
      source_archive_id: raw ? inputRecord.id : catalogRecord.id,
      source_json_pointer: raw ? `/models/${raw.index}` : null,
      source_row_sha256: raw ? digestValue(raw.row) : null,
      display_sha256: digestValue(document), images: origins,
    });
  }
  const snapshot = {
    schema_version: 1, release_id: 'seed_initial', workspace_revision: 1,
    categories: baseline.categories.map(({ slug, scenes: names, nail, ...category }, sortOrder) => ({
      id: slug, ...category, sort_order: sortOrder, nail: nail ?? null, scene_ids: scenes(names),
    })),
    scenes: baseline.allSceneTags.map(name => ({ id: scenes([name])[0], name })),
    nail_types: baseline.nailTypes.map(({ slug, name }) => ({ id: slug, name })),
    legacy_series: legacySeries, products, previously_published_routes: [],
  };
  validateContract('catalog-snapshot.schema.json#/$defs/PublishableSnapshot', snapshot);
  const manifest = { schema_version: 1, media: [...media.values()] };
  const snapshotJSON = json(snapshot);
  const manifestJSON = json(manifest);
  const provenance = {
    ...records,
    purpose: 'One-time, read-only initialization; never a runtime fallback or editing source.',
    text_digest_encoding: 'SHA256 of UTF-8 JSON.stringify(value), without trailing newline',
    media_root: 'src/assets/products',
    outputs: {
      'initial-catalog.json': sha256(snapshotJSON),
      'media-manifest.json': sha256(manifestJSON),
    },
    products: provenanceProducts,
  };
  // No output is written until archives, all media and the entire consumer graph are valid.
  const out = resolve(root, values.out);
  await mkdir(out, { recursive: true });
  for (const [name, bytes] of [['initial-catalog.json', snapshotJSON], ['media-manifest.json', manifestJSON], ['provenance.json', json(provenance)]]) {
    await writeFile(resolve(out, name), bytes);
  }
  console.log(json({
    products: products.length, tools: products.filter(product => product.kind === 'tool').length,
    entries: products.filter(product => product.kind === 'entry').length,
    categories: snapshot.categories.length, scenes: snapshot.scenes.length, legacy_series: legacySeries.length,
    media: media.size, snapshot_sha256: sha256(snapshotJSON), media_manifest_sha256: sha256(manifestJSON),
    provenance_sha256: sha256(json(provenance)),
  }).trim());
}

main().catch(error => {
  console.error(error.message);
  if (error.error?.fields) console.error(JSON.stringify(error.error.fields));
  process.exitCode = 1;
});
