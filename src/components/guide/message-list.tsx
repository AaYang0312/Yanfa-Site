// Adapted from Roboto Studio Aisle fd8e049, ui/message-list.tsx (MIT).
import { useEffect, useRef } from 'react';
import type { GuideMessage } from '../../generated/contracts';
import { shops } from '../../data/site';
import { GuideIcon } from './icon';
export function MessageList({ messages, busy, draft = '', unavailable = false }: { messages: GuideMessage[]; busy: boolean; draft?: string; unavailable?: boolean }) {
  const endRef = useRef<HTMLDivElement>(null);
  useEffect(() => { endRef.current?.scrollIntoView({ block: 'nearest' }); }, [messages, busy, draft, unavailable]);
  return <div className="yg-messages" role="log" aria-label="导购对话" aria-live="polite" aria-relevant="additions text">
    {messages.length ? messages.map((message, index) => <div key={index} className={'yg-message yg-message--' + message.role}><span className="yg-sr">{message.role === 'user' ? '您：' : '导购：'}</span>{message.content}</div>) : !busy && <div className="yg-welcome"><GuideIcon name="chat" size={32} /><h3>想了解哪款工具？</h3><p>输入型号，或说说你的作业需求。</p></div>}
    {busy && draft && <div className="yg-message yg-message--assistant" data-guide-draft><span className="yg-sr">导购（正在回复）：</span>{draft}</div>}
    {busy && !draft && <p role="status">正在整理回复…</p>}
    {unavailable && <div className="yg-busy" role="alert" data-guide-busy>
      <p>助手暂时繁忙，未能完成本轮推荐。有需要可以先看看我们的官方网店，或稍后重试。</p>
      <div className="yg-shops" aria-label="官方网店">
        {shops.map(shop => <a key={shop.url} href={shop.url} target="_blank" rel="noopener noreferrer" data-guide-shop><span>{shop.platform}</span><strong>{shop.name}</strong></a>)}
      </div>
      <a className="yg-shops-all" href="/shop/">查看全部官方店铺</a>
    </div>}
    <div ref={endRef} />
  </div>;
}
