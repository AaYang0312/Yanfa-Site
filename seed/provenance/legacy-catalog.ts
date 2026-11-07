import source from './product-source.json' with { type: 'json' };

export type CategorySlug =
  | 'straight'
  | 'steel'
  | 'staple'
  | 'pin'
  | 'coil'
  | 'remover'
  | 'insulation'
  | 'gas'
  | 'nails'
  | 'other';

export type FastenerType = 'brad' | 'steel-nail' | 'staple' | 'coil-nail' | 'pin';
export type NailType = FastenerType | 'insulation-nail' | 'gas-nail';

export type Category = {
  slug: CategorySlug;
  name: string;
  lines: string;
  hint: string;
  scene: string;
  use: string;
  nail?: NailType;
  scenes: string[];
  color: string;
};

export const categories: Category[] = [
  {
    slug: 'straight',
    name: '直钉枪',
    lines: 'F30 系列 5 款 / T50 系列 3 款',
    hint: 'F30 / T50 系列',
    scene: '家装木工专用',
    use: '打单根直排钉',
    nail: 'brad',
    scenes: ['家装木工'],
    color: '#8f0026',
  },
  {
    slug: 'steel',
    name: '钢排钉枪',
    lines: 'ST64 系列 3 款 / ST38 系列 1 款',
    hint: 'ST64 / ST38 系列',
    scene: '装修吊顶、打边龙骨',
    use: '打硬质钢排钉',
    nail: 'steel-nail',
    scenes: ['吊顶龙骨'],
    color: '#1c3350',
  },
  {
    slug: 'staple',
    name: '码钉枪',
    lines: 'N 系列 2 款 / K 系列 3 款 / J 系列 3 款 / P、G 各 1 款',
    hint: 'N / K / J / P / G 系列',
    scene: '打木包装箱、沙发架，木方拼接、挂网等',
    use: '打 U 型码钉',
    nail: 'staple',
    scenes: ['包装托盘', '家具木方'],
    color: '#5d6b78',
  },
  {
    slug: 'pin',
    name: '蚊钉枪',
    lines: 'P625G / P635G 共 2 款',
    hint: 'P625G / P635G',
    scene: '不同板材的钉合',
    use: '打无头蚊钉',
    nail: 'pin',
    scenes: ['板材钉合'],
    color: '#9aa4ad',
  },
  {
    slug: 'coil',
    name: '卷钉枪',
    lines: 'CN 系列 4 款',
    hint: 'CN 系列',
    scene: '连续高速打钉，生产木托盘、木包装箱等',
    use: '打专用卷钉',
    nail: 'coil-nail',
    scenes: ['包装托盘'],
    color: '#8f0026',
  },
  {
    slug: 'remover',
    name: '取钉枪',
    lines: '退钉枪 3 款 / 退螺丝枪 1 款 / 拔钉枪 2 款',
    hint: '退钉 / 退螺丝 / 拔钉',
    scene: '取钉子、退螺丝，废旧品回收',
    use: '拆卸钉子或螺丝，回收材料',
    scenes: ['退钉回收'],
    color: '#5d6b78',
  },
  {
    slug: 'insulation',
    name: '保温枪',
    lines: 'BW 系列 2 款 / DBW 系列 2 款',
    hint: 'BW / DBW 系列',
    scene: '墙面隔热层施工专用',
    use: '打保温钉',
    nail: 'insulation-nail',
    scenes: ['墙面保温'],
    color: '#1c3350',
  },
  {
    slug: 'gas',
    name: '气动瓦斯枪',
    lines: 'SR 系列 2 款',
    hint: 'SR 系列',
    scene: '木工装修打钉、水电工打卡座',
    use: '打瓦斯钉',
    nail: 'gas-nail',
    scenes: ['家装木工', '吊顶龙骨', '水电安装'],
    color: '#8f0026',
  },
  {
    slug: 'nails',
    name: '枪钉',
    lines: '直排钉 / 钢排钉 / U 型码钉 / 蚊钉等全系列',
    hint: '直排 / 钢排 / 码钉 / 蚊钉',
    scene: '与元发钉枪配套使用',
    use: '各类钉枪配套枪钉',
    scenes: [],
    color: '#5d6b78',
  },
  {
    slug: 'other',
    name: '其他',
    lines: '锂电工具 / 气动风批 / 气动工具各类配件',
    hint: '锂电 / 风批 / 配件',
    scene: '锂电与气动工具，以及钉枪维修配件',
    use: '配套工具与配件',
    scenes: [],
    color: '#9aa4ad',
  },
];

