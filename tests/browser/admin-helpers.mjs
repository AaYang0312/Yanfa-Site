// Shared real-origin browser harness. Secrets remain in memory, never in reports.
import assert from 'node:assert/strict';
import { mkdir, readFile, stat, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { randomUUID } from 'node:crypto';
import { setTimeout as delay } from 'node:timers/promises';

export function editable(product) {
  const { id, slug, version, updated_at, ...edit } = structuredClone(product);
  delete edit.guide_profile.reviews;
  return edit;
}
export function decodeToolResult(result) {
  assert(result && typeof result.structuredContent === 'object' && result.structuredContent !== null, 'MCP response requires structuredContent');
  // Conversational content is intentionally human-readable, not JSON.
  return { value: result.structuredContent, error: result.isError === true, text: (result.content ?? []).filter(item => item.type === 'text').map(item => item.text).join('\n') };
}
export function productEditLink(text, base, id) {
  const match = text.match(/(?:https?:\/\/[^\s]+)?\/admin\/products\/edit\/\?id=[A-Za-z0-9_%.-]+/);
  assert(match, 'MCP must return a readable product edit link');
  const link = new URL(match[0], base);
  assert.equal(link.origin, new URL(base).origin);
  assert.equal(link.searchParams.get('id'), id);
  return link;
}
export async function openHarness(name) {
  const args = process.argv.slice(2), option = key => { const index = args.indexOf(key); return index < 0 ? undefined : args[index + 1]; };
  const base = new URL(option('--base') ?? process.env.QA_URL);
  assert.equal(base.hostname, '127.0.0.1', 'browser smoke requires an isolated loopback instance');
  const out = resolve(option('--out') ?? process.env.QA_OUTPUT);
  const credentialFile = process.env.SMOKE_ADMIN_PASSWORD_FILE;
  assert(credentialFile, 'private password file required');
  if (process.platform !== 'win32') assert.equal((await stat(credentialFile)).mode & 0o077, 0, 'password file must be private');
  const password = (await readFile(credentialFile, 'utf8')).replace(/\r?\n$/, '');
  const secrets = new Set([password]);
  const { chromium } = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE ?? resolve('node_modules/playwright-core/index.mjs')).href);
  const browser = await chromium.launch({ executablePath: process.env.BROWSER_PATH, headless: true, args: ['--no-sandbox'] });
  const contexts = [], pendingReports = [], createdProducts = new Set(), issuedTokens = new Set();
  const report = { success: false, base: base.origin, checks: [], requests: [], screenshots: [], page_errors: [], states: {} };
  await mkdir(out, { recursive: true, mode: 0o700 });
  const redact = value => [...secrets].reduce((text, secret) => secret ? text.replaceAll(secret, '[REDACTED]') : text, String(value));
  const persist = () => writeFile(join(out, `${name}.json`), redact(JSON.stringify(report, null, 2)), { mode: 0o600 });
  async function page(width = 1440, existingContext) {
    const context = existingContext ?? await browser.newContext({ viewport: { width, height: width < 700 ? 900 : 1000 } });
    if (!existingContext) contexts.push(context);
    const page = await context.newPage(); page.setDefaultTimeout(20000);
    page.on('dialog', dialog => dialog.accept());
    page.on('pageerror', error => report.page_errors.push(redact(error.message)));
    page.on('requestfinished', request => {
      const path = new URL(request.url()).pathname;
      if (!path.startsWith('/api/admin/') || request.method() === 'GET') return;
      pendingReports.push((async () => {
        const response = await request.response(), headers = await request.allHeaders();
        report.requests.push({ path, method: request.method(), status: response?.status(), origin: headers.origin, csrf: !!headers['x-csrf-token'], conditional: /^"\d+"$/.test(headers['if-match'] ?? ''), idempotent: !!headers['idempotency-key'] });
      })().catch(() => {}));
    });
    return page;
  }
  async function login(page, next = '/admin/products/') {
    await page.goto(new URL(next, base).href);
    await page.waitForURL(url => url.pathname === '/admin/login/');
    assert.equal(new URL(page.url()).searchParams.get('next'), next);
    await page.getByLabel('用户名', { exact: true }).fill('admin');
    await page.getByLabel('密码', { exact: true }).fill(password);
    await Promise.all([page.waitForURL(new URL(next, base).href), page.getByRole('button', { name: '登录', exact: true }).click()]);
    await page.locator('#session-state').filter({ hasText: 'admin' }).waitFor({ state: 'attached' });
  }
  const response = async (page, method, path, status = 200) => {
    const result = await page.waitForResponse(r => new URL(r.url()).pathname === path && r.request().method() === method);
    assert.equal(result.status(), status, `${method} ${path} returned an unexpected status`);
    return result;
  };
  async function request(page, path, options = {}) {
    const headers = { ...options.headers };
    if (options.method && !['GET', 'HEAD'].includes(options.method)) {
      const session = await page.context().request.get(new URL('/api/admin/session', base).href);
      assert.equal(session.status(), 200, 'fixture session must be valid');
      headers.Origin = base.origin; headers['X-CSRF-Token'] = (await session.json()).csrf_token;
      if (/^\/api\/admin\/products(?:\/|$)/.test(path)) headers['Idempotency-Key'] ??= randomUUID();
    }
    return page.context().request.fetch(new URL(path, base).href, { ...options, headers });
  }
  async function readProduct(page, id) {
    const result = await request(page, `/api/admin/products/${id}`); assert.equal(result.status(), 200); return result.json();
  }
  async function save(page, id, status = id ? 200 : 201, retry = false) {
    const [result] = await Promise.all([response(page, id ? 'PUT' : 'POST', id ? `/api/admin/products/${id}` : '/api/admin/products', status), page.getByRole('button', { name: retry ? '重试保存' : '保存并更新官网', exact: true }).click()]);
    const value = await result.json();
    if (status < 300) {
      assert.match(result.headers().etag, /^"\d+"$/); assert.equal(result.headers().etag, `"${value.version}"`);
      await page.getByRole('button', { name: '保存并更新官网', exact: true }).waitFor();
      await page.waitForFunction(() => !document.querySelector('#save-product').disabled);
      if (!id) createdProducts.add(value.product.id);
    }
    return value;
  }
  async function create(page, name) {
    await page.goto(new URL('/admin/products/new/', base).href);
    await page.getByLabel('产品名称 / 型号', { exact: true }).fill(name);
    await page.getByLabel('产品简介', { exact: true }).fill('隔离浏览器验收资料，不代表在售产品。');
    await page.getByLabel(/^使用场景/).fill('隔离测试用途');
    return save(page);
  }
  async function waitForUpdate(page, id, version, state = 'succeeded') {
    const deadline = Date.now() + Number(process.env.ADMIN_UPDATE_TIMEOUT_MS ?? 180000);
    while (Date.now() < deadline) {
      const response = await request(page, '/api/admin/site-updates'); assert.equal(response.status(), 200);
      const item = (await response.json()).items.find(item => item.product_id === id);
      if (item && item.version >= version && item.state === state) return item;
      if (item?.version >= version && item.state === 'failed' && state === 'succeeded') assert.fail('confirmed product update failed');
      await delay(250);
    }
    assert.fail(`site update did not reach ${state}`);
  }
  async function remove(page, id) {
    const current = await readProduct(page, id);
    await page.goto(new URL(`/admin/products/edit/?id=${id}`, base).href);
    await Promise.all([response(page, 'DELETE', `/api/admin/products/${id}`), page.getByRole('button', { name: '删除产品', exact: true }).click()]);
    await page.getByRole('heading', { name: '产品已删除', exact: true }).waitFor();
    createdProducts.delete(id); return current.version + 1;
  }
  async function screenshot(page, filename) {
    const secret = page.locator('#token-secret');
    assert(!(await secret.count()) || !(await secret.inputValue()), 'refuse to capture a one-time secret');
    assert.equal(await page.locator('input[type=password]').count(), 0, 'refuse to capture login');
    await page.screenshot({ path: join(out, filename), fullPage: true }); report.screenshots.push(filename);
  }
  async function check(label, run) { await run(); report.checks.push(label); console.log(`PASS ${label}`); await persist(); }
  async function finish(error, cleanupPage) {
    // Only test-created products/connections are eligible for cleanup.
    if (cleanupPage && !cleanupPage.isClosed()) {
      for (const id of issuedTokens) await request(cleanupPage, `/api/admin/mcp-tokens/${id}`, { method: 'DELETE' }).catch(() => {});
      for (const id of createdProducts) {
        try { const current = await readProduct(cleanupPage, id); await request(cleanupPage, `/api/admin/products/${id}`, { method: 'DELETE', headers: { 'If-Match': `"${current.version}"` } }); } catch { /* Keep the original failure. */ }
      }
    }
    await Promise.allSettled(pendingReports);
    try {
      if (error) throw error;
      assert.deepEqual(report.page_errors, []);
      for (const row of report.requests) {
        assert.equal(row.origin, base.origin);
        if (row.path !== '/api/admin/login') assert(row.csrf);
        if (/^\/api\/admin\/products(?:\/|$)/.test(row.path)) {
          assert(row.idempotent); if (row.method !== 'POST') assert(row.conditional);
        }
      }
      report.success = true;
    } catch (failure) { report.error = redact(failure.message); console.error(report.error); process.exitCode = 1; }
    finally { for (const context of contexts) await context.close().catch(() => {}); await browser.close(); await persist(); }
  }
  return { base, report, page, login, response, request, readProduct, save, create, remove, waitForUpdate, screenshot, check, finish, secrets, issuedTokens, createdProducts, suffix: randomUUID().slice(0, 12), originalLinksOnly: args.includes('--original-links-only') };
}
