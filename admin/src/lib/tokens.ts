// Connection secrets appear once in the DOM; never persist or include in templates.
import type { TokenInfo, TokenInput, TokenIssued, TokenList, TokenScope } from '../../../src/generated/contracts';
import { api, getSession, notice, setDirty, showError } from './api';
import { element } from './dom';
export function mountTokens() {
  const form = document.querySelector<HTMLFormElement>('#token-form')!, root = document.querySelector('#tokens')!;
  const issue = document.querySelector<HTMLButtonElement>('#issue-token')!, secret = document.querySelector<HTMLInputElement>('#token-secret')!, display = document.querySelector<HTMLElement>('#issued-token')!;
  const name = form.elements.namedItem('name') as HTMLInputElement, expiry = form.elements.namedItem('expires_at') as HTMLInputElement;
  let busy = false;
  const initial = new Date(Date.now() + 30 * 86400000); initial.setMinutes(initial.getMinutes() - initial.getTimezoneOffset()); expiry.value = initial.toISOString().slice(0, 16);
  form.addEventListener('input', () => setDirty(true));
  const endpoint = `${location.origin}/mcp`;
  (document.querySelector('#mcp-endpoint') as HTMLInputElement).value = endpoint;
  const template = JSON.stringify({ mcpServers: { 'yuanfa-catalog': { type: 'streamableHttp', url: endpoint, headers: { Authorization: 'Bearer YOUR_CONNECTION_KEY' } } } }, null, 2);
  document.querySelector('#mcp-template')!.textContent = template;
  function clearSecret() { secret.value = ''; display.hidden = true; }
  window.addEventListener('pagehide', clearSecret);
  window.addEventListener('pageshow', event => { if (event.persisted) clearSecret(); });
  document.querySelector('#dismiss-token')!.addEventListener('click', () => { clearSecret(); notice('密钥已隐藏。如需重新连接，可以创建新的连接。'); });
  document.querySelector('#copy-token')!.addEventListener('click', async () => {
    try { if (!secret.value) return; await navigator.clipboard.writeText(secret.value); notice('连接密钥已复制。'); } catch { notice('无法自动复制，请选中连接密钥后手动复制。', true); secret.focus(); secret.select(); }
  });
  document.querySelector('#copy-template')!.addEventListener('click', async () => {
    try { await navigator.clipboard.writeText(template); notice('连接配置已复制，请在 WorkBuddy 中填入密钥。'); } catch { notice('无法自动复制，请展开连接配置后手动复制。', true); (document.querySelector('#mcp-template')!.parentElement as HTMLDetailsElement).open = true; document.querySelector<HTMLElement>('#mcp-template')!.focus(); }
  });
  const date = (value: string) => new Date(value).toLocaleString('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' });
  function record(info: TokenInfo) {
    const card = element('article', '', 'token-record');
    const content = element('div'); const heading = element('h3', info.name);
    const state = info.revoked_at ? '已撤销' : Date.parse(info.expires_at) <= Date.now() ? '已失效' : '有效';
    heading.append(element('span', state, `badge${state !== '有效' ? ' off' : ''}`)); content.append(heading);
    content.append(element('p', info.scopes.includes('products:write') ? '查询与编辑产品' : '只读查询'), element('p', `创建于 ${date(info.created_at)}　失效于 ${date(info.expires_at)}`, 'token-time'));
    const revoke = element('button', '撤销连接', 'text-button danger'); revoke.type = 'button'; revoke.disabled = Boolean(info.revoked_at);
    revoke.addEventListener('click', async () => {
      if (busy || !confirm(`撤销“${info.name}”后，WorkBuddy 将无法使用这个连接。确认撤销？`)) return;
      busy = true; revoke.disabled = true; issue.disabled = true;
      try { await api(`/api/admin/mcp-tokens/${encodeURIComponent(info.id)}`, { method: 'DELETE' }); clearSecret(); await load(); notice('连接已撤销。'); }
      catch (error) { showError(error); revoke.disabled = false; }
      finally { busy = false; issue.disabled = false; }
    });
    card.append(content, revoke); return card;
  }
  async function load() {
    await getSession(); const { data } = await api<TokenList>('/api/admin/mcp-tokens'); root.replaceChildren();
    for (const info of data.items) root.append(record(info));
    if (!data.items.length) root.append(element('p', '还没有连接。创建后即可在 WorkBuddy 中使用。', 'empty'));
  }
  form.addEventListener('submit', async event => {
    event.preventDefault(); if (busy) return;
    const expiration = new Date(expiry.value);
    const selected: TokenScope[] = ['products:read', 'media:read'];
    if (new FormData(form).get('permission') === 'edit') selected.push('products:write');
    if (!Number.isFinite(expiration.valueOf()) || expiration.valueOf() <= Date.now()) { notice('请选择未来的失效时间。', true); return; }
    if (secret.value && !confirm('创建新连接会隐藏上一个密钥。确认已保存上一个密钥？')) return;
    busy = true; issue.disabled = true; const controls = [...form.querySelectorAll<HTMLInputElement>('input')]; controls.forEach(input => input.disabled = true);
    try {
      clearSecret(); const input: TokenInput = { name: name.value, expires_at: expiration.toISOString(), scopes: selected };
      const { data } = await api<TokenIssued>('/api/admin/mcp-tokens', { method: 'POST', body: JSON.stringify(input) });
      secret.value = data.secret; display.hidden = false; setDirty(false); notice('连接已创建，请复制密钥到 WorkBuddy。'); secret.focus(); await load();
    } catch (error) { showError(error); }
    finally { busy = false; issue.disabled = false; controls.forEach(input => input.disabled = false); }
  });
  document.querySelector('#refresh-tokens')!.addEventListener('click', () => { if (!busy) void load().catch(showError); });
  void load().catch(showError);
}
