import assert from 'node:assert/strict';
import { mkdir, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { readSnapshot, listedProducts, publicImages, mediaFilename, sha256 } from './audit-build.mjs';
import { matchesCatalogItem } from '../src/scripts/catalog-filters.js';

assert.ok(process.env.QA_SNAPSHOT && process.env.QA_URL && process.env.QA_OUTPUT, 'QA_SNAPSHOT, QA_URL and QA_OUTPUT are required');
const snapshot = readSnapshot(process.env.QA_SNAPSHOT);
const listed = listedProducts(snapshot);
const origin = process.env.QA_URL;
const output = resolve(process.env.QA_OUTPUT);
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE ? pathToFileURL(process.env.PLAYWRIGHT_MODULE).href : 'playwright-core');
await mkdir(output, { recursive: true });
const browser = await chromium.launch({ headless: true, ...(process.env.BROWSER_PATH ? { executablePath: process.env.BROWSER_PATH } : { channel: 'chrome' }) });
const errors = [];
const results = [];
const sceneNames = new Map(snapshot.scenes.map(scene => [scene.id, scene.name]));
const categoryNames = new Map(snapshot.categories.map(category => [category.id, category.name]));
const state = { type: [], scene: [], nail: [], q: '' };
const expected = () => listed.filter(product => matchesCatalogItem({ type: product.category_id, scene: product.scene_ids.map(id => sceneNames.get(id)), nail: product.nail_type ?? '', search: [product.name, product.model, categoryNames.get(product.category_id), product.series, product.summary, product.fastener_text, ...product.features].filter(Boolean).join(' ') }, state));
const orderedImages = product => [...product.images.filter(image => image.media_id === product.cover_media_id), ...product.images.filter(image => image.media_id !== product.cover_media_id)];
const paths = new Set(['/', '/products/', '/about/', '/service/', '/media/', '/shop/', '/contact/']);
const galleryProduct = listed.find(product => product.images.length > 1);
const entryProduct = listed.find(product => product.kind === 'entry' && product.images.length);
for (const product of [listed[0], listed.at(-1), galleryProduct, entryProduct]) if (product) paths.add(`/products/${product.slug}/`);
for (const series of snapshot.legacy_series) paths.add(`/products/${series.slug}/`);
const withdrawn = snapshot.previously_published_routes.filter(route => !listed.some(product => product.id === route.product_id));
for (const route of withdrawn) paths.add(`/products/${route.slug}/`);
async function layout(page, path, width) {
  const response = await page.goto(`${origin}${path}`, { waitUntil: 'networkidle' });
  assert.equal(response.status(), 200, path);
  await page.evaluate(() => document.fonts.ready);
  const actual = await page.evaluate(() => ({ viewport: document.documentElement.clientWidth, document: document.documentElement.scrollWidth, headings: document.querySelectorAll('main h1').length, broken: [...document.images].filter(image => image.complete && !image.naturalWidth).map(image => image.src), release: document.querySelector('meta[name="release-id"]')?.content }));
  assert.ok(actual.document <= actual.viewport + 1, `${width}px overflow: ${path}`);
  assert.equal(actual.headings, 1, path); assert.deepEqual(actual.broken, [], path); assert.equal(actual.release, snapshot.release_id, path);
  results.push({ path, width, release_id: actual.release, overflow: false });
}
async function screenshot(page, filename) {
  await page.evaluate(async () => {
    for (const image of document.images) image.loading = 'eager';
    await Promise.all([...document.images].map(image => image.decode().catch(() => {})));
  });
  await page.screenshot({ path: resolve(output, filename), fullPage: true });
}
async function assertCards(page) {
  await page.waitForFunction(count => Number(document.querySelector('[data-result-count]').textContent) === count, expected().length);
  assert.deepEqual(await page.locator('[data-card]:visible a.card__head').evaluateAll(elements => elements.map(element => new URL(element.href).pathname).sort()), expected().map(product => `/products/${product.slug}/`).sort());
}
async function checkFilter(page, name, value) {
  const input = page.locator(`input[name="${name}"][value="${value}"]`);
  const details = input.locator('xpath=ancestor::details');
  if (!(await details.getAttribute('open') === '')) await details.locator('summary').click();
  await input.check(); state[name].push(value); await assertCards(page);
}
try {
  const desktop = await browser.newContext({ viewport: { width: 1440, height: 1000 }, reducedMotion: 'reduce' });
  const page = await desktop.newPage();
  page.on('pageerror', error => errors.push(error.message));
  page.on('response', response => { if (response.status() >= 400) errors.push(`${response.status()} ${response.url()}`); });
  for (const path of paths) await layout(page, path, 1440);
  for (const product of snapshot.products.filter(product => product.visibility !== 'listed' && !snapshot.previously_published_routes.some(route => route.product_id === product.id))) assert.equal((await page.request.get(`${origin}/products/${product.slug}/`)).status(), 404);
  assert.equal((await page.request.get(`${origin}/products/unknown-fixture-url/`)).status(), 404);
  for (const path of ['/catalog.json', '/manifest.json']) assert.equal((await page.request.get(`${origin}${path}`)).status(), 404);
  const publicIDs = new Set(publicImages(snapshot).map(image => image.media_id));
  for (const product of snapshot.products.filter(product => product.visibility !== 'listed')) for (const image of product.images.filter(image => !publicIDs.has(image.media_id))) assert.equal((await page.request.get(`${origin}/media/${mediaFilename(image)}`)).status(), 404);
  results.push({ interactions: 'never-published URLs and exclusive media denied', passed: true });

  await page.goto(`${origin}/products/`, { waitUntil: 'networkidle' });
  await assertCards(page);
  for (const category of snapshot.categories) assert.match(await page.locator(`input[name="type"][value="${category.id}"]`).locator('..').innerText(), new RegExp(`（${listed.filter(product => product.category_id === category.id).length}）`));
  if (listed.length) {
    const searchable = listed.find(product => product.model) ?? listed[0];
    state.q = (searchable.model || searchable.name).split('').join(' ');
    await page.locator('[data-catalog-search]').fill(state.q); await assertCards(page);
    await page.reload({ waitUntil: 'networkidle' }); await assertCards(page);
    state.q = ''; await page.locator('[data-catalog-search]').fill('');
  }
  for (const category of snapshot.categories.slice(0, 2)) await checkFilter(page, 'type', category.id);
  const prior = await page.locator('[data-card]:visible').count();
  await checkFilter(page, 'scene', snapshot.scenes[0].name);
  await page.reload({ waitUntil: 'networkidle' }); await assertCards(page);
  await page.goBack({ waitUntil: 'networkidle' });
  assert.equal(await page.locator('[data-card]:visible').count(), prior);
  await page.goForward({ waitUntil: 'networkidle' }); await assertCards(page);
  await checkFilter(page, 'nail', snapshot.nail_types[0].id);
  state.q = 'unknown-fixture-query'; await page.locator('[data-catalog-search]').fill(state.q); await assertCards(page);
  assert.equal(await page.locator('[data-empty]').isVisible(), true);
  await page.locator('[data-empty] button').click(); Object.assign(state, { type: [], scene: [], nail: [], q: '' }); await assertCards(page);
  assert.equal(new URL(page.url()).search, '');
  for (const category of snapshot.categories.filter(category => !listed.some(product => product.category_id === category.id))) {
    await page.goto(`${origin}/products/?type=${category.id}`, { waitUntil: 'networkidle' });
    assert.equal(await page.locator('[data-card]:visible').count(), 0); assert.equal(await page.locator('[data-empty]').isVisible(), true);
  }
  results.push({ interactions: 'counts, normalized search, union/intersection, refresh, back/forward, empty categories, reset', passed: true });

  await page.goto(`${origin}/products/`, { waitUntil: 'networkidle' });
  for (const product of listed.filter(product => product.images.length)) {
    const card = page.locator(`[data-card][href="/products/${product.slug}/"], [data-card]:has(a[href="/products/${product.slug}/"])`);
    assert.equal(await card.locator('img[data-media-id]').getAttribute('data-media-id'), orderedImages(product)[0].media_id);
  }
  for (const product of [galleryProduct, entryProduct].filter(Boolean)) {
    await page.goto(`${origin}/products/${product.slug}/`, { waitUntil: 'networkidle' });
    const images = orderedImages(product);
    assert.deepEqual(await page.locator('[data-gallery-panel]').evaluateAll(elements => elements.map(element => element.dataset.mediaId)), images.map(image => image.media_id));
    assert.equal(await page.locator('[data-gallery-panel]:visible').getAttribute('data-media-id'), images[0].media_id);
    if (images.length > 1) {
      for (let index = 0; index < images.length; index++) {
        const thumb = page.locator('[data-gallery-thumb]').nth(index); await thumb.click();
        assert.equal(await page.locator('[data-gallery-panel]:visible').getAttribute('data-media-id'), images[index].media_id);
        if (images[index].description) {
          const details = page.locator('[data-gallery-panel]:visible details');
          if (await details.getAttribute('open') === null) await details.locator('summary').click();
          assert.equal((await details.locator('p').textContent()).trim(), images[index].description);
        }
        await thumb.press('ArrowRight');
        assert.equal(await page.locator('[data-gallery-panel]:visible').getAttribute('data-media-id'), images[(index + 1) % images.length].media_id);
        await page.locator('[data-gallery-thumb]').nth((index + 1) % images.length).press('ArrowLeft');
        assert.equal(await page.locator('[data-gallery-panel]:visible').getAttribute('data-media-id'), images[index].media_id);
      }
    }
    const original = await page.request.get(`${origin}${await page.locator('[data-gallery-panel]:visible a').getAttribute('href')}`);
    assert.equal(original.status(), 200); assert.equal(sha256(await original.body()), images.at(-1).sha256);
    for (const section of ['features', 'usage']) if (!(section === 'features' ? product.features : product.notes).length) assert.equal(await page.locator(`#${section}, a[href="#${section}"]`).count(), 0);
    await screenshot(page, `${product.kind}-gallery-desktop.png`);
  }
  results.push({ interactions: 'explicit covers, edited gallery order, thumbnails, both arrow keys, exact original bytes, entry gallery, empty sections', passed: true });

  for (const series of snapshot.legacy_series) {
    await page.goto(`${origin}/products/${series.slug}/`, { waitUntil: 'networkidle' });
    const members = listed.filter(product => product.legacy_series_ids.includes(series.id));
    assert.equal(await page.locator('main [data-card]').count(), members.length);
    if (!members.length) assert.equal(await page.locator('[data-series-empty]').isVisible(), true);
  }
  for (const route of withdrawn) {
    await page.goto(`${origin}/products/${route.slug}/`, { waitUntil: 'networkidle' });
    assert.equal(await page.locator('[data-withdrawn]').isVisible(), true);
    assert.match(await page.locator('meta[name="robots"]').getAttribute('content'), /noindex/);
    assert.equal(await page.locator('[data-gallery]').count(), 0);
    assert.match(await page.locator('main a[href^="tel:"]').getAttribute('href'), /^tel:/);
  }
  await page.goto(`${origin}/`, { waitUntil: 'networkidle' });
  const promotionIDs = ['p_f30m', 'p_st64ml', 'p_st64gl', 'p_f30c', 'p_f30cl'];
  const promoted = promotionIDs.flatMap(id => listed.filter(product => product.id === id));
  assert.deepEqual(await page.locator('a.split__photo').evaluateAll(elements => elements.map(element => new URL(element.href).pathname)), promoted.map(product => `/products/${product.slug}/`));
  for (const [id, ids] of [['long-title', promotionIDs.slice(0, 3)], ['blower-title', promotionIDs.slice(3)]]) assert.equal(await page.locator(`#${id}`).count(), listed.some(product => ids.includes(product.id)) ? 1 : 0);
  const menu = page.locator('[data-mega-toggle]'); await menu.focus(); await menu.press('Enter'); assert.equal(await menu.getAttribute('aria-expanded'), 'true'); await page.keyboard.press('Escape'); assert.equal(await menu.getAttribute('aria-expanded'), 'false');
  await screenshot(page, 'home-desktop.png');
  await page.goto(`${origin}/products/`, { waitUntil: 'networkidle' }); await screenshot(page, 'catalog-desktop.png');
  results.push({ interactions: 'explicit poster targets, hidden empty promotions, series membership, historical noindex tombstones, desktop keyboard menu', passed: true });

  const mobile = await browser.newContext({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, reducedMotion: 'reduce' });
  const phone = await mobile.newPage(); phone.on('pageerror', error => errors.push(error.message));
  for (const path of ['/', '/products/', ...(galleryProduct ? [`/products/${galleryProduct.slug}/`] : []), '/contact/']) await layout(phone, path, 390);
  await phone.goto(`${origin}/`, { waitUntil: 'networkidle' }); await screenshot(phone, 'home-mobile.png');
  await phone.locator('[data-drawer-toggle]').click(); assert.equal(await phone.locator('[data-drawer]').isVisible(), true);
  await phone.locator('[data-drawer] a[href="/about/"]').click(); assert.equal(new URL(phone.url()).pathname, '/about/');
  await phone.goto(`${origin}/products/`, { waitUntil: 'networkidle' });
  if (listed.length) { const query = listed[0].model || listed[0].name; await phone.locator('[data-catalog-search]').fill(query); assert.ok(await phone.locator('[data-card]:visible').count() >= 1); }
  await screenshot(phone, 'catalog-mobile.png');
  if (galleryProduct) { await phone.goto(`${origin}/products/${galleryProduct.slug}/`, { waitUntil: 'networkidle' }); await phone.locator('[data-gallery-thumb]').nth(1).click(); assert.equal(await phone.locator('[data-gallery-panel]:visible').getAttribute('data-media-id'), orderedImages(galleryProduct)[1].media_id); await screenshot(phone, 'gallery-mobile.png'); }
  await phone.setViewportSize({ width: 320, height: 740 });
  for (const path of ['/', '/products/', ...(galleryProduct ? [`/products/${galleryProduct.slug}/`] : []), '/contact/']) await layout(phone, path, 320);
  assert.deepEqual(errors, [], 'Browser errors');
  const report = { release_id: snapshot.release_id, listed_products: listed.length, checks: results.length, errors, results };
  await writeFile(resolve(output, 'report.json'), `${JSON.stringify(report, null, 2)}\n`);
  console.log(JSON.stringify({ ...report, results: undefined, screenshots: output }, null, 2));
} finally { await browser.close(); }
