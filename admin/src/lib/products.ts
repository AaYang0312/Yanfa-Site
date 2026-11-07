import { api, getSession, showError } from './api';
import { element } from './dom';
import { categories } from './labels';
import type { AdminProductList } from '../../../src/generated/contracts';
const form = document.querySelector<HTMLFormElement>('#product-filter');
if (form) {
  const root = document.querySelector<HTMLElement>('#products')!;
  const more = document.querySelector<HTMLButtonElement>('#more-products')!;
  const category = form.elements.namedItem('category') as HTMLSelectElement;
  for (const [id, name] of Object.entries(categories)) { const option = element('option', name); option.value = id; category.append(option); }
  const initial = new URLSearchParams(location.search);
  for (const name of ['q', 'category', 'visibility']) (form.elements.namedItem(name) as HTMLInputElement).value = initial.get(name) ?? '';
  let cursor: string | null = null; let busy = false;
  async function load(reset: boolean) {
    if (busy) return; busy = true; more.disabled = true;
    try {
      await getSession(); const params = new URLSearchParams();
      for (const [key, value] of new FormData(form!)) if (String(value)) params.set(key, String(value));
      history.replaceState(null, '', `${location.pathname}?${params}`);
      if (!reset && cursor) params.set('cursor', cursor);
      const { data } = await api<AdminProductList>(`/api/admin/products?${params}`);
      if (reset) root.replaceChildren();
      for (const product of data.items) {
        const card = element('article', '', 'card product-card');
        const heading = element('h2'); const link = element('a', `${product.name || '未命名草稿'} ${product.model}`);
        link.href = `${root.dataset.review === 'true' ? '/admin/guide-reviews/review/' : '/admin/products/edit/'}?id=${encodeURIComponent(product.id)}`; heading.append(link);
        card.append(heading, element('p', `${categories[product.category_id]} · 排序 ${product.sort_order} · v${product.version}`), element('span', product.visibility === 'listed' ? '目标上架' : '目标下架', 'badge'), element('p', product.has_unpublished_changes ? '有未发布修改' : '与线上一致'), element('small', `${product.ever_published ? '曾经公开' : '从未公开'} · ${product.slug} · ${product.updated_at}`));
        root.append(card);
      }
      if (!root.childElementCount) root.append(element('p', '没有符合条件的产品。', 'empty'));
      cursor = data.next_cursor; more.hidden = !cursor;
    } catch (error) { showError(error); } finally { busy = false; more.disabled = false; }
  }
  form.addEventListener('submit', event => { event.preventDefault(); load(true); });
  more.addEventListener('click', () => load(false)); load(true);
}