export const nailTypes: { slug: NailType; name: string }[] = [
  { slug: 'brad', name: '直排钉' },
  { slug: 'steel-nail', name: '钢排钉' },
  { slug: 'staple', name: 'U 型码钉' },
  { slug: 'pin', name: '蚊钉' },
  { slug: 'coil-nail', name: '卷钉' },
  { slug: 'insulation-nail', name: '保温钉' },
  { slug: 'gas-nail', name: '瓦斯钉' },
];

export const sceneTags = ['家装木工', '吊顶龙骨', '包装托盘', '家具木方', '板材钉合', '退钉回收', '墙面保温', '水电安装'];

export type ProductImage = {
  file: string;
  label: string;
  role: string;
  source: string;
  sha256: string;
  description?: string;
};

export type Product = {
  slug: string;
  name: string;
  category: CategorySlug;
  model?: string;
  series?: string;
  nail?: NailType;
  summary: string;
  fastener?: string;
  applications: string[];
  features: string[];
  notes: string[];
  scenes?: string[];
  longNose?: boolean;
  blower?: boolean;
  images: ProductImage[];
};

const lines = (value: string) => value.split(/\r?\n/).map((line) => line.trim()).filter(Boolean);

// Display paragraphs may wrap inside a numbered item.
function paragraphs(value: string) {
  return value.trim().split(/\r?\n(?=\d+[.、]|（|[-－])/).map((line) => line.replace(/\r?\n/g, '').trim()).filter(Boolean);
}

function modelScenes(model: string, category: CategorySlug): string[] {
  if (category === 'straight') return model.startsWith('T50')
    ? ['家装木工', '家具木方', '包装托盘'] : ['家装木工', '板材钉合', '家具木方'];
  if (category === 'steel') return model === 'ST38LG' ? ['吊顶龙骨', '水电安装'] : ['吊顶龙骨', '家装木工'];
  if (category === 'staple') {
    if (model === 'GW838') return ['墙面挂网'];
    if (['N851G', 'N838G', '440KG', 'PW2638'].includes(model)) return ['包装托盘', '家具木方'];
    return ['家具木方', '软包藤艺', '板材钉合'];
  }
  return categoryBySlug(category).scenes;
}

// Descriptions for the public catalog images.
const imageDescriptions: Record<string, string> = {
  'f30m-detail-2.png': 'F30M 加长枪嘴直钉枪，图中标注枪嘴、排气盖、扳柄、压铸一体机身和气管接头。',
  'f30m-detail-3.png': 'F30M 规格表：使用 F 直排钉，使用气压 4–7 kgf/cm²，产品净重 1.1 kg，外形尺寸 240 × 60 × 213 mm，钉子长度 10–30 mm，钉子线径 1.26 mm，钉槽容量 100 枚，使用接管 6 mm。打钉规格示意标出 10、15、20、25、30 mm 五档钉长及 1.26 mm 线径。',
};

export const toolProducts: Product[] = source.models.map((item) => {
  const category = item.category as CategorySlug;
  return {
    ...item,
    slug: item.model.toLowerCase(),
    name: item.model,
    category,
    images: item.images.map((image) => ({ ...image, description: imageDescriptions[image.file] })),
    summary: item.summary,
    applications: paragraphs(item.applications),
    features: lines(item.features),
    notes: paragraphs(item.notes),
    scenes: modelScenes(item.model, category),
    longNose: /加长|超长/.test(item.summary + item.features) && /枪嘴/.test(item.summary + item.features),
    blower: /吹尘/.test(item.summary),
  };
});

