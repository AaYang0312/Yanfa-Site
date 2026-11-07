// Editing workflow: static pages hydrate from the authenticated workspace API.
// Keep server metadata out of ProductEdit; review only the last saved document.
// A 412 keeps the mutable form and requires an explicit baseline decision.
import { api, ApiError, getSession, isDirty, notice, setDirty, showError } from './api';
import { button, checkbox, element, input, reorder, select } from './dom';
import { categories, facts, legacySeries, nails, requirements, scenes } from './labels';
import { mediaPicker } from './media';
import { validateContract } from '../../../src/generated/validate-contract.mjs';
import type { GuideField, GuideProfileEdit, GuideReviewInput, MediaRecord, ProductCreate, ProductDocument, ProductEdit, ProductEnvelope, RequirementField, SceneID } from '../../../src/generated/contracts';

const form = document.querySelector<HTMLFormElement>('#product-editor')!;
const params = new URLSearchParams(location.search);
let id = params.get('id');
let slug = '';
let saved: ProductDocument | null = null;
let etag: string | null = null;
let busy = false;
let conflictVersion: ProductDocument | null = null;
let edit: ProductEdit = {
  kind: 'tool', name: '', model: '', category_id: 'straight', series: '', legacy_series_ids: [], summary: '', applications: [], features: [], notes: [], scene_ids: [], nail_type: null, fastener_text: '', long_nose: false, blower: false, images: [], cover_media_id: null, visibility: 'unlisted', sort_order: 0,
  guide_profile: { scene_ids: [], materials: [], substrates: [], excluded_materials: [], excluded_substrates: [], power_source: null, nail_type: null, fastener_length_mm: null, load_bearing: null, long_nose: null, blower: null, limitations: [], consultation_notes: [], required_requirements: [], sources: [] }, purchase_options: [],
};
const $ = (selector: string) => document.querySelector<HTMLElement>(selector)!;
function editable(product: ProductDocument): ProductEdit {
  const { id: _id, slug: _slug, version: _version, updated_at: _updated, guide_profile, ...fields } = structuredClone(product);
  const { reviews: _reviews, ...guide } = guide_profile;
  return { ...fields, guide_profile: guide };
}
function mark() { setDirty(true); }
function lines(text: string) { return text.split('\n').map(line => line.trim()).filter(Boolean); }
function choices<T extends string>(label: string, values: T[], options: Record<T, string>, update: (values: T[]) => void) {
  const group = element('fieldset'); group.append(element('legend', label)); const controls = element('div', '', 'choices');
  for (const key of Object.keys(options) as T[]) controls.append(checkbox(options[key], values.includes(key), checked => {
    values = checked ? [...values, key] : values.filter(value => value !== key); update(values); mark();
  }));
  group.append(controls); return group;
}
function renderBasic() {
  const root = $('#basic-fields'); root.replaceChildren(); const grid = element('div', '', 'grid');
  grid.append(input('固定路径 slug', slug, value => { slug = value; mark(); }, { max: 80, required: true, readOnly: !!id }), select('条目类型', edit.kind, { tool: '工具', consumable: '耗材', entry: '配套入口' }, value => { edit.kind = value as ProductEdit['kind']; mark(); }),
    input('产品名称', edit.name, value => { edit.name = value; mark(); }, { max: 120 }), input('型号', edit.model, value => { edit.model = value; mark(); }, { max: 80 }),
    select('分类', edit.category_id, categories, value => { edit.category_id = value as ProductEdit['category_id']; mark(); }), input('展示系列名称', edit.series, value => { edit.series = value; mark(); }, { max: 120 }),
    input('排序值', String(edit.sort_order), value => { edit.sort_order = Number(value); mark(); }, { type: 'number' }));
  const order = grid.querySelector<HTMLInputElement>('input[type=number]')!; order.min = '0'; order.step = '1'; order.required = true;
  const visibility = select('目标上下架状态', edit.visibility, { unlisted: '目标下架', listed: '目标上架' }, value => { edit.visibility = value as ProductEdit['visibility']; mark(); });
  visibility.querySelector('select')!.disabled = !id; grid.append(visibility); root.append(grid,
    input('产品简介', edit.summary, value => { edit.summary = value; mark(); }, { max: 500, multiline: true }));
  for (const [key, label] of [['applications', '用途（每行一项）'], ['features', '特点（每行一项）'], ['notes', '说明（每行一项）']] as const) root.append(input(label, edit[key].join('\n'), value => { edit[key] = lines(value); mark(); }, { multiline: true }));
  root.append(choices('展示场景', edit.scene_ids, scenes, values => { edit.scene_ids = values; }), choices('旧系列归属', edit.legacy_series_ids, legacySeries, values => { edit.legacy_series_ids = values; }));
  const tags = element('div', '', 'grid'); tags.append(select('展示枪钉类型', edit.nail_type ?? '', { '': '未知 / 不适用', ...nails }, value => { edit.nail_type = (value || null) as ProductEdit['nail_type']; mark(); }), input('展示紧固件规格', edit.fastener_text, value => { edit.fastener_text = value; mark(); }, { max: 1000 }), checkbox('展示加长枪嘴标签', edit.long_nose, value => { edit.long_nose = value; mark(); }), checkbox('展示吹尘标签', edit.blower, value => { edit.blower = value; mark(); })); root.append(tags);
}
function renderImages() {
  const root = $('#image-fields'); root.replaceChildren();
  if (!edit.images.length) root.append(element('p', '尚未关联图片；未完成草稿可先保存。', 'empty'));
  edit.images.forEach((image, index) => {
    const row = element('article', '', 'card image-row'); row.dataset.imageId = image.media_id;
    const preview = element('img'); preview.src = `/api/admin/media/${encodeURIComponent(image.media_id)}/file`; preview.alt = image.label || `产品图片 ${index + 1}`;
    row.append(element('h3', `图片 ${index + 1}`), preview, element('small', image.media_id), input('图片标题', image.label, value => { image.label = value; mark(); }, { max: 120 }), input('图片说明', image.description, value => { image.description = value; mark(); }, { max: 1000, multiline: true }));
    const cover = element('label', '', 'check'); const radio = element('input'); radio.type = 'radio'; radio.name = 'cover'; radio.value = image.media_id; radio.checked = edit.cover_media_id === image.media_id;
    radio.addEventListener('change', () => { edit.cover_media_id = image.media_id; mark(); }); cover.append(radio, document.createTextNode('用作封面')); row.append(cover);
    const actions = reorder(edit.images, index, renderImages, mark); actions.append(button('移除图片关联', () => { edit.images.splice(index, 1); if (edit.cover_media_id === image.media_id) edit.cover_media_id = null; mark(); renderImages(); })); row.append(actions); root.append(row);
  });
}
function addImage(media: Pick<MediaRecord, 'media_id'>) {
  if (edit.images.some(image => image.media_id === media.media_id)) { notice('此图已在产品中。'); return; }
  if (edit.images.length >= 12) { notice('最多关联 12 张图片。', true); return; }
  edit.images.push({ media_id: media.media_id, label: '', description: '' }); mark(); renderImages();
}
function renderGuide() {
  const guide = edit.guide_profile; const root = $('#guide-fields'); root.replaceChildren();
  root.append(choices<SceneID>('选型适用场景', guide.scene_ids, scenes, values => { guide.scene_ids = values; }));
  const grid = element('div', '', 'grid');
  for (const key of ['materials', 'substrates', 'excluded_materials', 'excluded_substrates', 'limitations', 'consultation_notes'] as const) grid.append(input(`选型${facts[key]}（每行一项）`, guide[key].join('\n'), value => { guide[key] = lines(value); mark(); }, { multiline: true }));
  grid.append(select('选型动力要求', guide.power_source ?? '', { '': '未知', pneumatic: '气动', gas: '瓦斯', electric: '电动', manual: '手动' }, value => { guide.power_source = (value || null) as GuideProfileEdit['power_source']; mark(); }), select('选型紧固件类型', guide.nail_type ?? '', { '': '未知', ...nails }, value => { guide.nail_type = (value || null) as GuideProfileEdit['nail_type']; mark(); }));
  for (const key of ['load_bearing', 'long_nose', 'blower'] as const) grid.append(select(`选型${facts[key]}`, guide[key] === null ? '' : String(guide[key]), { '': '未知', true: '是', false: '否' }, value => { guide[key] = value === '' ? null : value === 'true'; mark(); }));
  root.append(grid);
  const range = element('fieldset'); range.append(element('legend', '选型紧固件长度范围（mm）'));
  const known = checkbox('已知长度范围', guide.fastener_length_mm !== null, value => {
    guide.fastener_length_mm = value ? { min: 1, max: 1 } : null; mark(); renderGuide();
  }); range.append(known);
  if (guide.fastener_length_mm) for (const key of ['min', 'max'] as const) {
    const control = input(key === 'min' ? '最小长度 mm' : '最大长度 mm', String(guide.fastener_length_mm[key]), value => { guide.fastener_length_mm![key] = Number(value); mark(); }, { type: 'number', required: true });
    const number = control.querySelector('input')!; number.min = '0'; number.step = 'any'; range.append(control);
  }
  root.append(range, choices<RequirementField>('用途必需条件（需单独审核）', guide.required_requirements, requirements, values => { guide.required_requirements = values; }));
}
function renderSources() {
  const root = $('#source-fields'); root.replaceChildren();
  edit.guide_profile.sources.forEach((source, index) => {
    const row = element('article', '', 'card source-row'); row.append(element('h3', `来源 ${index + 1}`));
    row.append(input('来源 ID', source.id, value => { source.id = value; mark(); }, { max: 120, required: true }), select('来源类型', source.kind, { product_text: '展示文本', media: '私有素材', document: '已核验归档文档' }, value => { source.kind = value as typeof source.kind; mark(); renderSources(); }), input('来源引用', source.reference, value => { source.reference = value; mark(); }, { max: 1000, required: true }));
    if (source.kind === 'document') row.append(input('归档文档 SHA256', source.sha256, value => { source.sha256 = value; mark(); }, { max: 64, required: true }));
    else row.append(element('small', '保存时由服务器计算内容 SHA256。'));
    row.append(input('公开来源标签', source.public_label, value => { source.public_label = value; mark(); }, { max: 120 }), input('公开来源 HTTPS 地址（可空）', source.public_url ?? '', value => { source.public_url = value || null; mark(); }, { type: 'url', max: 2048 }));
    const actions = reorder(edit.guide_profile.sources, index, renderSources, mark); actions.append(button('移除来源', () => { edit.guide_profile.sources.splice(index, 1); mark(); renderSources(); })); row.append(actions); root.append(row);
  });
}
function renderChannels() {
  const root = $('#channel-fields'); root.replaceChildren(); $('#channel-empty').hidden = !!edit.purchase_options.length;
  edit.purchase_options.forEach((channel, index) => {
    const row = element('article', '', 'card channel-row'); row.append(element('h3', `渠道 ${index + 1}`), element('small', channel.id ? `渠道 ID：${channel.id}` : '新渠道：保存时生成稳定 ID'));
    row.append(input('平台', channel.platform, value => { channel.platform = value; mark(); }, { max: 120 }), input('店铺名称', channel.store_name, value => { channel.store_name = value; mark(); }, { max: 120 }), input('商品详情 HTTPS 地址', channel.url, value => { channel.url = value; mark(); }, { type: 'url', max: 2048 }), select('型号对应范围', channel.match_scope, { exact_model: '精确型号', listing_with_variants: '需要选择规格' }, value => { channel.match_scope = value as typeof channel.match_scope; mark(); }), input('SKU 选择提示', channel.sku_hint, value => { channel.sku_hint = value; mark(); }, { max: 500 }), checkbox('启用此渠道', channel.enabled, value => { channel.enabled = value; mark(); }));
    const actions = reorder(edit.purchase_options, index, renderChannels, mark); actions.append(button('删除购买渠道', () => { edit.purchase_options.splice(index, 1); mark(); renderChannels(); })); row.append(actions); root.append(row);
  });
}
async function sha256(value: unknown) {
  const hash = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(JSON.stringify(value)));
  return Array.from(new Uint8Array(hash), byte => byte.toString(16).padStart(2, '0')).join('');
}
function renderReviews() {
  const root = $('#review-fields'); root.replaceChildren();
  if (!saved) { root.append(element('p', '保存草稿后即可逐事实审核。')); return; }
  const profile = saved.guide_profile;
  for (const field of Object.keys(facts) as GuideField[]) {
    const row = element('article', '', 'card review-row'); row.dataset.fact = field;
    const review = profile.reviews.find(item => item.field === field);
    row.append(element('h3', facts[field]), element('pre', JSON.stringify(profile[field], null, 2), 'review-value'), element('p', review ? `已审核 · ${review.reviewed_by} · ${review.reviewed_at}` : '待核实：不能作为已核实规格'));
    if (profile[field] === null) { row.append(element('p', '未知值不能审核，请先补齐可靠资料。', 'muted')); root.append(row); continue; }
    const selected = new Set<string>();
    for (const source of profile.sources) {
      const sourceReview = element('div'); sourceReview.append(checkbox(`审核依据：${source.public_label || source.id}`, false, checked => { if (checked) selected.add(source.id); else selected.delete(source.id); }), element('small', `${source.kind} · ${source.reference} · SHA256 ${source.sha256}`)); row.append(sourceReview);
    }
    const confirmFact = button('确认此事实已核实', async () => {
      if (busy) return;
      if (isDirty()) { notice('请先保存本地编辑，再审核当前版本。', true); return; }
      if (!selected.size) { notice('请勾选支持此事实的来源。', true); return; }
      if (!confirm(`确认“${facts[field]}”与所选来源一致？审核将记录当前管理身份。`)) return;
      const sourceHashes = [...new Set(profile.sources.filter(source => selected.has(source.id)).map(source => source.sha256))];
      await mutate(async () => {
        const body: GuideReviewInput = { version: saved!.version, facts: [{ field, fact_sha256: await sha256(profile[field]), source_sha256s: sourceHashes }] };
        const result = await api<ProductEnvelope>(`/api/admin/products/${encodeURIComponent(id!)}/guide-review`, { method: 'POST', headers: { 'If-Match': etag! }, body: JSON.stringify(body) });
        accept(result.data, result.etag); notice('当前事实已审核；草稿尚未公开。');
      });
    });
    confirmFact.disabled = !profile.sources.length; row.append(confirmFact); root.append(row);
  }
}
function render() {
  form.hidden = false; renderBasic(); renderImages(); renderGuide(); renderSources(); renderChannels(); renderReviews();
  $('#product-meta').textContent = saved ? `ID ${saved.id} · v${saved.version} · 更新时间 ${saved.updated_at} · 目标${saved.visibility === 'listed' ? '上架' : '下架'}` : '新草稿：允许资料未完成，创建时目标下架。';
  $('#delete-product').hidden = !id;
}
function accept(envelope: ProductEnvelope, strongETag: string | null) {
  if (!strongETag || strongETag !== `"${envelope.version}"`) throw new Error('服务器未返回当前强 ETag，请重新读取。');
  saved = envelope.product; id = saved.id; slug = saved.slug; etag = strongETag; edit = editable(saved); conflictVersion = null;
  setDirty(false); $('#conflict').hidden = true; render();
}
function differences(local: unknown, remote: unknown, path = ''): { field: string; local: unknown; remote: unknown }[] {
  if (JSON.stringify(local) === JSON.stringify(remote)) return [];
  if (local && remote && typeof local === 'object' && typeof remote === 'object' && !Array.isArray(local) && !Array.isArray(remote)) {
    return [...new Set([...Object.keys(local), ...Object.keys(remote)])].flatMap(key => differences((local as Record<string, unknown>)[key], (remote as Record<string, unknown>)[key], path ? `${path}.${key}` : key));
  }
  return [{ field: path, local, remote }];
}
async function showConflict() {
  const result = await api<ProductEnvelope>(`/api/admin/products/${encodeURIComponent(id!)}`);
  conflictVersion = result.data.product;
  const root = $('#conflict'); root.hidden = false; root.replaceChildren();
  const title = element('h2', `版本冲突：本地基于 v${saved?.version}，服务器为 v${conflictVersion.version}`); title.id = 'conflict-title'; root.append(title, element('p', '服务器返回 412。本地表单已保留；未自动重试。请逐项比较后选择如何继续。'));
  for (const change of differences(edit, editable(conflictVersion))) {
    const row = element('div', '', 'card'); row.append(element('h3', change.field)); const values = element('div', '', 'diff');
    const local = element('div'); local.append(element('strong', '本地'), element('pre', JSON.stringify(change.local, null, 2) ?? '无'));
    const remote = element('div'); remote.append(element('strong', '服务器'), element('pre', JSON.stringify(change.remote, null, 2) ?? '无')); values.append(local, remote); row.append(values); root.append(row);
  }
  root.append(button('保留本地，继续比较', () => { notice('本地编辑仍保留。可继续修改，明确采用服务器版本后再保存。'); }), button('采用服务器版本并丢弃本地', () => {
    if (!confirm('确认丢弃本地编辑并读取服务器版本？')) return;
    accept(result.data, result.etag); notice('已采用服务器版本。');
  }), button('已比较，将本地编辑转到此服务器版本', () => {
    if (!confirm('这会保留全部本地字段，包括差异中列出的旧值。请确认已手动合并，需要的服务器修改已录入本地表单。随后仍需点击保存。')) return;
    saved = result.data.product; etag = result.etag; conflictVersion = null; root.hidden = true; renderReviews(); setDirty(true); notice(`已将本地编辑基线设为 v${saved.version}；请核对后显式保存。`);
  })); root.focus();
}
async function mutate(operation: () => Promise<void>) {
  if (busy) return; busy = true;
  // Prevent editing during an in-flight mutation, so a response cannot swallow
  // keystrokes made after the submitted snapshot.
  const controls = Array.from(form.querySelectorAll<HTMLInputElement | HTMLButtonElement | HTMLSelectElement | HTMLTextAreaElement>('input,button,select,textarea'), control => [control, control.disabled] as const);
  for (const [control] of controls) control.disabled = true;
  try { await operation(); } catch (error) {
    if (error instanceof ApiError && error.status === 412) { setDirty(true); try { await showConflict(); } catch (readError) { showError(readError); } }
    else showError(error);
  } finally {
    busy = false;
    for (const [control, disabled] of controls) if (control.isConnected) control.disabled = disabled;
  }
}
form.addEventListener('submit', event => {
  event.preventDefault();
  if (conflictVersion) { notice('请先在冲突区域明确选择处理方式。', true); $('#conflict').focus(); return; }
  mutate(async () => {
    const body = id ? structuredClone(edit) : { ...structuredClone(edit), slug, visibility: 'unlisted' } satisfies ProductCreate;
    try { validateContract(`product.schema.json#/$defs/${id ? 'ProductEdit' : 'ProductCreate'}`, body); }
    catch (error) { const detail = error as { error?: { fields: string[] } }; throw new Error(`字段不合法，请检查：${detail.error?.fields.join('、') ?? '产品资料'}`); }
    const result = await api<ProductEnvelope>(id ? `/api/admin/products/${encodeURIComponent(id)}` : '/api/admin/products', { method: id ? 'PUT' : 'POST', headers: id ? { 'If-Match': etag! } : {}, body: JSON.stringify(body) });
    const wasNew = !id; accept(result.data, result.etag);
    if (wasNew) history.replaceState(null, '', `/admin/products/edit/?id=${encodeURIComponent(id!)}`);
    notice('草稿已保存；尚未公开。');
  });
});
$('#add-source').addEventListener('click', () => {
  edit.guide_profile.sources.push({ id: `src_${crypto.randomUUID().replaceAll('-', '')}`, kind: 'product_text', reference: '/summary', sha256: '0'.repeat(64), public_label: '', public_url: null }); mark(); renderSources();
});
$('#add-channel').addEventListener('click', () => {
  edit.purchase_options.push({ id: null, platform: '', store_name: '', url: '', match_scope: 'exact_model', sku_hint: '', enabled: false }); mark(); renderChannels();
});
$('#delete-product').addEventListener('click', () => {
  if (!confirm('确认删除此从未公开的草稿？固定路径仍会保留。已公开产品只能下架。')) return;
  mutate(async () => { await api(`/api/admin/products/${encodeURIComponent(id!)}`, { method: 'DELETE', headers: { 'If-Match': etag! } }); setDirty(false); location.assign('/admin/products/'); });
});
async function initialize() {
  try {
    await getSession();
    if (id) {
      const result = await api<ProductEnvelope>(`/api/admin/products/${encodeURIComponent(id)}`); accept(result.data, result.etag);
      const mediaID = params.get('media_id'); if (mediaID) { addImage({ media_id: mediaID }); params.delete('media_id'); history.replaceState(null, '', `${location.pathname}?${params}`); }
    } else if (!location.pathname.endsWith('/new/')) { throw new Error('请从产品列表选择要编辑或审核的产品。'); }
    else render();
    let pickerInitialized = false;
    $('#media-picker').addEventListener('toggle', () => { if ((<HTMLDetailsElement>$('#media-picker')).open && !pickerInitialized) { pickerInitialized = true; mediaPicker($('#editor-media'), addImage); } });
    if (location.pathname.includes('/guide-reviews/')) $('#guide-review').scrollIntoView();
  } catch (error) { showError(error); }
}
initialize();
