// Secrets live only in the one-time DOM display; neither storage nor the
// configuration template receives them. Reopening/listing never retrieves one.
import type { TokenInfo, TokenInput, TokenIssued, TokenList, TokenScope } from '../../../src/generated/contracts';
import { api, getSession, notice, setDirty, showError } from './api';
import { checkbox, element } from './dom';

const scopeLabels: Record<TokenScope, string> = { 'products:read': '查询产品和当前文档版本', 'products:write': '创建、编辑和删除未公开草稿', 'media:read': '查询素材并取得受保护上传入口', 'candidates:write': '冻结完整工作区并查询候选', 'publications:write': '创建发布意图并查询结果（无确认权）' };
export function mountTokens() {
  const form = document.querySelector<HTMLFormElement>('#token-form')!, scopes = document.querySelector('#token-scopes')!, root = document.querySelector('#tokens')!;
  const issue = document.querySelector<HTMLButtonElement>('#issue-token')!, secret = document.querySelector<HTMLInputElement>('#token-secret')!, display = document.querySelector<HTMLElement>('#issued-token')!;
  const name = form.elements.namedItem('name') as HTMLInputElement, expiry = form.elements.namedItem('expires_at') as HTMLInputElement;
  let busy = false;
  for (const [scope, label] of Object.entries(scopeLabels)) {
    const control = checkbox(label, true, () => setDirty(true)); const input = control.querySelector('input')!; input.name = 'scope'; input.value = scope; scopes.append(control);
  }
  // datetime-local uses the user's local zone; the wire contract always uses UTC.
  const initial = new Date(Date.now() + 30 * 86400000); initial.setMinutes(initial.getMinutes() - initial.getTimezoneOffset()); expiry.value = initial.toISOString().slice(0, 16);
  form.addEventListener('input', () => setDirty(true));
  const endpoint = `${location.origin}/mcp`;
  (document.querySelector('#mcp-endpoint') as HTMLInputElement).value = endpoint;
  const template = JSON.stringify({ mcpServers: { 'yuanfa-catalog': { type: 'streamableHttp', url: endpoint, headers: { Authorization: 'Bearer ${YUANFA_MCP_TOKEN}' } } } }, null, 2);
  document.querySelector('#mcp-template')!.textContent = template;
  function clearSecret() { secret.value = ''; display.hidden = true; }
  window.addEventListener('pagehide', clearSecret);
  window.addEventListener('pageshow', event => { if (event.persisted) clearSecret(); });
  document.querySelector('#dismiss-token')!.addEventListener('click', () => { clearSecret(); notice('本次凭证展示已清除，无法再次查询原值。'); });
  document.querySelector('#copy-token')!.addEventListener('click', async () => {
    try { if (!secret.value) return; await navigator.clipboard.writeText(secret.value); notice('已复制本次凭证，请安全绑定。'); } catch { notice('剪贴板不可用，请选中上方本次凭证手动复制。', true); secret.focus(); secret.select(); }
  });
  document.querySelector('#copy-template')!.addEventListener('click', async () => {
    try { await navigator.clipboard.writeText(template); notice('已复制不含真实密钥的配置模板。'); } catch { notice('剪贴板不可用，请选中配置模板手动复制。', true); document.querySelector<HTMLElement>('#mcp-template')!.focus(); }
  });
  function record(info: TokenInfo) {
    const card = element('article', '', 'card token-record'); card.dataset.tokenId = info.id;
    card.append(element('h3', info.name), element('p', `ID：${info.id}`));
    const state = info.revoked_at ? '已撤销' : Date.parse(info.expires_at) <= Date.now() ? '已到期' : '有效';
    card.append(element('p', state, 'badge'), element('p', `签发：${info.created_at} · 到期：${info.expires_at}${info.revoked_at ? ' · 撤销：' + info.revoked_at : ''}`));
    const list = element('ul'); for (const scope of info.scopes) list.append(element('li', scopeLabels[scope])); card.append(list);
    const revoke = element('button', '撤销此凭证', 'danger'); revoke.type = 'button'; revoke.disabled = Boolean(info.revoked_at);
    revoke.addEventListener('click', async () => {
      if (busy || !confirm(`撤销「${info.name}」后，WorkBuddy 后续请求将失去授权。确认撤销？`)) return;
      busy = true; revoke.disabled = true; issue.disabled = true;
      try { await api(`/api/admin/mcp-tokens/${encodeURIComponent(info.id)}`, { method: 'DELETE' }); clearSecret(); await load(); notice('凭证已撤销，后续请求不再授权。'); }
      catch (error) { showError(error); revoke.disabled = false; }
      finally { busy = false; issue.disabled = false; }
    });
    card.append(revoke); return card;
  }
  async function load() {
    await getSession(); const { data } = await api<TokenList>('/api/admin/mcp-tokens'); root.replaceChildren();
    for (const info of data.items) root.append(record(info));
    if (!data.items.length) root.append(element('p', '尚无凭证。签发后只在本次显示原值。', 'empty'));
  }
  form.addEventListener('submit', async event => {
    event.preventDefault(); if (busy) return;
    const date = new Date(expiry.value), selected = [...form.querySelectorAll<HTMLInputElement>('[name=scope]:checked')].map(input => input.value as TokenScope);
    if (!selected.length || !Number.isFinite(date.valueOf()) || date.valueOf() <= Date.now()) { notice('请选择至少一项权限，并设置未来的到期时间。', true); return; }
    if (secret.value && !confirm('新签发会清除上次的一次性展示。已安全保存上次凭证？')) return;
    busy = true; issue.disabled = true; const controls = [...form.querySelectorAll<HTMLInputElement>('input')]; controls.forEach(input => input.disabled = true);
    try {
      clearSecret(); const input: TokenInput = { name: name.value, expires_at: date.toISOString(), scopes: selected };
      const { data } = await api<TokenIssued>('/api/admin/mcp-tokens', { method: 'POST', body: JSON.stringify(input) });
      secret.value = data.secret; display.hidden = false; setDirty(false); notice('凭证已签发，原值仅本次展示。'); secret.focus();
      await load();
    } catch (error) { showError(error); }
    finally { busy = false; issue.disabled = false; controls.forEach(input => input.disabled = false); }
  });
  document.querySelector('#refresh-tokens')!.addEventListener('click', () => { if (!busy) void load().catch(showError); });
  void load().catch(showError);
}
