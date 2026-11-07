// Adapted from Roboto Studio Aisle fd8e049, ui/message-list.tsx (MIT).
import { useEffect, useRef } from 'react';
import type { GuideMessage } from '../../generated/contracts';
export function MessageList({ messages, busy }: { messages: GuideMessage[]; busy: boolean }) {
  const endRef = useRef<HTMLDivElement>(null);
  useEffect(() => { endRef.current?.scrollIntoView({ block: 'nearest' }); }, [messages, busy]);
  return <div className="yg-messages" role="log" aria-label="已完成对话" aria-live="polite" aria-relevant="additions text">
    {messages.length ? messages.map((message, index) => <div key={index} className={'yg-message yg-message--' + message.role}><span className="yg-sr">{message.role === 'user' ? '您：' : '导购：'}</span>{message.content}</div>) : <p>请描述作业材料、紧固件和气源。型号页仅作为比较上下文，是否适用仍需核实。</p>}
    {busy && <p role="status">正在核实需求和已发布资料…</p>}
    <div ref={endRef} />
  </div>;
}
