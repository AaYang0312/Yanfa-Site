import { api, getSession, notice, showError } from './api';
import { button, element } from './dom';
import type { MediaList, MediaRecord } from '../../../src/generated/contracts';

// Keep the caller's initial after fixed for every cursor page. Response.after is
// the watermark for a later read, not a replacement filter for this traversal.
export function mediaPicker(root: HTMLElement, onSelect?: (media: MediaRecord) => void, initialAfter = '') {
  root.replaceChildren();
  const upload = element('div', '', 'card');
  const label = element('label', '上传图片'); const file = element('input'); file.type = 'file'; file.accept = '.jpg,.jpeg,.png,.webp'; label.append(file);
  upload.append(label, element('p', '单张 JPEG、PNG 或静态 WebP；最大 10 MiB、40 MP，单边不超过 12,000 px。上传后仍为私有素材。', 'muted'));
  const list = element('div', '', 'media-grid'); const state = element('p', '', 'muted');
  let cursor: string | null = null; let loading = false;
  const more = button('加载更多素材', () => load(false)); more.hidden = true;
  function card(media: MediaRecord, prepend = false) {
    if (list.querySelector(`[data-media-id="${CSS.escape(media.media_id)}"]`)) return;
    const item = element('article', '', 'card media-card'); item.dataset.mediaId = media.media_id;
    const image = element('img'); image.src = media.preview_url; image.alt = media.original_filename || `素材 ${media.media_id}`; image.loading = 'lazy';
    item.append(image, element('p', `${media.width} × ${media.height} · ${media.format} · ${media.bytes} 字节`), element('small', media.original_filename || media.media_id));
    const actions = element('div', '', 'actions');
    if (onSelect) actions.append(button('选择此图', () => onSelect(media)));
    actions.append(button('删除素材', async () => {
      if (!confirm('仅完全未被引用的素材可以删除。确认删除？')) return;
      try { await api(`/api/admin/media/${encodeURIComponent(media.media_id)}`, { method: 'DELETE' }); item.remove(); notice('未引用素材已删除。'); } catch (error) { showError(error); }
    }, 'danger'));
    item.append(actions); if (prepend) list.prepend(item); else list.append(item);
  }
  async function load(reset: boolean) {
    if (loading) return; loading = true; more.disabled = true;
    try {
      await getSession(); const params = new URLSearchParams({ limit: '20' });
      if (initialAfter) params.set('after', initialAfter);
      if (!reset && cursor) params.set('cursor', cursor);
      const { data } = await api<MediaList>(`/api/admin/media?${params}`);
      if (reset) list.replaceChildren();
      data.items.forEach(media => card(media)); cursor = data.next_cursor; more.hidden = !cursor;
      state.textContent = `本次读取时间标记：${data.after}${initialAfter ? `；只显示 ${initialAfter} 之后的素材` : ''}`;
      if (!list.childElementCount) state.textContent += '；暂无素材。';
    } catch (error) { showError(error); } finally { loading = false; more.disabled = false; }
  }
  upload.append(button('上传并选择', async () => {
    const selected = file.files?.[0];
    if (!selected) { notice('请先选择图片文件。', true); return; }
    try {
      const body = new FormData(); body.append('file', selected);
      const { data } = await api<MediaRecord>('/api/admin/media', { method: 'POST', body });
      card(data, true); file.value = ''; if (onSelect) onSelect(data);
      notice(onSelect ? '图片已上传并选择，请保存产品草稿以关联。' : '图片已上传，尚未关联产品。');
    } catch (error) { showError(error); }
  }), button('刷新素材', () => load(true)));
  root.append(upload, state, list, more); load(true);
}
const pageRoot = document.querySelector<HTMLElement>('#media-page');
if (pageRoot) {
  const params = new URLSearchParams(location.search); const productID = params.get('product_id');
  const select = productID ? (media: MediaRecord) => location.assign(`/admin/products/edit/?id=${encodeURIComponent(productID)}&media_id=${encodeURIComponent(media.media_id)}`) : undefined;
  mediaPicker(pageRoot, select, params.get('after') ?? '');
  if (productID) { const link = element('a', '返回产品编辑'); link.href = `/admin/products/edit/?id=${encodeURIComponent(productID)}`; pageRoot.before(link); }
}
