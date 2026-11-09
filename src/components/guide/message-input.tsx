// Adapted from Roboto Studio Aisle fd8e049, ui/message-input.tsx (MIT).
import { useState, type KeyboardEvent } from 'react';
import { GuideIcon } from './icon';
export function MessageInput({ onSend, disabled, busy, onCancel }: { onSend: (text: string) => void; disabled: boolean; busy: boolean; onCancel: () => void }) {
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
    <div className="yg-composer">
      <textarea id="guide-message" value={value} rows={2} maxLength={4000} disabled={disabled} placeholder="输入型号或作业需求…" onChange={event => setValue(event.target.value)} onKeyDown={handleKeyDown} />
      {busy ? <button className="yg-icon-button yg-send" type="button" aria-label="取消本轮" title="取消本轮" onClick={onCancel}><GuideIcon name="stop" /></button> : <button className="yg-icon-button yg-send" type="submit" aria-label="发送" title="发送" disabled={disabled || !value.trim() || [...value.trim()].length > 2000}><GuideIcon name="send" /></button>}
    </div>
    {[...value].length > 2000 && <p role="alert">每条消息最多 2000 字。</p>}
  </form>;
}
