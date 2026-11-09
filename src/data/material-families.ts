// Permanent route ownership only. Membership and visibility come from the frozen
// catalog at build time; this contains no separate product copy or media source.
// Also retains these four source-defined family paths after business deletion
// in a fresh seed import that has never published the old generic records.
export const materialFamilies = [
  { id: 'p_nail-staple', slug: 'nail-staple', name: 'U 型码钉', members: ['nail-high-carbon-n','nail-special-k4','nail-n','nail-j4','nail-j10','nail-10f','nail-k'] },
  { id: 'p_lithium', slug: 'lithium', name: '锂电工具', members: ['f30-lithium','422j-lithium','1022j-lithium'] },
  { id: 'p_air-screwdriver', slug: 'air-screwdriver', name: '气动风批', members: ['yf1206','yf1208','yf1210','yf1206q','yf1210hl'] },
  { id: 'p_parts', slug: 'parts', name: '气动工具配件', members: ['quick-coupler','spider-workbench','pu-air-hose','repair-kit','original-driver-blade'] },
];
