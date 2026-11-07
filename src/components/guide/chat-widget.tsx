// Adapted from Roboto Studio Aisle fd8e049, ui/chat-widget.tsx (MIT).
import { useEffect, useRef, useState } from 'react';
import { GuideSession } from '../../scripts/guide/session';
import { ChatPanel } from './chat-panel';
import './guide.css';
export default function GuideWidget({ releaseId, contextProductId, contactHref }: { releaseId: string; contextProductId?: string; contactHref: string }) {
  const [isOpen, setIsOpen] = useState(false);
  const [session] = useState(() => new GuideSession(releaseId));
  const launcher = useRef<HTMLButtonElement>(null);
  const returnFocus = useRef<HTMLElement | null>(null);
  const root = useRef<HTMLDivElement>(null);
  const close = () => { setIsOpen(false); (returnFocus.current || launcher.current)?.focus(); };
  useEffect(() => {
    const click = (event: MouseEvent) => { const button = event.target instanceof Element ? event.target.closest<HTMLElement>('[data-guide-open]') : null; if (button) { returnFocus.current = button; setIsOpen(true); } };
    document.addEventListener('click', click);
    return () => document.removeEventListener('click', click);
  }, []);
  useEffect(() => {
    const viewport = window.visualViewport;
    const resize = () => {
      root.current?.style.setProperty('--yg-vh', `${viewport?.height ?? window.innerHeight}px`);
      root.current?.style.setProperty('--yg-vbottom', `${Math.max(0, window.innerHeight - (viewport?.height ?? window.innerHeight) - (viewport?.offsetTop ?? 0))}px`);
    };
    resize(); viewport?.addEventListener('resize', resize); viewport?.addEventListener('scroll', resize);
    return () => { viewport?.removeEventListener('resize', resize); viewport?.removeEventListener('scroll', resize); };
  }, []);
  return <div className="yg" data-guide-widget ref={root}>
    {/* Aisle keeps its panel mounted so toggling preserves useChat and input. */}
    <div className="yg-position" id="guide-panel" hidden={!isOpen}><ChatPanel session={session} open={isOpen} onClose={close} contextProductId={contextProductId} contactHref={contactHref} /></div>
    <button ref={launcher} className="yg-launcher" type="button" aria-controls="guide-panel" aria-expanded={isOpen} aria-label={isOpen ? '关闭工具导购' : '帮我选工具'} onClick={() => { if (isOpen) close(); else { returnFocus.current = launcher.current; setIsOpen(true); } }}> {isOpen ? '关闭导购' : '帮我选工具'} </button>
  </div>;
}
