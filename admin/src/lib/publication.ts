// The server owns the full workspace freeze and every activation check. Review
// inputs below stay bound to the first fetched resource; polling changes status only.
import type { Candidate, CandidateDiff, ConfirmationInput, ErrorDetail, PublicationInput, PublicationList, PublicationRequest, SessionResponse } from '../../../src/generated/contracts';
import { api, ApiError, getSession, notice, showError } from './api';
import { element } from './dom';

const candidateLabels = { queued: '排队构建', building: '正在构建与审计', ready: '构建与审计成功', failed: '构建失败，官网未变化' };
const requestLabels = { awaiting_confirmation: '待人工确认，尚未上线', queued: '已确认，等待执行，尚未报告生效', activating: '正在激活，尚未报告生效', succeeded: '执行成功', stale: '已过期：工作区或线上激活基线发生变化', expired: '已超过 24 小时有效期', cancelled: '已取消，未执行本次切换', failed: '执行失败' };
const fieldLabels: Record<string, string> = { product: '完整产品（新增或移除）', name: '产品名称', model: '型号', slug: '路径', images: '图片、顺序与图注', cover_media_id: '封面图片', visibility: '目标上下架状态', guide_profile: '选型资料与审核', purchase_options: '购买渠道：链接、店铺、SKU、启停与顺序', legacy_series_ids: '旧系列归属', category_id: '分类', summary: '简介', applications: '用途', features: '特点', notes: '说明', scene_ids: '场景', series: '展示系列', fastener_text: '紧固件规格', nail_type: '紧固件类型', long_nose: '加长枪嘴标签', blower: '吹尘标签', kind: '条目类型', sort_order: '排序', version: '文档版本', updated_at: '编辑时间', categories: '分类字典', scenes: '场景字典', nail_types: '紧固件字典', legacy_series: '旧系列字典' };
const el = <T extends HTMLElement>(id: string) => document.getElementById(id) as T;
const idPath = (id: string) => encodeURIComponent(id);
const reviewURL = (id: string) => `/admin/publication-requests/?id=${idPath(id)}`;
const time = (value: string | null) => value ? `${new Date(value).toLocaleString()} (${value})` : '—';
function link(text: string, href: string) { const a = element('a', text); a.href = href; return a; }
function meta(root: HTMLElement, rows: [string, string][]) {
  root.replaceChildren();
  for (const [label, value] of rows) root.append(element('dt', label), element('dd', value));
}
function failure(root: HTMLElement, error: ErrorDetail | null) {
  root.hidden = !error; root.textContent = error ? `${error.message}（${error.code}）${error.fields?.length ? ' 字段：' + error.fields.join('、') : ''}` : '';
}
function pretty(value: string | null) {
  if (value === null) return '无';
  try { return JSON.stringify(JSON.parse(value), null, 2); } catch { return value; }
}
function imageIDs(value: string | null, field: string): string[] {
  if (value === null) return [];
  try {
    const data = JSON.parse(value);
    if (field === 'cover_media_id') return typeof data === 'string' ? [data] : [];
    const images = field === 'images' ? data : field === 'product' ? data?.images : [];
    return Array.isArray(images) ? [...new Set<string>(images.map(item => item.media_id).filter(id => typeof id === 'string'))] : [];
  } catch { return []; }
}
function drawDiff(root: HTMLElement, diff: CandidateDiff) {
  root.replaceChildren();
  const intro = element('section', '', 'card');
  intro.append(element('h2', '完整目标与基线的差异'), element('p', `比较基线：${diff.baseline_release_id ?? '首次发布（无线上版本）'}。以下包含整个工作区，不限当前产品表单。`));
  if (!diff.changes.length) intro.append(element('p', '冻结目标与比较基线没有字段差异。', 'empty'));
  root.append(intro);
  for (const change of diff.changes) {
    const item = element('details', '', 'card publication-change');
    item.dataset.productId = change.product_id ?? ''; item.dataset.field = change.field;
    item.append(element('summary', `${change.product_id ?? '全站字典'} · ${fieldLabels[change.field] ?? change.field}`));
    const grid = element('div', '', 'diff');
    for (const [label, json] of [['基线内容', change.before_json], ['目标内容', change.after_json]] as const) {
      const column = element('div'); column.append(element('h3', label), element('pre', pretty(json)));
      const ids = imageIDs(json, change.field);
      for (const id of ids) {
        const a = link(`查看私有素材 ${id}`, `/api/admin/media/${idPath(id)}/file`); a.target = '_blank'; a.rel = 'noopener';
        const image = element('img'); image.src = a.href; image.alt = `${label} ${id}`; image.loading = 'lazy'; image.className = 'publication-thumbnail';
        a.prepend(image); column.append(a);
      }
      grid.append(column);
    }
    item.append(grid); root.append(item);
  }
  const routes = element('details', '', 'card'); routes.append(element('summary', '受影响的页面与路由'));
  const routeList = element('ul');
  for (const route of diff.affected_routes) routeList.append(element('li', route));
  routes.append(routeList); root.append(routes);
  const warnings = element('section', '', 'card'); warnings.append(element('h2', '选型资料与发布警告'));
  if (!diff.warnings.length) warnings.append(element('p', '本次没有服务端审阅警告。'));
  else { const list = element('ul'); for (const warning of diff.warnings) list.append(element('li', warning)); warnings.append(list); }
  root.append(warnings);
}
// A failed/uncertain network response is retried only by an explicit user action,
// with the same body and key. A different body is always a different operation.
function keyedPost<T>(path: string) {
  let body = '', key = '';
  return async (input: unknown) => {
    const next = JSON.stringify(input);
    if (body !== next || !key) { body = next; key = crypto.randomUUID(); }
    const result = await api<T>(path, { method: 'POST', headers: { 'Idempotency-Key': key }, body });
    key = ''; return result.data;
  };
}
async function freshState() {
  await getSession();
  const { data } = await api<SessionResponse>('/api/admin/session');
  el('session-state').textContent = `${data.username} · 工作区版本 ${data.workspace_revision} · 线上版本 ${data.active_release_id ?? '尚未发布'} · 保存仅更新草稿`;
  return data;
}
function poll(read: () => Promise<boolean>) {
  let timer: number | undefined; let stopped = false; let enabled = false; let running = false;
  async function run() {
    if (stopped || !enabled || running) return;
    running = true; let again = false;
    try { again = await read(); } catch (error) { showError(error); }
    finally { running = false; if (again && enabled && !stopped) timer = window.setTimeout(run, 1500); }
  }
  window.addEventListener('pagehide', () => { stopped = true; clearTimeout(timer); }, { once: true });
  window.addEventListener('pageshow', event => { if (event.persisted) location.reload(); });
  return { start: () => { enabled = true; clearTimeout(timer); void run(); }, stop: () => { enabled = false; clearTimeout(timer); } };
}
function openForm(formID: string, path: string) {
  const form = el<HTMLFormElement>(formID);
  (form.elements.namedItem('id') as HTMLInputElement).value = new URLSearchParams(location.search).get('id') ?? '';
  form.addEventListener('submit', event => {
    event.preventDefault(); const id = String(new FormData(form).get('id')).trim();
    if (id) location.assign(`${path}?id=${idPath(id)}`);
  });
}

