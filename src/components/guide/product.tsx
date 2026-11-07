// Adapted from Roboto Studio Aisle fd8e049, ui/product.tsx (MIT).
import type { GuideRecommendation, GuideResult, GuideSelection, GuideRequirements } from '../../generated/contracts';
const fieldLabels: Record<string, string> = { scene_ids: '作业场景', material: '材料', substrate: '基材', nail_type: '枪钉类型', fastener_length_mm: '紧固件长度（mm）', air_supply: '现场压缩空气气源', load_bearing: '承重用途', long_nose: '加长枪嘴要求', blower: '吹尘要求' };
export function Requirements({ requirements }: { requirements: GuideRequirements }) {
  return <details className="yg-requirements"><summary>已提交的需求条件</summary><dl>{Object.entries(requirements).map(([field, value]) => <div key={field}><dt>{fieldLabels[field]}</dt><dd>{value === null || (Array.isArray(value) && !value.length) ? '未知' : typeof value === 'boolean' ? value ? '是' : '否' : Array.isArray(value) ? value.join('、') : value}</dd></div>)}</dl></details>;
}
function ProductCard({ product, selection, onSelect, onPurchase }: { product: GuideRecommendation | GuideSelection; selection?: GuideSelection; onSelect: (id: string) => void; onPurchase: (href: string) => Promise<void> }) {
  return <article className="yg-product" data-guide-product={product.product_id} data-guide-selection={selection ? 'true' : undefined}>
    <div className="yg-product-heading">{product.image && <a href={product.detail_url}><img src={product.image.url} alt={product.image.label || product.name} width="64" height="64" loading="lazy" /></a>}<div><h3>{selection ? '已选型号：' : ''}{product.model || product.name}</h3><p>{product.name}</p></div></div>
    <p>{product.match_level === 'matched' ? '已审核条件匹配' : '仍有条件无法确认，请先核实以下缺口。'}</p>
    {!!product.matched_facts.length && <ul>{product.matched_facts.map((fact, index) => <li key={index}>{fact.text}{fact.sources.map((source, sourceIndex) => <small key={sourceIndex}> · {source.public_url ? <a href={source.public_url} rel="noreferrer">{source.public_label}</a> : source.public_label}</small>)}</li>)}</ul>}
    {!!product.missing_fields.length && <ul className="yg-gaps" aria-label="待确认条件">{product.missing_fields.map(field => <li key={field}>{fieldLabels[field] || `产品资料待核实：${field.replace('guide_profile.', '')}`}</li>)}</ul>}
    {product.confirmation_notes.map((note, index) => <p className="yg-confirmation" key={index}>{note}</p>)}
    <div className="yg-product-actions"><a href={product.detail_url}>查看详情</a>{!selection && <button type="button" onClick={() => onSelect(product.product_id)}>选这款</button>}</div>
    {selection && <div className="yg-purchase" aria-label="所选型号商城入口">
      {selection.purchase_status === 'unavailable' && <p>该型号暂无可用商城映射，请查看详情或电话咨询。</p>}
      {selection.purchase_options.map(option => <div key={option.purchase_option_id}>
        <a data-guide-purchase href={option.handoff_url} onClick={event => { event.preventDefault(); void onPurchase(option.handoff_url); }}>去{option.platform} · {option.store_name}查看/购买{selection.model || selection.name}{option.match_scope === 'listing_with_variants' ? '（进入商城后选择规格）' : ''}</a>
        {option.match_scope === 'listing_with_variants' && <p>{option.sku_hint || '进入商城后选择对应型号与规格。'}</p>}
      </div>)}
    </div>}
  </article>;
}
/** Button selection and textual selected render exactly the same result. */
export function GuideResultView({ result, onSelect, onPurchase, onAnswer }: { result: GuideResult; onSelect: (id: string) => void; onPurchase: (href: string) => Promise<void>; onAnswer: (text: string) => void }) {
  return <section className="yg-result" aria-label="导购完整结果" data-guide-state={result.state}>
    {result.state === 'no_match' && <><h3>选型结果说明</h3><ul className="yg-conflicts">{result.conflicts.map((reason, index) => <li key={index}>{reason}</li>)}</ul></>}
    {result.question && <div className="yg-question"><p>{result.question.text}</p>{result.question.options.map(option => <button type="button" key={option} onClick={() => onAnswer(option)}>{option}</button>)}</div>}
    {result.state !== 'selected' && !!result.missing_fields.length && <><p>以下条件或资料尚缺，无法确认适用性：</p><ul>{result.missing_fields.map(field => <li key={field}>{fieldLabels[field] || `产品资料待核实：${field.replace('guide_profile.', '')}`}</li>)}</ul></>}
    {result.recommendations.map(product => <ProductCard key={product.product_id} product={product} onSelect={onSelect} onPurchase={onPurchase} />)}
    {result.selection && <ProductCard product={result.selection} selection={result.selection} onSelect={onSelect} onPurchase={onPurchase} />}
    <a className="yg-contact" href={result.contact_href}>电话咨询适用性</a>
  </section>;
}
