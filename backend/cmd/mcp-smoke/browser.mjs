// Real product-editor handoff for the SDK smoke. Credentials only enter stdin.
import assert from 'node:assert/strict';
import { mkdir } from 'node:fs/promises';
import { pathToFileURL } from 'node:url';
import path from 'node:path';
let input = ''; for await (const chunk of process.stdin) input += chunk;
const data = JSON.parse(input); input = '';
let browser;
try {
  const { chromium } = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE || path.resolve('node_modules/playwright-core/index.mjs')).href);
  browser = await chromium.launch({ headless: true, executablePath: process.env.BROWSER_PATH, args: ['--no-sandbox'] });
  const context = await browser.newContext({ viewport: { width: 390, height: 844 } });
  const page = await context.newPage(); page.setDefaultTimeout(20000);
  const errors = []; page.on('pageerror', () => errors.push('browser page error'));
  const target = new URL(data.url, data.base); assert.equal(target.origin, data.base);
  assert.equal(target.pathname, '/admin/products/edit/');
  const productID = target.searchParams.get('id'); assert(productID);
  await page.goto(target.href); await page.waitForURL(url => url.pathname === '/admin/login/');
  assert.equal(new URL(page.url()).searchParams.get('next'), target.pathname + target.search);
  await page.getByLabel('用户名', { exact: true }).fill('admin'); await page.getByLabel('密码', { exact: true }).fill(data.password);
  await page.getByRole('button', { name: '登录', exact: true }).click(); data.password = '';
  await page.waitForURL(target.href); await page.locator('#product-editor').waitFor({ state: 'visible' });
  const result = { original_url: target.href, login_return_url: page.url(), action: data.action };
  await mkdir(data.out, { recursive: true });
  if (data.action === 'upload') {
    const before = await context.request.get(new URL(`/api/admin/products/${productID}`, data.base).href); const original = await before.json();
    const uploaded = page.waitForResponse(r => r.request().method() === 'POST' && new URL(r.url()).pathname === '/api/admin/media');
    await page.locator('#upload-images').setInputFiles({ name: 'sdk-smoke.png', mimeType: 'image/png', buffer: Buffer.from(data.png, 'base64') });
    const response = await uploaded; assert.equal(response.status(), 201); const media = await response.json();
    await page.getByText('图片已添加，请保存产品以更新官网。', { exact: true }).waitFor();
    const current = await context.request.get(new URL(`/api/admin/products/${productID}`, data.base).href);
    assert.deepEqual((await current.json()).product, original.product, 'upload alone must not save product');
    result.media_id = media.media_id; result.product_unchanged = true;
    await page.screenshot({ path: path.join(data.out, 'upload-login-return.png'), fullPage: false });
  } else if (data.action === 'confirm') {
    assert.equal(productID, data.product_id);
    await page.locator('#site-update').filter({ hasText: '有待确认的修改' }).waitFor();
    assert.equal(await page.locator('#site-update').getByText('官网已更新', { exact: true }).count(), 0);
    await page.screenshot({ path: path.join(data.out, 'product-before-confirm.png'), fullPage: false });
    const saved = page.waitForResponse(r => r.request().method() === 'PUT' && new URL(r.url()).pathname === `/api/admin/products/${productID}`);
    await page.getByRole('button', { name: '保存并更新官网', exact: true }).click();
    const response = await saved; assert.equal(response.status(), 200); const envelope = await response.json();
    assert.equal(envelope.product.id, productID); assert.equal(envelope.version, data.expected_version + 1);
    const headers = response.request().headers(); assert.equal(headers['if-match'], `"${data.expected_version}"`); assert(headers['idempotency-key']); assert(headers['x-csrf-token']); assert.equal(headers.origin, data.base);
    await page.locator('#site-update').getByText('官网已更新', { exact: true }).waitFor({ timeout: 180000 });
    const progress = await context.request.get(new URL('/api/admin/site-updates', data.base).href);
    const update = (await progress.json()).items.find(item => item.product_id === productID);
    assert(update && update.version >= envelope.version && update.state === 'succeeded');
    result.product_id = productID; result.version = envelope.version; result.state = update.state; result.public_url = update.public_url;
    await page.screenshot({ path: path.join(data.out, 'product-confirmed.png'), fullPage: false });
  } else throw new Error('unsupported browser action');
  assert.deepEqual(errors, []); result.page_errors = errors;
  console.log(JSON.stringify(result)); await context.close();
} catch {
  console.error('Native browser action failed; credentials and page data suppressed.'); process.exitCode = 1;
} finally { if (browser) await browser.close(); }
