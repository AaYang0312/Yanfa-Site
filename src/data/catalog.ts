import type { CategoryID, NailType as ContractNailType, CatalogSnapshot, SnapshotProductImage } from '../generated/contracts';
import { readSnapshot, listedProducts, mediaFilename } from '../../scripts/audit-build.mjs';
import { materialFamilies } from './material-families.ts';

export type CategorySlug = CategoryID;
export type NailType = ContractNailType;
export type FastenerType = Extract<NailType, 'brad' | 'steel-nail' | 'staple' | 'coil-nail' | 'pin'>;
export type Category = {
  slug: CategorySlug; name: string; lines: string; hint: string; scene: string;
  use: string; nail?: NailType; scenes: string[]; color: string;
};
export type ProductImage = SnapshotProductImage & { file: string };
export type Product = {
  id: string; slug: string; kind: 'tool' | 'consumable' | 'entry'; name: string;
  category: CategorySlug; model: string; series: string; nail?: NailType;
  summary: string; fastener: string; applications: string[]; features: string[];
  notes: string[]; scenes: string[]; longNose: boolean; blower: boolean;
  cover_media_id: string | null; images: ProductImage[];
};

export function createCatalog(snapshot: CatalogSnapshot) {
  const scenes = new Map(snapshot.scenes.map(scene => [scene.id, scene.name]));
  const names = (ids: string[]) => ids.map(id => {
    const name = scenes.get(id);
    if (!name) throw new Error(`Unknown scene: ${id}`);
    return name;
  });
  const products: Product[] = listedProducts(snapshot).map(item => ({
    id: item.id, slug: item.slug, kind: item.kind, name: item.name, model: item.model,
    category: item.category_id, series: item.series, summary: item.summary, nail: item.nail_type ?? undefined,
    fastener: item.fastener_text, applications: item.applications, features: item.features, notes: item.notes,
    scenes: names(item.scene_ids), longNose: item.long_nose, blower: item.blower,
    cover_media_id: item.cover_media_id,
    images: item.images.map(image => ({ ...image, file: mediaFilename(image) })),
  }));
  const categories: Category[] = [...snapshot.categories].sort((a, b) => a.sort_order - b.sort_order || a.id.localeCompare(b.id)).map(item => ({
    slug: item.id, name: item.name, lines: `${products.filter(product => product.category === item.id).length} 项在售产品`,
    hint: item.hint, scene: item.scene, use: item.use, nail: item.nail ?? undefined,
    scenes: names(item.scene_ids), color: item.color,
  }));
  const legacySeries = snapshot.legacy_series.map(series => {
    const members = new Set(series.product_ids);
    return { id: series.id, slug: series.slug, name: series.name, items: products.filter(product => members.has(product.id)) };
  });
  const listedIDs = new Set(products.map(product => product.id));
  const historical = snapshot.previously_published_routes.filter(route => !listedIDs.has(route.product_id));
  const familyHubs = materialFamilies.filter(family => {
    const owner = snapshot.products.find(product => product.slug === family.slug);
    const route = snapshot.previously_published_routes.find(route => route.slug === family.slug);
    if (snapshot.legacy_series.some(series => series.slug === family.slug) || (owner && owner.id !== family.id) || (route && route.product_id !== family.id)) return false;
    if (listedIDs.has(family.id)) return false;
    if (route) return true;
    // Fresh seed imports may delete an old generic entry before its first
    // publication. Explicit source mappings keep that family path, but only
    // once all members exist, and never take a known product/series route.
    return !owner && family.members.every(slug => products.some(product => product.slug === slug));
  })
    .map(family => ({ ...family, items: products.filter(product => family.members.includes(product.slug)) }));
  const withdrawnRoutes = historical.filter(route => !familyHubs.some(family => family.id === route.product_id && family.slug === route.slug));
  return { products, categories, legacySeries, withdrawnRoutes, familyHubs };
}

const snapshot: CatalogSnapshot = readSnapshot(process.env.CATALOG_SNAPSHOT);
export const releaseId = snapshot.release_id;
export const { products, categories, legacySeries, withdrawnRoutes, familyHubs } = createCatalog(snapshot);
export const toolProducts = products.filter(product => product.kind === 'tool');
export const catalogCounts = (items: { kind: Product['kind'] }[]) => ({
  tools: items.filter(product => product.kind === 'tool').length,
  parts: items.filter(product => product.kind === 'entry').length,
  nails: items.filter(product => product.kind === 'consumable').length,
});
export const productCounts = catalogCounts(products);
export const productCountText = `${productCounts.tools} 款工具、${productCounts.parts} 项配件、${productCounts.nails} 类枪钉`;
export const nailTypes = snapshot.nail_types.map(item => ({ slug: item.id, name: item.name }));
export const allSceneTags = snapshot.scenes.map(scene => scene.name);
export function categoryBySlug(slug: CategorySlug) {
  const category = categories.find(item => item.slug === slug);
  if (!category) throw new Error(`Unknown category: ${slug}`);
  return category;
}
export const productBySlug = (slug: string) => products.find(item => item.slug === slug);
export const nailName = (slug?: NailType) => nailTypes.find(item => item.slug === slug)?.name ?? '';
export const productsIn = (slug: CategorySlug) => products.filter(item => item.category === slug);
export const representativeProduct = (slug: CategorySlug) => productsIn(slug)[0];
export const nailOf = (product: Product) => product.nail;
export const scenesOf = (product: Product) => product.scenes;
export const imagesOf = (product: Product) => [
  ...product.images.filter(image => image.media_id === product.cover_media_id),
  ...product.images.filter(image => image.media_id !== product.cover_media_id),
];
export const searchTextOf = (product: Product) => [
  product.name, product.model, categoryBySlug(product.category).name, product.series,
  product.summary, product.fastener, ...product.applications, ...product.features, ...product.notes,
].filter(Boolean).join(' ');
