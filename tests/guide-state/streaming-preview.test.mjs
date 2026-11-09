import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import { createRequire } from 'node:module';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { build } from 'esbuild';
import { selected, streamFrames, encodeFrames, memoryStorage } from './fixtures.mjs';

// Bundle real components and protocol code together; no SDK or state-machine mocks.
const root = fileURLToPath(new URL('../../', import.meta.url));
const temporary = await mkdtemp(join(tmpdir(), 'yuanfa-guide-streaming-'));
let ui;
try {
  const bundle = await build({
    stdin: { resolveDir: root, contents: [
      'export { ChatPanel } from "./src/components/guide/chat-panel.tsx";',
      'export { MessageList } from "./src/components/guide/message-list.tsx";',
      'export { GuideSession, SESSION_KEY } from "./src/scripts/guide/session.ts";',
      'export { GuideStreamFence, observeResponse } from "./src/scripts/guide/protocol.ts";',
      'export { createElement } from "react";',
      'export { renderToStaticMarkup } from "react-dom/server";'
    ].join('\n') },
    bundle: true, platform: 'node', format: 'cjs', jsx: 'automatic', write: false
  });
  const entry = join(temporary, 'streaming.cjs');
  await writeFile(entry, bundle.outputFiles[0].text);
  ui = createRequire(import.meta.url)(entry);
} finally {
  await rm(temporary, { recursive: true, force: true });
}
const { GuideSession, SESSION_KEY, GuideStreamFence, observeResponse, ChatPanel, MessageList, createElement: h, renderToStaticMarkup: render } = ui;
const headers = { 'content-type': 'text/event-stream', 'x-vercel-ai-ui-message-stream': 'v1' };
const encoder = new TextEncoder();
const beginFrames = () => streamFrames().slice(0, 2);
const delta = text => ({ type: 'text-delta', id: 'text-1', delta: text });
const ending = () => streamFrames().slice(3);
const request = { kind: 'consult', text: '介绍一下 F30', selected_product_id: null };
async function ready() {
  const storage = memoryStorage(), session = new GuideSession('r_test');
  session.hydrate(storage); await session.recheck(async () => 'r_test');
  return { session, storage };
}
function controlled(onText, signal) {
  let source, cancellation;
  const response = new Response(new ReadableStream({
    start(controller) { source = controller; },
    cancel(reason) { cancellation = reason; }
  }), { headers });
  const fence = new GuideStreamFence();
  const reader = observeResponse(response, fence, signal, onText).body.getReader();
  return {
    fence, reader,
    get cancellation() { return cancellation; },
    async bytes(bytes) {
      const reading = reader.read(); source.enqueue(bytes);
      assert.equal((await reading).done, false);
    },
    async frames(frames) { await this.bytes(encoder.encode(encodeFrames(frames))); },
    async end() { source.close(); return reader.read(); },
    fail(error) { source.error(error); }
  };
}
const transcript = (session, draft) => render(h(MessageList, { messages: session.state.messages, busy: session.state.busy, draft }));
const panel = session => render(h(ChatPanel, { session, open: false, onClose() {}, contactHref: 'tel:123456' }));

test('validated text is visible before EOF without storing a draft; final commit shows one reply', async () => {
  const { session, storage } = await ready();
  await session.commit(session.begin(request), selected, '上一轮完整回复', async () => 'r_test');
  assert.match(panel(session), /data-guide-selection="true"/);
  const oldResult = session.state.result;
  const ticket = session.begin(request);
  let draft = '';
  const snapshots = [];
  const stream = controlled(text => { draft = text; snapshots.push(transcript(session, draft)); });
  await stream.frames(beginFrames());
  await stream.frames([delta('先看 F30')]);
  assert.match(snapshots[0], /data-guide-draft/);
  assert.match(snapshots[0], /先看 F30/);
  assert.equal(stream.fence.result, null);
  assert.throws(() => stream.fence.finish(), /未完整结束/);
  assert.equal(session.state.result, oldResult);
  assert.equal(session.operable, false);
  assert.doesNotMatch(panel(session), /data-guide-product/);
  assert.doesNotMatch(storage.getItem(SESSION_KEY), /先看 F30/);
  await stream.frames([delta('，机身小巧。')]);
  assert.equal(draft, '先看 F30，机身小巧。');
  assert.match(snapshots[1], /先看 F30，机身小巧。/);
  assert.doesNotMatch(storage.getItem(SESSION_KEY), /机身小巧/);
  await stream.frames(ending());
  assert.equal(session.state.result, oldResult);
  assert.equal((await stream.end()).done, true);
  assert.equal(await session.commit(ticket, stream.fence.finish(), stream.fence.text, async () => 'r_test'), true);
  assert.equal(transcript(session, draft).split(draft).length - 1, 1);
  assert.doesNotMatch(transcript(session, draft), /data-guide-draft/);
  assert.equal(session.state.messages.filter(message => message.role === 'assistant' && message.content === draft).length, 1);
  assert.match(panel(session), /data-guide-selection="true"/);
});

