import { api, getSession, showError } from './api';
import { element } from './dom';
import { categories } from './labels';
import { mediaPreview, missingImage } from './media';
import { siteUpdateState } from './product-state';
import { renderUpdate, updateLabels, watchSiteUpdates } from './site-updates';
import type { AdminProductList, ProductEnvelope, ProductSummary, SiteUpdate } from '../../../src/generated/contracts';

const form = document.querySelector<HTMLFormElement>('#product-filter')!;
const root = document.querySelector<HTMLElement>('#products')!;
const banner = document.querySelector<HTMLElement>('#list-update')!;
const more = document.querySelector<HTMLButtonElement>('#more-products')!;
const category = form.elements.namedItem('category') as HTMLSelectElement;
for (const [id, name] of Object.entries(categories)) { const option = element('option', name); option.value = id; category.append(option); }
const initial = new URLSearchParams(location.search);
for (const name of ['q', 'category', 'visibility']) (form.elements.namedItem(name) as HTMLInputElement).value = initial.get(name) ?? '';
let cursor: string | null = null, count = 0, request = 0;
let controller: AbortController | null = null;
let coverController = new AbortController();
let updates: SiteUpdate[] | null = null;
const rows = new Map<string, { product: ProductSummary; state: HTMLElement }>();
let refreshUpdates: () => Promise<void> = async () => {};
function updateProgress() {
  for (const { product, state } of rows.values()) {
    const update = updates?.find(item => item.product_id === product.id);
    const status = update ? siteUpdateState(update, product.version) : product.has_unpublished_changes ? 'draft' : null;
    state.textContent = status ? updateLabels[status] : '官网进度待查询';
    state.className = `product-progress ${status ?? ''}`;
  }
  if (updates === null) {
    renderUpdate(banner, undefined, 0, refreshUpdates, { unavailable: true }); return;
  }
  // This is deliberately global: a failed deletion no longer has a product row.
  const failed = updates.filter(item => item.state === 'failed');
  const waiting = updates.filter(item => item.state === 'pending' || item.state === 'updating');
  const current = failed[0] ?? waiting[0];
  if (!current) { banner.hidden = true; banner.dataset.signature = ''; return; }
  renderUpdate(banner, current, current.version, refreshUpdates);
  banner.querySelector('strong')!.textContent = failed.length ? `${failed.length} 项修改已保存，官网更新失败` : '正在更新官网';
}
function row(product: ProductSummary) {
  const article = element('article', '', 'product-row');
  const identity = element('div', '', 'product-identity');
  const thumbnail = element('div', '', 'product-thumb'); thumbnail.append(missingImage('正在读取图片'));
  const title = element('div'); const heading = element('h2');
  const href = `/admin/products/edit/?id=${encodeURIComponent(product.id)}`;
  const link = element('a', product.name || product.model || '未命名产品'); link.href = href; heading.append(link); title.append(heading);
  if (product.model && product.model !== product.name) title.append(element('p', product.model));
  identity.append(thumbnail, title);
  const state = element('div', '', 'product-state');
  state.append(element('span', product.visibility === 'listed' ? '上架' : '下架', `badge${product.visibility === 'listed' ? '' : ' off'}`));
  const progress = element('p', '', 'product-progress'); state.append(progress);
  const edit = element('a', '编辑', 'button'); edit.href = href; edit.setAttribute('aria-label', `编辑 ${product.name || product.model || '产品'}`);
  article.append(identity, element('span', categories[product.category_id], 'product-category'), state, edit); root.append(article);
  rows.set(product.id, { product, state: progress });
  return { product, thumbnail };
}
async function loadCovers(items: ReturnType<typeof row>[], signal: AbortSignal) {
  const queue = [...items];
  await Promise.all(Array.from({ length: Math.min(4, queue.length) }, async () => {
    while (queue.length && !signal.aborted) {
      const item = queue.shift()!;
      try {
        const { data } = await api<ProductEnvelope>(`/api/admin/products/${encodeURIComponent(item.product.id)}`, { signal });
        if (item.thumbnail.isConnected) item.thumbnail.replaceWith(mediaPreview(data.product.cover_media_id ?? data.product.images[0]?.media_id ?? null, `${item.product.name || item.product.model}封面`));
      } catch {
        if (!signal.aborted && item.thumbnail.isConnected) item.thumbnail.replaceChildren(missingImage('图片暂不可用'));
      }
    }
  }));
}
async function load(reset: boolean) {
  if (!reset && more.disabled) return;
  controller?.abort(); controller = new AbortController(); const signal = controller.signal;
  if (reset) { coverController.abort(); coverController = new AbortController(); }
  const generation = ++request; more.disabled = true; root.setAttribute('aria-busy', 'true');
  try {
    await getSession(); const params = new URLSearchParams();
    for (const [key, value] of new FormData(form)) if (String(value).trim()) params.set(key, String(value).trim());
    history.replaceState(null, '', `${location.pathname}${params.size ? `?${params}` : ''}`);
    params.set('limit', '20'); if (!reset && cursor) params.set('cursor', cursor);
    const { data } = await api<AdminProductList>(`/api/admin/products?${params}`, { signal });
    if (generation !== request) return;
    if (reset) { root.replaceChildren(); rows.clear(); count = 0; }
    const newRows = data.items.map(row); count += data.items.length;
    if (!count) {
      const empty = element('div', '', 'empty'); empty.append(missingImage('没有符合条件的产品'), element('p', '试试其他名称、型号或分类，也可以新增产品。'));
      const create = element('a', '新增产品', 'button'); create.href = '/admin/products/new/'; empty.append(create); root.append(empty);
    }
    cursor = data.next_cursor; more.hidden = !cursor;
    document.querySelector('#product-count')!.textContent = count ? `已显示 ${count} 项产品${cursor ? '' : '，已全部加载'}` : '';
    updateProgress(); void loadCovers(newRows, coverController.signal);
  } catch (error) { if (!signal.aborted) { showError(error); if (!count) root.replaceChildren(element('p', '产品暂时无法读取，请点击搜索重试。', 'empty')); } }
  finally { if (generation === request) { more.disabled = false; root.setAttribute('aria-busy', 'false'); } }
}
form.addEventListener('submit', event => { event.preventDefault(); void load(true); });
form.querySelectorAll('select').forEach(select => select.addEventListener('change', () => void load(true)));
more.addEventListener('click', () => void load(false));
void getSession().then(() => { refreshUpdates = watchSiteUpdates(items => { updates = items; updateProgress(); }); void load(true); }).catch(showError);
