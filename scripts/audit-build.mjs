import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { readdir, readFile, stat } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import { resolve, relative, sep } from 'node:path';
import { pathToFileURL } from 'node:url';
import { parseArgs } from 'node:util';
import { validateContract } from '../src/generated/validate-contract.mjs';

export const sha256 = bytes => createHash('sha256').update(bytes).digest('hex');
export const mediaFilename = image => `${image.sha256}.${image.format === 'jpeg' ? 'jpg' : image.format}`;
export const listedProducts = snapshot => snapshot.products.filter(product => product.visibility === 'listed')
  .sort((a, b) => a.sort_order - b.sort_order || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
export function publicImages(snapshot) {
  const images = new Map();
  for (const product of listedProducts(snapshot)) for (const image of product.images) {
    const previous = images.get(image.media_id);
    if (previous) assert.deepEqual(
      [image.sha256, image.format, image.bytes, image.width, image.height, image.path],
      [previous.sha256, previous.format, previous.bytes, previous.width, previous.height, previous.path],
      `Conflicting media identity: ${image.media_id}`,
    );
    images.set(image.media_id, image);
  }
  return [...images.values()];
}
export function readSnapshot(path) {
  assert.ok(path, 'CATALOG_SNAPSHOT is required; no catalog fallback is available');
  const snapshot = validateContract('catalog-snapshot.schema.json#/$defs/PublishableSnapshot', JSON.parse(readFileSync(path, 'utf8')));
  const routes = new Map(snapshot.products.map(product => [product.slug, product.id]));
  const series = new Set(snapshot.legacy_series.map(item => item.slug));
  for (const route of snapshot.previously_published_routes) {
    assert.ok(!series.has(route.slug), `Historical product route conflicts with series: ${route.slug}`);
    assert.ok(!routes.has(route.slug) || routes.get(route.slug) === route.product_id, `Historical route changed owner: ${route.slug}`);
  }
  publicImages(snapshot);
  return snapshot;
}
export async function walkFiles(root) {
  const files = [];
  for (const entry of await readdir(root, { withFileTypes: true })) {
    assert.ok(!entry.isSymbolicLink(), `Unexpected output symlink: ${entry.name}`);
    const path = resolve(root, entry.name);
    if (entry.isDirectory()) files.push(...await walkFiles(path));
    else files.push(path);
  }
  return files.sort();
}
export async function auditSite(snapshotPath, sitePath) {
  assert.ok(sitePath, '--site is required');
  const snapshot = readSnapshot(snapshotPath);
  const root = resolve(sitePath);
  const files = await walkFiles(root);
  const htmlFiles = files.filter(file => file.endsWith('.html'));
  const htmlByPath = new Map(await Promise.all(htmlFiles.map(async file => [file, await readFile(file, 'utf8')])));
  const failures = [];
  let references = 0;
  const unescape = text => text.replaceAll('&amp;', '&').replaceAll('&#38;', '&').replaceAll('&#39;', "'").replaceAll('&quot;', '"');
  for (const [file, html] of htmlByPath) {
    const page = `/${relative(root, file).split(sep).join('/')}`.replace(/index\.html$/, '');
    if ([...html.matchAll(/<h1(?:\s|>)/g)].length !== 1) failures.push(`${page}: expected one h1`);
    if (!html.includes(`name="release-id" content="${snapshot.release_id}"`)) failures.push(`${page}: wrong release ID`);
    if (/\/api\/admin\/media|catalog\.json|src\/assets\/products|seed\/provenance/.test(html)) failures.push(`${page}: private build input exposed`);
    const values = [...html.matchAll(/\b(?:href|src)=["']([^"']*)["']/g)].map(match => match[1]);
    for (const match of html.matchAll(/\bsrcset=["']([^"']*)["']/g)) values.push(...match[1].split(',').map(entry => entry.trim().split(/\s+/)[0]));
    for (const value of values) {
      if (!value || /^(?:https?:|mailto:|tel:|data:|\/\/)/i.test(value)) continue;
      const url = new URL(unescape(value), `http://local.invalid${page}`);
      let target = resolve(root, `.${decodeURIComponent(url.pathname)}`);
      if (target !== root && !target.startsWith(`${root}${sep}`)) { failures.push(`${page}: escaping path ${value}`); continue; }
      try {
        if ((await stat(target)).isDirectory()) target = resolve(target, 'index.html');
        await stat(target);
        references++;
        if (url.hash && target.endsWith('.html')) {
          const targetHTML = htmlByPath.get(target) ?? await readFile(target, 'utf8');
          const id = decodeURIComponent(url.hash.slice(1));
          if (!targetHTML.includes(`id="${id}"`) && !targetHTML.includes(`id='${id}'`)) failures.push(`${page}: missing anchor ${value}`);
        }
      } catch { failures.push(`${page}: missing file ${value}`); }
    }
  }
  const listed = listedProducts(snapshot);
  const expected = new Set([...listed.map(product => product.slug), ...snapshot.previously_published_routes.map(route => route.slug), ...snapshot.legacy_series.map(series => series.slug)]);
  for (const slug of expected) assert.ok(htmlByPath.has(resolve(root, 'products', slug, 'index.html')), `Missing route: ${slug}`);
  for (const product of snapshot.products.filter(product => product.visibility !== 'listed' && !expected.has(product.slug))) {
    assert.ok(!htmlByPath.has(resolve(root, 'products', product.slug, 'index.html')), `Private draft route emitted: ${product.slug}`);
  }
  for (const route of snapshot.previously_published_routes.filter(route => !listed.some(product => product.id === route.product_id))) {
    const html = htmlByPath.get(resolve(root, 'products', route.slug, 'index.html'));
    assert.match(html, /name="robots" content="noindex/);
    assert.match(html, /已下架/);
    assert.match(html, /href="tel:/);
    assert.doesNotMatch(html, /data-gallery/);
  }
  const originalFiles = files.filter(file => relative(root, file).split(sep)[0] === 'media' && !file.endsWith('.html'));
  const allowedOriginals = new Set(publicImages(snapshot).map(mediaFilename));
  assert.deepEqual(originalFiles.map(file => relative(resolve(root, 'media'), file)).sort(), [...allowedOriginals].sort(), 'Original resource authorization differs from listed media');
  for (const image of publicImages(snapshot)) assert.equal(sha256(await readFile(resolve(root, 'media', mediaFilename(image)))), image.sha256, 'Original image SHA mismatch');
  for (const file of files) assert.ok(!/\.(?:json|map)$/.test(file), `Private data or source map emitted: ${relative(root, file)}`);
  assert.deepEqual(failures, [], failures.join('\n'));
  const routes = htmlFiles.map(file => relative(root, file).split(sep).join('/'));
  const resources = await Promise.all(files.filter(file => !file.endsWith('.html')).map(async file => {
    const bytes = await readFile(file);
    return { path: relative(root, file).split(sep).join('/'), sha256: sha256(bytes), bytes: bytes.length };
  }));
  return { pages: htmlFiles.length, checkedLocalReferences: references, listedProducts: listed.length, originals: allowedOriginals.size, failures: 0, routes, resources };
}
if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  const { values } = parseArgs({ options: { snapshot: { type: 'string' }, site: { type: 'string' } } });
  const { routes, resources, ...report } = await auditSite(values.snapshot, values.site);
  console.log(JSON.stringify(report, null, 2));
}
