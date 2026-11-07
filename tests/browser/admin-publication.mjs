// V09 drives the built /admin through the real Go origin. No mocked API success,
// SQL fixtures, traces, videos or credential screenshots. Tokens remain in RAM.
import assert from 'node:assert/strict';
import { readFile, writeFile, mkdir, mkdtemp, rm, copyFile } from 'node:fs/promises';
import { resolve, join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { randomBytes, createHash } from 'node:crypto';
import { parseArgs } from 'node:util';
import { client, editable, incomplete, initialize, startServer, stopServer, live, passwordFile } from '../smoke/publication.mjs';
import { copyTemplate } from '../../scripts/build-candidate.mjs';

const { values } = parseArgs({ options: { base: { type: 'string' }, out: { type: 'string' }, 'original-links-only': { type: 'boolean', default: false } } });
assert(values.base && values.out, '--base and --out required');
const base = new URL(values.base), out = resolve(values.out), root = process.cwd();
assert.equal(base.hostname, '127.0.0.1', 'V09 requires its isolated loopback Go origin');
await mkdir(out, { recursive: true, mode: 0o700 });
const password = await passwordFile(), suffix = randomBytes(6).toString('hex');
const report = { success: false, base: base.origin, checks: [], requests: [], screenshots: [], page_errors: [], states: {}, commands: [] };
const { chromium } = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE ?? resolve('node_modules/playwright-core/index.mjs')).href);
const browserServer = await chromium.launchServer({ executablePath: process.env.BROWSER_PATH, headless: true, args: ['--no-sandbox'], env: { ...process.env, TMPDIR: '/proc/self/cwd/.work/browser-tmp' } });
const browser = await chromium.connect(browserServer.wsEndpoint());
report.browser_pid = browserServer.process().pid;
const contexts = []; let tokenInMemory;
async function context(width = 1440) {
  const ctx = await browser.newContext({ viewport: { width, height: 1000 } }); contexts.push(ctx); return ctx;
}
function track(page) {
  page.setDefaultTimeout(20000);
  page.on('pageerror', error => report.page_errors.push(error.message));
  page.on('dialog', dialog => dialog.accept());
  page.on('requestfinished', async request => {
    const url = new URL(request.url()); if (!url.pathname.startsWith('/api/admin/') || request.method() === 'GET') return;
    const response = await request.response(), headers = await request.allHeaders();
    const row = { method: request.method(), path: url.pathname, origin: headers.origin, csrf_present: Boolean(headers['x-csrf-token']), status: response.status(), idempotency_key_present: Boolean(headers['idempotency-key']) };
    // Review envelopes contain no credential. All other bodies/headers are omitted.
    if (/publication-requests|candidates$/.test(url.pathname)) row.body = request.postDataJSON();
    report.requests.push(row);
  });
}
async function login(page, origin = base.origin, next = '/admin/') {
  await page.goto(origin + '/admin/login/?next=' + encodeURIComponent(next));
  await page.getByLabel('用户名', { exact: true }).fill('admin'); await page.getByLabel('密码', { exact: true }).fill(password);
  await Promise.all([page.waitForURL(origin + next), page.getByRole('button', { name: '登录', exact: true }).click()]);
  await page.locator('#session-state').filter({ hasText: 'admin' }).waitFor();
}
function response(page, method, path, status) {
  return page.waitForResponse(r => new URL(r.url()).pathname === path && r.request().method() === method && r.status() === status);
}
async function shot(page, name) {
  const secret = page.locator('#token-secret');
  if (await secret.count()) assert(!(await secret.inputValue()), 'refuse screenshot of one-time credential');
  await page.screenshot({ path: join(out, name), fullPage: true }); report.screenshots.push(name);
}
async function check(name, run) {
  await run(); report.checks.push(name); console.log('PASS ' + name);
  await writeFile(join(out, 'admin-publication.json'), JSON.stringify(report, null, 2), { mode: 0o600 });
}
async function navigatedResource(page, pagePath, apiPath, previousID = null) {
  await page.waitForURL(url => url.pathname === pagePath && Boolean(url.searchParams.get('id')) && url.searchParams.get('id') !== previousID);
  const id = new URL(page.url()).searchParams.get('id');
  const result = await page.context().request.get(new URL(apiPath + encodeURIComponent(id), page.url()).href);
  assert.equal(result.status(), 200); return result.json();
}
async function candidate(page, api, { ready = true, lostResponse = false } = {}) {
  await page.goto(base.origin + '/admin/candidates/');
  let firstID, firstKey;
  if (lostResponse) {
    const handler = async route => {
      firstKey = route.request().headers()['idempotency-key'];
      const actual = await route.fetch(); assert.equal(actual.status(), 202); firstID = (await actual.json()).id;
      await route.abort('failed');
    };
    await page.route('**/api/admin/candidates', handler);
    await page.locator('#freeze-workspace').click(); await page.locator('#notice.error').waitFor();
    await page.unroute('**/api/admin/candidates', handler);
  }
  const [r] = await Promise.all([response(page, 'POST', '/api/admin/candidates', 202), page.locator('#freeze-workspace').click()]);
  const c = await navigatedResource(page, '/admin/candidates/', '/api/admin/candidates/');
  if (lostResponse) { assert.equal(c.id, firstID); assert.equal(r.request().headers()['idempotency-key'], firstKey); report.states.lost_response_same_candidate_and_key = true; }
  await page.locator('#candidate-meta').filter({ hasText: c.id }).waitFor();
  if (!ready) return c;
  const done = await api.waitCandidate(c.id); assert.equal(done.state, 'ready', done.error?.code);
  await page.locator('#candidate-state').filter({ hasText: '构建与审计成功' }).waitFor({ timeout: 20000 }); return done;
}
async function intent(page, c) {
  await page.goto(new URL(c.review_url, base).href);
  await Promise.all([response(page, 'POST', '/api/admin/publication-requests', 201), page.locator('#request-publish').click()]);
  const p = await navigatedResource(page, '/admin/publication-requests/', '/api/admin/publication-requests/');
  await page.locator('#request-state').filter({ hasText: '待人工确认' }).waitFor();
  assert.equal(await page.locator('#publication-result').isVisible(), false); return p;
}
async function confirm(page, p, api) {
  await page.getByLabel('我已审阅以上完整差异、受影响页面、图片和购买渠道，确认指定目标及版本', { exact: true }).check();
  await Promise.all([response(page, 'POST', `/api/admin/publication-requests/${p.id}/confirm`, 202), page.locator('#confirm-publication').click()]);
  const done = await api.waitRequest(p.id); assert.equal(done.state, 'succeeded', done.error?.code);
  await page.locator('#publication-result').filter({ hasText: done.result_release_id }).waitFor();
  assert(await page.locator('#publication-result').textContent().then(text => text.includes(done.completed_at)));
  assert.equal(await page.getByRole('link', { name: '打开成功结果的官网链接' }).getAttribute('href'), done.site_url);
  return done;
}
async function workspace(api) {
  const list = (await api.expect('/api/admin/products?limit=100', {}, 200, 'workspace read')).json;
  const products = [];
  for (const item of list.items) products.push((await api.expect('/api/admin/products/' + item.id, {}, 200, 'workspace document')).json);
  assert.equal(list.next_cursor, null, 'fixture workspace fits this full observation');
  return { revision: (await api.session()).workspace_revision, products };
}

