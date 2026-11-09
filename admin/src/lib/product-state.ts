import type { ProductDocument, ProductEdit, SiteUpdate } from '../../../src/generated/contracts';

export const MAX_IMAGES = 20;
export type { SiteUpdate } from '../../../src/generated/contracts';
export type UpdateState = SiteUpdate['state'];

export function siteUpdateState(update: SiteUpdate | undefined, savedVersion: number): UpdateState {
  if (!update || update.version < savedVersion) return 'pending';
  return update.state;
}
export function newSlug() { return `product-${crypto.randomUUID().replaceAll('-', '')}`; }
export function newProduct(): ProductEdit {
  return {
    kind: 'tool', name: '', model: '', category_id: 'straight', series: '', legacy_series_ids: [], summary: '', applications: [], features: [], notes: [], scene_ids: [], nail_type: null, fastener_text: '', long_nose: false, blower: false, images: [], cover_media_id: null, visibility: 'listed', sort_order: 0,
    guide_profile: { scene_ids: [], materials: [], substrates: [], excluded_materials: [], excluded_substrates: [], power_source: null, nail_type: null, fastener_length_mm: null, load_bearing: null, long_nose: null, blower: null, limitations: [], consultation_notes: [], required_requirements: [], sources: [] }, purchase_options: [],
  };
}
export function editable(product: ProductDocument): ProductEdit {
  const { id, slug, version, updated_at, guide_profile, ...fields } = structuredClone(product);
  const { reviews, ...guide } = guide_profile;
  return { ...fields, guide_profile: guide };
}
export const fieldLabels = {
  name: '产品名称 / 型号', model: '型号', category_id: '分类', series: '系列', summary: '简介', applications: '使用场景 / 适用工具与用途', features: '产品特点', notes: '注意事项', scene_ids: '场景分类', nail_type: '枪钉类型', fastener_text: '适用枪钉', long_nose: '加长枪嘴', blower: '吹尘功能', visibility: '上架状态', images: '产品图片',
} as const;
export type VisibleField = keyof typeof fieldLabels;
export function mergeEdits(base: ProductEdit, local: ProductEdit, remote: ProductEdit, choices: Partial<Record<VisibleField, 'local' | 'remote'>> = {}) {
  const edit = structuredClone(remote);
  const conflicts: VisibleField[] = [];
  for (const key of Object.keys(fieldLabels) as VisibleField[]) {
    const value = (item: ProductEdit) => key === 'images' ? [item.images, item.cover_media_id] : item[key];
    const same = (a: ProductEdit, b: ProductEdit) => JSON.stringify(value(a)) === JSON.stringify(value(b));
    if (same(local, base) || same(local, remote)) continue;
    const overlap = !same(remote, base);
    if (overlap && !choices[key]) conflicts.push(key);
    if (!overlap || choices[key] === 'local') {
      Object.assign(edit, { [key]: structuredClone(local[key]) });
      if (key === 'images') edit.cover_media_id = local.cover_media_id;
    }
  }
  return { edit, conflicts };
}
export function moveImage<T>(images: T[], from: number, to: number) {
  if (from === to || from < 0 || to < 0 || from >= images.length || to >= images.length) return;
  images.splice(to, 0, images.splice(from, 1)[0]);
}
