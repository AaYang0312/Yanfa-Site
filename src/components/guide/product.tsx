// Adapted from Roboto Studio Aisle fd8e049, ui/product.tsx (MIT).
import type { GuideRecommendation, GuideResult, GuideSelection, GuideRequirements } from '../../generated/contracts';
import { GuideIcon } from './icon';
const fieldLabels: Record<string, string> = { scene_ids: '作业场景', material: '材料', substrate: '基材', nail_type: '枪钉类型', fastener_length_mm: '紧固件长度（mm）', air_supply: '现场压缩空气气源', load_bearing: '承重用途', long_nose: '加长枪嘴要求', blower: '吹尘要求' };
const valueLabels: Record<string, string> = { 'home-woodworking': '家装木工', 'ceiling-framing': '吊顶龙骨', 'packaging-pallets': '包装托盘', 'furniture-timber': '家具木方', 'panel-fastening': '板材钉合', 'nail-removal': '退钉回收', 'wall-insulation': '墙面保温', 'electrical-plumbing': '水电安装', 'wall-mesh': '墙面挂网', 'upholstery-rattan': '软包藤艺', brad: '直排钉', 'steel-nail': '钢排钉', staple: 'U 型码钉', 'coil-nail': '卷钉', pin: '蚊钉', 'insulation-nail': '保温钉', 'gas-nail': '瓦斯钉' };
type Requirement = keyof GuideRequirements;
const profileRequirements: Partial<Record<string, Requirement>> = {
  scene_ids: 'scene_ids', materials: 'material', excluded_materials: 'material',
  substrates: 'substrate', excluded_substrates: 'substrate', power_source: 'air_supply',
  nail_type: 'nail_type', fastener_length_mm: 'fastener_length_mm',
  load_bearing: 'load_bearing', long_nose: 'long_nose', blower: 'blower'
};
const compatibilityLabels: Record<Requirement, string> = {
  scene_ids: '作业场景适配', material: '材料适配', substrate: '基材适配',
  nail_type: '枪钉类型', fastener_length_mm: '钉长范围', air_supply: '气源要求',
  load_bearing: '承重用途', long_nose: '加长枪嘴', blower: '吹尘功能'
};
const publicLabels: Record<string, string> = {
  ...fieldLabels, ...valueLabels, materials: '适用材料', substrates: '适用基材',
  excluded_materials: '不适用材料', excluded_substrates: '不适用基材', power_source: '动力来源',
  limitations: '使用限制', consultation_notes: '使用说明', required_requirements: '适配条件',
  pneumatic: '气动', gas: '燃气', electric: '电动', manual: '手动'
};
function publicText(text: string) {
  return text.replace(/\b(?:guide_profile\.)?([a-z][a-z0-9_-]*)\b/g, (token, field: string) => publicLabels[field] || token);
}
function hasRequirement(requirements: GuideRequirements, field: Requirement | undefined) {
  if (!field) return false;
  const value = requirements[field];
  return value != null && (Array.isArray(value) ? value.length > 0 : typeof value === 'string' ? value.trim().length > 0 : true);
}
function relatedRequirement(field: string): Requirement | undefined {
  if (field.startsWith('guide_profile.')) return profileRequirements[field.slice('guide_profile.'.length)];
  return Object.hasOwn(fieldLabels, field) ? field as Requirement : profileRequirements[field];
}
function repeatedIn(reply: string, text: string) {
  const normalize = (value: string) => publicText(value).replace(/[\p{P}\p{Z}\s]/gu, '');
  const content = normalize(text);
  return !!content && normalize(reply).includes(content);
}
export function Requirements({ requirements }: { requirements: GuideRequirements }) {
  const known = Object.entries(requirements).filter(([, value]) => value !== null && (!Array.isArray(value) || value.length));
  return <div className="yg-requirements">{known.length ? <><dl>{known.map(([field, value]) => <div key={field}><dt>{fieldLabels[field]}</dt><dd>{typeof value === 'boolean' ? value ? '是' : '否' : Array.isArray(value) ? value.map(item => valueLabels[item] || item).join('、') : typeof value === 'string' ? valueLabels[value] || value : value}</dd></div>)}</dl><p>可在对话中补充或更正。</p></> : <p>暂无条件，直接在对话中补充即可。</p>}</div>;
}
function ProductCard({ product, selected, requirements, reply, askedRequirement, contactHref, onPurchase }: { product: GuideRecommendation | GuideSelection; selected?: boolean; requirements: GuideRequirements; reply: string; askedRequirement?: Requirement; contactHref: string; onPurchase: (href: string) => Promise<void> }) {
  const sources = [...new Map(product.matched_facts.flatMap(fact => fact.sources).map(source => [source.public_url || source.public_label, source])).values()];
  const reason = 'recommendation_reason' in product ? product.recommendation_reason : undefined;
  const gaps = [...new Set(product.missing_fields.map(relatedRequirement).filter((field): field is Requirement => field !== undefined && field !== askedRequirement && (hasRequirement(requirements, field) || (field === 'air_supply' && product.missing_fields.includes('air_supply')))))];
  const facts = reply ? [] : product.matched_facts;
  const options = product.purchase_options ?? [];
  return <article className="yg-product" data-guide-product={product.product_id} data-guide-selection={selected ? 'true' : undefined}>
    <div className="yg-product-heading">{product.image && <img src={product.image.url} alt={product.image.label || product.name} width="64" height="64" loading="lazy" />}<div><h3>{selected ? '已选型号：' : ''}{product.model || product.name}</h3>{!selected && product.model && product.model !== product.name && <p>{product.name}</p>}</div></div>
    {reason && <p>{publicText(reason)}</p>}
    {!!facts.length && <ul>{facts.map((fact, index) => <li key={index}>{publicText(fact.text)}</li>)}</ul>}
    {!!gaps.length && <p className="yg-gaps">还需确认：{gaps.map(field => compatibilityLabels[field]).join('、')}。</p>}
    {reason && !gaps.length && product.match_level === 'needs_confirmation' && !product.missing_fields.some(field => relatedRequirement(field) === askedRequirement && askedRequirement !== undefined) && <p className="yg-gaps">适用条件待核实。</p>}
    {!selected && !reply && !reason && product.confirmation_notes.map((note, index) => <p key={index}>{publicText(note)}</p>)}
    <div className="yg-product-actions">
      {!!sources.length && <details className="yg-sources"><summary>资料来源</summary><ul>{sources.map((source, index) => <li key={index}>{source.public_url ? <a href={source.public_url} rel="noreferrer">{source.public_label}</a> : source.public_label}</li>)}</ul></details>}
      <div className="yg-toolbar"><a className="yg-detail" href={product.detail_url} aria-label="查看详情">查看详情<GuideIcon name="external" /></a>{!options.length && <a className="yg-contact" href={contactHref}>联系咨询</a>}</div>
    </div>
    {options.length > 0 && <div className="yg-purchase" aria-label={selected ? '所选型号商城入口' : '推荐型号商城入口'}>
      {options.map(option => <div key={option.purchase_option_id}>
        <a data-guide-purchase href={option.handoff_url} onClick={event => { event.preventDefault(); void onPurchase(option.handoff_url); }}>去{option.platform}查看 · {option.store_name}</a>
        {option.match_scope === 'listing_with_variants' && <p>{option.sku_hint || '进入商城后选择对应型号与规格。'}</p>}
      </div>)}
    </div>}
  </article>;
}
/** Chat owns the reply; cards also work for selections returned without chat text. */
export function GuideResultView({ result, onPurchase, onAnswer }: { result: GuideResult; onPurchase: (href: string) => Promise<void>; onAnswer: (text: string) => void }) {
  const reply = result.reply?.trim() || '';
  const conflicts = result.conflicts.filter(reason => !repeatedIn(reply, reason));
  const questionText = result.question ? publicText(result.question.text) : '';
  const questionRepeated = repeatedIn(reply, questionText);
  const options = [...new Set(result.question?.options || [])];
  const more = result.additional_recommendations ?? [];
  const cardProps = { requirements: result.confirmed_requirements, reply, askedRequirement: result.question?.field ?? undefined, contactHref: result.contact_href, onPurchase };
  return <section className="yg-result" aria-label="导购完整结果" data-guide-state={result.state}>
    {result.state === 'no_match' && <>{!!conflicts.length && <><h3>选型结果说明</h3><ul className="yg-conflicts">{conflicts.map((reason, index) => <li key={index}>{publicText(reason)}</li>)}</ul></>}<a className="yg-contact" href={result.contact_href}>联系咨询</a></>}
    {result.question && (!questionRepeated || !!options.length) && <div className="yg-question" role="group" aria-label={questionText}>
      {!questionRepeated && <p>{questionText}</p>}
      {options.map(option => <button type="button" key={option} onClick={() => onAnswer(option)}>{publicText(option)}</button>)}
    </div>}
    {result.recommendations.map(product => <ProductCard key={product.product_id} product={product} {...cardProps} />)}
    {!!more.length && <details className="yg-more" key={result.recommendations.map(product => product.product_id).concat(more.map(product => product.product_id)).join(',')}>
      <summary>查看更多匹配（{more.length} 款）</summary>
      {more.map(product => <ProductCard key={product.product_id} product={product} {...cardProps} />)}
    </details>}
    {result.selection && <ProductCard product={result.selection} selected {...cardProps} />}
  </section>;
}