async function returnedReviewLinks(page, candidate, publication, media) {
  let publicationPage;
  report.states.original_review_links = [];
  for (const [kind, resource, metaSelector, apiPath] of [
    ['candidate', candidate, '#candidate-meta', '/api/admin/candidates/' + candidate.id],
    ['publication', publication, '#request-meta', '/api/admin/publication-requests/' + publication.id],
  ]) {
    const original = new URL(resource.review_url, base);
    assert.equal(original.origin, base.origin, 'review link remains on the actual Go origin');
    const direct = await page.goto(original.href); assert.equal(direct.status(), 200);
    await page.locator(metaSelector).filter({ hasText: resource.id }).waitFor();
    assert.equal(page.url(), original.href, 'navigate the service URL without rewriting it');
    assert.equal(await page.locator('form input[name=id]').inputValue(), resource.id);
    const authenticatedMeta = await page.locator(metaSelector).textContent();
    assert(authenticatedMeta.includes(resource.snapshot_sha256));
    const fresh = await context(kind === 'candidate' ? 390 : 320), freshPage = await fresh.newPage(); track(freshPage);
    const privateFile = base.origin + '/api/admin/media/' + media.media_id + '/file';
    assert.equal((await fresh.request.get(base.origin + apiPath)).status(), 401);
    assert.equal((await fresh.request.get(privateFile)).status(), 401);
    assert.equal((await fresh.request.get(base.origin + '/media/' + media.sha256 + '.png')).status(), 404);
    const anonymousDirect = await freshPage.goto(original.href); assert.equal(anonymousDirect.status(), 200);
    await freshPage.waitForURL(url => url.pathname === '/admin/login/');
    assert.equal(new URL(freshPage.url()).searchParams.get('next'), original.pathname + original.search + original.hash);
    await freshPage.getByLabel('用户名', { exact: true }).fill('admin');
    await freshPage.getByLabel('密码', { exact: true }).fill(password);
    await Promise.all([freshPage.waitForURL(original.href), freshPage.getByRole('button', { name: '登录', exact: true }).click()]);
    await freshPage.locator(metaSelector).filter({ hasText: resource.id }).waitFor();
    assert.equal(await freshPage.locator('form input[name=id]').inputValue(), resource.id);
    assert.equal(await freshPage.locator(metaSelector).textContent(), authenticatedMeta, 'login returns to the original frozen review');
    const loaded = await fresh.request.get(base.origin + apiPath); assert.equal(loaded.status(), 200);
    const current = await loaded.json();
    const fields = ['id', 'snapshot_sha256', 'expected_active_release_id', 'diff', ...(kind === 'candidate' ? ['workspace_revision'] : ['kind', 'candidate_id', 'release_id', 'expected_workspace_revision'])];
    for (const field of fields) assert.deepEqual(current[field], resource[field], 'original review field preserved: ' + field);
    const file = await fresh.request.get(privateFile); assert.equal(file.status(), 200);
    assert.equal(createHash('sha256').update(await file.body()).digest('hex'), media.sha256);
    await shot(freshPage, 'original-' + kind + '-login-return.png');
    report.states.original_review_links.push({ kind, id: resource.id, original_review_url: resource.review_url, authenticated_status: direct.status(), anonymous_api_status: 401, anonymous_image_status: 401, anonymous_public_image_status: 404, login_return_url: freshPage.url(), private_image_sha256: media.sha256, bound_review_preserved: true });
    if (kind === 'publication') publicationPage = freshPage;
    else await fresh.close();
  }
  return publicationPage;
}
async function rejectChangedReview(page, pending, api, doc, edit) {
  const original = await page.locator('#request-meta').textContent();
  await api.expect('/api/admin/products/' + doc.id, { method: 'PUT', headers: { 'If-Match': '"' + doc.version + '"' }, body: edit }, 200, 'workspace changed after original-link review');
  const changedSession = await page.context().request.get(base.origin + '/api/admin/session');
  assert((await changedSession.json()).workspace_revision > pending.expected_workspace_revision);
  const refreshed = response(page, 'GET', '/api/admin/publication-requests/' + pending.id, 200);
  await page.locator('#refresh-request').click(); await refreshed;
  await page.locator('#reviewed-workspace').check();
  const [r] = await Promise.all([response(page, 'POST', '/api/admin/publication-requests/' + pending.id + '/confirm', 409), page.locator('#confirm-publication').click()]);
  assert.equal((await r.json()).error.code, 'WORKSPACE_CHANGED'); await page.locator('#request-conflict').filter({ hasText: '不会自动重试' }).waitFor();
  assert.equal(await page.locator('#request-meta').textContent(), original); assert.equal(await page.locator('#confirm-publication').isDisabled(), true);
  assert.equal(await page.locator('#publication-result').isVisible(), false);
  assert.equal((await api.session()).active_release_id, pending.expected_active_release_id); await shot(page, 'confirmation-conflict.png');
  const posts = report.requests.filter(row => row.path === '/api/admin/publication-requests/' + pending.id + '/confirm');
  assert.equal(posts.length, 1); assert.deepEqual(posts[0].body, api.confirmation(pending));
  report.states.original_review_conflict = { request_id: pending.id, status: r.status(), submitted_review: posts[0].body, display_preserved: true };
}
try {
  await live(base.origin);
  const api = client(base.origin, password, report); await api.login();
  const admin = await context(), page = await admin.newPage(); track(page); await login(page);
  if (values['original-links-only']) {
    await check('T06B original candidate and publication links survive login and preserve stale review envelope', async () => {
      const before = await api.session();
      const png = await page.evaluate(async color => {
        const canvas = document.createElement('canvas'); canvas.width = 31; canvas.height = 23;
        const ctx = canvas.getContext('2d'); ctx.fillStyle = '#' + color; ctx.fillRect(0, 0, 31, 23);
        return [...new Uint8Array(await (await new Promise(resolve => canvas.toBlob(resolve, 'image/png'))).arrayBuffer())];
      }, suffix.slice(0, 6));
      const form = new FormData(); form.append('file', new Blob([Buffer.from(png)], { type: 'image/png' }), 'original-review-private.png');
      const media = (await api.expect('/api/admin/media', { method: 'POST', raw: form }, 201, 'private original-link fixture image')).json;
      const draft = incomplete('v09-original-link-' + suffix); draft.name = '原始审阅链接隔离草稿';
      draft.images = [{ media_id: media.media_id, label: '私有审阅图', description: '' }]; draft.cover_media_id = media.media_id;
      const doc = (await api.expect('/api/admin/products', { method: 'POST', body: draft }, 201, 'private original-link fixture draft')).json.product;
      const c = await candidate(page, api), p = await intent(page, c);
      const reviewPage = await returnedReviewLinks(page, c, p, media);
      const edit = editable(doc); edit.summary = '原始链接登录审阅之后发生的工作区修改';
      await rejectChangedReview(reviewPage, p, api, doc, edit);
      assert.equal((await api.session()).active_release_id, before.active_release_id);
      report.states.targeted_fixture = { candidate_id: c.id, request_id: p.id, product_id: doc.id, media_id: media.media_id, active_release_id: before.active_release_id };
    });
  } else {
  report.states.baseline_listed_count = (await api.expect('/api/admin/products?limit=100', {}, 200, 'initial listed count')).json.items.filter(product => product.visibility === 'listed').length;
  let baseline, added, stale, product, privateImage, privatePath, cancelled, channelRelease, roundTripCandidate, roundTripPending;
  const slug = 'v09-product-' + suffix;
  await check('Go same-origin built admin freezes whole workspace and confirms first release', async () => {
    assert.equal((await api.session()).active_release_id, null, 'V09 starts with its own fresh initialized DB');
    assert.equal((await fetch(base.origin + '/')).status, 503);
    baseline = await candidate(page, api, { lostResponse: true }); const pending = await intent(page, baseline);
    assert.equal((await fetch(base.origin + '/')).status, 503, 'intent alone cannot activate');
    await confirm(page, pending, api); report.states.baseline = baseline.id;
    await shot(page, 'first-confirmed-release.png');
  });
  await check('P02 create complete tool with unique images, series and channel through real admin', async () => {
    await page.goto(base.origin + '/admin/products/new/');
    await page.getByLabel('固定路径 slug', { exact: true }).fill(slug);
    let r = response(page, 'POST', '/api/admin/products', 201); await page.getByRole('button', { name: '保存草稿', exact: true }).click(); product = (await (await r).json()).product;
    for (const [label, text] of [['产品名称', 'V09 隔离工具 ' + suffix], ['型号', 'V09-' + suffix], ['产品简介', '仅为本地隔离验收，不代表客户资料'], ['展示系列名称', 'V09 隔离系列'], ['用途（每行一项）', '真实后台发布验收'], ['特点（每行一项）', '隔离数据'], ['说明（每行一项）', '客户资料待核实']]) await page.getByLabel(label, { exact: true }).fill(text);
    await page.getByLabel('分类', { exact: true }).selectOption('straight');
    await page.getByLabel('目标上下架状态', { exact: true }).selectOption('listed');
    await page.getByRole('group', { name: '旧系列归属', exact: true }).getByLabel('F30 系列', { exact: true }).check();
    await page.locator('#media-picker summary').click();
    const png = await page.evaluate(async color => {
      const canvas = document.createElement('canvas'); canvas.width = 37; canvas.height = 29; const ctx = canvas.getContext('2d'); ctx.fillStyle = '#' + color; ctx.fillRect(0, 0, 37, 29); ctx.fillStyle = '#fff'; ctx.fillRect(2, 2, 3, 7);
      return [...new Uint8Array(await (await new Promise(resolve => canvas.toBlob(resolve, 'image/png'))).arrayBuffer())];
    }, suffix.slice(0, 6));
    await page.getByLabel('上传图片', { exact: true }).setInputFiles({ name: 'v09-private.png', mimeType: 'image/png', buffer: Buffer.from(png) });
    r = response(page, 'POST', '/api/admin/media', 201); await page.getByRole('button', { name: '上传并选择', exact: true }).click(); privateImage = await (await r).json(); privatePath = '/media/' + privateImage.sha256 + '.png';
    await page.locator('#media-picker summary').click();
    await page.getByLabel('用作封面', { exact: true }).check();
    await page.getByRole('button', { name: '添加购买渠道', exact: true }).click();
    const channel = page.locator('.channel-row');
    await channel.getByLabel('平台', { exact: true }).fill('隔离平台'); await channel.getByLabel('店铺名称', { exact: true }).fill('隔离店铺 A');
    await channel.getByLabel('商品详情 HTTPS 地址', { exact: true }).fill('https://item.example.test/v09/' + suffix + '/before');
    await channel.getByLabel('型号对应范围', { exact: true }).selectOption('listing_with_variants'); await channel.getByLabel('SKU 选择提示', { exact: true }).fill('选择隔离规格 A'); await channel.getByLabel('启用此渠道', { exact: true }).check();
    r = response(page, 'PUT', '/api/admin/products/' + product.id, 200); await page.getByRole('button', { name: '保存草稿', exact: true }).click(); product = (await (await r).json()).product;
    assert.equal(product.cover_media_id, privateImage.media_id); assert.deepEqual(product.legacy_series_ids, ['s_f30']);
    assert.equal((await fetch(base.origin + '/products/' + slug + '/')).status, 404); assert.equal((await fetch(base.origin + privatePath)).status, 404);
    report.states.product = { id: product.id, slug, media_id: privateImage.media_id, sha256: privateImage.sha256 };
  });
  await check('P09 editing during real build marks candidate stale and preserves frozen diff', async () => {
    stale = await candidate(page, api, { ready: false });
    const doc = (await api.expect('/api/admin/products/p_f30m', {}, 200, 'parallel editor read')).json.product;
    const edit = editable(doc); edit.summary += ' V09 whole workspace second product';
    await api.expect('/api/admin/products/' + doc.id, { method: 'PUT', headers: { 'If-Match': '"' + doc.version + '"' }, body: edit }, 200, 'second product edited during build');
    const done = await api.waitCandidate(stale.id); assert.equal(done.workspace_changed, true); assert.equal(done.state, 'ready');
    await page.locator('#refresh-candidate').click(); await page.locator('#candidate-stale').filter({ hasText: '候选已过期' }).waitFor();
    assert.equal(await page.locator('#request-publish').isDisabled(), true);
    assert(done.diff.changes.some(change => change.product_id === product.id)); assert(!done.diff.changes.some(change => change.product_id === doc.id && (change.after_json ?? '').includes('V09 whole workspace')));
    await shot(page, 'stale-frozen-candidate.png'); report.states.stale_candidate = stale.id;
  });
  await check('P10 protected same-origin root preview, gallery, original image and exit', async () => {
    added = await candidate(page, api);
    assert(added.diff.changes.some(change => change.product_id === 'p_f30m')); assert(added.diff.changes.some(change => change.product_id === product.id));
    await page.locator('#preview-candidate').click(); await page.waitForURL(base.origin + '/');
    await page.locator('#yuanfa-preview').filter({ hasText: '其他官网标签页' }).waitFor();
    assert.equal(await page.locator('meta[name=release-id]').getAttribute('content'), added.id);
    let html = await admin.request.get(base.origin + '/'); assert.equal(html.headers()['cache-control'], 'private, no-store'); assert.equal(html.headers()['x-robots-tag'], 'noindex');
    await page.goto(base.origin + '/products/' + slug + '/'); const image = page.locator('[data-gallery-panel]:not([hidden]) img').first(); await image.scrollIntoViewIfNeeded(); await image.evaluate(image => image.decode());
    assert(await image.evaluate(image => image.naturalWidth > 0));
    const original = await admin.request.get(base.origin + privatePath); assert.equal(original.status(), 200); assert.equal(original.headers()['cache-control'], 'private, no-store');
    const anonymous = await context(), stranger = await anonymous.newPage();
    assert.equal((await stranger.goto(base.origin + '/products/' + slug + '/')).status(), 404);
    assert.equal((await stranger.goto(base.origin + privatePath)).status(), 404);
    assert.equal((await anonymous.request.get(base.origin + '/api/admin/candidates/' + added.id)).status(), 401);
    assert.equal((await anonymous.request.get(base.origin + '/api/admin/media/' + privateImage.media_id + '/file')).status(), 401);
    const previewCookie = (await admin.cookies()).find(cookie => cookie.name === 'yuanfa_preview'); assert(previewCookie?.httpOnly); await anonymous.addCookies([previewCookie]);
    assert.equal((await stranger.goto(base.origin + '/')).status(), 401, 'preview cookie alone grants no access');
    await page.goto(base.origin + '/products/f30m/'); const thumbs = page.locator('[data-gallery-thumb]'); assert(await thumbs.count() > 1); await thumbs.nth(1).click();
    const selected = await page.locator('[data-gallery-panel]:not([hidden])').getAttribute('data-media-id'); await page.keyboard.press('ArrowRight'); assert.notEqual(await page.locator('[data-gallery-panel]:not([hidden])').getAttribute('data-media-id'), selected);
    await page.locator('[data-gallery-panel]:not([hidden]) img').evaluate(image => image.decode());
    for (const width of [1440, 390, 320]) {
      await page.setViewportSize({ width, height: 900 });
      assert.equal(await page.locator('[data-guide-root]:visible,[data-guide]:visible,astro-island[component-url*="guide"]:visible').count(), 0);
      const hiddenByPreview = await page.evaluate(() => { const probe = document.createElement('div'); probe.dataset.guideRoot = ''; document.body.append(probe); const hidden = getComputedStyle(probe).display === 'none'; probe.remove(); return hidden; }); assert(hiddenByPreview, 'preview wrapper hides guide entry');
      await shot(page, 'preview-gallery-' + width + '.png');
    }
    await page.locator('#yuanfa-preview-exit').click(); await page.waitForURL(base.origin + '/'); assert.equal(await page.locator('#yuanfa-preview').count(), 0); assert.equal(await page.locator('meta[name=release-id]').getAttribute('content'), baseline.id);
    assert.equal((await fetch(base.origin + privatePath)).status, 404);
  });
  await check('Pending intent is cancellable and never claims a live release', async () => {
    cancelled = await intent(page, added); assert.equal((await api.session()).active_release_id, baseline.id);
    await page.locator('#cancel-publication').click(); await page.locator('#request-state').filter({ hasText: '已取消' }).waitFor();
    assert.equal(await page.locator('#confirm-publication').isDisabled(), true); assert.equal(await page.locator('#cancel-publication').isDisabled(), true); assert.equal(await page.locator('#publication-result').isVisible(), false);
    assert.equal((await api.session()).active_release_id, baseline.id); report.states.cancelled = cancelled.id;
  });
  await check('Review deadline disables confirmation without replacing server target', async () => {
    const pending = await intent(page, added); const original = await page.locator('#request-meta').textContent();
    await page.clock.setFixedTime(new Date(Date.parse(pending.expires_at) + 1000)); await page.locator('#refresh-request').click();
    await page.locator('#request-state').filter({ hasText: '已到期' }).waitFor(); assert.equal(await page.locator('#confirm-publication').isDisabled(), true); assert.equal(await page.locator('#request-meta').textContent(), original);
    await page.clock.setFixedTime(new Date()); await page.locator('#cancel-publication').click();
    await page.locator('#request-state').filter({ hasText: '已取消' }).waitFor();
    report.states.deadline_check = 'browser clock only; real server 24h expiry not fast-forwarded';
  });
  await check('P09 original review links and login return preserve the envelope rejected by real 409', async () => {
    const pending = await intent(page, added);
    const reviewPage = await returnedReviewLinks(page, added, pending, privateImage);
    const doc = (await api.expect('/api/admin/products/' + product.id, {}, 200, 'channel edit source')).json.product;
    const edit = editable(doc); edit.purchase_options[0].store_name = '隔离店铺 B'; edit.purchase_options[0].url = 'https://item.example.test/v09/' + suffix + '/after'; edit.purchase_options[0].sku_hint = '选择隔离规格 B';
    await rejectChangedReview(reviewPage, pending, api, doc, edit);
  });
  await check('O01 mobile keyboard confirmation publishes full workspace and P02 catalog updates', async () => {
    added = await candidate(page, api); const pending = await intent(page, added);
    await page.setViewportSize({ width: 320, height: 900 });
    await page.locator('#reviewed-workspace').focus(); await page.keyboard.press('Space'); await page.keyboard.press('Tab');
    assert.equal(await page.evaluate(() => document.activeElement?.id), 'confirm-publication');
    await page.keyboard.press('Enter'); const done = await api.waitRequest(pending.id); assert.equal(done.state, 'succeeded'); await page.locator('#publication-result').filter({ hasText: added.id }).waitFor();
    assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)); await shot(page, 'confirmed-mobile-320.png');
    report.states.added = added.id;
    const publicPage = await (await context(390)).newPage(); await publicPage.goto(base.origin + '/products/');
    const card = publicPage.locator('[data-card]').filter({ has: publicPage.locator(`a[href="/products/${slug}/"]`) }); assert.equal(await card.count(), 1);
    const catalogBefore = await publicPage.locator('[data-card]').count();
    assert.equal(catalogBefore, report.states.baseline_listed_count + 1, 'newly published tool is included in the full catalog count');
    await publicPage.locator('input[type=search]').fill('V09-' + suffix); assert.equal(await publicPage.locator('[data-card]:not([hidden])').count(), 1);
    await publicPage.locator('input[type=search]').fill(''); await publicPage.locator('input[name=type][value=straight]').check();
    assert(await card.isVisible()); assert(await publicPage.locator('[data-card]:not([hidden])').count() < catalogBefore);
    await publicPage.goto(base.origin + '/products/f30/');
    await publicPage.getByRole('link', { name: 'V09 隔离工具 ' + suffix, exact: true }).click();
    await publicPage.waitForURL(base.origin + '/products/' + slug + '/'); assert.equal(await publicPage.locator('meta[name=release-id]').getAttribute('content'), added.id); await publicPage.locator('[data-gallery-panel]:not([hidden]) img').evaluate(image => image.decode());
    assert.equal((await fetch(base.origin + privatePath)).status, 200);
  });
  await check('Channel URL store SKU and whole-workspace differences are reviewable before confirmation', async () => {
    const doc = (await api.expect('/api/admin/products/' + product.id, {}, 200, 'after publication edit')).json.product;
    const edit = editable(doc); edit.purchase_options[0].store_name = '隔离店铺 C'; edit.purchase_options[0].url = 'https://item.example.test/v09/' + suffix + '/third'; edit.purchase_options[0].sku_hint = '选择隔离规格 C';
    await api.expect('/api/admin/products/' + product.id, { method: 'PUT', headers: { 'If-Match': '"' + doc.version + '"' }, body: edit }, 200, 'channel diff fixture');
    const c = await candidate(page, api); const diff = page.locator('.publication-change[data-field=purchase_options]'); await diff.locator('summary').click();
    for (const text of ['隔离店铺 B', '隔离店铺 C', '/after', '/third', '选择隔离规格 B', '选择隔离规格 C']) assert((await diff.textContent()).includes(text));
    await page.setViewportSize({ width: 390, height: 900 }); assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)); await shot(page, 'channel-diff-390.png');
    const pending = await intent(page, c); await confirm(page, pending, api); channelRelease = c; report.states.channel_release = c.id;
    roundTripCandidate = await candidate(page, api); roundTripPending = await intent(page, roundTripCandidate);
  });
  await check('P12 historical rollback reviews whole target, preserves workspace and historical resources', async () => {
    const before = await workspace(api);
    await page.goto(base.origin + '/admin/publications/'); const row = page.locator('.publication-record[data-release-id="' + baseline.id + '"]');
    await Promise.all([response(page, 'POST', '/api/admin/publication-requests', 201), row.getByRole('button', { name: '审阅回退到此版本' }).click()]);
    const pending = await navigatedResource(page, '/admin/publication-requests/', '/api/admin/publication-requests/'); assert.equal(pending.kind, 'rollback');
    await page.locator('#request-scope').filter({ hasText: '不覆盖工作区草稿' }).waitFor();
    await confirm(page, pending, api); assert.deepEqual(await workspace(api), before);
    await page.locator('#publication-result').filter({ hasText: '线上已回退，编辑区仍有未发布内容' }).waitFor();
    const withdrawn = await fetch(base.origin + '/products/' + slug + '/'); assert.equal(withdrawn.status, 200); assert.equal(withdrawn.headers.get('x-robots-tag'), 'noindex'); assert((await withdrawn.text()).includes('该产品已下架'));
    const html = await (await fetch(base.origin + '/products/')).text(); assert(!html.includes(`href="/products/${slug}/"`)); assert(html.includes(baseline.id));
    assert.equal((await fetch(base.origin + privatePath)).status, 200, 'previously successful image retained');
    await shot(page, 'rollback-keeps-workspace-390.png'); report.states.final_release = baseline.id;
  });
  await check('P09 equal-workspace activation round trip never revives old candidate or pending intent', async () => {
    const before = await workspace(api);
    await page.goto(base.origin + '/admin/publications/');
    const row = page.locator('.publication-record[data-release-id="' + channelRelease.id + '"]').first();
    await Promise.all([response(page, 'POST', '/api/admin/publication-requests', 201), row.getByRole('button', { name: '审阅回退到此版本' }).click()]);
    const back = await navigatedResource(page, '/admin/publication-requests/', '/api/admin/publication-requests/'); await confirm(page, back, api);
    assert.deepEqual(await workspace(api), before); assert.equal((await api.session()).active_release_id, channelRelease.id);
    await page.goto(base.origin + '/admin/candidates/?id=' + roundTripCandidate.id);
    await page.locator('#candidate-state').filter({ hasText: '构建与审计成功' }).waitFor();
    assert.equal((await api.expect('/api/admin/candidates/' + roundTripCandidate.id, {}, 200, 'same workspace after round trip')).json.workspace_changed, false);
    await Promise.all([response(page, 'POST', '/api/admin/publication-requests', 409), page.locator('#request-publish').click()]);
    await page.locator('#candidate-stale').filter({ hasText: '冲突' }).waitFor(); assert.equal(await page.locator('#request-publish').isDisabled(), true);
    await shot(page, 'round-trip-candidate-conflict.png');
    await page.goto(base.origin + '/admin/publication-requests/?id=' + roundTripPending.id); await page.locator('#request-state').filter({ hasText: '已过期' }).waitFor(); assert.equal(await page.locator('#confirm-publication').isDisabled(), true);
    await page.goto(base.origin + '/admin/publications/'); const original = page.locator('.publication-record[data-release-id="' + baseline.id + '"]').first();
    await Promise.all([response(page, 'POST', '/api/admin/publication-requests', 201), original.getByRole('button', { name: '审阅回退到此版本' }).click()]);
    const final = await navigatedResource(page, '/admin/publication-requests/', '/api/admin/publication-requests/'); await confirm(page, final, api);
    assert.deepEqual(await workspace(api), before); report.states.round_trip = { candidate: roundTripCandidate.id, pending: roundTripPending.id, workspace_unchanged: true, new_intent_status: 409 };
  });
  await check('WorkBuddy one-time issue list template copy and browser-only authority', async () => {
    await page.goto(base.origin + '/admin/workbuddy/'); await page.getByLabel('凭证名称', { exact: true }).fill('V09 isolated WorkBuddy ' + suffix);
    const issue = response(page, 'POST', '/api/admin/mcp-tokens', 201); await page.locator('#issue-token').click();
    const issued = await (await issue).json(); tokenInMemory = issued.secret; const tokenID = issued.token.id;
    await page.locator('#issued-token').waitFor({ state: 'visible' }); assert(Boolean(await page.locator('#token-secret').inputValue()) && await page.locator('#token-secret').inputValue() === tokenInMemory);
    const template = await page.locator('#mcp-template').textContent(); assert(!template.includes(tokenInMemory)); assert(template.includes('${YUANFA_MCP_TOKEN}')); assert.equal(JSON.parse(template).mcpServers['yuanfa-catalog'].url, base.origin + '/mcp');
    await admin.grantPermissions(['clipboard-read', 'clipboard-write']); await page.locator('#copy-template').click(); assert.equal(await page.evaluate(() => navigator.clipboard.readText()), template);
    assert(await page.evaluate(value => !Object.values(localStorage).some(item => item.includes(value)) && !Object.values(sessionStorage).some(item => item.includes(value)), tokenInMemory));
    for (const path of ['/api/admin/mcp-tokens', '/api/admin/publication-requests/' + cancelled.id + '/confirm', '/api/admin/products/' + product.id + '/guide-review']) {
      const r = await fetch(base.origin + path, { method: path.endsWith('mcp-tokens') ? 'GET' : 'POST', headers: { Authorization: 'Bearer ' + tokenInMemory, Origin: base.origin, 'Content-Type': 'application/json' }, body: path.endsWith('mcp-tokens') ? undefined : '{}' }); assert.equal(r.status, 403);
    }
    await page.locator('#dismiss-token').click(); assert.equal(await page.locator('#token-secret').inputValue(), '');
    await page.reload(); const listed = await admin.request.get(base.origin + '/api/admin/mcp-tokens'); const body = await listed.text(); assert(!body.includes(tokenInMemory)); assert(!body.includes('secret_hash'));
    assert.equal(await page.locator('#token-secret').inputValue(), '');
    const row = page.locator('.token-record[data-token-id="' + tokenID + '"]'); await row.waitFor();
    await Promise.all([response(page, 'DELETE', '/api/admin/mcp-tokens/' + tokenID, 204), row.getByRole('button', { name: '撤销此凭证' }).click()]); await row.filter({ hasText: '已撤销' }).waitFor();
    report.states.token = { id: tokenID, one_time_displayed: true, template_secret_free: true, list_secret_free: true, revoked: true, bearer_browser_denials: 3 };
    tokenInMemory = undefined; await shot(page, 'workbuddy-redacted-list.png');
  });
  await check('Logout exits preview; unauthenticated candidate and confirmation links require login', async () => {
    const preview = await candidate(page, api); await page.locator('#preview-candidate').click(); await page.waitForURL(base.origin + '/');
    await page.goto(base.origin + '/admin/candidates/?id=' + preview.id); await page.locator('#logout').click(); await page.waitForURL('**/admin/login/**');
    assert.equal((await admin.cookies()).some(cookie => cookie.name === 'yuanfa_preview'), false); await page.goto(base.origin + '/'); assert.equal(await page.locator('meta[name=release-id]').getAttribute('content'), baseline.id);
    await page.goto(base.origin + '/admin/publication-requests/?id=' + cancelled.id); await page.waitForURL('**/admin/login/**'); assert(new URL(page.url()).searchParams.get('next').includes(cancelled.id));
  });
  await check('P08 actual failed build and full 300s timeout are visible without changing live release', async () => {
    const folder = await mkdtemp(resolve('.work/worker/v09-failures-')), origin = 'http://127.0.0.1:' + (22000 + Number(process.env.TASK_NUMBER ?? 9));
    const env = { ...process.env, TMPDIR: resolve('.work/worker/tmp'), GOTMPDIR: resolve('.work/worker/go-tmp'), DATA_DIR: join(folder, 'data'), HTTP_ADDR: new URL(origin).host, SITE_URL: origin, TEMPLATE_DIR: root };
    const binary = resolve('.work/bin/yuanfa'); let owned;
    try {
      await initialize(binary, env, report); owned = startServer(binary, env, report); await live(origin, owned);
      let failureAPI = client(origin, password, report); await failureAPI.login(); const c = await failureAPI.waitCandidate((await failureAPI.freeze()).id); assert.equal(c.state, 'ready'); await failureAPI.publish(c);
      const beforeHTML = (await failureAPI.request('/', { anonymous: true })).bytes;
      const manifest = JSON.parse(await readFile(join(env.DATA_DIR, 'artifacts', c.id, 'manifest.json'), 'utf8'));
      const imagePath = '/' + manifest.resources.find(resource => resource.path.startsWith('media/')).path;
      const beforeImage = (await failureAPI.request(imagePath, { anonymous: true })).bytes;
      await stopServer(owned); owned = undefined;
      const template = join(folder, 'template'); await copyTemplate(root, template); await copyFile(resolve('scripts/build-candidate.mjs'), join(template, 'scripts/build-candidate.mjs'));
      const config = join(template, 'astro.config.mjs'), original = await readFile(config, 'utf8'); await writeFile(config, 'throw new Error("isolated V09 real template failure");\n' + original);
      env.TEMPLATE_DIR = template; owned = startServer(binary, env, report); await live(origin, owned); failureAPI = client(origin, password, report); await failureAPI.login();
      const failureContext = await context(390), failurePage = await failureContext.newPage(); track(failurePage); await login(failurePage, origin, '/admin/candidates/');
      for (const kind of ['BUILD_FAILED', 'BUILD_TIMEOUT']) {
        if (kind === 'BUILD_TIMEOUT') {
          await stopServer(owned); owned = undefined; await writeFile(config, 'await new Promise(resolve=>setTimeout(resolve,360000));\n' + original); delete env.BUILD_TIMEOUT;
          owned = startServer(binary, env, report); await live(origin, owned); failureAPI = client(origin, password, report); await failureAPI.login();
          await failurePage.reload();
        }
        const previousID = new URL(failurePage.url()).searchParams.get('id'), started = Date.now();
        await Promise.all([response(failurePage, 'POST', '/api/admin/candidates', 202), failurePage.locator('#freeze-workspace').click()]);
        const queued = await navigatedResource(failurePage, '/admin/candidates/', '/api/admin/candidates/', previousID); const failed = await failureAPI.waitCandidate(queued.id); const elapsed = Date.now() - started;
        assert.equal(failed.state, 'failed'); assert.equal(failed.error.code, kind); if (kind === 'BUILD_TIMEOUT') assert(elapsed >= 299000 && elapsed < 330000);
        await failurePage.locator('#candidate-error').filter({ hasText: kind }).waitFor(); assert.equal(await failurePage.locator('#request-publish').isDisabled(), true);
        assert.equal((await failureAPI.session()).active_release_id, c.id); assert((await failureAPI.request('/', { anonymous: true })).bytes.equals(beforeHTML)); assert((await failureAPI.request(imagePath, { anonymous: true })).bytes.equals(beforeImage));
        report.states[kind] = { candidate: failed.id, release: c.id, elapsed_ms: elapsed, html_sha256: createHash('sha256').update(beforeHTML).digest('hex'), image_sha256: createHash('sha256').update(beforeImage).digest('hex') };
        await shot(failurePage, kind.toLowerCase() + '-390.png');
      }
    } finally { await stopServer(owned); await rm(folder, { recursive: true, force: true }); }
  });
  }
  assert.deepEqual(report.page_errors, []);
  for (const request of report.requests) {
    assert([base.origin, 'http://127.0.0.1:' + (22000 + Number(process.env.TASK_NUMBER ?? 9))].includes(request.origin), 'write Origin must match its actual Go page');
    if (request.path !== '/api/admin/login') assert(request.csrf_present);
    if (/candidates$|publication-requests(?:\/[^/]+\/confirm)?$/.test(request.path)) assert(request.idempotency_key_present);
  }
  report.success = true;
} catch (error) {
  // Assertions never compare or include credential strings. No automatic failure screenshot.
  report.error = tokenInMemory ? 'Failure occurred while a one-time credential was in memory; details omitted.' : error.message.replaceAll(password, '[REDACTED]');
  console.error(report.error); process.exitCode = 1;
} finally {
  tokenInMemory = undefined;
  for (const ctx of contexts) await ctx.close().catch(() => {});
  await browser.close(); await browserServer.close();
  await writeFile(join(out, 'admin-publication.json'), JSON.stringify(report, null, 2), { mode: 0o600 });
}
