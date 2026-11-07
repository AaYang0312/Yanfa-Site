// Adapted from Roboto Studio Aisle fd8e049, ui/chat-panel.tsx (MIT).
import { useChat } from '@ai-sdk/react';
import { DefaultChatTransport, type UIMessage } from 'ai';
import { useEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react';
import { GuideSession, plainHistory, type Ticket } from '../../scripts/guide/session';
import { GuideHTTPError, GuideStreamFence, currentRelease, observeResponse, responseError } from '../../scripts/guide/protocol';
import { MessageInput } from './message-input';
import { MessageList } from './message-list';
import { GuideResultView, Requirements } from './product';

type Flight = { ticket: Ticket; fence: GuideStreamFence; controller: AbortController; error?: Error };
export function ChatPanel({ session, open, onClose, contextProductId, contactHref }: { session: GuideSession; open: boolean; onClose: () => void; contextProductId?: string; contactHref: string }) {
  const state = useSyncExternalStore(session.subscribe, session.snapshot, session.snapshot);
  const flight = useRef<Flight | null>(null);
  const [stopping, setStopping] = useState(false);
  const transport = useMemo(() => new DefaultChatTransport({
    api: '/api/guide/messages', credentials: 'omit', headers: { Accept: 'text/event-stream' },
    prepareSendMessagesRequest: ({ messages }) => {
      const text = messages.at(-1)?.parts.filter(part => part.type === 'text').map(part => part.text).join('') || '';
      const history = plainHistory(messages.slice(0, -1).filter(message => message.role === 'user' || message.role === 'assistant').map(message => ({ role: message.role as 'user' | 'assistant', content: message.parts.filter(part => part.type === 'text').map(part => part.text).join('') })));
      return { body: { release_id: session.pageRelease, message: text, messages: history, requirements: session.state.requirements, ...(contextProductId ? { context_product_id: contextProductId } : {}) } };
    },
    fetch: async (input, init) => {
      const active = flight.current;
      if (!active) throw Error('请求已结束。');
      const signal = AbortSignal.any([active.controller.signal, ...(init?.signal ? [init.signal] : [])]);
      const response = await fetch(input, { ...init, signal });
      if (!response.ok) throw await responseError(response);
      return observeResponse(response, active.fence, signal);
    },
  }), [session, contextProductId]);
  const { sendMessage, setMessages, stop } = useChat({ transport, onError: error => { if (flight.current) flight.current.error = error; }, onFinish: ({ isAbort, isDisconnect, isError }) => { if ((isAbort || isDisconnect || isError) && flight.current) flight.current.error ??= Error('回复未完整结束，请重新提交或撤销。'); } });
  useEffect(() => {
    try { session.hydrate(window.sessionStorage); } catch { session.hydrate(); }
    void session.recheck(currentRelease);
    const leaving = () => {
      const active = flight.current;
      if (active) { session.fail(active.ticket, '页面已离开，本轮未完成。请重新提交或明确撤销。'); active.controller.abort(); void stop(); }
    };
    const focus = () => { if (!session.state.busy) void session.recheck(currentRelease); };
    window.addEventListener('pagehide', leaving); window.addEventListener('focus', focus);
    return () => { leaving(); window.removeEventListener('pagehide', leaving); window.removeEventListener('focus', focus); };
  }, [session, stop]);
  useEffect(() => { if (open) document.getElementById('guide-message')?.focus({ preventScroll: true }); }, [open]);
  const failed = (active: Flight, error: unknown) => {
    if (flight.current !== active || !session.state.busy || session.state.pending_turn?.request_id !== active.ticket.id) return;
    if (error instanceof GuideHTTPError && error.status === 409 && error.code === 'CATALOG_CHANGED') session.changed();
    else session.fail(active.ticket, error instanceof GuideHTTPError ? `HTTP ${error.status} · ${error.code}：${error.message}${error.fields.length ? '（' + error.fields.join('、') + '）' : ''}` : '回复未完成或数据无效，条件未更新。请重新提交或明确撤销。');
  };
  const consult = async (text: string, retry = false) => {
    if (stopping || state.busy) return;
    const history = retry && state.messages.at(-1)?.role === 'user' && state.messages.at(-1)?.content === text ? state.messages.slice(0, -1) : state.messages;
    let ticket;
    try { ticket = session.begin({ kind: 'consult', text, selected_product_id: null }, retry); } catch { return; }
    const active: Flight = { ticket, fence: new GuideStreamFence(), controller: new AbortController() };
    flight.current = active;
    setMessages(history.map((message, index): UIMessage => ({ id: 'history-' + index, role: message.role, parts: [{ type: 'text', text: message.content }] })));
    try {
      await sendMessage({ text });
      if (active.error) throw active.error;
      const result = active.fence.finish();
      await session.commit(ticket, result, active.fence.text, currentRelease);
    } catch (error) { failed(active, error); }
    finally { if (flight.current === active) flight.current = null; }
  };
  const select = async (id: string, retry = false) => {
    if (stopping || state.busy) return;
    let ticket;
    try { ticket = session.begin({ kind: 'select', text: '', selected_product_id: id }, retry); } catch { return; }
    const active: Flight = { ticket, fence: new GuideStreamFence(), controller: new AbortController() }; flight.current = active;
    try {
      const response = await fetch('/api/guide/messages', { method: 'POST', credentials: 'omit', headers: { 'Content-Type': 'application/json', Accept: 'application/json' }, body: JSON.stringify({ release_id: session.pageRelease, selected_product_id: id, requirements: session.state.requirements }), signal: active.controller.signal });
      if (!response.ok) throw await responseError(response);
      if (!response.headers.get('content-type')?.startsWith('application/json')) throw Error('结果格式无效。');
      await session.commit(ticket, await response.json(), '', currentRelease);
    } catch (error) { failed(active, error); }
    finally { if (flight.current === active) flight.current = null; }
  };
  const cancel = async () => {
    const active = flight.current;
    if (!active) return;
    setStopping(true);
    session.fail(active.ticket, '已取消，本轮条件未提交。请重新提交或明确撤销。'); active.controller.abort();
    try { await stop(); } finally { setStopping(false); }
  };
  const pending = state.pending_turn;
  return <section className="yg-panel" role="dialog" aria-label="工具选型导购" onKeyDown={event => { if (event.key === 'Escape' && !event.nativeEvent.isComposing) { event.stopPropagation(); onClose(); } }}>
    <header><h2>帮我选工具</h2><button type="button" onClick={onClose} aria-label="关闭导购">关闭</button></header>
    <div className="yg-scroll">
      <nav aria-label="导购备用入口"><a href="/products/">搜索与筛选</a><a href={contactHref}>电话咨询</a></nav>
      {contextProductId && <p className="yg-context">正在比较本页型号；页面上下文不代表适用性已确认。</p>}
      {state.storageLimited && <p role="status" className="yg-warning">浏览器存储不可用：会话仅保留在当前页面，跨页不会保留。</p>}
      <MessageList messages={state.messages} busy={state.busy} />
      <Requirements requirements={state.requirements} />
      {state.notice && <p role="status" className="yg-warning" data-guide-notice>{state.notice}</p>}
      {state.stale && <a href={typeof window === 'undefined' ? '/products/' : window.location.href}>刷新当前资料</a>}
      {!state.checked && !state.busy && !state.stale && <button type="button" onClick={() => void session.recheck(currentRelease)}>重试复核目录</button>}
      {pending && <div className="yg-pending" role="status"><p>有未完成的{pending.kind === 'consult' ? '咨询或条件更正' : '型号选择'}；旧选择和购买已停用。</p>{pending.text && <p>{pending.text}</p>}
        {state.busy ? <button type="button" onClick={() => void cancel()}>取消本轮</button> : <><button type="button" disabled={stopping || !state.checked || state.stale} onClick={() => pending.kind === 'consult' ? void consult(pending.text, true) : void select(pending.selected_product_id!, true)}>重新提交</button><button type="button" disabled={stopping} onClick={() => void session.discard(currentRelease)}>明确撤销未完成请求</button></>}
      </div>}
      {session.operable && state.result && <GuideResultView result={state.result} onSelect={id => void select(id)} onAnswer={text => void consult(text)} onPurchase={async href => { await session.recheck(currentRelease); if (session.operable) window.location.assign(href); }} />}
    </div>
    <MessageInput onSend={text => void consult(text)} disabled={!session.operable || stopping} />
    <footer><button type="button" onClick={async () => { await cancel(); session.clear(); setMessages([]); }}>清空本标签页会话</button><small>Enter 发送，Shift+Enter 换行</small></footer>
  </section>;
}
