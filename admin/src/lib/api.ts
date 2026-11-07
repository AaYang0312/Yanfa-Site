import type { ErrorDetail, SessionResponse } from '../../../src/generated/contracts';

export class ApiError extends Error {
  constructor(public status: number, public detail: ErrorDetail) { super(detail.message); }
}
let session: SessionResponse | null = null;
let sessionPromise: Promise<SessionResponse> | null = null;
let dirty = false;
export function setDirty(value: boolean) {
  dirty = value;
  document.querySelector('#unsaved')?.toggleAttribute('hidden', !value);
}
export const isDirty = () => dirty;
export function notice(message: string, error = false) {
  const el = document.querySelector<HTMLElement>('#notice')!;
  el.textContent = message;
  el.className = `notice${error ? ' error' : ''}`;
  el.focus();
}
export function showError(error: unknown) {
  if (error instanceof ApiError) {
    notice(`${error.message}（${error.status} / ${error.detail.code}）${error.detail.fields?.length ? ' 字段：' + error.detail.fields.join('、') : ''}`, true);
  } else notice(error instanceof Error ? error.message : '操作失败，请重试。', true);
}
export async function api<T>(path: string, options: RequestInit = {}): Promise<{ data: T; etag: string | null }> {
  const headers = new Headers(options.headers);
  if (options.body && !(options.body instanceof FormData)) headers.set('Content-Type', 'application/json');
  if (options.method && !['GET', 'HEAD'].includes(options.method) && path !== '/api/admin/login') {
    const s = await getSession();
    headers.set('X-CSRF-Token', s.csrf_token);
  }
  const response = await fetch(path, { ...options, headers, credentials: 'same-origin', cache: 'no-store' });
  const data = response.status === 204 ? null : await response.json();
  if (!response.ok) {
    if (response.status === 401) {
      session = null;
      sessionPromise = null;
      const el = document.querySelector('#session-state');
      if (el) {
        el.replaceChildren(document.createTextNode('会话已失效。本地内容仍保留；请在新标签页登录后重新提交。 '));
        const link = document.createElement('a'); link.href = '/admin/login/'; link.target = '_blank'; link.rel = 'noopener'; link.textContent = '重新登录'; el.append(link);
      }
    }
    throw new ApiError(response.status, data.error);
  }
  return { data, etag: response.headers.get('ETag') };
}
export function getSession(): Promise<SessionResponse> {
  if (session) return Promise.resolve(session);
  if (!sessionPromise) sessionPromise = api<SessionResponse>('/api/admin/session').then(({ data }) => {
    session = data;
    const el = document.querySelector('#session-state');
    if (el) el.textContent = `${data.username} · 工作区版本 ${data.workspace_revision} · 线上版本 ${data.active_release_id ?? '尚未发布'} · 保存仅更新草稿`;
    return data;
  }).catch(error => { sessionPromise = null; throw error; });
  return sessionPromise;
}
export function initSession() {
  getSession().catch(error => {
    if (error instanceof ApiError && error.status === 401) location.replace(`/admin/login/?next=${encodeURIComponent(location.pathname + location.search + location.hash)}`);
    else showError(error);
  });
  document.querySelector('#logout')?.addEventListener('click', async () => {
    if (dirty && !confirm('有未保存内容，仍要退出吗？')) return;
    try { await api('/api/admin/logout', { method: 'POST' }); setDirty(false); location.assign('/admin/login/'); } catch (error) { showError(error); }
  });
}
window.addEventListener('beforeunload', event => { if (dirty) { event.preventDefault(); event.returnValue = ''; } });
document.addEventListener('click', event => {
  const anchor = (event.target as Element).closest('a');
  if (!dirty || !anchor || anchor.target === '_blank' || anchor.hasAttribute('download') || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return;
  const destination = new URL(anchor.href, location.href);
  if (destination.pathname === location.pathname && destination.search === location.search) return;
  if (!confirm('有未保存内容，离开将丢失这些编辑。仍要离开吗？')) event.preventDefault();
});
