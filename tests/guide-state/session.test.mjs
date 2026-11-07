import test from 'node:test';
import assert from 'node:assert/strict';
import { GuideSession, SESSION_KEY, unknownRequirements, checkedResult } from '../../src/scripts/guide/session.ts';
import { GuideStreamFence, GuideHTTPError, observeResponse, responseError } from '../../src/scripts/guide/protocol.ts';
import { selected, streamFrames, encodeFrames, memoryStorage } from './fixtures.mjs';
const consult = { kind: 'consult', text: '现场没有气源，更正之前条件', selected_product_id: null };
async function ready(storage = memoryStorage()) { const session = new GuideSession('r_test'); session.hydrate(storage); await session.recheck(async () => 'r_test'); return session; }

test('only the full ordered protocol survives arbitrary UTF-8 and CRLF chunk boundaries', () => {
  const bytes = new TextEncoder().encode(encodeFrames(streamFrames()).replaceAll('\n\n', '\r\n\r\n'));
  const fence = new GuideStreamFence();
  for (const byte of bytes) fence.push(Uint8Array.of(byte));
  assert.deepEqual(fence.finish(), selected);
  assert.equal(fence.text, '中文第一行\n第二行："引号"与\\转义');
});

test('data alone, SDK-style ready, repeated data, stream errors and missing terminators cannot commit', () => {
  const frames = streamFrames();
  const cases = [frames.filter(x => x.type !== 'data-guide-result'), frames.filter(x => x.type !== 'finish'), frames.slice(0, -1), frames.slice(0, 4), [...frames.slice(0, 5), frames[4], ...frames.slice(5)], [...frames.slice(0, 5), { type: 'error', errorText: 'failed' }], [...frames, frames[4]], [frames[0], { ...frames[1], id: 'other' }, ...frames.slice(2)]];
  for (const input of cases) { const fence = new GuideStreamFence(); assert.throws(() => { fence.push(new TextEncoder().encode(encodeFrames(input))); fence.finish(); }); }
});

test('all result sources strictly reject missing conflicts and invalid state/conflict combinations', () => {
  const noMatch = { ...structuredClone(selected), state: 'no_match', selection: null, missing_fields: [], conflicts: ['该工具需要压缩空气，现场明确没有可用气源。'] };
  assert.deepEqual(checkedResult(noMatch), noMatch);
  const absent = structuredClone(noMatch); delete absent.conflicts;
  for (const result of [absent, { ...noMatch, conflicts: [] }, { ...noMatch, conflicts: null }, { ...noMatch, conflicts: ['\u2003'] }, { ...selected, conflicts: ['冲突'] }, { ...selected, state: 'clarifying', selection: null, conflicts: ['冲突'] }]) {
    assert.throws(() => checkedResult(result));
    const fence = new GuideStreamFence(); assert.throws(() => { fence.push(new TextEncoder().encode(encodeFrames(streamFrames(result)))); fence.finish(); });
  }
});

test('complete JSON/SSE candidates wait for current release and cancellation invalidates queued commit', async () => {
  const session = await ready(); const ticket = session.begin(consult);
  let release; const gate = new Promise(resolve => { release = resolve; });
  const committing = session.commit(ticket, selected, '完整回复', () => gate);
  assert.equal(session.operable, false); assert.deepEqual(session.state.requirements, unknownRequirements());
  session.fail(ticket, '已取消'); release('r_test'); assert.equal(await committing, false);
  assert.deepEqual(session.state.requirements, unknownRequirements()); assert.equal(session.state.pending_turn.text, consult.text);
  await session.discard(async () => 'r_test');
  const next = session.begin(consult); assert.equal(await session.commit(next, selected, '完整回复', async () => 'r_new'), false);
  assert.equal(session.state.stale, true); assert.equal(session.state.result, null); assert.equal(session.operable, false);
});