export function mountCandidate() {
  openForm('candidate-open', '/admin/candidates/');
  let candidate: Candidate | null = null; let baseline: SessionResponse | null = null; let busy = false; let conflict = false;
  const id = new URLSearchParams(location.search).get('id');
  const freeze = el<HTMLButtonElement>('freeze-workspace'), preview = el<HTMLButtonElement>('preview-candidate'), request = el<HTMLButtonElement>('request-publish');
  const create = keyedPost<Candidate>('/api/admin/candidates');
  const makeRequest = keyedPost<PublicationRequest>('/api/admin/publication-requests');
  function controls() {
    freeze.disabled = busy || !baseline;
    preview.disabled = busy || candidate?.state !== 'ready';
    request.disabled = busy || candidate?.state !== 'ready' || candidate.workspace_changed || conflict;
  }
  async function readWorkspace() {
    baseline = await freshState();
    el('freeze-baseline').textContent = `将冻结工作区版本 ${baseline.workspace_revision}；当前线上 ${baseline.active_release_id ?? '尚未发布'}。所有已保存修改均包含在内。`;
    controls();
  }
  async function load() {
    if (!id) return false;
    await getSession(); const next = (await api<Candidate>(`/api/admin/candidates/${idPath(id)}`)).data;
    const first = candidate === null; candidate = next; el('candidate-detail').hidden = false;
    if (first) {
      meta(el('candidate-meta'), [['候选 ID', next.id], ['冻结工作区版本', String(next.workspace_revision)], ['快照 SHA-256', next.snapshot_sha256], ['预期线上版本', next.expected_active_release_id ?? '无'], ['schema / 构建代码', `${next.schema_version} / ${next.code_version}`], ['创建时间', time(next.created_at)]]);
      drawDiff(el('candidate-diff'), next.diff);
    }
    el('candidate-state').textContent = `${candidateLabels[next.state]} · 开始 ${time(next.started_at)} · 完成 ${time(next.completed_at)}`;
    failure(el('candidate-error'), next.error);
    const stale = el('candidate-stale'); stale.hidden = !next.workspace_changed && !conflict;
    stale.textContent = conflict ? '候选的线上激活基线或工作区已冲突；请重新生成完整工作区候选并审阅。' : '候选已过期：冻结后工作区已修改。预览仍是冻结内容，不能发布；请重新生成并审阅。';
    controls(); return next.state === 'queued' || next.state === 'building';
  }
  const polling = poll(load);
  async function act(action: () => Promise<void>) {
    if (busy) return; busy = true; controls();
    try { await action(); } catch (error) { showError(error); } finally { busy = false; controls(); }
  }
  el('refresh-workspace').addEventListener('click', () => void act(readWorkspace));
  freeze.addEventListener('click', () => void act(async () => {
    if (!baseline) return;
    const c = await create({ expected_workspace_revision: baseline.workspace_revision }); location.assign(`/admin/candidates/?id=${idPath(c.id)}`);
  }));
  el('refresh-candidate').addEventListener('click', () => polling.start());
  preview.addEventListener('click', () => void act(async () => {
    if (!candidate || candidate.state !== 'ready') return;
    await api('/api/admin/preview-session', { method: 'POST', body: JSON.stringify({ candidate_id: candidate.id }) }); location.assign('/');
  }));
  request.addEventListener('click', () => void act(async () => {
    if (!candidate) return;
    const input: PublicationInput = { kind: 'publish', candidate_id: candidate.id, release_id: null, snapshot_sha256: candidate.snapshot_sha256, expected_active_release_id: candidate.expected_active_release_id, expected_workspace_revision: candidate.workspace_revision };
    try { const p = await makeRequest(input); location.assign(reviewURL(p.id)); }
    catch (error) { if (error instanceof ApiError && error.status === 409) { conflict = true; await load(); } throw error; }
  }));
  void readWorkspace().catch(showError); if (id) polling.start();
}

