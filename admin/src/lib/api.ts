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
  const el = document.querySelector<HTMLElement>('#notice');
  if (!el) return;
  el.textContent = message; el.className = `notice${error ? ' error' : ''}`;
  el.focus({ preventScroll: true });
}
export function errorMessage(error: unknown): string {
  if (!(error instanceof ApiError)) return error instanceof TypeError ? '网络连接中断，请检查网络后重试。当前输入仍保留。' : error instanceof Error ? error.message : '操作未完成，请重试。';
  if (error.status === 401) return '登录已过期。请在新标签页重新登录，再回到这里继续；当前输入仍保留。';
  if (error.status === 403) return '当前登录无法完成此操作，请重新登录后重试。';
  if (error.status === 404) return '未找到这项内容，可能已被删除。请返回列表刷新。';
  if (error.status === 409 || error.status === 412) return '产品已被其他人修改。当前输入已保留，请比较最新内容后继续。';
  if (error.status === 413) return '图片太大，请选择不超过 10 MB 的图片。';
  if (error.status === 415) return '图片格式不支持，请使用 JPG、PNG 或静态 WebP。';
  if (error.status === 429) return '操作过于频繁，请稍后再试。';
  if (error.status >= 500) return '服务暂时不可用，请稍后重试。当前输入仍保留。';
  const labels: Record<string, string> = { name: '产品名称', model: '型号', category_id: '分类', series: '系列', summary: '简介', applications: '使用场景', features: '产品特点', notes: '注意事项', images: '产品图片', cover_media_id: '封面图片', fastener_text: '适用枪钉', expires_at: '失效时间', username: '用户名', password: '密码' };
  const fields = [...new Set((error.detail.fields ?? []).flatMap(field => Object.entries(labels).filter(([key]) => field.split(/[./[\]]/).includes(key)).map(([, label]) => label)))];
  return fields.length ? `请检查${fields.join('、')}，补充或调整后再保存。` : '资料未能保存，请检查填写内容和图片后重试。';
}
export function showError(error: unknown) { notice(errorMessage(error), true); }
export async function api<T>(path: string, options: RequestInit = {}): Promise<{ data: T; etag: string | null }> {
  const headers = new Headers(options.headers);
  if (options.body && !(options.body instanceof FormData)) headers.set('Content-Type', 'application/json');
  if (options.method && !['GET', 'HEAD'].includes(options.method) && path !== '/api/admin/login') headers.set('X-CSRF-Token', (await getSession()).csrf_token);
  const response = await fetch(path, { ...options, headers, credentials: 'same-origin', cache: 'no-store' });
  const data = response.status === 204 ? null : await response.json().catch(() => null);
  if (!response.ok) {
    if (response.status === 401 || response.status === 403) { session = null; sessionPromise = null; }
    if (response.status === 401) {
      const el = document.querySelector('#session-state');
      if (el) {
        el.replaceChildren(document.createTextNode('登录已过期，输入仍保留。'));
        const link = document.createElement('a'); link.href = '/admin/login/'; link.target = '_blank'; link.rel = 'noopener'; link.textContent = '重新登录'; el.append(link);
      }
    }
    throw new ApiError(response.status, data?.error ?? { code: 'UNAVAILABLE', message: '', fields: [] });
  }
  if (response.status !== 204 && data === null) throw new TypeError('Invalid response');
  return { data, etag: response.headers.get('ETag') };
}
export function getSession(): Promise<SessionResponse> {
  if (session) return Promise.resolve(session);
  if (!sessionPromise) sessionPromise = api<SessionResponse>('/api/admin/session').then(({ data }) => {
    session = data;
    const el = document.querySelector('#session-state'); if (el) el.textContent = data.username;
    return data;
  }).catch(error => { sessionPromise = null; throw error; });
  return sessionPromise;
}
export function initSession() {
  getSession().catch(error => {
    if (error instanceof ApiError && error.status === 401 && !dirty) location.replace(`/admin/login/?next=${encodeURIComponent(location.pathname + location.search)}`);
    else showError(error);
  });
  document.querySelector('#logout')?.addEventListener('click', async () => {
    if (dirty && !confirm('有未保存内容，仍要退出吗？')) return;
    try { await api('/api/admin/logout', { method: 'POST' }); setDirty(false); location.assign('/admin/login/'); } catch (error) { showError(error); }
  });
}
window.addEventListener('beforeunload', event => { if (dirty) { event.preventDefault(); event.returnValue = ''; } });
document.addEventListener('click', event => {
  const anchor = event.target instanceof Element ? event.target.closest('a') : null;
  if (!dirty || !anchor || anchor.target === '_blank' || anchor.hasAttribute('download') || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return;
  const destination = new URL(anchor.href, location.href);
  if (destination.pathname === location.pathname && destination.search === location.search) return;
  if (!confirm('有未保存内容，离开将丢失这些编辑。仍要离开吗？')) event.preventDefault();
  else setDirty(false);
});