test('failed correction persists across navigation, cannot use old conditions until explicit withdrawal and recheck', async () => {
  const storage = memoryStorage(), session = await ready(storage);
  await session.commit(session.begin({ ...consult, text: '原条件' }), selected, '已核实', async () => 'r_test');
  const previous = structuredClone(session.state.requirements), ticket = session.begin(consult);
  session.fail(ticket, '网络失败'); assert.equal(session.operable, false);
  const restored = await ready(storage); assert.equal(restored.operable, false); assert.equal(restored.state.pending_turn.text, consult.text); assert.deepEqual(restored.state.requirements, previous);
  assert.throws(() => restored.begin({ kind: 'select', selected_product_id: 'p_test', text: '' }));
  let release; const gate = new Promise(resolve => { release = resolve; }); const withdraw = restored.discard(() => gate);
  assert.equal(restored.operable, false); release('r_test'); await withdraw; assert.equal(restored.operable, true);
  const retry = restored.begin({ ...consult, text: '重新咨询' }); const corrected = structuredClone(selected); corrected.confirmed_requirements.air_supply = false;
  await restored.commit(retry, corrected, '完整新条件', async () => 'r_test'); assert.equal(restored.state.requirements.air_supply, false); assert.equal(restored.state.pending_turn, null);
});

test('old malformed results preserve validated text/conditions but never restore cards; unavailable storage stays usable in memory', async () => {
  const storage = memoryStorage(), source = await ready(storage); await source.commit(source.begin(consult), selected, '历史回复', async () => 'r_test');
  const saved = JSON.parse(storage.getItem(SESSION_KEY)); delete saved.result.conflicts; storage.setItem(SESSION_KEY, JSON.stringify(saved));
  const restored = await ready(storage); assert.equal(restored.state.result, null); assert.equal(restored.state.messages.at(-1).content, '历史回复'); assert.deepEqual(restored.state.requirements, selected.confirmed_requirements);
  const broken = { getItem() { throw Error('denied'); }, setItem() { throw Error('denied'); }, removeItem() { throw Error('denied'); } };
  const memory = await ready(broken); assert.equal(memory.state.storageLimited, true); await memory.commit(memory.begin(consult), selected, '内存回复', async () => 'r_test'); assert.equal(memory.operable, true);
  source.clear(); assert.equal(storage.getItem(SESSION_KEY), null);
});

test('HTTP identity and abort remain failures after a complete candidate was seen', async () => {
  const error = await responseError(new Response(JSON.stringify({ error: { code: 'CATALOG_CHANGED', message: '目录更新', fields: ['release_id'] } }), { status: 409 }));
  assert.ok(error instanceof GuideHTTPError); assert.equal(error.status, 409); assert.equal(error.code, 'CATALOG_CHANGED'); assert.deepEqual(error.fields, ['release_id']);
  const abort = new AbortController(), fence = new GuideStreamFence(); abort.abort();
  const response = observeResponse(new Response(encodeFrames(streamFrames()), { headers: { 'content-type': 'text/event-stream', 'x-vercel-ai-ui-message-stream': 'v1' } }), fence, abort.signal);
  await assert.rejects(response.text(), error => error.name === 'AbortError'); assert.equal(fence.result, null);
});

test('an explicitly retried selection can be revalidated after navigation to the current release', async () => {
  const storage = memoryStorage(), old = await ready(storage);
  await old.commit(old.begin({ ...consult, text: '原条件' }), selected, '完整回复', async () => 'r_test');
  const ticket = old.begin({ kind: 'select', text: '', selected_product_id: 'p_test' }); old.fail(ticket, '切版中断');
  const current = new GuideSession('r_current'); current.hydrate(storage); await current.recheck(async () => 'r_current');
  assert.equal(current.state.result, null); assert.equal(current.operable, false);
  const retry = current.begin({ kind: 'select', text: '', selected_product_id: 'p_test' }, true);
  await current.commit(retry, { ...structuredClone(selected), release_id: 'r_current' }, '', async () => 'r_current');
  assert.equal(current.operable, true); assert.equal(current.state.result.release_id, 'r_current');
});