export function mountRequest() {
  openForm('request-open', '/admin/publication-requests/');
  const id = new URLSearchParams(location.search).get('id'); if (!id) return;
  let pinned: PublicationRequest | null = null; let current: PublicationRequest | null = null; let busy = false; let conflict = false;
  const form = el<HTMLFormElement>('confirmation-form'), confirmButton = el<HTMLButtonElement>('confirm-publication'), cancel = el<HTMLButtonElement>('cancel-publication'), reviewed = el<HTMLInputElement>('reviewed-workspace');
  const confirmPost = keyedPost<PublicationRequest>(`/api/admin/publication-requests/${idPath(id)}/confirm`);
  function controls() {
    const allowed = current?.state === 'awaiting_confirmation' && !conflict && Date.parse(current.expires_at) > Date.now();
    confirmButton.disabled = busy || !allowed || !reviewed.checked;
    cancel.disabled = busy || current?.state !== 'awaiting_confirmation';
    reviewed.disabled = busy || !allowed;
  }
  function status(next: PublicationRequest) {
    if (current && current.state !== 'awaiting_confirmation' && next.state === 'awaiting_confirmation') return;
    if (current && ['succeeded', 'failed', 'stale', 'expired', 'cancelled'].includes(current.state)) return;
    current = next;
    const expiredHere = next.state === 'awaiting_confirmation' && Date.parse(next.expires_at) <= Date.now();
    el('request-state').textContent = `${expiredHere ? '已到期，不能确认；服务器将在读取时核实期限' : requestLabels[next.state]} · 有效期至 ${time(next.expires_at)}`;
    failure(el('request-error'), next.error);
    const result = el('publication-result'); result.hidden = next.state !== 'succeeded';
    if (next.state === 'succeeded' && next.result_release_id && next.completed_at && next.site_url) {
      result.replaceChildren(element('h2', next.kind === 'rollback' ? '官网已回退' : '官网已生效'));
      const dl = element('dl', '', 'publication-meta'); meta(dl, [['实际生效 release ID', next.result_release_id], ['完成时间', time(next.completed_at)]]); result.append(dl, link('打开成功结果的官网链接', next.site_url));
      result.append(element('p', next.kind === 'rollback' ? '线上已回退，编辑区仍有未发布内容。回退没有覆盖草稿；下次发布必须重新生成并审阅完整工作区候选。' : '本次完整工作区候选已成功激活。以后保存的草稿仍须重新构建、审阅和确认。', 'notice'));
      notice(next.kind === 'rollback' ? '官网已回退，编辑区仍有未发布内容。' : '官网已生效。');
      void freshState().catch(showError);
    }
    controls();
  }
  async function load() {
    await getSession(); const next = (await api<PublicationRequest>(`/api/admin/publication-requests/${idPath(id!)}`)).data;
    if (!pinned) {
      pinned = next; el('request-detail').hidden = false;
      el('request-scope').textContent = next.kind === 'rollback' ? '本次将整个公开网站回退至指定历史成功版本，不覆盖工作区草稿。请审阅与绑定线上基线的全部差异。' : '本次发布包含整个工作区，不限于当前产品或表单。执行状态与生效结果见下方。';
      meta(el('request-meta'), [['意图 ID', next.id], ['操作', next.kind === 'rollback' ? '历史版本回退' : '完整工作区发布'], ['固定目标 ID', (next.kind === 'rollback' ? next.release_id : next.candidate_id) ?? '无'], ['已审阅快照 SHA-256', next.snapshot_sha256], ['已审阅预期线上版本', next.expected_active_release_id ?? '无'], ['已审阅工作区版本', String(next.expected_workspace_revision)], ['创建时间', time(next.created_at)]]);
      drawDiff(el('request-diff'), next.diff);
    }
    status(next); return ['awaiting_confirmation', 'queued', 'activating'].includes(next.state);
  }
  const polling = poll(load);
  async function act(action: () => Promise<void>) {
    if (busy) return; busy = true; controls();
    try { await action(); } catch (error) {
      if (error instanceof ApiError && error.status === 409) {
        conflict = true; el('request-conflict').hidden = false;
        el('request-conflict').textContent = `${error.message}（409 / ${error.detail.code}）。原审阅目标保持不变；请重新生成并审阅，不会自动重试确认。`;
        try { await load(); } catch { /* Keep the original actionable conflict visible. */ }
      }
      showError(error);
    } finally { busy = false; controls(); }
  }
  reviewed.addEventListener('change', controls);
  form.addEventListener('submit', event => { event.preventDefault(); if (!pinned || confirmButton.disabled) return; void act(async () => {
    const input: ConfirmationInput = { snapshot_sha256: pinned!.snapshot_sha256, expected_active_release_id: pinned!.expected_active_release_id, expected_workspace_revision: pinned!.expected_workspace_revision };
    const next = await confirmPost(input); status(next);
    if (next.state !== 'succeeded') notice('确认已接收，正在等待执行结果。');
    polling.start();
  }); });
  cancel.addEventListener('click', () => void act(async () => {
    const next = (await api<PublicationRequest>(`/api/admin/publication-requests/${idPath(id!)}/cancel`, { method: 'POST' })).data;
    status(next); notice('待确认意图已取消。'); polling.stop();
  }));
  el('refresh-request').addEventListener('click', () => polling.start()); polling.start();
}

