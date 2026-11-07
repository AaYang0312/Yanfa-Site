import { checkedResult } from './session.ts';
import type { GuideResult, ReleaseResponse } from '../../generated/contracts';
import { validateContract } from '../../generated/validate-contract.mjs';

/** Observe bytes delivered to the SDK; SDK ready/onFinish alone is insufficient. */
export class GuideStreamFence {
  private decoder = new TextDecoder('utf-8', { fatal: true });
  private buffer = '';
  private phase: 'start' | 'text-start' | 'text' | 'data' | 'finish' | 'done' | 'eof' = 'start';
  private textID = '';
  result: GuideResult | null = null;
  text = '';
  push(bytes: Uint8Array) {
    this.buffer += this.decoder.decode(bytes, { stream: true });
    this.frames();
  }
  private frames() {
    // Event boundaries may split anywhere, including between CR and LF.
    for (;;) {
      const boundary = /\r?\n\r?\n/.exec(this.buffer);
      if (!boundary) return;
      const frame = this.buffer.slice(0, boundary.index);
      this.buffer = this.buffer.slice(boundary.index + boundary[0].length);
      const data = frame.split(/\r?\n/).filter(line => line.startsWith('data:')).map(line => line.slice(5).replace(/^ /, '')).join('\n');
      if (!data) { if (frame.trim() && !frame.split(/\r?\n/).every(line => line.startsWith(':'))) throw Error('消息流事件无效。'); continue; }
      if (this.phase === 'eof') throw Error('结束标记后出现额外数据。');
      if (data === '[DONE]') {
        if (this.phase !== 'done') throw Error('消息流缺少完整结果或 finish。');
        this.phase = 'eof';
        continue;
      }
      const chunk = JSON.parse(data);
      if (!chunk || typeof chunk !== 'object') throw Error('消息流数据无效。');
      if (chunk.type === 'error' || chunk.type === 'abort') throw Error('回复未完成，请重新提交或撤销。');
      switch (this.phase) {
        case 'start':
          if (chunk.type !== 'start' || typeof chunk.messageId !== 'string' || !chunk.messageId) throw Error('消息流缺少 start。');
          this.phase = 'text-start'; break;
        case 'text-start':
          if (chunk.type !== 'text-start' || typeof chunk.id !== 'string' || !chunk.id) throw Error('消息流缺少文本开始事件。');
          this.textID = chunk.id; this.phase = 'text'; break;
        case 'text':
          if (chunk.id !== this.textID) throw Error('消息流文本 ID 不一致。');
          if (chunk.type === 'text-delta' && typeof chunk.delta === 'string') this.text += chunk.delta;
          else if (chunk.type === 'text-end') this.phase = 'data';
          else throw Error('消息流文本事件无效。');
          break;
        case 'data':
          if (chunk.type !== 'data-guide-result') throw Error('消息流缺少完整结果。');
          this.result = checkedResult(chunk.data); this.phase = 'finish'; break;
        case 'finish':
          if (chunk.type !== 'finish' || (chunk.finishReason && !['stop', 'length'].includes(chunk.finishReason))) throw Error('消息流缺少成功 finish 或含重复结果。');
          this.phase = 'done'; break;
        default: throw Error('消息流结束顺序无效。');
      }
    }
  }
  finish() {
    this.buffer += this.decoder.decode(); this.frames();
    if (this.buffer.trim() || this.phase !== 'eof' || !this.result) throw Error('回复未完整结束，请重新提交或撤销。');
    return this.result;
  }
}

export class GuideHTTPError extends Error {
  readonly status: number;
  readonly code: string;
  readonly fields: string[];
  constructor(status: number, code: string, message: string, fields: string[] = []) {
    super(message); this.status = status; this.code = code; this.fields = fields;
  }
}
export async function responseError(response: Response) {
  let body;
  try { body = await response.json(); } catch { /* Keep HTTP identity even for an unreadable response body. */ }
  return new GuideHTTPError(response.status, typeof body?.error?.code === 'string' ? body.error.code : response.status === 503 ? 'UNAVAILABLE' : 'HTTP_ERROR', typeof body?.error?.message === 'string' ? body.error.message : '导购暂不可用，请使用筛选或电话咨询。', Array.isArray(body?.error?.fields) ? body.error.fields.filter((value: unknown) => typeof value === 'string') : []);
}
export async function currentRelease() {
  const response = await fetch('/api/catalog/release', { cache: 'no-store', credentials: 'omit' });
  if (!response.ok) throw await responseError(response);
  const body = validateContract('public-catalog.schema.json#/$defs/ReleaseResponse', await response.json()) as ReleaseResponse;
  return body.release_id;
}

export function observeResponse(response: Response, fence: GuideStreamFence, signal?: AbortSignal | null) {
  if (!response.body || !response.headers.get('content-type')?.startsWith('text/event-stream') || response.headers.get('x-vercel-ai-ui-message-stream') !== 'v1') throw Error('导购消息流协议无效。');
  const reader = response.body.getReader();
  const body = new ReadableStream<Uint8Array>({
    async pull(controller) {
      try {
        if (signal?.aborted) throw new DOMException('Aborted', 'AbortError');
        const chunk = await reader.read();
        if (signal?.aborted) throw new DOMException('Aborted', 'AbortError');
        if (chunk.done) { fence.finish(); controller.close(); }
        else { fence.push(chunk.value); controller.enqueue(chunk.value); }
      } catch (error) { await reader.cancel(error).catch(() => {}); controller.error(error); }
    },
    cancel(reason) { return reader.cancel(reason); },
  });
  return new Response(body, { status: response.status, statusText: response.statusText, headers: response.headers });
}
