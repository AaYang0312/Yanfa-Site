// V14 imports the accepted T12 process owner. All consultation responses come
// directly from its real Go/Eino/tools/stream encoder; no browser routes or SSE fixtures.
import assert from 'node:assert/strict';
import { mkdir, readFile, writeFile, rm } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { parseArgs } from 'node:util';
import { createGuideHarness, setupGuideFixture, waitGuide } from '../integration/guide/harness.mjs';
import { decodeContract } from '../../src/generated/validate-contract.mjs';
import { launchBrowser, shot, readable, openGuide, send, result, stored, noActions, failure, saveReport } from '../integration/platform/browser.mjs';
import { captureGuide } from '../integration/platform/capture.mjs';

const { values } = parseArgs({ options: { go: { type: 'string' }, root: { type: 'string' }, 'task-number': { type: 'string' }, out: { type: 'string' } } });
assert.ok(values.go && values.root && values['task-number'] && values.out, '--go --root --task-number --out required');
const root = resolve(values.root), out = resolve(values.out); await mkdir(out, { recursive: true, mode: 0o700 });
const report = { success: false, cwd: process.cwd(), argv: process.argv, scope: 'Actual useChat consumes unmodified Go/Eino/public-tool/HTTP streams. Integration-tag model proposals and stream faults only; not Q01/Q02/Q04/Q05 customer/provider/commerce/OpenResty acceptance. Production has no integration controls.', checks: [], screenshots: [], page_errors: [], scenarios: [] };
// Chromium alone uses the short socket path. The imported harness and all Go
// children retain the approved absolute private TMPDIR and GOTMPDIR.
const browserTmp = process.env.TMPDIR;
process.env.TMPDIR = join(root, '.work', process.env.RUN_ROLE || 'worker', 'tmp');
const harness = await createGuideHarness({ go: values.go, root, taskNumber: Number(values['task-number']), out: join(out, 'processes'), withAdmin: true });
let browser;
const configure = async (ctx, value) => { for (const name of ['continue', 'reached.json']) await rm(join(ctx.controlDir, name), { force: true }); await writeFile(join(ctx.controlDir, 'scenario.json'), JSON.stringify(value), { mode: 0o600 }); };
const proposal = (requirements, intent = 'select', reference = 'F30M') => ({ intent, requirements, candidate_product_ids: intent === 'consult' ? ['p_f30m'] : [], selected_product_id: intent === 'select' ? 'p_f30m' : null, selection_reference: intent === 'select' ? reference : null });
const reached = (ctx, name) => waitGuide(async () => { try { return JSON.parse(await readFile(join(ctx.controlDir, 'reached.json'), 'utf8')).barrier === name; } catch (error) { if (error.code !== 'ENOENT') throw error; } }, name);
function frames(raw) { return raw.trim().split(/\r?\n\r?\n/).map(line => { assert.ok(line.startsWith('data: ')); const value = line.slice(6); return value === '[DONE]' ? value : JSON.parse(value); }); }
function complete(raw) {
  const events = frames(raw); assert.deepEqual(events.map(e => e === '[DONE]' ? e : e.type), ['start', 'text-start', 'text-delta', 'text-end', 'data-guide-result', 'finish', '[DONE]']);
  return decodeContract('guide.schema.json#/$defs/GuideResult', JSON.stringify(events.find(e => e.type === 'data-guide-result').data));
}
async function scenario(name, fn) {
  const entry = { name, http: [], network: [], checks: [], observations: {} }; report.scenarios.push(entry);
  await harness.runScenario(name, async ctx => {
    const f = await setupGuideFixture(ctx.baseURL, entry); entry.release_id = f.candidate.id;
    const context = await browser.newContext({ viewport: { width: 390, height: 844 } });
    const page = await context.newPage(); page.setDefaultTimeout(30000);
    const capture = await captureGuide(context, page, entry, out, name); page.on('pageerror', error => report.page_errors.push(error.message));
    page.on('request', request => { if (new URL(request.url()).pathname.startsWith('/go/')) entry.network.push({ kind: 'purchase-navigation', url: request.url() }); });
    try {
      await page.goto(ctx.qaURL + '/products/f30m/'); await openGuide(page);
      await fn(ctx, f, page, entry, () => capture.flush());
      await capture.flush(); entry.stats = await ctx.readStats();
      assert.equal(entry.stats.active_model_calls, 0); entry.success = true;
    } finally { await context.close(); await capture.flush(); await saveReport(out, report); }
  });
  report.checks.push(name); console.log('PASS ' + name);
}
async function selected(ctx, f, page) {
  await configure(ctx, { model_proposal: proposal(f.fixture.requirements), get_product_id: f.product.id });
  await send(page, '我就选F30M，木材木方20mm，气源未知');
  const value = await result(page, 'selected'); assert.deepEqual(value.confirmed_requirements, f.fixture.requirements); return value;
}
try {
  browser = await launchBrowser();
  await scenario('selected-correction-keyboard', async (ctx, f, page, entry, flush) => {
    const req = f.fixture.requirements;
    await configure(ctx, { model_proposal: proposal(req, 'consult'), get_product_id: f.product.id });
    const json = await f.api.expect('/api/guide/messages', { method: 'POST', anonymous: true, headers: { Accept: 'application/json' }, body: { release_id: f.candidate.id, message: 'F30M适合什么材料？', requirements: req } }, 200, 'actual Go JSON consultation');
    await send(page, 'F30M适合什么材料？'); const rec = await result(page, 'clarifying');
    assert.deepEqual(rec, decodeContract('guide.schema.json#/$defs/GuideResult', json.bytes.toString()));
    assert.equal(rec.selection, null); assert.ok(rec.recommendations.some(p => p.product_id === f.product.id));
    let stats = await ctx.readStats(); assert.equal(stats.agent_runs, 2); assert.equal(stats.select_calls, 0);
    const original = page.url();
    await page.getByRole('button', { name: '选这款', exact: true }).focus(); await page.keyboard.press('Enter');
    const button = await result(page, 'selected'); const after = await ctx.readStats();
    assert.equal(after.model_calls, stats.model_calls); assert.equal(after.agent_runs, stats.agent_runs); assert.equal(after.select_calls, 1);
    assert.deepEqual(button.confirmed_requirements, req); assert.deepEqual(button.missing_fields, ['air_supply']); assert.deepEqual(button.selection.missing_fields, button.missing_fields);
    assert.equal(button.selection.match_level, 'needs_confirmation'); assert.equal(page.url(), original);
    entry.observations.explicit_selection = { before: stats, after, result: button };
    for (const width of [390, 320]) {
      await page.setViewportSize({ width, height: 844 }); await page.locator('.yg-scroll').evaluate(node => { node.scrollTop = node.scrollHeight; });
      await readable(page);
      const gap = await page.locator('.yg-confirmation').first().boundingBox(), purchase = await page.locator('[data-guide-purchase]').last().boundingBox();
      assert.ok(gap.y >= 0 && purchase.y + purchase.height <= 844, 'air confirmation and shop visible together');
      entry.observations['selected_' + width] = { gap, purchase, viewport: page.viewportSize() };
      await shot(page, out, 'selected-' + width + '.png', report);
    }
    await page.setViewportSize({ width: 390, height: 480 }); await page.getByLabel('作业需求或条件更正').focus();
    const input = await page.locator('#guide-message').boundingBox(); assert.ok(input.y >= 0 && input.y + input.height < 480);
    await shot(page, out, 'keyboard-390.png', report); entry.observations.keyboard = { input, headless_native_keyboard: true, physical_mobile_keyboard: false };
    await page.setViewportSize({ width: 390, height: 844 });
    const text = await selected(ctx, f, page); assert.deepEqual(text, button);
    stats = await ctx.readStats(); assert.equal(stats.agent_runs, after.agent_runs + 1); assert.equal(stats.select_calls, after.select_calls + 1);
    assert.ok(text.selection.matched_facts.some(fact => fact.sources.some(s => s.public_label.includes('\n'))), 'Chinese multiline public source survived Go and useChat');
    await flush();
    const saved = await stored(page); await page.keyboard.press('Escape'); await page.locator('.yg-launcher').focus(); await page.keyboard.press('Enter');
    assert.deepEqual((await stored(page)).result, saved.result);
    await page.goto(ctx.baseURL + '/products/'); await openGuide(page); assert.deepEqual((await stored(page)).requirements, req); assert.deepEqual((await stored(page)).messages, saved.messages);
    const merged = { ...req, air_supply: false };
    await configure(ctx, { model_proposal: proposal(merged) }); await send(page, '我就选F30M，刚才说错了，现场没有气源');
    const corrected = await result(page, 'no_match'); assert.deepEqual(corrected.confirmed_requirements, merged); assert.equal(corrected.selection, null); assert.ok(corrected.conflicts.length); await noActions(page);
    const correctedStats = await ctx.readStats(); assert.equal(correctedStats.agent_runs, stats.agent_runs + 1); assert.equal(correctedStats.select_calls, stats.select_calls + 1);
    entry.observations.same_turn_correction = { before: stats, after: correctedStats, result: corrected };
    await shot(page, out, 'same-turn-correction-390.png', report);
    await configure(ctx, { model_proposal: proposal(merged, 'select', '第二款') }); await send(page, '就第二款'); const ambiguous = await result(page, 'clarifying'); assert.equal(ambiguous.selection, null); assert.equal(ambiguous.question.field, null); await noActions(page);
    assert.equal(entry.network.filter(x => x.kind === 'purchase-navigation').length, 0);
    const successful = entry.network.filter(x => x.status === 200 && x.accept === 'text/event-stream');
    // response-body capture is passive and the original stream reaches useChat.
    await waitGuide(() => successful.every(x => x.raw !== undefined), 'passive Go stream capture');
    for (const stream of successful) complete(stream.raw);
    const request = entry.network.find(x => x.request?.selected_product_id);
    assert.deepEqual(request.request.requirements, req); assert.deepEqual(Object.keys(request.request).sort(), ['release_id', 'requirements', 'selected_product_id']);
    assert.equal(request.accept, 'application/json');
    entry.checks.push('Go JSON/SSE equivalence', 'inquiry does not select', 'explicit ID zero extra models', 'text selection one Agent run', 'same-turn correction uses all nine fields', 'Chinese multiline data', '390/320 screenshots', 'keyboard focus/Enter/Escape', 'navigation restores complete history');
  });
  for (const fault of ['error', 'disconnect', 'missing-finish', 'missing-done']) {
    await scenario('incomplete-' + fault, async (ctx, f, page, entry, flush) => {
      const previous = await selected(ctx, f, page); const req = { ...f.fixture.requirements, air_supply: false };
      await configure(ctx, { model_proposal: proposal(req), stream_fault: fault });
      // The SDK cancels its network reader on an error event. Preserve a
      // separate real HTTP client's full fault bytes, and record that browser
      // cancellation separately instead of inventing a browser response body.
      const request = { release_id: f.candidate.id, message: '我就选F30M，现场没有气源', requirements: previous.confirmed_requirements, context_product_id: f.product.id };
      const response = await fetch(ctx.baseURL + '/api/guide/messages', { method: 'POST', headers: { 'Content-Type': 'application/json', Accept: 'text/event-stream' }, body: JSON.stringify(request) });
      assert.equal(response.status, 200); const raw = await response.text(); const events = frames(raw);
      const faultFile = join(out, 'incomplete-' + fault + '-direct-client.sse'); await writeFile(faultFile, raw, { mode: 0o600 });
      entry.observations.direct_fault_client = { request, status: response.status, headers: Object.fromEntries(response.headers), file: faultFile, raw };
      assert.equal(events.filter(x => x.type === 'data-guide-result').length, 1); assert.ok(!events.includes('[DONE]'));
      if (fault === 'error') assert.ok(events.some(x => x.type === 'error'));
      if (fault === 'missing-done') assert.ok(events.some(x => x.type === 'finish')); else assert.ok(!events.some(x => x.type === 'finish'));
      await send(page, request.message); await failure(page);
      const failed = await stored(page); assert.deepEqual(failed.requirements, previous.confirmed_requirements); assert.ok(failed.pending_turn);
      await flush();
      await page.reload(); await page.locator('.yg-launcher').click(); await failure(page);
      assert.deepEqual((await stored(page)).pending_turn, failed.pending_turn); await noActions(page);
      await shot(page, out, fault + '-pending-390.png', report);
      await configure(ctx, { model_proposal: proposal(req) }); await page.getByRole('button', { name: '重新提交', exact: true }).click();
      const recovered = await result(page, 'no_match'); assert.deepEqual(recovered.confirmed_requirements, req); await noActions(page);
      await flush();
      const bad = entry.network.filter(x => x.request?.message === request.message)[0];
      assert.equal(bad.status, 200);
      if (bad.loading_error) entry.observations.browser_fault_delivery = { loading_error: bad.loading_error, raw_body_unavailable: true };
      else { assert.ok(bad.raw); assert.deepEqual(frames(bad.raw).map(x => x.type), events.map(x => x.type)); }
      assert.deepEqual(recovered, events.find(x => x.type === 'data-guide-result').data);
      entry.observations.pending = failed.pending_turn; entry.observations.recovered = recovered;
      entry.checks.push('Go stream incomplete', 'useChat never commits partial data or corrected requirements', 'reload cannot revive old purchase', 'successful retry applies correction');
    });
  }
  await scenario('cancellation', async (ctx, f, page, entry) => {
    const previous = await selected(ctx, f, page);
    await configure(ctx, { model_error: 'block' }); await send(page, '我就选F30M，现场没有气源'); await reached(ctx, 'model'); await noActions(page);
    await page.getByRole('button', { name: '取消本轮', exact: true }).click(); await failure(page);
    await waitGuide(async () => (await ctx.readStats()).active_model_calls === 0, 'actual provider cancellation');
    assert.deepEqual((await stored(page)).requirements, previous.confirmed_requirements); assert.ok((await stored(page)).pending_turn);
    await configure(ctx, { model_proposal: proposal({ ...f.fixture.requirements, air_supply: false }) });
    await page.getByRole('button', { name: '重新提交', exact: true }).click(); await result(page, 'no_match'); await noActions(page);
    entry.checks.push('browser abort reaches Go model context', 'active model count drained', 'retry reuses released capacity');
  });
  await scenario('release-completion-fences', async (ctx, f, page, entry) => {
    const previous = await selected(ctx, f, page), handoff = previous.selection.purchase_options[0].handoff_url;
    const freezeNext = async label => { const p = await f.get('p_f30cl'), e = structuredClone(p); const { editable } = await import('../smoke/publication.mjs'); const edit = editable(e); edit.name += ' ' + label; await f.save(p, edit); const c = await f.api.waitCandidate((await f.api.freeze()).id); assert.equal(c.state, 'ready'); return c; };
    const next = await freezeNext('V14-before');
    await configure(ctx, { model_proposal: proposal(f.fixture.requirements), pause_before_result: true });
    await send(page, '我就选F30M'); await reached(ctx, 'before-result'); await noActions(page);
    assert.equal(entry.network.filter(x => x.request?.message === '我就选F30M').length, 0, 'no HTTP headers before complete business result');
    await f.api.publish(next); await ctx.releaseBarrier(); await page.getByRole('link', { name: '刷新当前资料', exact: true }).waitFor(); await noActions(page);
    const old = await f.api.expect(handoff, { anonymous: true }, 409, 'old purchase release invalid'); assert.match(old.headers.get('content-type'), /^text\/html/); assert.equal(old.headers.get('location'), null);
    entry.observations.before_response = { old_release: previous.release_id, current_release: next.id, purchase_status: old.status };
    await page.getByRole('link', { name: '刷新当前资料', exact: true }).click(); await page.locator('.yg-launcher').click();
    await page.getByRole('button', { name: '清空本标签页会话', exact: true }).click();
    await page.waitForFunction(() => !document.querySelector('#guide-message').disabled);
    const selectedNow = await selected(ctx, f, page); assert.equal(selectedNow.release_id, next.id);
    const final = await freezeNext('V14-stream');
    await configure(ctx, { model_proposal: proposal({ ...f.fixture.requirements, air_supply: false }), stream_fault: 'pause' });
    await send(page, '我就选F30M，现场没有气源'); await reached(ctx, 'stream'); await noActions(page);
    const paused = await stored(page); assert.deepEqual(paused.requirements, f.fixture.requirements); assert.ok(paused.pending_turn);
    await f.api.publish(final); await ctx.releaseBarrier();
    await page.getByRole('link', { name: '刷新当前资料', exact: true }).waitFor(); await noActions(page);
    assert.deepEqual((await stored(page)).requirements, f.fixture.requirements); assert.ok((await stored(page)).pending_turn);
    await waitGuide(() => entry.network.some(x => x.request?.message === '我就选F30M，现场没有气源' && x.raw !== undefined), 'complete stale Go stream captured');
    const stream = entry.network.findLast(x => x.request?.message === '我就选F30M，现场没有气源'); const value = complete(stream.raw);
    assert.equal(value.release_id, next.id); assert.equal(value.confirmed_requirements.air_supply, false);
    const current = (await f.api.expect('/api/catalog/release', { anonymous: true }, 200, 'post-stream current release')).json; assert.equal(current.release_id, final.id);
    entry.observations.during_stream = { stream_result: value, current_release: final.id, committed_requirements: (await stored(page)).requirements };
    await shot(page, out, 'release-changed-390.png', report);
    await page.reload(); await page.locator('.yg-launcher').click(); await failure(page); assert.equal((await stored(page)).result, null);
    const rejected = entry.network.find(x => x.status === 409); assert.equal(JSON.parse(rejected.raw).error.code, 'CATALOG_CHANGED');
    entry.checks.push('activation before BeginResponse returns actual JSON409', 'activation during stream does not wait for SSE', 'complete old stream rejected by current-release recheck', 'old purchase actual HTML409 without redirect', 'reload preserves pending correction and no cards');
  });
  assert.deepEqual(report.page_errors, []); report.success = true;
} catch (error) { report.error = error.stack; throw error; }
finally { try { await browser?.close(); } finally { try { await harness.close(); } finally { process.env.TMPDIR = browserTmp; await saveReport(out, report); } } }
