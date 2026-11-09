import type { SiteUpdateList } from '../../../src/generated/contracts';
import { api, showError } from './api';
import { button, element } from './dom';
import { siteUpdateState, type SiteUpdate, type UpdateState } from './product-state';
let retryKey: string | null = null;
export const updateLabels: Record<UpdateState, string> = { pending: '等待更新官网', updating: '正在更新官网', succeeded: '官网已更新', failed: '修改已保存，官网更新失败', draft: '有待确认的修改' };
export function publicLink(url: string | null) {
  if (!url) return null;
  try { const parsed = new URL(url, location.origin); return parsed.origin === location.origin && ['https:', 'http:'].includes(parsed.protocol) ? parsed.href : null; } catch { return null; }
}
export function watchSiteUpdates(receive: (items: SiteUpdate[] | null) => void) {
  let stopped = false, loading = false, timer: ReturnType<typeof setTimeout>;
  async function refresh() {
    if (stopped || loading) return;
    clearTimeout(timer); loading = true;
    try { const { data } = await api<SiteUpdateList>('/api/admin/site-updates'); if (!stopped) receive(data.items); }
    catch { if (!stopped) receive(null); }
    finally { loading = false; if (!stopped) timer = setTimeout(refresh, document.hidden ? 15000 : 3000); }
  }
  void refresh();
  window.addEventListener('pagehide', () => { stopped = true; clearTimeout(timer); }, { once: true });
  window.addEventListener('pageshow', event => { if (event.persisted) { stopped = false; void refresh(); } });
  return refresh;
}
export function renderUpdate(root: HTMLElement, update: SiteUpdate | undefined, version: number, refresh: () => Promise<void>, options: { unavailable?: boolean; deleted?: boolean } = {}) {
  const state = siteUpdateState(update, version);
  const signature = JSON.stringify([state, update?.public_url, options]);
  if (root.dataset.signature === signature) return;
  root.dataset.signature = signature; root.hidden = false;
  root.className = `update-status ${state}`; root.replaceChildren();
  const copy = element('div');
  copy.append(element('strong', options.unavailable ? '暂时无法查询官网更新进度' : options.deleted && state === 'succeeded' ? '产品已删除，官网已更新' : updateLabels[state]));
  copy.append(element('p', options.unavailable ? '请检查网络后刷新进度。已提交的修改会继续处理。' : state === 'failed' ? '当前官网保持上次成功的内容。可以重试更新，无需再次填写。' : state === 'draft' ? '请核对产品内容，点击“保存并更新官网”后才会更新。' : state === 'succeeded' ? '本次保存的内容已生效。' : '修改已保存，可以留在此页等待，也可以返回产品列表。'));
  root.append(copy);
  if (options.unavailable) root.append(button('刷新进度', refresh));
  else if (state === 'failed') root.append(button('重试更新官网', async () => {
    try { retryKey ??= crypto.randomUUID(); await api('/api/admin/site-updates/retry', { method: 'POST', headers: { 'Idempotency-Key': retryKey } }); retryKey = null; root.dataset.signature = ''; await refresh(); } catch (error) { showError(error); }
  }));
  else if (state === 'succeeded') {
    const href = options.deleted ? '/' : publicLink(update?.public_url ?? null);
    if (href) { const link = element('a', '查看官网', 'button'); link.href = href; link.target = '_blank'; link.rel = 'noopener'; root.append(link); }
  }
}
