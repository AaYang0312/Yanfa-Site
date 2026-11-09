export const site = {
  name: '元发工具',
  brand: '元发',
  brandEn: 'YANFA',
  brandSub: 'PNEUMATIC TOOLS',
  legalName: '元发工具（武汉）有限公司',
  city: '湖北武汉',
  founded: 2011,
  tagline: '专注气动工具的创新品牌',
  description:
    '元发创立于 2011 年，隶属元发工具（武汉）有限公司，提供全系列气动钉枪、枪钉及配套产品，首创加长枪嘴系列与带吹尘功能钉枪，整机享 6 个月免费质保。',
  phone: '189 6212 8286',
  address: '',
  icp: '',
  copyrightYear: 2026,
};

export function show(value: string, fallback = '待补充') {
  const text = value.trim();
  return text || fallback;
}

export const telHref = () => `tel:${site.phone.replace(/\s/g, '')}`;

export const topNav = [
  { label: '首页', href: '/' },
  { label: '公司概况', href: '/about/' },
  { label: '官方媒体', href: '/media/' },
  { label: '网上商城', href: '/shop/' },
  { label: '联系', href: '/contact/' },
];

export const footerPages = [
  { label: '产品中心', href: '/products/' },
  { label: '公司概况', href: '/about/' },
  { label: '官方媒体', href: '/media/' },
  { label: '网上商城', href: '/shop/' },
  { label: '联系', href: '/contact/' },
];

export const warranty = {
  headline: '我们承诺，安心相伴',
  short: '6 个月免费整机质保',
  lead: '为了保障您的使用无忧，每一款元发工具都享有贴心的保修服务。',
  items: [
    { title: '保修时长', text: '自下单之日起，提供 6 个月整机免费保修。' },
    { title: '保修范围', text: '正常使用下出现的整机质量问题。磨损，以及使用非标钉导致的问题，不在保修范围内。' },
    { title: '运费说明', text: '寄出运费由客户承担，维修完成后寄回的运费由元发承担。' },
    { title: '小贴士', text: '请保管好购买凭证，报修时一并提供。' },
  ],
};

export const media = [
  {
    slug: 'wechat',
    platform: '微信视频号',
    name: '元发YANFA五金工具',
    bio: '元发美特气钉枪零售批发，装修工具工厂店。',
    image: 'wechat-video',
  },
  {
    slug: 'douyin-nailgun',
    platform: '抖音',
    name: '元发钉枪',
    bio: '元发钉枪，工业级品质，好用不贵，值得拥有。',
    image: 'douyin-nailgun',
  },
  {
    slug: 'douyin-tools',
    platform: '抖音',
    name: '元发工具',
    bio: '专业工具 20 年，用信誉赢得信赖。',
    image: 'douyin-tools',
  },
] as const;

export const mediaNote = '欢迎关注官方微信及抖音号，观看最新产品及服务视频，与主播沟通交流。主播每晚直播。';

export const shops = [
  {
    name: '淘宝直销店',
    platform: '淘宝',
    url: 'https://shop180724049.taobao.com/category.htm?spm=pc_detail.30350276.shop_block.dshopinfo.1fdf7dd6vVixem&topIds=681624719114',
  },
  {
    name: '淘宝专营店',
    platform: '淘宝',
    url: 'https://shop261104122.taobao.com/category.htm?spm=pc_detail.30350276.shop_block.dshopinfo.799a7dd6KD7msC&topIds=729922235705',
  },
  {
    name: '天猫旗舰店',
    platform: '天猫',
    url: 'https://yuanfajj.tmall.com/category.htm?spm=pc_detail.30350276.shop_block.dshopinfo.a9d87dd6oh4oA2&topIds=986147715447',
  },
  {
    name: '天猫严选旗舰店',
    platform: '天猫',
    url: 'https://tgchhd.tmall.com/category.htm?spm=pc_detail.30350276.shop_block.dshopinfo.68467dd6UUzpq9&topIds=990911614600',
  },
  {
    name: '京东专营店',
    platform: '京东',
    url: 'https://mall.jd.com/index-12394507.html?from=pc&cid=0',
  },
];

export const pendingShops = ['拼多多店', '抖音店', '微信小店'];
