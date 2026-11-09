import { api, ApiError, errorMessage, getSession, notice, setDirty, showError } from './api';
import { button, checkbox, element, input, select } from './dom';
import { categories, nails, scenes } from './labels';
import { mediaPreview, missingImage, uploadImage } from './media';
import { editable, fieldLabels, MAX_IMAGES, mergeEdits, moveImage, newProduct, newSlug, type VisibleField } from './product-state';
import { renderUpdate, watchSiteUpdates } from './site-updates';
import { validateContract } from '../../../src/generated/validate-contract.mjs';
import type { ProductDocument, ProductEdit, ProductEnvelope, ProductImage, SiteUpdate } from '../../../src/generated/contracts';

const form = document.querySelector<HTMLFormElement>('#product-editor')!;
const $ = (selector: string) => document.querySelector<HTMLElement>(selector)!;
const saveButton = $('#save-product') as HTMLButtonElement;
const deleteButton = $('#delete-product') as HTMLButtonElement;
const uploader = $('#upload-images') as HTMLInputElement;
let id = new URLSearchParams(location.search).get('id');
const slug = newSlug();
let saved: ProductDocument | null = null, etag: string | null = null;
let edit = newProduct();
let busy = false, uploading = false, deleted = false, conflict = false;
let separateModel = false;
let updates: SiteUpdate[] | null = [], trackedVersion: number | null = null;
let refreshUpdates: () => Promise<void> = async () => {};
let pending: { path: string; method: 'POST' | 'PUT' | 'DELETE'; body?: string; key: string; etag?: string; version: number } | null = null;
function mark() { setDirty(true); }
function lines(text: string) { return text.split('\n').map(value => value.trim()).filter(Boolean); }
function lock() {
  $('.editor-layout').inert = busy || uploading || !!pending || conflict;
  saveButton.disabled = busy || uploading || conflict || deleted || pending?.method === 'DELETE';
  deleteButton.disabled = busy || uploading || conflict || deleted || !!pending;
  saveButton.textContent = busy ? '正在保存…' : pending ? '重试保存' : '保存并更新官网';
}
function renderBasic() {
  const root = $('#basic-fields'); root.replaceChildren();
  const identity = element('div', '', 'grid');
  const primary = input(separateModel ? '产品名称' : '产品名称 / 型号', edit.name, value => {
    const sameModel = edit.name === edit.model;
    edit.name = value;
    if (!separateModel && ((!!id && sameModel) || (!id && edit.kind === 'tool'))) edit.model = value;
    mark();
  }, { max: separateModel || edit.kind !== 'tool' ? 120 : 80, required: true });
  identity.append(primary);
  if (separateModel) identity.append(input('型号（可留空）', edit.model, value => { edit.model = value; mark(); }, { max: 80 }));
  root.append(identity);
  const different = checkbox('单独填写型号', separateModel, value => {
    separateModel = value;
    if (!value && edit.model && edit.model !== edit.name) { separateModel = true; notice('名称与型号不同，已保留两个输入。若型号不适用，可清空型号。'); }
    renderBasic();
  });
  different.classList.add('model-toggle'); root.append(different);
  const grid = element('div', '', 'grid');
  grid.append(select('产品分类', edit.category_id, categories, value => {
    edit.category_id = value as ProductEdit['category_id'];
    if (!id) {
      edit.kind = value === 'nails' ? 'consumable' : value === 'other' ? 'entry' : 'tool';
      if (!separateModel) edit.model = edit.kind === 'tool' ? edit.name : '';
    }
    mark(); renderBasic(); renderDisplay();
  }), input('系列（可留空）', edit.series, value => { edit.series = value; mark(); }, { max: 120 }));
  root.append(grid, input('产品简介', edit.summary, value => { edit.summary = value; mark(); }, { max: 500, multiline: true }));
  const state = select('官网展示', edit.visibility, { listed: '上架 · 在官网展示', unlisted: '下架 · 不在官网展示' }, value => { edit.visibility = value as ProductEdit['visibility']; mark(); });
  state.classList.add('visibility-field'); root.append(state);
}
function renderDisplay() {
  const root = $('#display-fields'); root.replaceChildren();
  if (edit.kind === 'tool' || edit.fastener_text || edit.nail_type) {
    const grid = element('div', '', 'grid');
    grid.append(input('适用枪钉（可留空）', edit.fastener_text, value => { edit.fastener_text = value; mark(); }, { max: 1000 }), select('枪钉类型（可不选）', edit.nail_type ?? '', { '': '不选择', ...nails }, value => { edit.nail_type = (value || null) as ProductEdit['nail_type']; mark(); })); root.append(grid);
  }
  const applicationLabel = edit.kind === 'consumable' || edit.category_id === 'nails' ? '适用工具与用途' : '使用场景';
  for (const [key, label] of [['applications', applicationLabel], ['features', '产品特点'], ['notes', '注意事项']] as const) {
    const control = input(label, edit[key].join('\n'), value => { edit[key] = lines(value); mark(); }, { multiline: true });
    control.append(element('small', '每行一项，可留空。')); root.append(control);
  }
  const group = element('fieldset'); group.append(element('legend', '场景分类（可多选）'));
  const choices = element('div', '', 'choices');
  for (const [key, label] of Object.entries(scenes)) choices.append(checkbox(label, edit.scene_ids.includes(key as keyof typeof scenes), checked => {
    edit.scene_ids = checked ? [...edit.scene_ids, key as keyof typeof scenes] : edit.scene_ids.filter(value => value !== key); mark();
  }));
  group.append(choices); root.append(group);
  if (edit.kind === 'tool' || edit.long_nose || edit.blower) {
    const tags = element('div', '', 'choices display-tags');
    tags.append(checkbox('加长枪嘴', edit.long_nose, checked => { edit.long_nose = checked; mark(); }), checkbox('吹尘功能', edit.blower, checked => { edit.blower = checked; mark(); })); root.append(tags);
  }
}
let dragged: string | null = null;
function reorderImage(from: number, to: number) {
  const mediaID = edit.images[from]?.media_id;
  moveImage(edit.images, from, to); mark(); renderImages();
  if (mediaID) $('#image-fields').querySelector<HTMLElement>(`[data-media-id="${CSS.escape(mediaID)}"] .image-preview`)?.focus();
}
function renderImages() {
  const root = $('#image-fields'); root.replaceChildren();
  $('#image-count').textContent = `${edit.images.length} / ${MAX_IMAGES}`;
  uploader.disabled = edit.images.length >= MAX_IMAGES;
  if (!edit.images.length) {
    const empty = element('div', '', 'image-empty'); empty.append(missingImage(), element('p', '可以先保存文字资料，官网将显示“暂无实物图”。')); root.append(empty);
  }
  edit.images.forEach((image, index) => {
    const isCover = edit.cover_media_id === image.media_id;
    const card = element('article', '', `image-card${isCover ? ' is-cover' : ''}`); card.dataset.mediaId = image.media_id;
    const preview = mediaPreview(image.media_id, image.label || `${edit.name || '产品'}图片 ${index + 1}`); preview.className = 'image-preview'; preview.tabIndex = 0; preview.draggable = true; preview.setAttribute('aria-label', `图片 ${index + 1}，使用下方按钮调整顺序`);
    if (isCover) preview.append(element('span', '封面', 'badge'));
    preview.addEventListener('dragstart', event => { if (busy || uploading || pending || conflict) { event.preventDefault(); return; } dragged = image.media_id; event.dataTransfer?.setData('text/plain', image.media_id); if (event.dataTransfer) event.dataTransfer.effectAllowed = 'move'; card.setAttribute('aria-grabbed', 'true'); });
    preview.addEventListener('dragend', () => { dragged = null; card.removeAttribute('aria-grabbed'); root.querySelectorAll('.drag-over').forEach(item => item.classList.remove('drag-over')); });
    card.addEventListener('dragover', event => { if (dragged && dragged !== image.media_id) { event.preventDefault(); card.classList.add('drag-over'); } });
    card.addEventListener('dragleave', event => { if (!card.contains(event.relatedTarget as Node | null)) card.classList.remove('drag-over'); });
    card.addEventListener('drop', event => { event.preventDefault(); if (!dragged) return; const from = edit.images.findIndex(item => item.media_id === dragged); dragged = null; reorderImage(from, index); });
    const details = element('div', '', 'image-details');
    details.append(input('图片标题（可选）', image.label, value => { image.label = value; mark(); }, { max: 120 }));
    const more = element('details'); more.append(element('summary', image.description ? '编辑图片说明' : '添加图片说明（可选）'), input('图片说明', image.description, value => { image.description = value; mark(); }, { multiline: true, max: 1000 })); details.append(more);
    const actions = element('div', '', 'image-actions');
    const cover = button(isCover ? '已设为封面' : '设为封面', () => { edit.cover_media_id = image.media_id; mark(); renderImages(); }); cover.disabled = isCover;
    const up = button('前移', () => reorderImage(index, index - 1)); up.disabled = index === 0; up.setAttribute('aria-label', `将图片 ${index + 1} 前移`);
    const down = button('后移', () => reorderImage(index, index + 1)); down.disabled = index === edit.images.length - 1; down.setAttribute('aria-label', `将图片 ${index + 1} 后移`);
    actions.append(cover, button('替换', () => chooseReplacement(image)), up, down, button('移除', () => {
      edit.images.splice(index, 1); if (isCover) edit.cover_media_id = edit.images[0]?.media_id ?? null; mark(); renderImages();
    }, 'text-button danger'));
    details.append(actions); card.append(preview, details); root.append(card);
  });
}
function chooseReplacement(image: ProductImage) {
  const file = element('input'); file.type = 'file'; file.accept = 'image/jpeg,image/png,image/webp';
  file.addEventListener('change', () => { if (file.files?.length) void uploadFiles([...file.files], image.media_id); }); file.click();
}
async function uploadFiles(files: File[], replaceID?: string) {
  if (busy || uploading || pending || conflict || !files.length) return;
  if (!replaceID && edit.images.length + files.length > MAX_IMAGES) { notice(`最多添加 ${MAX_IMAGES} 张图片，目前还可以添加 ${MAX_IMAGES - edit.images.length} 张。`, true); uploader.value = ''; return; }
  uploading = true; lock(); let completed = 0;
  try {
    for (const file of files) {
      $('#upload-status').textContent = `正在上传图片 ${completed + 1} / ${files.length}…`;
      const media = await uploadImage(file);
      if (edit.images.some(image => image.media_id === media.media_id && image.media_id !== replaceID)) throw new Error('这张图片已经在产品中，请选择另一张图片。');
      if (replaceID) {
        const image = edit.images.find(image => image.media_id === replaceID)!;
        image.media_id = media.media_id;
        if (edit.cover_media_id === replaceID) edit.cover_media_id = media.media_id;
      } else {
        edit.images.push({ media_id: media.media_id, label: '', description: '' });
        if (!edit.cover_media_id) edit.cover_media_id = media.media_id;
      }
      completed++; mark(); renderImages();
    }
    $('#upload-status').textContent = '图片已添加，请保存产品以更新官网。';
  } catch (error) { $('#upload-status').textContent = `${completed ? `已添加 ${completed} 张。` : ''}${errorMessage(error)}`; }
  finally { uploading = false; uploader.value = ''; lock(); }
}
uploader.addEventListener('change', () => { void uploadFiles([...(uploader.files ?? [])]); });
function render() {
  form.hidden = false; renderBasic(); renderDisplay(); renderImages(); deleteButton.hidden = !id;
  $('.delete-area').hidden = !id;
  $('#product-meta').textContent = saved ? `${saved.name || saved.model || '产品'} · ${categories[saved.category_id]}` : '填写产品资料，保存后自动更新官网。';
  lock();
}
function accept(envelope: ProductEnvelope, strongETag: string | null) {
  if (!strongETag || strongETag !== `"${envelope.version}"`) throw new TypeError('Missing write acknowledgement');
  saved = envelope.product; id = saved.id; etag = strongETag; edit = editable(saved);
  separateModel = !!edit.model && edit.name !== edit.model;
  conflict = false; $('#conflict').hidden = true; setDirty(false); render();
}
function paintUpdate() {
  if (!id || trackedVersion === null) return;
  const update = updates?.find(item => item.product_id === id);
  renderUpdate($('#site-update'), update, trackedVersion, refreshUpdates, { unavailable: updates === null, deleted });
}
function humanValue(key: VisibleField, value: ProductEdit): string {
  if (key === 'images') return `${value.images.length} 张图片${value.images.length ? `，封面为第 ${value.images.findIndex(item => item.media_id === value.cover_media_id) + 1} 张` : ''}。请核对图片与说明。`;
  if (key === 'category_id') return categories[value.category_id];
  if (key === 'scene_ids') return value.scene_ids.map(scene => scenes[scene]).join('、') || '未选择';
  if (key === 'visibility') return value.visibility === 'listed' ? '上架' : '下架';
  if (key === 'nail_type') return value.nail_type ? nails[value.nail_type] : '未选择';
  const field = value[key];
  if (typeof field === 'boolean') return field ? '是' : '否';
  return Array.isArray(field) ? field.join('\n') || '未填写' : String(field || '未填写');
}
async function showConflict() {
  conflict = true; lock();
  const root = $('#conflict'); root.hidden = false; root.replaceChildren();
  const title = element('h2', '产品有新的修改'); title.id = 'conflict-title';
  root.append(title, element('p', '你的输入已保留。请比较并选择要保留的内容，或重新读取最新产品。'));
  try {
    const result = await api<ProductEnvelope>(`/api/admin/products/${encodeURIComponent(id!)}`);
    const remote = editable(result.data.product), base = editable(saved!);
    const { conflicts } = mergeEdits(base, edit, remote);
    const choices: Partial<Record<VisibleField, 'local' | 'remote'>> = {};
    for (const key of conflicts) {
      const section = element('div', '', 'conflict-field'); section.append(element('h3', fieldLabels[key]));
      const values = element('div', '', 'diff');
      for (const [label, product] of [['你的修改', edit], ['最新内容', remote]] as const) {
        const column = element('div'); column.append(element('strong', label), element('p', humanValue(key, product)));
        if (key === 'images') { const thumbs = element('div', '', 'conflict-images'); for (const image of product.images) thumbs.append(mediaPreview(image.media_id, image.label)); column.append(thumbs); }
        values.append(column);
      }
      section.append(values, select('保留哪一份内容', '', { '': '请选择', local: '保留你的修改', remote: '采用最新内容' }, value => { if (value) choices[key] = value as 'local' | 'remote'; else delete choices[key]; })); root.append(section);
    }
    if (!conflicts.length) root.append(element('p', '两份修改没有重叠，可以合并后继续编辑。'));
    const actions = element('div', '', 'actions');
    actions.append(button('合并修改，继续编辑', () => {
      const merged = mergeEdits(base, edit, remote, choices);
      if (merged.conflicts.length) { notice('请为每个有差异的项目选择要保留的内容。', true); return; }
      edit = merged.edit; saved = result.data.product; etag = result.etag; conflict = false; root.hidden = true; separateModel = !!edit.model && edit.name !== edit.model;
      setDirty(true); render(); notice('内容已合并，请检查后点击“保存并更新官网”。');
    }, 'primary'), button('重新读取最新产品', () => {
      if (!confirm('重新读取会放弃这次尚未保存的输入。确认重新读取？')) return;
      accept(result.data, result.etag); trackedVersion = saved!.version; paintUpdate(); notice('已读取最新内容。');
    })); root.append(actions);
  } catch (error) {
    root.append(element('p', errorMessage(error)), button('重新比较最新内容', showConflict));
  }
  root.focus();
}
async function submitPending() {
  if (!pending || busy) return;
  const operation = pending; busy = true; lock();
  try {
    const headers: Record<string, string> = { 'Idempotency-Key': operation.key };
    if (operation.etag) headers['If-Match'] = operation.etag;
    const result = await api<ProductEnvelope>(operation.path, { method: operation.method, headers, ...(operation.body ? { body: operation.body } : {}) });
    if (operation.method === 'DELETE') {
      pending = null; deleted = true; trackedVersion = operation.version + 1; setDirty(false); form.hidden = true; $('#deleted-product').hidden = false;
      $('#conflict').hidden = true; notice('产品已删除，正在更新官网。');
    } else {
      const wasNew = !id; accept(result.data, result.etag); pending = null; trackedVersion = result.data.version;
      if (wasNew) history.replaceState(null, '', `/admin/products/edit/?id=${encodeURIComponent(id!)}`);
      notice('修改已保存，正在更新官网。');
    }
    updates = updates?.filter(item => item.product_id !== id) ?? [];
    $('#save-recovery')?.remove(); paintUpdate(); await refreshUpdates();
  } catch (error) {
    if (error instanceof ApiError && (error.status === 412 || error.detail.code === 'VERSION_CONFLICT')) {
      pending = null; setDirty(true); await showConflict();
    } else if (error instanceof ApiError && (error.status === 401 || error.status === 403)) {
      setDirty(true); showError(error);
      if (operation.method === 'DELETE' && !$('#save-recovery')) {
        const retry = button('重试删除', submitPending); retry.id = 'save-recovery'; $('#notice').append(retry);
      }
    } else if (error instanceof ApiError && error.status < 500) {
      pending = null; showError(error);
    } else {
      setDirty(true); notice('暂时无法确认保存结果。你的内容已保留，请重试同一次操作。', true);
      if (operation.method === 'DELETE' && !$('#save-recovery')) {
        const retry = button('重试删除', submitPending); retry.id = 'save-recovery'; $('#notice').append(retry);
      }
    }
  } finally { busy = false; lock(); }
}
form.addEventListener('submit', event => {
  event.preventDefault(); if (busy || uploading || conflict) return;
  if (!pending) {
    const body = id ? structuredClone(edit) : { ...structuredClone(edit), slug };
    try { validateContract(`product.schema.json#/$defs/${id ? 'ProductEdit' : 'ProductCreate'}`, body); }
    catch (error) { const detail = error as { error?: { fields: string[] } }; showError(new ApiError(422, { code: '', message: '', fields: detail.error?.fields ?? [] })); return; }
    pending = { path: id ? `/api/admin/products/${encodeURIComponent(id)}` : '/api/admin/products', method: id ? 'PUT' : 'POST', body: JSON.stringify(body), key: crypto.randomUUID(), ...(etag ? { etag } : {}), version: saved?.version ?? 0 };
  }
  void submitPending();
});
deleteButton.addEventListener('click', () => {
  if (!id || busy || pending || uploading || conflict) return;
  if (!confirm(`删除“${edit.name || edit.model || '此产品'}”？产品将从官网移除，尚未保存的输入也会丢弃。`)) return;
  pending = { path: `/api/admin/products/${encodeURIComponent(id)}`, method: 'DELETE', key: crypto.randomUUID(), etag: etag!, version: saved!.version };
  void submitPending();
});
async function initialize() {
  try {
    await getSession();
    if (id) { const result = await api<ProductEnvelope>(`/api/admin/products/${encodeURIComponent(id)}`); accept(result.data, result.etag); trackedVersion = result.data.version; }
    else if (/\/new\/?$/.test(location.pathname)) render();
    else throw new Error('请从产品列表选择需要编辑的产品。');
    refreshUpdates = watchSiteUpdates(items => { updates = items; paintUpdate(); });
  } catch (error) { showError(error); }
}
void initialize();