test('abort interrupts a pending read immediately and preview never becomes history or stored result', async () => {
  const { session, storage } = await ready();
  const ticket = session.begin(request), abort = new AbortController();
  const previews = [];
  const stream = controlled(text => previews.push(text), abort.signal);
  await stream.frames([...beginFrames(), delta('尚未完成的回复')]);
  assert.deepEqual(previews, ['尚未完成的回复']);
  const pendingRead = stream.reader.read();
  const rejected = assert.rejects(pendingRead, error => error.name === 'AbortError');
  session.fail(ticket, '已取消'); abort.abort();
  await rejected;
  assert.equal(stream.cancellation.name, 'AbortError');
  assert.equal(session.state.result, null);
  assert.equal(session.state.messages.some(message => message.role === 'assistant'), false);
  assert.doesNotMatch(storage.getItem(SESSION_KEY), /尚未完成的回复/);
  assert.doesNotMatch(transcript(session, previews[0]), /data-guide-draft|尚未完成的回复/);
  assert.equal(await session.commit(ticket, selected, previews[0], async () => 'r_test'), false);
  assert.equal(previews.length, 1);
  assert.match(panel(session), /aria-label="重新提交"/);
  assert.doesNotMatch(panel(session), /data-guide-product/);
});

test('UTF-8 characters and SSE CRLF boundaries can split byte by byte without broken preview characters', async () => {
  const previews = [];
  const stream = controlled(text => previews.push(text));
  const text = '中文🛠️第一段\n第二段："引号"与\\转义';
  const frames = [...beginFrames(), delta('中文🛠️'), delta('第一段\n第二段："引号"与\\转义'), ...ending()];
  const bytes = encoder.encode(encodeFrames(frames).replaceAll('\n\n', '\r\n\r\n'));
  for (const byte of bytes) await stream.bytes(Uint8Array.of(byte));
  assert.deepEqual(previews, ['中文🛠️', text]);
  assert.equal((await stream.end()).done, true);
  assert.equal(stream.fence.text, text);
  assert.deepEqual(stream.fence.finish(), selected);
  assert.doesNotMatch(previews.join(''), /\uFFFD/);
});

test('an invalid text event is never published to preview and a truncated stream cannot commit', async () => {
  const previews = [];
  const stream = controlled(text => previews.push(text));
  await stream.frames([...beginFrames(), delta('有效前缀')]);
  await assert.rejects(stream.frames([{ ...delta('不该显示'), id: 'another-message' }]), /文本 ID 不一致/);
  assert.deepEqual(previews, ['有效前缀']);
  assert.equal(stream.fence.result, null);
  const truncated = controlled();
  await truncated.frames([...beginFrames(), delta('只有文本，没有结果')]);
  await assert.rejects(truncated.end(), /未完整结束/);
  assert.throws(() => truncated.fence.finish(), /未完整结束/);
});

test('transport failures keep partial text out of history and leave recovery available', async () => {
  const { session, storage } = await ready();
  const ticket = session.begin(request);
  let draft = '';
  const stream = controlled(text => { draft = text; });
  await stream.frames([...beginFrames(), delta('连接断开前的文本')]);
  const reading = stream.reader.read();
  const rejected = assert.rejects(reading, /connection lost/);
  stream.fail(Error('connection lost'));
  await rejected;
  session.fail(ticket, '回复未完成');
  assert.doesNotMatch(transcript(session, draft), /连接断开前的文本|data-guide-draft/);
  assert.doesNotMatch(storage.getItem(SESSION_KEY), /连接断开前的文本/);
  assert.match(panel(session), /aria-label="重新提交"/);
  assert.equal(await session.commit(ticket, selected, draft, async () => 'r_test'), false);
});

test('clear, catalog change and a newer ticket reject any completed older stream', async () => {
  for (const invalidate of ['clear', 'catalog', 'new-turn']) {
    const { session, storage } = await ready();
    const ticket = session.begin(request);
    const stream = controlled();
    await stream.frames([...beginFrames(), delta('过期请求文本')]);
    if (invalidate === 'clear') session.clear();
    else if (invalidate === 'catalog') session.changed();
    else {
      session.fail(ticket, '取消');
      await session.discard(async () => 'r_test');
      session.begin({ ...request, text: '新的需求' });
    }
    await stream.frames(ending()); await stream.end();
    assert.equal(await session.commit(ticket, stream.fence.finish(), stream.fence.text, async () => 'r_test'), false);
    assert.equal(session.state.messages.some(message => message.role === 'assistant'), false);
    assert.doesNotMatch(storage.getItem(SESSION_KEY) || '', /过期请求文本/);
    assert.doesNotMatch(panel(session), /data-guide-product/);
  }
});
