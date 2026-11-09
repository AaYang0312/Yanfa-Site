type IconName = 'chat' | 'close' | 'send' | 'stop' | 'clear' | 'conditions' | 'phone' | 'retry' | 'external' | 'check';
export function GuideIcon({ name, size = 20 }: { name: IconName; size?: number }) {
  return <svg viewBox="0 0 24 24" width={size} height={size} aria-hidden="true" focusable="false" fill="none" stroke="currentColor" strokeWidth="1.65" strokeLinecap="round" strokeLinejoin="round">
    {name === 'chat' && <><path d="M20 11.5a8.5 8.5 0 0 1-8.5 8.5 10 10 0 0 1-4-.9L3 21l1.9-4.5a10 10 0 0 1-.9-4A8.5 8.5 0 0 1 12.5 4H16a4 4 0 0 1 4 4z" /><path d="M8 10h8M8 14h5" /></>}
    {name === 'close' && <path d="m6 6 12 12M6 18 18 6" />}
    {name === 'send' && <path d="M12 19V5m-6 6 6-6 6 6" />}
    {name === 'stop' && <rect x="6" y="6" width="12" height="12" rx="2" />}
    {name === 'clear' && <><path d="M4 7h16M9 3h6M6 7l1 14h10l1-14M10 11v6m4-6v6" /></>}
    {name === 'conditions' && <><path d="M4 7h3m4 0h9M4 17h9m4 0h3" /><circle cx="9" cy="7" r="2" /><circle cx="15" cy="17" r="2" /></>}
    {name === 'phone' && <path d="m8 3 3 5-3 3c1 2 3 4 5 5l3-3 5 3v3c0 1-1 2-2 2C10 20 4 14 3 5c0-1 1-2 2-2z" />}
    {name === 'retry' && <><path d="M20 8a8 8 0 1 0 0 8M20 3v5h-5" /></>}
    {name === 'external' && <><path d="M14 4h6v6m0-6-9 9M10 4H5a1 1 0 0 0-1 1v14a1 1 0 0 0 1 1h14a1 1 0 0 0 1-1v-5" /></>}
    {name === 'check' && <path d="m5 12 4 4L19 6" />}
  </svg>;
}