export function mountPublications() {
  const root = el('publications'), more = el<HTMLButtonElement>('more-publications');
  let cursor: string | null = null; let busy = false; let state: SessionResponse | null = null;
  const makeRequest = keyedPost<PublicationRequest>('/api/admin/publication-requests');
  async function load(reset: boolean) {
    if (busy) return; busy = true; more.disabled = true;
    try {
      if (reset) state = await freshState();
      const params = new URLSearchParams({ limit: '20' }); if (!reset && cursor) params.set('cursor', cursor);
      const data = (await api<PublicationList>(`/api/admin/publications?${params}`)).data;
      el('active-publication').textContent = `当前线上版本：${data.active_release_id ?? '尚未发布'}。历史成功只代表该次操作的结果，是否当前线上以此处为准。`;
      if (reset) root.replaceChildren();
      for (const p of data.items) {
        const card = element('article', '', 'card publication-record'); card.dataset.requestId = p.id; card.dataset.releaseId = p.result_release_id ?? '';
        const h = element('h2'); h.append(link(`${p.kind === 'rollback' ? '回退' : '发布'} · ${p.id}`, reviewURL(p.id)));
        card.append(h, element('p', requestLabels[p.state]));
        const dl = element('dl', '', 'publication-meta'); meta(dl, [['目标', (p.candidate_id ?? p.release_id) ?? '无'], ['创建', time(p.created_at)], ['意图期限', time(p.expires_at)]]); card.append(dl);
        if (p.error) { const error = element('p', '', 'notice error'); failure(error, p.error); card.append(error); }
        if (p.state === 'succeeded' && p.result_release_id && p.completed_at && p.site_url) {
          card.append(element('p', `成功 release：${p.result_release_id} · 完成：${time(p.completed_at)}`), link('查看官网', p.site_url));
          if (p.kind === 'rollback') card.append(element('p', '线上已回退，编辑区仍有未发布内容。', 'notice'));
          const rollback = element('button', '审阅回退到此版本'); rollback.type = 'button';
          rollback.addEventListener('click', async () => {
            if (busy || !state) return; busy = true; rollback.disabled = true;
            try {
              const input: PublicationInput = { kind: 'rollback', candidate_id: null, release_id: p.result_release_id, snapshot_sha256: p.snapshot_sha256, expected_active_release_id: state.active_release_id, expected_workspace_revision: state.workspace_revision };
              const next = await makeRequest(input); location.assign(reviewURL(next.id));
            } catch (error) { showError(error); } finally { busy = false; rollback.disabled = false; }
          }); const actions = element('div', '', 'actions'); actions.append(rollback); card.append(actions);
        }
        root.append(card);
      }
      if (!root.childElementCount) root.append(element('p', '没有发布或回退记录。先生成完整工作区候选。', 'empty'));
      cursor = data.next_cursor; more.hidden = !cursor;
    } catch (error) { showError(error); } finally { busy = false; more.disabled = false; }
  }
  el('refresh-publications').addEventListener('click', () => void load(true)); more.addEventListener('click', () => void load(false)); void load(true);
}
