import { api } from './api';
import { element } from './dom';
import type { MediaRecord } from '../../../src/generated/contracts';

export function missingImage(text = '未添加图片') {
  const root = element('div', '', 'missing-image');
  const ns = 'http://www.w3.org/2000/svg';
  const icon = document.createElementNS(ns, 'svg'); icon.setAttribute('viewBox', '0 0 32 32'); icon.setAttribute('fill', 'none'); icon.setAttribute('stroke', 'currentColor'); icon.setAttribute('stroke-width', '1.3'); icon.setAttribute('aria-hidden', 'true');
  const rect = document.createElementNS(ns, 'rect'); rect.setAttribute('x', '4'); rect.setAttribute('y', '4'); rect.setAttribute('width', '24'); rect.setAttribute('height', '24'); rect.setAttribute('rx', '2');
  const path = document.createElementNS(ns, 'path'); path.setAttribute('d', 'm4 23 7-7 6 6 4-4 7 7M20 10h.01');
  icon.append(rect, path); root.append(icon, element('span', text)); return root;
}
export function mediaPreview(mediaID: string | null, alt = '') {
  const root = element('div', '', 'product-thumb');
  if (!mediaID) { root.append(missingImage()); return root; }
  const image = element('img'); image.src = `/api/admin/media/${encodeURIComponent(mediaID)}/file`; image.alt = alt; image.loading = 'lazy';
  image.addEventListener('error', () => { image.remove(); root.append(missingImage('图片暂不可用')); }, { once: true });
  root.append(image); return root;
}
export async function uploadImage(file: File): Promise<MediaRecord> {
  if (!['image/jpeg', 'image/png', 'image/webp'].includes(file.type)) throw new Error('请使用 JPG、PNG 或静态 WebP 图片。');
  if (file.size > 10 * 1024 * 1024) throw new Error('单张图片不能超过 10 MB，请压缩后再上传。');
  const body = new FormData(); body.append('file', file);
  const { data } = await api<MediaRecord>('/api/admin/media', { method: 'POST', body });
  return data;
}