const companion = (slug: string, name: string, category: CategorySlug, summary: string, nail?: NailType): Product => ({
  slug, name, category, summary, nail, images: [], applications: [], features: [], notes: [],
});

export const products: Product[] = [
  ...toolProducts,
  companion('nail-brad', '直排钉', 'nails', 'F / T 直排钉，按钉枪型号匹配规格。', 'brad'),
  companion('nail-steel', '钢排钉', 'nails', 'ST 钢排钉，按钉枪型号匹配规格。', 'steel-nail'),
  companion('nail-staple', 'U 型码钉', 'nails', 'N / K / J / P 系列码钉，钉冠与钉长须匹配。', 'staple'),
  companion('nail-pin', '蚊钉', 'nails', '蚊钉枪配套无头钉，按型号选择钉长。', 'pin'),
  companion('lithium', '锂电工具', 'other', '锂电类五金工具，联系了解在售型号。'),
  companion('air-screwdriver', '气动风批', 'other', '气动螺丝批，按作业要求匹配。'),
  companion('parts', '气动工具配件', 'other', '按具体型号匹配维修配件。'),
];

export function categoryBySlug(slug: CategorySlug) {
  return categories.find((item) => item.slug === slug)!;
}
export const productBySlug = (slug: string) => products.find((item) => item.slug === slug);
export const nailName = (slug?: NailType) => nailTypes.find((item) => item.slug === slug)?.name ?? '';
export const productsIn = (slug: CategorySlug) => products.filter((item) => item.category === slug);

export function nailOf(product: Product) {
  return product.nail ?? categoryBySlug(product.category).nail;
}

export function scenesOf(product: Product) {
  if (product.category === 'nails') {
    const gun = categories.find((item) => item.nail === product.nail);
    return gun?.scenes ?? [];
  }
  return product.scenes ?? categoryBySlug(product.category).scenes;
}

export function searchTextOf(product: Product) {
  return [product.name, categoryBySlug(product.category).name, product.series, product.summary, product.fastener, ...product.features].filter(Boolean).join(' ');
}

export const longNoseModels = ['f30m', 'st64ml', 'st64gl'].map((slug) => {
  const product = productBySlug(slug)!;
  return { model: product.model!, name: categoryBySlug(product.category).name, product };
});

export const blowerProducts = toolProducts.filter((product) => product.blower);
export const representativeProduct = (category: CategorySlug) => toolProducts.find((product) => product.category === category);
export const allSceneTags = [...sceneTags, '墙面挂网', '软包藤艺'];

export const legacySeries = [
  { slug: 'f30', name: 'F30 系列', items: toolProducts.filter((item) => item.series === 'F30系列') },
  { slug: 't50', name: 'T50 系列', items: toolProducts.filter((item) => item.category === 'straight' && item.series === 'T50系列') },
  { slug: 'st64', name: 'ST64 系列', items: toolProducts.filter((item) => item.series === 'ST64系列') },
  { slug: 'st38', name: 'ST38 系列', items: toolProducts.filter((item) => item.series === 'ST38系列') },
  ...['N', 'K', 'J', 'P', 'G'].map((letter) => ({
    slug: `staple-${letter.toLowerCase()}`, name: `${letter} 系列`,
    items: toolProducts.filter((item) => item.category === 'staple' && item.series === `${letter}系列`),
  })),
  { slug: 'p625-p635', name: 'P625G / P635G', items: productsIn('pin') },
  { slug: 'cn', name: 'CN 系列', items: productsIn('coil') },
  { slug: 'np', name: 'NP 系列', items: toolProducts.filter((item) => item.model?.startsWith('NP')) },
  { slug: 'remover-t50', name: 'T50 退钉枪', items: [productBySlug('t50scg')!] },
  { slug: 'bd', name: 'BD 拔钉枪', items: toolProducts.filter((item) => item.series === '拔钉枪') },
  { slug: 'bw', name: 'BW 系列', items: toolProducts.filter((item) => item.series === 'BW系列') },
  { slug: 'dbw', name: 'DBW 系列', items: toolProducts.filter((item) => item.series === 'DBW系列') },
  { slug: 'sr', name: 'SR 系列', items: productsIn('gas') },
];
