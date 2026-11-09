// Real browser confirmation, automatic publication, retry, deletion, and MCP handoff.
// Network-loss tests discard a real response; they never fabricate API success.
import assert from 'node:assert/strict';
import { readFile, realpath, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { editable, decodeToolResult, openHarness, productEditLink } from './admin-helpers.mjs';
const h = await openHarness('admin-publication');
let page, product, failure, editSecret, readSecret;
let restoreBuild;
async function failIsolatedBuild() {
  // Explicitly opt in with a disposable TEMPLATE_DIR used only by this server.
  const directory = process.env.ADMIN_FAILURE_TEMPLATE_DIR;
  assert(directory, 'ADMIN_FAILURE_TEMPLATE_DIR must point to the isolated server template copy');
  const configuration = await realpath(join(directory, 'astro.config.mjs'));
  assert.notEqual(configuration, await realpath('astro.config.mjs'), 'never alter the working source template');
  const original = await readFile(configuration);
  const injected = Buffer.concat([Buffer.from('throw new Error("isolated admin publication failure");\n'), original]);
  restoreBuild = async () => {
    assert((await readFile(configuration)).equals(injected), 'fixture template changed unexpectedly; do not overwrite it');
    await writeFile(configuration, original); restoreBuild = undefined;
  };
  await writeFile(configuration, injected);
}
async function publicProduct(id) {
  const release = await (await fetch(new URL('/api/catalog/release', h.base))).json();
  const response = await fetch(new URL(`/api/catalog/products/${id}?release_id=${encodeURIComponent(release.release_id)}`, h.base));
  assert.equal(response.status, 200); return (await response.json()).product;
}
async function withdrawnProduct(product) {
  const release = await (await fetch(new URL('/api/catalog/release', h.base))).json();
  const detail = await fetch(new URL('/api/catalog/products/' + product.id + '?release_id=' + encodeURIComponent(release.release_id), h.base));
  assert.equal(detail.status, 404, 'withdrawn product must leave the public catalog');
  const oldURL = await fetch(new URL('/products/' + product.slug + '/', h.base));
  assert.equal(oldURL.status, 200, 'previously public URL stays available');
  assert.match(await oldURL.text(), /data-withdrawn/);
  const listing = await (await fetch(new URL('/products/', h.base))).text();
  assert(!listing.includes('href="/products/' + product.slug + '/"'), 'withdrawn product remains in public listing');
}
let rpcID = 1;
async function rpc(secret, method, params) {
  const id = rpcID++;
  const response = await fetch(new URL('/mcp', h.base), { method: 'POST', headers: { Authorization: `Bearer ${secret}`, 'Content-Type': 'application/json', Accept: 'application/json, text/event-stream', 'MCP-Protocol-Version': '2025-03-26' }, body: JSON.stringify({ jsonrpc: '2.0', id, method, params }) });
  assert.equal(response.status, 200, 'MCP request rejected');
  const text = await response.text();
  const message = response.headers.get('content-type')?.includes('text/event-stream') ? text.split(/\r?\n/).filter(line => line.startsWith('data:')).map(line => JSON.parse(line.slice(5))).find(item => item.id === id) : JSON.parse(text);
  assert(message && !message.error, 'MCP protocol failure'); return message.result;
}
async function tool(secret, name, arguments_) { return decodeToolResult(await rpc(secret, 'tools/call', { name, arguments: arguments_ })); }
async function connection(label, permission) {
  await page.goto(new URL('/admin/settings/', h.base).href);
  await page.getByLabel('连接名称', { exact: true }).fill(label);
  await page.getByLabel(permission === 'edit' ? '查询与编辑产品' : '只读查询', { exact: true }).check();
  const created = h.response(page, 'POST', '/api/admin/mcp-tokens', 201); await page.getByRole('button', { name: '创建连接', exact: true }).click(); const result = await (await created).json();
  const secret = result.secret; h.secrets.add(secret); h.issuedTokens.add(result.token.id);
  await page.locator('#issued-token').waitFor({ state: 'visible' });
  assert(Boolean(secret) && await page.locator('#token-secret').inputValue() === secret, 'one-time secret missing');
  const template = await page.locator('#mcp-template').textContent(); assert(!template.includes(secret)); assert.equal(JSON.parse(template).mcpServers['yuanfa-catalog'].url, new URL('/mcp', h.base).href);
  assert(!await page.locator('body').innerText().then(text => text.includes(result.token.id)), 'internal token ID is visible');
  assert(await page.evaluate(value => !Object.values(localStorage).some(item => item.includes(value)) && !Object.values(sessionStorage).some(item => item.includes(value)), secret), 'secret persisted in browser storage');
  await page.getByRole('button', { name: '已保存，隐藏密钥', exact: true }).click(); await page.reload(); await page.locator('.token-record').filter({ hasText: label }).waitFor();
  assert.equal(await page.locator('#token-secret').inputValue(), '');
  const list = await h.request(page, '/api/admin/mcp-tokens'); assert(!(await list.text()).includes(secret));
  await rpc(secret, 'initialize', { protocolVersion: '2025-03-26', capabilities: {}, clientInfo: { name: 'admin-browser-smoke', version: '1' } });
  return { secret, id: result.token.id, label };
}
try {
  page = await h.page(); await h.login(page);
  await h.check('lost create response retries one identical operation without duplicate products', async () => {
    await page.goto(new URL('/admin/products/new/', h.base).href);
    await page.getByLabel('产品名称 / 型号', { exact: true }).fill(`PUB-${h.suffix}`);
    await page.getByLabel('产品简介', { exact: true }).fill('自动更新隔离验收'); await page.getByLabel(/^使用场景/).fill('隔离用途');
    let first, originalKey, originalBody;
    const intercept = async route => {
      if (route.request().method() !== 'POST') return route.continue();
      originalKey = route.request().headers()['idempotency-key']; originalBody = route.request().postData();
      const response = await route.fetch(); assert.equal(response.status(), 201); first = await response.json(); h.createdProducts.add(first.product.id); await route.abort('failed');
    };
    await page.route(url => url.pathname === '/api/admin/products', intercept);
    await page.getByRole('button', { name: '保存并更新官网', exact: true }).click(); await page.getByRole('button', { name: '重试保存', exact: true }).waitFor();
    assert.equal(await page.getByLabel('产品名称 / 型号', { exact: true }).inputValue(), `PUB-${h.suffix}`);
    await page.unrouteAll();
    const repeated = h.response(page, 'POST', '/api/admin/products', 201); product = await h.save(page, null, 201, true); const response = await repeated;
    assert.equal(product.product.id, first.product.id); assert.equal(product.version, first.version);
    assert.equal(response.request().headers()['idempotency-key'], originalKey); assert.equal(response.request().postData(), originalBody);
    const list = await h.request(page, `/api/admin/products?q=${encodeURIComponent(`PUB-${h.suffix}`)}`); assert.equal((await list.json()).items.length, 1);
  });
  const id = product.product.id;
  await h.check('success requires confirmed version and is reflected in public product and HTML', async () => {
    await h.waitForUpdate(page, id, product.version); await page.locator('#site-update').getByText('官网已更新', { exact: true }).waitFor();
    assert.equal((await publicProduct(id)).summary, '自动更新隔离验收');
    const publicHTML = await (await fetch(new URL(`/products/${product.product.slug}/`, h.base))).text(); assert(publicHTML.includes('暂无实物图'));
    await h.screenshot(page, 'confirmed-product-desktop.png');
  });
  await h.check('WorkBuddy settings use minimal scopes and keep connection secrets one-time', async () => {
    editSecret = await connection(`Edit-${h.suffix}`, 'edit');
    readSecret = await connection(`Read-${h.suffix}`, 'read');
    const list = await h.request(page, '/api/admin/mcp-tokens'); const tokens = (await list.json()).items;
    assert.deepEqual(tokens.find(item => item.id === editSecret.id).scopes.toSorted(), ['media:read', 'products:read', 'products:write']);
    assert.deepEqual(tokens.find(item => item.id === readSecret.id).scopes.toSorted(), ['media:read', 'products:read']);
    await h.screenshot(page, 'settings-secret-free.png');
  });
  await h.check('machine drafts stay private until their original edit link is confirmed in browser', async () => {
    const original = await tool(editSecret.secret, 'get_product', { product_id: id }); assert(!original.error);
    const edit = editable(original.value.product); edit.summary = 'WorkBuddy 待确认文字';
    const draft = await tool(editSecret.secret, 'update_product', { product_id: id, expected_version: original.value.version, edit }); assert(!draft.error);
    const link = productEditLink(draft.text, h.base, id); assert.match(draft.text, /尚未更新官网/);
    await h.waitForUpdate(page, id, draft.value.version, 'draft'); assert.equal((await publicProduct(id)).summary, original.value.product.summary);
    const reader = await tool(readSecret.secret, 'update_product', { product_id: id, expected_version: draft.value.version, edit }); assert(reader.error); assert.equal(reader.value.error.code, 'FORBIDDEN');
    assert.equal((await h.readProduct(page, id)).version, draft.value.version);
    const denied = await fetch(new URL('/api/admin/site-updates/retry', h.base), { method: 'POST', headers: { Authorization: `Bearer ${editSecret.secret}`, Origin: h.base.origin } }); assert.equal(denied.status, 403);
    const handoff = await h.page(390); await h.login(handoff, link.pathname + link.search);
    await handoff.locator('#site-update').filter({ hasText: '有待确认的修改' }).waitFor();
    assert.equal(await handoff.locator('#site-update').getByText('官网已更新', { exact: true }).count(), 0);
    assert.equal(await handoff.getByLabel('产品简介', { exact: true }).inputValue(), edit.summary);
    product = await h.save(handoff, id); await h.waitForUpdate(handoff, id, product.version); await handoff.locator('#site-update').getByText('官网已更新', { exact: true }).waitFor();
    assert.equal((await publicProduct(id)).summary, edit.summary); await h.screenshot(handoff, 'workbuddy-confirmed-mobile.png'); await handoff.close();
  });
  if (!h.originalLinksOnly) {
    await h.check('newer draft is not published by another product save; listing changes update public catalog', async () => {
      const current = await h.readProduct(page, id), edit = editable(current.product); edit.summary = '未确认的新版本不得公开';
      const draft = await tool(editSecret.secret, 'update_product', { product_id: id, expected_version: current.version, edit }); assert(!draft.error);
      const another = await h.create(page, `OTHER-${h.suffix}`); await h.waitForUpdate(page, another.product.id, another.version);
      assert.equal((await publicProduct(id)).summary, current.product.summary);
      await page.goto(new URL(`/admin/products/edit/?id=${id}`, h.base).href); await page.locator('#site-update').filter({ hasText: '有待确认的修改' }).waitFor();
      await page.getByLabel('官网展示', { exact: true }).selectOption('unlisted'); product = await h.save(page, id); await h.waitForUpdate(page, id, product.version);
      await withdrawnProduct(product.product);
      await page.getByLabel('官网展示', { exact: true }).selectOption('listed'); product = await h.save(page, id); await h.waitForUpdate(page, id, product.version); assert.equal((await publicProduct(id)).summary, edit.summary);
      const deletedVersion = await h.remove(page, another.product.id); await h.waitForUpdate(page, another.product.id, deletedVersion);
    });
    await h.check('failed update preserves public bytes; global retry includes a deleted product', async () => {
      const before = Buffer.from(await (await fetch(new URL(`/products/${product.product.slug}/`, h.base))).arrayBuffer());
      // Keep valid product data: induce a real build failure in the dedicated
      // template copy, without fabricating API responses or weakening validation.
      await failIsolatedBuild();
      await page.goto(new URL(`/admin/products/edit/?id=${id}`, h.base).href);
      await page.getByLabel('产品简介', { exact: true }).fill('更新失败时仍保留上次公开版本');
      product = await h.save(page, id); await h.waitForUpdate(page, id, product.version, 'failed'); await page.locator('#site-update').filter({ hasText: '官网更新失败' }).waitFor();
      assert(Buffer.from(await (await fetch(new URL(`/products/${product.product.slug}/`, h.base))).arrayBuffer()).equals(before));
      const doomed = await h.create(page, `DELETE-${h.suffix}`); await h.waitForUpdate(page, doomed.product.id, doomed.version, 'failed');
      const deletedVersion = await h.remove(page, doomed.product.id); await h.waitForUpdate(page, doomed.product.id, deletedVersion, 'failed');
      await page.goto(new URL(`/admin/products/?q=${encodeURIComponent(`DELETE-${h.suffix}`)}`, h.base).href);
      await page.getByText('没有符合条件的产品', { exact: true }).waitFor(); await page.locator('#list-update').filter({ hasText: '官网更新失败' }).waitFor();
      const actualRetry = page.waitForResponse(r => new URL(r.url()).pathname === '/api/admin/site-updates/retry' && r.request().method() === 'POST');
      await page.getByRole('button', { name: '重试更新官网', exact: true }).click(); const response = await actualRetry; assert.equal(response.status(), 202); assert.equal((await response.json()).status, 'pending'); assert.equal(response.request().postData(), null);
      await h.waitForUpdate(page, doomed.product.id, deletedVersion, 'failed'); await h.screenshot(page, 'global-deletion-failure.png');
      await restoreBuild();
      await page.goto(new URL(`/admin/products/edit/?id=${id}`, h.base).href);
      await page.getByLabel('产品简介', { exact: true }).fill('恢复构建后发布完整资料');
      product = await h.save(page, id); await h.waitForUpdate(page, id, product.version); await h.waitForUpdate(page, doomed.product.id, deletedVersion);
      const finalVersion = await h.remove(page, id); await h.waitForUpdate(page, id, finalVersion);
      await page.locator('#site-update').filter({ hasText: '产品已删除，官网已更新' }).waitFor();
      await withdrawnProduct(product.product);
    });
  }
  await h.check('revocation denies further machine reads and old workflow routes redirect', async () => {
    for (const token of [editSecret, readSecret]) {
      await page.goto(new URL('/admin/settings/', h.base).href); const row = page.locator('.token-record').filter({ hasText: token.label }); await row.waitFor();
      await Promise.all([h.response(page, 'DELETE', `/api/admin/mcp-tokens/${token.id}`, 204), row.getByRole('button', { name: '撤销连接', exact: true }).click()]); await row.filter({ hasText: '已撤销' }).waitFor();
      const denied = await fetch(new URL('/mcp', h.base), { method: 'POST', headers: { Authorization: `Bearer ${token.secret}`, 'Content-Type': 'application/json', Accept: 'application/json, text/event-stream' }, body: JSON.stringify({ jsonrpc: '2.0', id: rpcID++, method: 'tools/list', params: {} }) }); assert.equal(denied.status, 401); h.issuedTokens.delete(token.id);
    }
    for (const path of ['/admin/candidates/', '/admin/publications/', '/admin/publication-requests/', '/admin/guide-reviews/', '/admin/media/']) { await page.goto(new URL(path, h.base).href); await page.waitForURL(new URL('/admin/products/', h.base).href); }
  });
} catch (error) { failure = error; }
finally {
  try { if (restoreBuild) await restoreBuild(); } catch (error) { failure ??= error; }
  await h.finish(failure, page);
}
