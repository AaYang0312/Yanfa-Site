// Passive CDP byte capture. No Fetch interception, response replacement or page
// fetch wrapper: useChat receives the original Go response unchanged.
import assert from 'node:assert/strict';
import { writeFile } from 'node:fs/promises';
import { join } from 'node:path';
export async function captureGuide(context, page, entry, out, name) {
  const cdp = await context.newCDPSession(page), requests = new Map(), responses = new Map(), streams = new Map(), captures = [];
  // Chromium decodes text/event-stream without a charset as Windows-1252 for
  // getResponseBody, although fetch/useChat correctly decode SSE as UTF-8.
  // This inverse is lossless for every byte; the transport probe records the
  // recovered bytes against the actual HTTP bytes and browser fetch text.
  const inverse = new Map(Array.from({ length: 256 }, (_, byte) => [new TextDecoder('windows-1252').decode(Uint8Array.of(byte)), byte]));
  assert.equal(inverse.size, 256);
  await cdp.send('Network.enable');
  cdp.on('Network.requestWillBeSent', event => {
    if (new URL(event.request.url).pathname === '/api/guide/messages') requests.set(event.requestId, event.request);
  });
  cdp.on('Network.responseReceived', event => {
    const request = requests.get(event.requestId); if (!request) return;
    const record = { request_id: event.requestId, status: event.response.status, headers: Object.fromEntries(Object.entries(event.response.headers).map(([key, value]) => [key.toLowerCase(), value])), cdp_charset: event.response.charset, request: JSON.parse(request.postData), accept: Object.entries(request.headers).find(([key]) => key.toLowerCase() === 'accept')?.[1] };
    responses.set(event.requestId, record); entry.network.push(record);
    if (record.headers['content-type']?.startsWith('text/event-stream')) {
      const stream = { initial: null, chunks: [] }; streams.set(event.requestId, stream);
      // Events can arrive before this command resolves. Keep the buffered prefix
      // separate so it always precedes the subsequent dataReceived byte chunks.
      stream.ready = cdp.send('Network.streamResourceContent', { requestId: event.requestId }).then(value => {
        stream.initial = Buffer.from(value.bufferedData, 'base64');
      }).catch(error => { record.stream_error = error.stack; });
      captures.push(stream.ready);
    }
  });
  cdp.on('Network.dataReceived', event => {
    const stream = streams.get(event.requestId);
    if (stream && event.data !== undefined) stream.chunks.push(Buffer.from(event.data, 'base64'));
  });
  function finish(event, loadingError) {
    const record = responses.get(event.requestId); if (!record) return;
    if (loadingError) record.loading_error = loadingError;
    captures.push((async () => {
      const stream = streams.get(event.requestId); await stream?.ready;
      let bytes;
      if (stream && stream.initial !== null) {
        bytes = Buffer.concat([stream.initial, ...stream.chunks]);
        record.capture = 'CDP streamResourceContent bufferedData + dataReceived bytes';
        record.buffered_byte_length = stream.initial.length;
        record.chunk_byte_length = bytes.length - stream.initial.length;
      } else {
        // A resource may finish before streaming can be enabled. The original
        // completed-response path remains valid; a cancelled body is not guessed.
        if (loadingError) return;
        const value = await cdp.send('Network.getResponseBody', { requestId: event.requestId });
        if (value.base64Encoded) bytes = Buffer.from(value.body, 'base64');
        else if (record.headers['content-type']?.startsWith('text/event-stream') && !record.cdp_charset) {
          assert.ok([...value.body].every(char => inverse.has(char)), 'Chromium default charset must be reversible');
          bytes = Buffer.from([...value.body].map(char => inverse.get(char)));
        } else bytes = Buffer.from(value.body, 'utf8');
        record.capture = value.base64Encoded ? 'CDP base64 bytes' : record.cdp_charset || 'CDP Windows-1252 inverse to UTF-8 bytes';
      }
      record.byte_length = bytes.length; record.captured_on = loadingError ? 'loadingFailed' : 'loadingFinished';
      if (record.headers['content-type']?.startsWith('text/event-stream')) { record.file = join(out, name + '-' + entry.network.indexOf(record) + '.sse'); await writeFile(record.file, bytes, { mode: 0o600 }); }
      record.raw = new TextDecoder('utf-8', { fatal: true }).decode(bytes);
    })().catch(error => { record.capture_error = error.stack; }));
  }
  cdp.on('Network.loadingFinished', event => finish(event));
  cdp.on('Network.loadingFailed', event => finish(event, event.errorText));
  return { async flush() { await Promise.all(captures); assert.deepEqual(entry.network.filter(record => record.capture_error), [], 'passive byte capture must succeed'); } };
}
