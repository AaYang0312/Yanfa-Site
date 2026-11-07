// Adapted from Roboto Studio Aisle fd8e049, ui/message-input.tsx (MIT).
import { useState, type KeyboardEvent } from 'react';
export function MessageInput({ onSend, disabled }: { onSend: (text: string) => void; disabled: boolean }) {
  const [value, setValue] = useState('');
  const handleSend = () => {
    const text = value.trim();
    if (!text || disabled || [...text].length > 2000) return;
    onSend(text); setValue('');
  };
  const handleKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === 'Enter' && !event.shiftKey && !event.metaKey && !event.ctrlKey && !event.altKey && !event.nativeEvent.isComposing && event.keyCode !== 229) { event.preventDefault(); handleSend(); }
  };
  return <form className="yg-input" onSubmit={event => { event.preventDefault(); handleSend(); }}>
    <label htmlFor="guide-message" className="yg-sr">作业需求或条件更正</label>
    <textarea id="guide-message" value={value} rows={2} maxLength={4000} disabled={disabled} placeholder="描述材料、钉长和现场气源…" onChange={event => setValue(event.target.value)} onKeyDown={handleKeyDown} />
    <button type="submit" disabled={disabled || !value.trim() || [...value.trim()].length > 2000}>发送</button>
    {[...value].length > 2000 && <p role="alert">每条消息最多 2000 字。</p>}
  </form>;
}
