import { readFile } from 'node:fs/promises';
const fixtures = JSON.parse(await readFile(new URL('../contracts/fixtures.json', import.meta.url), 'utf8'));
export const selected = fixtures.find(item => item.name === 'selected unknown air keeps condition').data;
export function streamFrames(result = selected, text = '中文第一行\n第二行："引号"与\\转义') {
  return [{ type: 'start', messageId: 'reply-1' }, { type: 'text-start', id: 'text-1' }, { type: 'text-delta', id: 'text-1', delta: text }, { type: 'text-end', id: 'text-1' }, { type: 'data-guide-result', data: structuredClone(result) }, { type: 'finish', finishReason: 'stop' }, '[DONE]'];
}
export function encodeFrames(frames) { return frames.map(frame => 'data: ' + (typeof frame === 'string' ? frame : JSON.stringify(frame)) + '\n\n').join(''); }
export function memoryStorage() {
  const map = new Map();
  return { getItem: key => map.get(key) ?? null, setItem: (key, value) => map.set(key, value), removeItem: key => map.delete(key) };
}
