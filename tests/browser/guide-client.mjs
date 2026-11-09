// V13: production T11 JSON plus browser-only SSE protocol fixtures. No T12 import.
import assert from 'node:assert/strict';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { resolve, join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { parseArgs } from 'node:util';
import { client, editable, hash, passwordFile, startServer, stopServer, live } from '../smoke/publication.mjs';
import { decodeContract } from '../../src/generated/validate-contract.mjs';
import { streamFrames, encodeFrames } from '../guide-state/fixtures.mjs';
const { values } = parseArgs({ options: { base: { type: 'string' }, out: { type: 'string' } } });
assert.ok(values.base && values.out, '--base and --out required');
const base = values.base, out = resolve(values.out);
assert.equal(new URL(base).hostname, '127.0.0.1');
await mkdir(out, { recursive: true, mode: 0o700 });
const report = { success: false, checks: [], http: [], commands: [], requests: [], results: {}, screenshots: [], observations: {}, page_errors: [], scope: 'Actual no-model production T11; SSE and fault responses are browser-only protocol fixtures. Synthetic reviewed facts and .test channel mappings do not accept Q01/Q02/Q04 or actual model/commerce/proxy behavior.' };
const api = client(base, await passwordFile(), report);
const scenario = JSON.parse(await readFile('tests/fixtures/guide/selection.json', 'utf8'));
const site = JSON.parse(await readFile('src/generated/site-config.json', 'utf8'));
const KEY = 'yuanfa.guide.v1';
let owned, browserServer, browser;
const contexts = [];
const pass = async (label, run) => { await run(); report.checks.push(label); console.log('PASS ' + label); await writeFile(join(out, 'guide-client.json'), JSON.stringify(report, null, 2)); };
const publish = async () => { const candidate = await api.waitCandidate((await api.freeze()).id); assert.equal(candidate.state, 'ready', candidate.error?.code); await api.publish(candidate); return candidate; };
const get = async id => (await api.expect('/api/admin/products/' + id, {}, 200, 'read browser rule fixture')).json.product;
const save = async (product, edit) => (await api.expect('/api/admin/products/' + product.id, { method: 'PUT', headers: { 'If-Match': '"' + product.version + '"' }, body: edit }, 200, 'save browser rule fixture')).json.product;
const actualSelect = async (release, id, requirements, name) => {
  const response = await api.expect('/api/guide/messages', { method: 'POST', anonymous: true, headers: { Accept: 'application/json' }, body: { release_id: release, selected_product_id: id, requirements } }, 200, name);
  const value = decodeContract('guide.schema.json#/$defs/GuideResult', response.bytes.toString()); report.results[name] = value; return value;
};
const recommendation = selected => { const { purchase_options, purchase_status, ...card } = selected.selection; return { ...structuredClone(selected), state: 'recommendations', recommendations: [card], selection: null }; };
const stored = page => page.evaluate(key => JSON.parse(sessionStorage.getItem(key)), KEY);
const pendingDone = page => page.waitForFunction(key => { const s = JSON.parse(sessionStorage.getItem(key)); return s && s.pending_turn === null; }, KEY);
async function screenshot(page, name) { await page.screenshot({ path: join(out, name), fullPage: false }); report.screenshots.push(name); }
async function open(page) { await page.locator('.yg-launcher').click(); await page.getByRole('dialog', { name: '工具选型导购' }).waitFor({ state: 'visible' }); await page.waitForFunction(() => !document.querySelector('#guide-message').disabled); }
async function send(page, text) { await page.getByLabel('作业需求或条件更正').fill(text); await page.getByRole('button', { name: '发送', exact: true }).click(); }
const noActions = async page => { assert.equal(await page.locator('[data-guide-selection], [data-guide-purchase], .yg-result button').count(), 0); };
const waitFailure = async page => { await page.getByRole('button', { name: '明确撤销未完成请求' }).waitFor({ state: 'visible' }); await noActions(page); };
const waitResult = async (page, state) => { await page.locator('[data-guide-state="' + state + '"]').waitFor({ state: 'visible' }); await pendingDone(page); };
let fixture = { result: null, fault: null, status: null, gate: null };
let releaseGate = null;
let fixtureNumber = 0;
async function makePage(width = 390, storageOff = false) {
  const context = await browser.newContext({ viewport: { width, height: 844 } }); contexts.push(context);
  if (storageOff) await context.addInitScript(() => { Object.defineProperty(window, 'sessionStorage', { get() { throw new DOMException('denied', 'SecurityError'); } }); });
  const page = await context.newPage(); page.setDefaultTimeout(180000);
  page.on('pageerror', error => report.page_errors.push(error.message));
  page.on('request', request => { const path = new URL(request.url()).pathname; if (path.startsWith('/go/')) report.requests.push({ path, kind: 'purchase-navigation' }); });
  await page.route('**/api/catalog/release', async route => { if (releaseGate) { const gate = releaseGate; releaseGate = null; gate.reached(); await gate.wait; } await route.continue(); });
  await page.route('**/api/guide/messages', async route => {
    const request = route.request(), body = request.postDataJSON();
    decodeContract('guide.schema.json#/$defs/GuideRequest', JSON.stringify(body));
    const mode = fixture;
    report.requests.push({ kind: body.selected_product_id ? mode.jsonResult ? 'browser-JSON-fault' : 'production-JSON' : 'browser-SSE-fixture', body, accept: request.headers().accept });
    if (body.selected_product_id) {
      if (mode.jsonResult) await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(mode.jsonResult) });
      else await route.continue();
      return;
    }
    if (!mode.result && !mode.status && !mode.fault) { await route.continue(); return; }
    if (mode.gate) { mode.gate.reached(); await mode.gate.wait; }
    if (mode.status) {
      await route.fulfill({ status: mode.status, contentType: 'application/json', body: JSON.stringify({ error: { code: mode.status === 409 ? 'CATALOG_CHANGED' : mode.status === 429 ? 'RATE_LIMITED' : 'VALIDATION_FAILED', message: '受控协议错误', fields: mode.status === 422 ? ['requirements.air_supply'] : [] } }) }); return;
    }
    if (mode.fault === 'network') { await route.abort('failed'); return; }
    let frames = streamFrames(mode.result, '中文第一行\n第二行："引号"、\\反斜线。::document{id="invented"} 只是纯文本。');
    if (mode.fault === 'no-data') frames = frames.filter(x => x.type !== 'data-guide-result');
    if (mode.fault === 'no-finish') frames = frames.filter(x => x.type !== 'finish');
    if (mode.fault === 'no-DONE') frames = frames.slice(0, -1);
    if (mode.fault === 'duplicate') frames.splice(5, 0, frames[4]);
    if (mode.fault === 'error') frames = [...frames.slice(0, 5), { type: 'error', errorText: '受控中断' }];
    if (mode.fault === 'truncated') frames = frames.slice(0, 5);
    const bytes = encodeFrames(frames);
    await writeFile(join(out, 'protocol-' + (++fixtureNumber) + '-' + (mode.fault || 'complete') + '.sse'), bytes);
    try { await route.fulfill({ status: 200, headers: { 'content-type': 'text/event-stream; charset=utf-8', 'cache-control': 'no-store', 'x-vercel-ai-ui-message-stream': 'v1' }, body: bytes }); } catch (error) { if (!mode.gate) throw error; }
  });
  await page.goto(base + '/products/f30m/'); return page;
}
async function seedSession(page, result, pending = null) {
  await page.evaluate(({ key, value }) => sessionStorage.setItem(key, JSON.stringify(value)), { key: KEY, value: { version: 1, messages: [{ role: 'user', content: '历史原条件' }, { role: 'assistant', content: '历史完整回复' }], result, requirements: result.confirmed_requirements, release_id: result.release_id, pending_turn: pending } });
  await page.reload();
  if (pending) { await page.locator('.yg-launcher').click(); await page.getByRole('button', { name: '明确撤销未完成请求' }).waitFor(); }
  else await open(page);
}
try {
  for (const key of Object.keys(process.env)) assert.ok(!/^MODEL_/i.test(key), 'ordinary application must omit all MODEL_*');
  if (process.env.SMOKE_MANAGE_SERVER === '1') owned = startServer(resolve(process.env.SMOKE_YUANFA_BINARY || '.work/bin/yuanfa'), { ...process.env, HTTP_ADDR: new URL(base).host, SITE_URL: base }, report);
  await live(base, owned); await api.login();
  let product = await get('p_f30m'), edit = editable(product);
  edit.guide_profile = structuredClone(scenario.profile);
  edit.guide_profile.sources = [{ id: 't13_source', kind: 'product_text', reference: '/summary', sha256: hash(JSON.stringify(product.summary)), public_label: 'T13 隔离审核来源', public_url: null }];
  edit.purchase_options = structuredClone(scenario.options); product = await save(product, edit);
  const facts = scenario.reviewed_fields.map(field => ({ field, fact_sha256: hash(JSON.stringify(product.guide_profile[field])), source_sha256s: [product.guide_profile.sources[0].sha256] }));
  product = (await api.expect('/api/admin/products/' + product.id + '/guide-review', { method: 'POST', headers: { 'If-Match': '"' + product.version + '"' }, body: { version: product.version, facts } }, 200, 'review exact synthetic facts')).json.product;
  const first = await publish(); report.release_id = first.id;
  assert.equal((await api.expect('/health/ready', { anonymous: true }, 200, 'production no-model health')).json.model, 'unconfigured');
  const selected = await actualSelect(first.id, product.id, scenario.requirements, 'air-null');
  const denied = await actualSelect(first.id, product.id, { ...scenario.requirements, air_supply: false }, 'air-false');
  const materialGap = await actualSelect(first.id, product.id, { ...scenario.requirements, air_supply: true, material: null }, 'air-true-material-null');
  const noMapping = await actualSelect(first.id, 'p_f30cl', scenario.requirements, 'unknown-profile-no-mapping');
  const rec = recommendation(selected);
  const { chromium } = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE || resolve('node_modules/playwright-core/index.mjs')).href);
  browserServer = await chromium.launchServer({ executablePath: process.env.BROWSER_PATH, headless: true, args: ['--no-sandbox'] }); browser = await chromium.connect(browserServer.wsEndpoint()); report.browser_pid = browserServer.process().pid;
  const page = await makePage();
  await pass('production 503 preserves pending correction and compact contact action', async () => {
    await open(page); await send(page, '真实无模型咨询'); await waitFailure(page);
    assert.match(await page.locator('[data-guide-notice]').innerText(), /HTTP 503.*UNAVAILABLE/);
    assert.equal(await page.getByRole('navigation', { name: '导购备用入口' }).count(), 0);
    assert.equal(await page.getByRole('dialog', { name: '工具选型导购' }).getByRole('link', { name: '电话咨询', exact: true }).getAttribute('href'), site.contact_href);
    await page.getByRole('button', { name: '明确撤销未完成请求' }).click(); await page.waitForFunction(() => !document.querySelector('#guide-message').disabled);
  });
  let buttonSelection;
  await pass('G12 button JSON uses complete requirements; air gap and both store channels remain visible', async () => {
    fixture = { result: rec }; await send(page, '木材木方20mm，气源未知'); await waitResult(page, 'recommendations');
    assert.equal(await page.locator('[data-guide-product]').count(), 1); assert.equal(await page.locator('[data-guide-purchase]').count(), 0);
    await page.getByRole('button', { name: '选这款', exact: true }).click(); await waitResult(page, 'selected');
    assert.deepEqual((await stored(page)).result, selected); assert.equal(await page.locator('[data-guide-purchase]').count(), 2);
    assert.match(await page.locator('[data-guide-selection]').innerText(), /压缩空气/); assert.match(await page.locator('[data-guide-selection]').innerText(), /进入商城后选择/);
    assert.equal(await page.locator('.yg-contact').getAttribute('href'), site.contact_href);
    buttonSelection = await page.locator('[data-guide-selection]').innerText();
    const request = report.requests.filter(x => x.kind === 'production-JSON').at(-1); assert.deepEqual(request.body.requirements, scenario.requirements); assert.deepEqual(Object.keys(request.body).sort(), ['release_id', 'requirements', 'selected_product_id']);
    assert.equal(request.accept, 'application/json');
    await page.locator('.yg-scroll').evaluate(element => { element.scrollTop = element.scrollHeight; }); await screenshot(page, 'selected-390.png');
    await page.setViewportSize({ width: 320, height: 844 });
    const scroll = page.locator('.yg-scroll'); await scroll.evaluate(element => { element.scrollTop = element.scrollHeight; });
    const gap = await page.locator('.yg-confirmation').first().boundingBox(), shop = await page.locator('[data-guide-purchase]').last().boundingBox();
    report.observations.mobile_same_screen = { gap, shop, viewport: page.viewportSize() }; assert.ok(gap.y >= 0 && shop.y + shop.height < 844);
    await screenshot(page, 'selected-320.png');
    await page.setViewportSize({ width: 390, height: 480 }); await page.getByLabel('作业需求或条件更正').focus(); await screenshot(page, 'keyboard-focused-390.png');
    const input = await page.locator('#guide-message').boundingBox(), close = await page.getByRole('button', { name: '关闭导购', exact: true }).boundingBox(); assert.ok(input.y + input.height < 480 && close.y >= 0);
    report.observations.keyboard = { viewport: page.viewportSize(), input, close, method: 'Native keyboard focus and reduced visual viewport; no OS virtual keyboard is present in headless Chromium.' };
    await page.setViewportSize({ width: 390, height: 844 });
  });
  await pass('complete SSE selected shares selection renderer and never auto-navigates', async () => {
    fixture = { result: selected }; const before = page.url(); await page.getByLabel('作业需求或条件更正').fill('我就选F30M'); await page.keyboard.press('Enter'); await waitResult(page, 'selected');
    assert.equal(await page.locator('[data-guide-selection]').innerText(), buttonSelection); assert.equal(page.url(), before); assert.equal(report.requests.filter(x => x.kind === 'purchase-navigation').length, 0);
    assert.match(await page.locator('.yg-messages').innerText(), /中文第一行\n第二行/); assert.match(await page.locator('.yg-messages').innerText(), /::document/); assert.equal(await page.locator('[data-guide-product]').count(), 1);
    const sent = report.requests.filter(x => x.kind === 'browser-SSE-fixture').at(-1).body; assert.equal(sent.context_product_id, 'p_f30m'); assert.deepEqual(sent.requirements, selected.confirmed_requirements); assert.ok(sent.messages.every(x => ['user', 'assistant'].includes(x.role) && typeof x.content === 'string')); assert.ok(!('parts' in sent));
    await page.locator('.yg-scroll').evaluate(element => { element.scrollTop = element.scrollHeight; }); await screenshot(page, 'text-selected-390.png');
  });
  await pass('close/open, keyboard IME and cross-page restore preserve committed history and page context', async () => {
    const input = page.getByLabel('作业需求或条件更正'); await input.fill('草稿保留'); await page.keyboard.press('Escape'); assert.equal(await page.locator('.yg-launcher').evaluate(node => node === document.activeElement), true);
    await page.locator('.yg-launcher').click(); assert.equal(await input.inputValue(), '草稿保留');
    const before = report.requests.length; await input.dispatchEvent('keydown', { key: 'Enter', code: 'Enter', isComposing: true, keyCode: 229 }); assert.equal(report.requests.length, before);
    await input.fill('第一行'); await page.keyboard.press('Shift+Enter'); await page.keyboard.type('第二行'); assert.equal(await input.inputValue(), '第一行\n第二行');
    const saved = await stored(page); await page.locator('[data-guide-selection] a').filter({ hasText: '查看详情' }).click(); await open(page); assert.deepEqual((await stored(page)).requirements, saved.requirements); assert.equal((await stored(page)).messages.at(-1).content, saved.messages.at(-1).content);
    await page.goto(base + '/products/'); await open(page); fixture = { result: rec }; await send(page, '仅询问F30M适合什么材料'); await waitResult(page, 'recommendations');
    assert.equal(await page.locator('[data-guide-selection]').count(), 0); assert.equal('context_product_id' in report.requests.filter(x => x.kind === 'browser-SSE-fixture').at(-1).body, false);
  });
  await pass('G13 real T11 air=false reasons and complete SSE no_match render identically without old actions', async () => {
    // Deliberately stale protocol candidate: Select must recompute the full
    // current conditions, rather than trusting the candidate's previous grade.
    const staleCandidate = { ...structuredClone(rec), confirmed_requirements: denied.confirmed_requirements };
    fixture = { result: staleCandidate }; await send(page, '更正：没有气源'); await waitResult(page, 'recommendations'); await page.getByRole('button', { name: '选这款', exact: true }).click(); await waitResult(page, 'no_match');
    assert.deepEqual((await stored(page)).result, denied); const reasons = await page.locator('.yg-conflicts').innerText(); assert.match(reasons, /需要压缩空气.*没有可用气源/); await noActions(page); assert.equal(await page.locator('.yg-contact').getAttribute('href'), site.contact_href);
    await page.locator('.yg-scroll').evaluate(element => { element.scrollTop = element.scrollHeight; }); await screenshot(page, 'no-match-json-390.png');
    fixture = { result: denied }; await send(page, '我就选F30M，现场没有气源'); await waitResult(page, 'no_match'); assert.equal(await page.locator('.yg-conflicts').innerText(), reasons); await noActions(page);
    await page.setViewportSize({ width: 320, height: 844 }); await page.locator('.yg-scroll').evaluate(element => { element.scrollTop = element.scrollHeight; }); await screenshot(page, 'no-match-sse-320.png'); await page.setViewportSize({ width: 390, height: 480 }); await page.getByLabel('作业需求或条件更正').focus(); await page.locator('.yg-scroll').evaluate(element => { element.scrollTop = element.scrollHeight; }); await screenshot(page, 'no-match-keyboard-390.png'); await page.setViewportSize({ width: 390, height: 844 });
  });
  await pass('unknown facts and true air retain real independent gaps; missing mappings retain detail and consultation', async () => {
    fixture = { result: recommendation(materialGap) }; await send(page, '气源有，材料不清楚'); await waitResult(page, 'recommendations'); await page.getByRole('button', { name: '选这款', exact: true }).click(); await waitResult(page, 'selected'); assert.deepEqual((await stored(page)).result, materialGap); assert.deepEqual((await stored(page)).result.missing_fields, ['material']); assert.equal(await page.locator('[data-guide-purchase]').count(), 2);
    fixture = { result: recommendation(noMapping) }; await send(page, '比较另一型号'); await waitResult(page, 'recommendations'); await page.getByRole('button', { name: '选这款', exact: true }).click(); await waitResult(page, 'selected'); assert.deepEqual((await stored(page)).result, noMapping); assert.match(await page.locator('[data-guide-selection]').innerText(), /无法确认|待核实/); assert.match(await page.locator('[data-guide-selection]').innerText(), /暂无可用商城映射/); assert.equal(await page.locator('[data-guide-purchase]').count(), 0); assert.equal(await page.locator('.yg-contact').getAttribute('href'), site.contact_href);
    const empty = { ...structuredClone(denied), conflicts: ['当前已发布目录暂无可供导购的上架商品，请联系咨询。'] }; fixture = { result: empty }; await send(page, '受控空目录协议'); await waitResult(page, 'no_match'); assert.equal(await page.locator('.yg-conflicts').innerText(), empty.conflicts[0]); await noActions(page);
  });
  await pass('all malformed results and incomplete/error streams retain pending correction across reload and prevent old purchases', async () => {
    const missing = structuredClone(denied); delete missing.conflicts;
    const bad = [{ name: 'missing-conflicts', result: missing }, { name: 'empty-conflicts', result: { ...denied, conflicts: [] } }, { name: 'wrong-conflicts', result: { ...denied, conflicts: 'conflict' } }, { name: 'selected-conflict', result: { ...selected, conflicts: ['非法冲突'] } }, { name: 'clarifying-conflict', result: { ...selected, state: 'clarifying', selection: null, conflicts: ['非法冲突'] } }, ...['no-data', 'no-finish', 'no-DONE', 'duplicate', 'error', 'truncated', 'network'].map(fault => ({ name: fault, result: denied, fault }))];
    report.observations.rejected = [];
    for (const mode of bad) {
      await seedSession(page, selected); fixture = mode; await send(page, '更正没有气源：' + mode.name); await waitFailure(page); const failed = await stored(page); assert.deepEqual(failed.requirements, selected.confirmed_requirements); assert.ok(failed.pending_turn);
      await page.reload(); await page.locator('.yg-launcher').click(); await page.getByRole('button', { name: '明确撤销未完成请求' }).waitFor(); await noActions(page); assert.deepEqual((await stored(page)).pending_turn, failed.pending_turn);
      fixture = { result: denied }; await page.getByRole('button', { name: '重新提交', exact: true }).click(); await waitResult(page, 'no_match'); assert.equal((await stored(page)).requirements.air_supply, false); await noActions(page); report.observations.rejected.push(mode.name);
    }
  });
  await pass('cancellation before response, leave-page pending and explicit withdrawal never restore old operations early', async () => {
    await seedSession(page, selected); let release, reached; const arrived = new Promise(resolve => { reached = resolve; }); fixture = { result: denied, gate: { wait: new Promise(resolve => { release = resolve; }), reached } };
    await send(page, '更正没有气源，取消'); await arrived; await noActions(page); await page.getByRole('button', { name: '取消本轮', exact: true }).click(); release(); await waitFailure(page); assert.equal((await stored(page)).requirements.air_supply, null);
    await page.goto(base + '/products/'); await page.locator('.yg-launcher').click(); await page.getByRole('button', { name: '明确撤销未完成请求' }).waitFor(); await noActions(page);
    await page.getByRole('button', { name: '明确撤销未完成请求' }).click(); await page.locator('[data-guide-selection]').waitFor({ state: 'visible' }); assert.equal((await stored(page)).pending_turn, null);
    await seedSession(page, selected); let leaveRelease, leaveReached; const leaveArrived = new Promise(resolve => { leaveReached = resolve; }); fixture = { result: denied, gate: { wait: new Promise(resolve => { leaveRelease = resolve; }), reached: leaveReached } }; await send(page, '页面离开中的更正'); await leaveArrived; await page.goto(base + '/products/f30m/'); leaveRelease(); await page.locator('.yg-launcher').click(); await page.getByRole('button', { name: '明确撤销未完成请求' }).waitFor(); await noActions(page); assert.equal((await stored(page)).pending_turn.text, '页面离开中的更正');
    await screenshot(page, 'pending-restored-390.png');
  });
  await pass('malformed JSON selections use the same strict result boundary and keep old actions disabled', async () => {
    const missing = structuredClone(denied); delete missing.conflicts;
    for (const invalid of [missing, { ...denied, conflicts: [] }, { ...selected, conflicts: ['非法冲突'] }, { ...selected, state: 'clarifying', selection: null, conflicts: ['非法冲突'] }]) {
      await seedSession(page, rec); fixture = { jsonResult: invalid }; await page.getByRole('button', { name: '选这款', exact: true }).click(); await waitFailure(page);
      assert.deepEqual((await stored(page)).requirements, selected.confirmed_requirements); assert.equal((await stored(page)).pending_turn.kind, 'select');
      fixture = {}; await page.getByRole('button', { name: '重新提交', exact: true }).click(); await waitResult(page, 'selected'); assert.deepEqual((await stored(page)).result, selected);
    }
  });
  await pass('strict stored results reject old wire and missing no_match conflicts without reviving actions', async () => {
    for (const invalid of [(() => { const x = structuredClone(selected); delete x.conflicts; return x; })(), (() => { const x = structuredClone(denied); delete x.conflicts; return x; })(), { ...denied, conflicts: [] }]) {
      await seedSession(page, invalid); assert.equal((await stored(page)).result, null); assert.ok((await stored(page)).messages.some(x => x.content === '历史完整回复')); await noActions(page); assert.match(await page.locator('[data-guide-notice]').innerText(), /不完整/);
    }
    await page.getByRole('button', { name: '清空本标签页会话' }).click(); assert.equal(await page.evaluate(key => sessionStorage.getItem(key), KEY), null); assert.equal(await page.locator('.yg-message').count(), 0);
  });
  await pass('storage disabled is explicit and keeps same-page useChat plus selection usable', async () => {
    const memory = await makePage(320, true); fixture = { result: selected }; await open(memory); await memory.getByText('浏览器存储不可用：会话仅保留在当前页面，跨页不会保留。', { exact: true }).waitFor(); await send(memory, '内存会话'); await memory.locator('[data-guide-selection]').waitFor(); assert.equal(await memory.locator('[data-guide-purchase]').count(), 2); await screenshot(memory, 'storage-disabled-320.png'); await memory.close();
  });
  await pass('O01 native mobile navigation, catalog filters and gallery keyboard behavior remain usable', async () => {
    const original = await makePage(320);
    const thumbs = original.locator('[data-gallery-thumb]'); await thumbs.nth(1).click(); const before = await original.locator('[data-gallery-panel]:not([hidden])').getAttribute('data-media-id'); await original.keyboard.press('ArrowRight'); assert.notEqual(await original.locator('[data-gallery-panel]:not([hidden])').getAttribute('data-media-id'), before);
    await original.getByRole('button', { name: '打开菜单', exact: true }).click(); assert.equal(await original.locator('[data-drawer-toggle]').getAttribute('aria-expanded'), 'true'); await original.keyboard.press('Escape'); assert.equal(await original.locator('[data-drawer-toggle]').getAttribute('aria-expanded'), 'false');
    await original.goto(base + '/products/?type=straight'); assert.equal(await original.locator('input[name="type"][value="straight"]').isChecked(), true);
    await original.getByLabel('搜索型号、系列或功能', { exact: true }).fill('f 30-m'); await original.waitForFunction(() => new URL(location.href).searchParams.get('q') === 'f 30-m');
    const visible = original.locator('[data-card]:not([hidden])'); assert.equal(await visible.count(), 1); assert.equal(await visible.locator('.card__head').getAttribute('href'), '/products/f30m/');
    await original.locator('[data-guide-open]').click(); await original.getByRole('dialog', { name: '工具选型导购' }).waitFor({ state: 'visible' }); await original.keyboard.press('Escape'); assert.equal(await original.locator('[data-guide-open]').evaluate(node => node === document.activeElement), true); assert.equal(new URL(original.url()).searchParams.get('q'), 'f 30-m');
    await screenshot(original, 'original-controls-320.png'); await original.close();
  });
  await pass('HTTP 422/429/409 identities are visible, and 409 disables refresh-bound old results', async () => {
    for (const status of [422, 429, 409]) {
      await seedSession(page, selected); fixture = { status }; await send(page, '受控HTTP边界' + status);
      if (status === 409) { await page.getByRole('link', { name: '刷新当前资料', exact: true }).waitFor(); await noActions(page); }
      else { await waitFailure(page); assert.match(await page.locator('[data-guide-notice]').innerText(), new RegExp('HTTP ' + status)); }
    }
  });
  await pass('actual publication between full stream and release recheck rejects commit and old cards', async () => {
    await seedSession(page, selected); let reached, release; const arrived = new Promise(resolve => { reached = resolve; }); releaseGate = { reached, wait: new Promise(resolve => { release = resolve; }) }; fixture = { result: denied };
    await send(page, '完整流后切版'); await arrived; await noActions(page);
    const other = await get('p_f30cl'), changed = editable(other); changed.name += ' T13隔离切版'; await save(other, changed); const second = await publish(); report.changed_release_id = second.id; release(); await page.getByRole('link', { name: '刷新当前资料', exact: true }).waitFor(); await noActions(page); assert.equal((await stored(page)).requirements.air_supply, null); assert.ok((await stored(page)).pending_turn);
    await screenshot(page, 'release-changed-390.png'); await page.getByRole('link', { name: '刷新当前资料', exact: true }).click(); await page.locator('.yg-launcher').click(); await page.getByRole('button', { name: '明确撤销未完成请求' }).waitFor(); await noActions(page); assert.equal((await stored(page)).release_id, second.id); assert.equal((await stored(page)).result, null);
    await seedSession(page, selected, { request_id: 'interrupted-selection', kind: 'select', text: '', selected_product_id: product.id, correcting: true });
    await noActions(page); await page.getByRole('button', { name: '重新提交', exact: true }).click(); await waitResult(page, 'selected');
    assert.equal((await stored(page)).result.release_id, second.id); assert.equal((await stored(page)).requirements.air_supply, null);
    report.observations.explicit_current_release_reselection = (await stored(page)).result;
  });
  assert.deepEqual(report.page_errors, []); report.success = true;
} catch (error) { report.error = error.stack; throw error; }
finally {
  try { for (const context of contexts) await context.close().catch(() => {}); await browser?.close(); await browserServer?.close(); }
  finally { try { await stopServer(owned); } finally { await writeFile(join(out, 'guide-client.json'), JSON.stringify(report, null, 2), { mode: 0o600 }); } }
}
