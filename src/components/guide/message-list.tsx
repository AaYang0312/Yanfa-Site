// Adapted from Roboto Studio Aisle fd8e049, ui/message-list.tsx (MIT).
import { useEffect, useRef } from 'react';
import type { GuideMessage } from '../../generated/contracts';
import { GuideIcon } from './icon';
export function MessageList({ messages, busy, draft = '' }: { messages: GuideMessage[]; busy: boolean; draft?: string }) {
  const endRef = useRef<HTMLDivElement>(null);
  useEffect(() => { endRef.current?.scrollIntoView({ block: 'nearest' }); }, [messages, busy, draft]);
  return <div className="yg-messages" role="log" aria-label="导购对话" aria-live="polite" aria-relevant="additions text">
    {messages.length ? messages.map((message, index) => <div key={index} className={'yg-message yg-message--' + message.role}><span className="yg-sr">{message.role === 'user' ? '您：' : '导购：'}</span>{message.content}</div>) : !busy && <div className="yg-welcome"><GuideIcon name="chat" size={32} /><h3>想了解哪款工具？</h3><p>输入型号，或说说你的作业需求。</p></div>}
    {busy && draft && <div className="yg-message yg-message--assistant" data-guide-draft><span className="yg-sr">导购（正在回复）：</span>{draft}</div>}
    {busy && !draft && <p role="status">正在整理回复…</p>}
    <div ref={endRef} />
  </div>;
}
