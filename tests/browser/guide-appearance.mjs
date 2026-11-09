// UI-only browser regression. Model responses are controlled browser fixtures;
// no products, credentials, or public model configuration are changed.
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { readFile, stat, mkdir, writeFile } from 'node:fs/promises';
import { resolve, sep, extname, join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { selected, streamFrames, encodeFrames } from '../guide-state/fixtures.mjs';
const { chromium } = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE || resolve('node_modules/playwright-core/index.mjs')).href);
const out = resolve(process.env.GUIDE_QA_OUTPUT || '.work/guide-appearance');
await mkdir(out, { recursive: true });
let server, browser, releaseGate;
const report = { passed: false, checks: [], page_errors: [], scope: 'UI regression with browser-only response fixtures; no real model validation.' };
try {
  let base = process.env.GUIDE_BASE_URL;
  if (!base) {
    assert.ok(process.env.GUIDE_SITE_DIR, 'Set GUIDE_SITE_DIR or GUIDE_BASE_URL');
    const site = resolve(process.env.GUIDE_SITE_DIR);
    const types = { '.html': 'text/html', '.css': 'text/css', '.js': 'application/javascript', '.svg': 'image/svg+xml', '.png': 'image/png', '.webp': 'image/webp', '.jpg': 'image/jpeg', '.woff2': 'font/woff2' };
    server = createServer(async (req, res) => {
      try {
        let file = resolve(site, '.' + decodeURIComponent(new URL(req.url, 'http://localhost').pathname));
        if (file !== site && !file.startsWith(site + sep)) { res.writeHead(403).end(); return; }
        if ((await stat(file)).isDirectory()) file = resolve(file, 'index.html');
        res.setHeader('Content-Type', types[extname(file)] || 'application/octet-stream'); res.end(await readFile(file));
      } catch { res.writeHead(404).end(); }
    });
    await new Promise(done => server.listen(0, '127.0.0.1', done));
    base = 'http://127.0.0.1:' + server.address().port;
  }
  assert.equal(new URL(base).hostname, '127.0.0.1');
  const html = await (await fetch(base + '/products/bw120g/')).text();
  const release = html.match(/name="release-id" content="([^"]+)"/)[1];
  const result = structuredClone(selected); result.release_id = release;
  result.confirmed_requirements = JSON.parse(await readFile(new URL('../fixtures/guide/selection.json', import.meta.url), 'utf8')).requirements;
  result.question = { field: 'air_supply', text: '现场有可用的压缩空气气源吗？', options: ['有气源', '没有气源'] };
  const source = { public_label: '隔离测试资料', public_url: 'https://example.test/guide-ui' };
  result.selection.matched_facts = [{ field: 'materials', text: '受控夹具中的材料说明', sources: [source] }, { field: 'substrates', text: '受控夹具中的基材说明', sources: [source] }];
  const recommendation = structuredClone(result); recommendation.state = 'recommendations';
  const { purchase_options, purchase_status, ...card } = recommendation.selection;
  recommendation.recommendations = [card]; recommendation.selection = null;
  browser = await chromium.launch({ headless: true, ...(process.env.BROWSER_PATH ? { executablePath: process.env.BROWSER_PATH } : { channel: 'chrome' }) });
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
  const page = await context.newPage(); page.setDefaultTimeout(15000);
  page.on('pageerror', error => report.page_errors.push(error.message));
  await page.route('**/api/catalog/release', route => route.fulfill({ contentType: 'application/json', body: JSON.stringify({ schema_version: 1, release_id: release }) }));
  let mode = 'success', requests = [];
  await page.route('**/api/guide/messages', async route => {
    const body = route.request().postDataJSON(); requests.push(body);
    if (mode === 'hold') await new Promise(done => { releaseGate = done; });
    try {
      if (mode === 'failure') { await route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ error: { code: 'UNAVAILABLE', message: '服务暂时不可用', fields: [] } }) }); return; }
      if (body.selected_product_id) { await route.fulfill({ contentType: 'application/json', body: JSON.stringify(result) }); return; }
      await route.fulfill({ headers: { 'content-type': 'text/event-stream; charset=utf-8', 'x-vercel-ai-ui-message-stream': 'v1' }, body: encodeFrames(streamFrames(recommendation, '已收到你的需求，请核对型号资料。')) });
    } catch { /* cancellation can close the request */ }
  });
  const launcher = page.locator('.yg-launcher'), input = page.locator('#guide-message');
  const panel = page.getByRole('dialog', { name: '工具选型导购' });
  const open = async () => { await launcher.click(); await panel.waitFor(); await page.waitForFunction(() => !document.querySelector('#guide-message').disabled && getComputedStyle(document.querySelector('#guide-panel')).opacity === '1'); };
  await page.goto(base + '/products/bw120g/'); await open();
  assert.equal(await panel.locator('footer, nav').count(), 0);
  assert.equal(await panel.getByRole('link').count(), 1);
  assert.match(await panel.getByRole('link', { name: '电话咨询' }).getAttribute('href'), /^tel:/);
  assert.doesNotMatch(await panel.innerText(), /Shift\+Enter|搜索与筛选|已提交的需求条件/);
  for (const name of ['关闭导购', '清空本标签页会话', '需求条件', '发送']) assert.equal(await panel.getByRole('button', { name, exact: true }).innerText(), '');
  await panel.screenshot({ path: join(out, 'assistant-desktop.png') });
  await input.fill('保留尚未发送的内容'); await input.press('Escape');
  assert.equal(await launcher.getAttribute('aria-expanded'), 'false');
  assert.equal(await page.locator('#guide-panel').evaluate(el => el.inert), true);
  assert.equal(await launcher.evaluate(el => document.activeElement === el), true);
  await open(); assert.equal(await input.inputValue(), '保留尚未发送的内容');
  report.checks.push('compact icon toolbar, no redundant links or key hints, draft and focus preserved');
  const conditions = panel.getByRole('button', { name: '需求条件', exact: true });
  const scrollSize = await panel.locator('.yg-scroll').evaluate(el => ({ height: el.clientHeight, scroll: el.scrollHeight }));
  await conditions.click(); await page.locator('#guide-conditions').waitFor({ state: 'visible' });
  assert.deepEqual(await panel.locator('.yg-scroll').evaluate(el => ({ height: el.clientHeight, scroll: el.scrollHeight })), scrollSize);
  assert.equal(await panel.locator('.yg-scroll .yg-requirements').count(), 0);
  await conditions.press('Escape'); assert.equal(await conditions.getAttribute('aria-expanded'), 'false');
  assert.equal(await launcher.getAttribute('aria-expanded'), 'true');
  await conditions.click(); await conditions.press('Tab');
  assert.equal(await conditions.getAttribute('aria-expanded'), 'false');
  assert.equal(await panel.getByRole('link', { name: '电话咨询', exact: true }).evaluate(el => document.activeElement === el), true);
  for (const width of [320, 390]) {
    await page.setViewportSize({ width, height: 844 });
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
    assert.equal(await panel.evaluate(el => el.scrollWidth > el.clientWidth), false);
    const toolbar = await panel.locator('.yg-toolbar').first().boundingBox(); assert.ok(toolbar.x >= 0 && toolbar.x + toolbar.width <= width);
    await panel.screenshot({ path: join(out, `assistant-${width}.png`) });
  }
  await page.emulateMedia({ reducedMotion: 'reduce' });
  assert.equal(await page.locator('#guide-panel').evaluate(el => getComputedStyle(el).transitionDuration), '0s');
  await page.emulateMedia({ reducedMotion: 'no-preference' });
  report.checks.push('conditions overlay does not resize chat, mobile 320/390 layout, reduced motion');
  await page.setViewportSize({ width: 1440, height: 1000 });
  await input.fill('木材木方，20mm，气源未知'); await panel.getByRole('button', { name: '发送', exact: true }).click();
  await page.locator('[data-guide-state="recommendations"]').waitFor();
  assert.equal(requests.at(-1).context_product_id, 'p_bw120g');
  assert.equal(await page.locator('.yg-sources[open]').count(), 0);
  await page.locator('.yg-sources summary').click();
  assert.equal(await page.locator('.yg-sources a').count(), 1);
  assert.equal(await page.locator('.yg-sources a').getAttribute('href'), source.public_url);
  await page.locator('.yg-sources summary').click();
  await conditions.click();
  await page.waitForFunction(() => getComputedStyle(document.querySelector('#guide-conditions')).opacity === '1');
  assert.match(await page.locator('#guide-conditions').innerText(), /木材/);
  assert.doesNotMatch(await page.locator('#guide-conditions').innerText(), /未知/);
  await panel.screenshot({ path: join(out, 'assistant-conditions.png') });
  await conditions.press('Escape');
  await panel.getByRole('button', { name: '选这款', exact: true }).click(); await page.locator('[data-guide-state="selected"]').waitFor();
  assert.deepEqual(requests.at(-1).requirements, result.confirmed_requirements);
  assert.match(await page.locator('.yg-question').innerText(), /气源/);
  assert.equal(await panel.getByRole('link', { name: '电话咨询', exact: true }).count(), 1);
  await panel.screenshot({ path: join(out, 'assistant-result.png') });
  report.checks.push('fixture conversation and icon selection keep conditions and suitability gaps');
  mode = 'hold'; await input.fill('更正条件'); await panel.getByRole('button', { name: '发送', exact: true }).click();
  await panel.getByRole('button', { name: '取消本轮' }).click();
  await panel.getByRole('button', { name: '明确撤销未完成请求' }).waitFor();
  releaseGate?.(); mode = 'success';
  await panel.getByRole('button', { name: '明确撤销未完成请求' }).click();
  await page.waitForFunction(() => !document.querySelector('#guide-message').disabled);
  mode = 'failure'; await input.fill('测试未完成恢复'); await panel.getByRole('button', { name: '发送', exact: true }).click();
  await panel.getByRole('button', { name: '重新提交', exact: true }).waitFor();
  assert.equal(await page.locator('[data-guide-selection], [data-guide-purchase]').count(), 0);
  mode = 'success'; await panel.getByRole('button', { name: '重新提交', exact: true }).click(); await page.locator('[data-guide-state="recommendations"]').waitFor();
  await panel.getByRole('button', { name: '清空本标签页会话', exact: true }).click(); await page.locator('.yg-welcome').waitFor();
  report.checks.push('stop, discard, failed request retry, clear remain functional');
  await page.goto(base + '/products/yf1206/');
  const placeholder = page.locator('.detail__media .product-placeholder');
  assert.equal((await placeholder.innerText()).trim(), ''); assert.equal(await placeholder.locator('circle').count(), 3);
  assert.match(await placeholder.getAttribute('aria-label'), /暂无实物图/);
  assert.equal(await placeholder.evaluate(el => getComputedStyle(el).animationName), 'none');
  await page.screenshot({ path: join(out, 'other-product.png') });
  report.checks.push('static three-dot image placeholder with accessible meaning');
  assert.deepEqual(report.page_errors, []); report.passed = true;
  console.log(JSON.stringify(report));
} finally {
  releaseGate?.(); await browser?.close();
  if (server) await new Promise(done => server.close(done));
  await writeFile(join(out, 'report.json'), JSON.stringify(report, null, 2));
}
