// Adapted from Roboto Studio Aisle fd8e049, ui/chat-panel.tsx (MIT).
import { useChat } from '@ai-sdk/react';
import { DefaultChatTransport, type UIMessage } from 'ai';
import { useEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react';
import { GuideSession, plainHistory, type Ticket } from '../../scripts/guide/session';
import { GuideHTTPError, GuideStreamFence, currentRelease, observeResponse, responseError } from '../../scripts/guide/protocol';
import { MessageInput } from './message-input';
import { MessageList } from './message-list';
import { GuideResultView, Requirements } from './product';
import { GuideIcon } from './icon';

type Flight = { ticket: Ticket; fence: GuideStreamFence; controller: AbortController; error?: Error };
export function ChatPanel({ session, open, onClose, contextProductId, contactHref }: { session: GuideSession; open: boolean; onClose: () => void; contextProductId?: string; contactHref: string }) {
  const state = useSyncExternalStore(session.subscribe, session.snapshot, session.snapshot);
  const flight = useRef<Flight | null>(null);
  const [draft, setDraft] = useState<{ ticket: Ticket; text: string } | null>(null);
  useEffect(() => {
    setDraft(current => current && (!state.busy || state.stale || state.pending_turn?.request_id !== current.ticket.id) ? null : current);
  }, [state.busy, state.stale, state.pending_turn?.request_id]);
  const [stopping, setStopping] = useState(false);
  const [conditionsOpen, setConditionsOpen] = useState(false);
  const conditionsButton = useRef<HTMLButtonElement>(null);
  const conditionsPanel = useRef<HTMLElement>(null);
  useEffect(() => { if (!open) setConditionsOpen(false); }, [open]);
  useEffect(() => {
    if (!conditionsOpen) return;
    const outside = (event: Event) => {
      if (event.target instanceof Node && !conditionsPanel.current?.contains(event.target) && !conditionsButton.current?.contains(event.target)) setConditionsOpen(false);
    };
    document.addEventListener('pointerdown', outside);
    document.addEventListener('focusin', outside);
    return () => { document.removeEventListener('pointerdown', outside); document.removeEventListener('focusin', outside); };
  }, [conditionsOpen]);
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
      return observeResponse(response, active.fence, signal, text => {
        if (flight.current === active && !signal.aborted && session.state.busy && !session.state.stale && session.state.pending_turn?.request_id === active.ticket.id) setDraft({ ticket: active.ticket, text });
      });
    },
  }), [session, contextProductId]);
  const { sendMessage, setMessages, stop } = useChat({ transport, onError: error => { if (flight.current) flight.current.error = error; }, onFinish: ({ isAbort, isDisconnect, isError }) => { if ((isAbort || isDisconnect || isError) && flight.current) flight.current.error ??= Error('回复未完整结束，请重新提交或撤销。'); } });
  useEffect(() => {
    try { session.hydrate(window.sessionStorage); } catch { session.hydrate(); }
    void session.recheck(currentRelease);
    const leaving = () => {
      const active = flight.current;
      if (active) { setDraft(null); session.fail(active.ticket, '页面已离开，本轮未完成。请重新提交或明确撤销。'); active.controller.abort(); void stop(); }
    };
    const focus = () => { if (!session.state.busy) void session.recheck(currentRelease); };
    window.addEventListener('pagehide', leaving); window.addEventListener('focus', focus);
    return () => { leaving(); window.removeEventListener('pagehide', leaving); window.removeEventListener('focus', focus); };
  }, [session, stop]);
  useEffect(() => { if (open) document.getElementById('guide-message')?.focus({ preventScroll: true }); }, [open]);
  const failed = (active: Flight, error: unknown) => {
    if (flight.current !== active || !session.state.busy || session.state.pending_turn?.request_id !== active.ticket.id) return;
    setDraft(null);
    if (error instanceof GuideHTTPError && error.status === 409 && error.code === 'CATALOG_CHANGED') session.changed();
    else session.fail(active.ticket, error instanceof GuideHTTPError ? `HTTP ${error.status} · ${error.code}：${error.message}${error.fields.length ? '（' + error.fields.join('、') + '）' : ''}` : '回复未完成或数据无效，条件未更新。请重新提交或明确撤销。');
  };
  const consult = async (text: string, retry = false) => {
    if (stopping || state.busy) return;
    const history = retry && state.messages.at(-1)?.role === 'user' && state.messages.at(-1)?.content === text ? state.messages.slice(0, -1) : state.messages;
    let ticket;
    try { ticket = session.begin({ kind: 'consult', text, selected_product_id: null }, retry); } catch { return; }
    const active: Flight = { ticket, fence: new GuideStreamFence(), controller: new AbortController() };
    flight.current = active; setDraft(null);
    setMessages(history.map((message, index): UIMessage => ({ id: 'history-' + index, role: message.role, parts: [{ type: 'text', text: message.content }] })));
    try {
      await sendMessage({ text });
      if (active.error) throw active.error;
      const result = active.fence.finish();
      await session.commit(ticket, result, active.fence.text, currentRelease);
    } catch (error) { failed(active, error); }
    finally { if (flight.current === active) { flight.current = null; setDraft(null); } }
  };
  const select = async (id: string, retry = false) => {
    if (stopping || state.busy) return;
    let ticket;
    try { ticket = session.begin({ kind: 'select', text: '', selected_product_id: id }, retry); } catch { return; }
    const active: Flight = { ticket, fence: new GuideStreamFence(), controller: new AbortController() }; flight.current = active; setDraft(null);
    try {
      const response = await fetch('/api/guide/messages', { method: 'POST', credentials: 'omit', headers: { 'Content-Type': 'application/json', Accept: 'application/json' }, body: JSON.stringify({ release_id: session.pageRelease, selected_product_id: id, requirements: session.state.requirements }), signal: active.controller.signal });
      if (!response.ok) throw await responseError(response);
      if (!response.headers.get('content-type')?.startsWith('application/json')) throw Error('结果格式无效。');
      await session.commit(ticket, await response.json(), '', currentRelease);
    } catch (error) { failed(active, error); }
    finally { if (flight.current === active) { flight.current = null; setDraft(null); } }
  };
  const cancel = async () => {
    setDraft(null);
    const active = flight.current;
    if (!active) return;
    setStopping(true);
    session.fail(active.ticket, '已取消，本轮条件未提交。请重新提交或明确撤销。'); active.controller.abort();
    try { await stop(); } finally { setStopping(false); }
  };
  const pending = state.pending_turn;
  const preview = draft && state.busy && !state.stale && draft.ticket === flight.current?.ticket && pending?.request_id === draft.ticket.id ? draft.text : '';
  return <section className="yg-panel" role="dialog" aria-label="工具选型导购" onKeyDown={event => { if (event.key === 'Escape' && !event.nativeEvent.isComposing) { event.stopPropagation(); if (conditionsOpen) { setConditionsOpen(false); conditionsButton.current?.focus(); } else onClose(); } }}>
    <header><h2>帮我选工具</h2><div className="yg-toolbar">
      <button ref={conditionsButton} className="yg-icon-button" type="button" aria-label="需求条件" title="需求条件" aria-expanded={conditionsOpen} aria-controls="guide-conditions" onClick={() => setConditionsOpen(value => !value)}><GuideIcon name="conditions" /></button>
      <a className="yg-icon-button" href={contactHref} aria-label="电话咨询" title="电话咨询"><GuideIcon name="phone" /></a>
      <button className="yg-icon-button" type="button" aria-label="清空本标签页会话" title="清空对话" onClick={async () => { await cancel(); session.clear(); setMessages([]); setConditionsOpen(false); }}><GuideIcon name="clear" /></button>
      <button className="yg-icon-button" type="button" onClick={onClose} aria-label="关闭导购" title="关闭导购"><GuideIcon name="close" /></button>
    </div></header>
    <aside ref={conditionsPanel} id="guide-conditions" className="yg-conditions-popover" data-open={conditionsOpen} aria-hidden={!conditionsOpen} inert={!conditionsOpen} aria-labelledby="guide-conditions-heading"><h3 id="guide-conditions-heading">需求条件</h3><Requirements requirements={state.requirements} /></aside>
    <div className="yg-scroll">
      {state.storageLimited && <p role="status" className="yg-warning">浏览器存储不可用：会话仅保留在当前页面，跨页不会保留。</p>}
      <MessageList messages={state.messages} busy={state.busy} draft={preview} />
      {state.notice && <p role="status" className="yg-warning" data-guide-notice>{state.notice}</p>}
      {state.stale && <a href={typeof window === 'undefined' ? '/products/' : window.location.href}>刷新当前资料</a>}
      {!state.checked && !state.busy && !state.stale && <button type="button" onClick={() => void session.recheck(currentRelease)}>重试复核目录</button>}
      {pending && !state.busy && <div className="yg-pending" role="status"><p>本轮尚未完成，请重试或撤销。</p><div className="yg-recovery-actions">
        <button className="yg-icon-button" type="button" aria-label="重新提交" title="重新提交" disabled={stopping || !state.checked || state.stale} onClick={() => pending.kind === 'consult' ? void consult(pending.text, true) : void select(pending.selected_product_id!, true)}><GuideIcon name="retry" /></button><button className="yg-icon-button" type="button" aria-label="明确撤销未完成请求" title="撤销未完成请求" disabled={stopping} onClick={() => void session.discard(currentRelease)}><GuideIcon name="close" /></button></div>
      </div>}
      {session.operable && state.result && <GuideResultView result={state.result} onSelect={id => void select(id)} onAnswer={text => void consult(text)} onPurchase={async href => { await session.recheck(currentRelease); if (session.operable) window.location.assign(href); }} />}
    </div>
    <MessageInput onSend={text => void consult(text)} disabled={!session.operable || stopping} busy={state.busy} onCancel={() => void cancel()} />
  </section>;
}
