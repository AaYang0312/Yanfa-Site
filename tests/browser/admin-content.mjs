// Real UI CRUD, private image handling, concurrency, and session recovery.
import assert from 'node:assert/strict';
import { resolve } from 'node:path';
import { editable, openHarness } from './admin-helpers.mjs';
const h = await openHarness('admin-content');
let page, cleanupPage, product, failure;
try {
  page = await h.page(); await h.login(page); cleanupPage = await h.page(1440, page.context());
  await h.check('login lands on products with only product/settings navigation', async () => {
    assert.equal(new URL(page.url()).pathname, '/admin/products/');
    assert.deepEqual(await page.getByRole('navigation', { name: '后台导航' }).getByRole('link').allTextContents(), ['产品管理', '设置']);
    assert.equal(await page.getByRole('link', { name: '查看官网', exact: true }).getAttribute('target'), '_blank');
  });
  await h.check('create listed product without exposing identifiers and with neutral empty images', async () => {
    product = await h.create(page, `QA-${h.suffix}`);
    assert.equal(product.product.visibility, 'listed'); assert.equal(product.product.model, `QA-${h.suffix}`);
    assert.match(product.product.slug, /^[a-z0-9][a-z0-9-]{0,79}$/);
    assert.deepEqual(product.product.images, []);
    await page.getByText('未添加图片', { exact: true }).waitFor();
    assert.equal(await page.getByLabel(/slug|数据库|排序值|来源 ID|选型/).count(), 0);
  });
  const id = product.product.id;
  await h.check('editable display fields round-trip while hidden business fields survive', async () => {
    const original = await h.readProduct(page, id), edit = editable(original.product);
    edit.legacy_series_ids = ['s_f30']; edit.sort_order = 73;
    edit.guide_profile.consultation_notes = ['隐藏资料必须保留'];
    const host = process.env.PURCHASE_ALLOWED_HOSTS?.split(',').find(Boolean);
    if (host) edit.purchase_options = [{ id: null, platform: '测试商城', store_name: '测试店铺', url: `https://${host}/qa-${h.suffix}`, match_scope: 'exact_model', sku_hint: '', enabled: true }];
    const fixture = await h.request(page, `/api/admin/products/${id}`, { method: 'PUT', headers: { 'If-Match': `"${original.version}"` }, data: edit }); assert.equal(fixture.status(), 200);
    const hidden = (await fixture.json()).product;
    await page.reload();
    await page.getByLabel('产品分类', { exact: true }).selectOption('staple');
    await page.getByLabel('系列（可留空）', { exact: true }).fill('隔离验收系列');
    await page.getByLabel(/^使用场景/).fill('验收用途一\n验收用途二');
    await page.getByLabel(/^产品特点/).fill('验收特点'); await page.getByLabel(/^注意事项/).fill('验收说明');
    await page.getByLabel('适用枪钉（可留空）', { exact: true }).fill('隔离规格 15–45 mm');
    await page.getByLabel('枪钉类型（可不选）', { exact: true }).selectOption('staple');
    await page.getByRole('group', { name: '场景分类（可多选）', exact: true }).getByLabel('家装木工', { exact: true }).check();
    await page.getByLabel('加长枪嘴', { exact: true }).check(); await page.getByLabel('吹尘功能', { exact: true }).check();
    product = await h.save(page, id); const p = product.product;
    assert.deepEqual(p.applications, ['验收用途一', '验收用途二']); assert.deepEqual(p.features, ['验收特点']); assert.deepEqual(p.notes, ['验收说明']); assert.equal(p.category_id, 'staple'); assert.equal(p.series, '隔离验收系列');
    assert.equal(p.nail_type, 'staple'); assert.equal(p.fastener_text, '隔离规格 15–45 mm'); assert(p.long_nose && p.blower); assert.deepEqual(p.scene_ids, ['home-woodworking']);
    for (const field of ['kind', 'legacy_series_ids', 'sort_order', 'guide_profile', 'purchase_options']) assert.deepEqual(p[field], hidden[field], `hidden ${field} changed`);
  });
  await h.check('20 image gallery uploads, replaces, reorders, selects cover and persists captions', async () => {
    const generated = await page.evaluate(() => Array.from({ length: 20 }, (_, index) => {
      const canvas = document.createElement('canvas'); canvas.width = 40; canvas.height = 30;
      const context = canvas.getContext('2d'); context.fillStyle = `hsl(${index * 17} 60% 40%)`; context.fillRect(0, 0, 40, 30); context.fillStyle = '#fff'; context.fillText(String(index), 5, 20);
      return canvas.toDataURL('image/png').split(',')[1];
    }));
    await page.locator('#upload-images').setInputFiles(generated.map((data, index) => ({ name: `qa-${index}.png`, mimeType: 'image/png', buffer: Buffer.from(data, 'base64') })));
    await page.getByText('图片已添加，请保存产品以更新官网。', { exact: true }).waitFor({ timeout: 60000 });
    const cards = page.locator('.image-card'); assert.equal(await cards.count(), 20); assert(await page.locator('#upload-images').isDisabled());
    await cards.nth(0).getByLabel('图片标题（可选）', { exact: true }).fill('正面图');
    await cards.nth(0).getByText('添加图片说明（可选）', { exact: true }).click();
    await cards.nth(0).getByLabel('图片说明', { exact: true }).fill('正面说明');
    await cards.nth(1).getByRole('button', { name: '将图片 2 前移', exact: true }).click();
    await cards.nth(0).getByRole('button', { name: '设为封面', exact: true }).click();
    const [chooser] = await Promise.all([page.waitForEvent('filechooser'), cards.nth(0).getByRole('button', { name: '替换', exact: true }).click()]);
    const uploaded = h.response(page, 'POST', '/api/admin/media', 201); await chooser.setFiles(resolve('tests/fixtures/media/valid.jpg')); const replacement = await (await uploaded).json();
    await page.waitForFunction(() => !document.querySelector('#save-product').disabled);
    product = await h.save(page, id); assert.equal(product.product.images.length, 20); assert.equal(product.product.cover_media_id, replacement.media_id); assert.equal(product.product.images[0].media_id, replacement.media_id);
    assert.equal(product.product.images[1].label, '正面图'); assert.equal(product.product.images[1].description, '正面说明');
    await page.reload(); await cards.nth(19).waitFor(); assert.equal(await cards.count(), 20);
    await cards.nth(0).getByRole('button', { name: '移除', exact: true }).click();
    product = await h.save(page, id); assert.equal(product.product.images.length, 19); assert.equal(product.product.cover_media_id, product.product.images[0].media_id);
  });
  await h.check('invalid and oversized uploads leave saved gallery intact', async () => {
    const before = product.product.images;
    const denied = h.response(page, 'POST', '/api/admin/media', 422);
    await page.locator('#upload-images').setInputFiles({ name: 'broken.png', mimeType: 'image/png', buffer: Buffer.from('not an image') }); await denied;
    await page.waitForFunction(() => !document.querySelector('#save-product').disabled);
    let uploads = 0; const countUpload = request => { if (request.method() === 'POST' && new URL(request.url()).pathname === '/api/admin/media') uploads++; }; page.on('request', countUpload);
    await page.locator('#upload-images').setInputFiles({ name: 'large.png', mimeType: 'image/png', buffer: Buffer.alloc(10 * 1024 * 1024 + 1) });
    await page.locator('#upload-status').filter({ hasText: '不能超过 10 MB' }).waitFor(); page.off('request', countUpload); assert.equal(uploads, 0);
    assert.equal(await page.locator('.image-card').count(), 19); assert.deepEqual((await h.readProduct(page, id)).product.images, before);
    const guest = await h.page(390); const privateImage = await guest.context().request.get(new URL(`/api/admin/media/${before[0].media_id}/file`, h.base).href); assert.equal(privateImage.status(), 401); await guest.close();
  });
  await h.check('independent editors retain local input on conflict and explicitly merge or reload', async () => {
    const other = await h.page(390); await h.login(other, `/admin/products/edit/?id=${id}`);
    await other.getByLabel('产品简介', { exact: true }).fill('第二会话本地输入');
    await page.getByLabel('产品简介', { exact: true }).fill('第一会话已保存内容'); await h.save(page, id);
    await h.save(other, id, 412); await other.locator('#conflict').waitFor({ state: 'visible' });
    assert.equal(await other.getByLabel('产品简介', { exact: true }).inputValue(), '第二会话本地输入');
    assert.match(await other.locator('#conflict').textContent(), /第一会话已保存内容/); assert(await other.locator('#save-product').isDisabled());
    await other.getByLabel('保留哪一份内容', { exact: true }).selectOption('local');
    await other.getByRole('button', { name: '合并修改，继续编辑', exact: true }).click();
    assert.equal((await h.readProduct(other, id)).product.summary, '第一会话已保存内容', 'merge must not write without save');
    product = await h.save(other, id); assert.equal(product.product.summary, '第二会话本地输入');
    await page.getByLabel('产品简介', { exact: true }).fill('过时的本地输入'); await h.save(page, id, 412);
    await page.getByRole('button', { name: '重新读取最新产品', exact: true }).click();
    assert.equal(await page.getByLabel('产品简介', { exact: true }).inputValue(), '第二会话本地输入'); await other.close();
  });
  await h.check('search/category filters show the saved cover and mobile keyboard editing fits', async () => {
    const list = await h.page(1440, page.context()); await list.goto(new URL(`/admin/products/?q=${encodeURIComponent(`QA-${h.suffix}`)}&category=staple&visibility=listed`, h.base).href);
    await list.getByRole('heading', { name: `QA-${h.suffix}`, exact: true }).waitFor(); assert.equal(await list.locator('.product-row').count(), 1);
    await list.locator('.product-row img').evaluate(async image => { await image.decode(); }); assert(await list.locator('.product-row img').evaluate(image => image.naturalWidth > 0));
    await h.screenshot(list, 'product-list-desktop.png'); await list.close();
    for (const width of [390, 320]) {
      await page.setViewportSize({ width, height: 900 }); await page.getByLabel('系列（可留空）', { exact: true }).focus();
      await page.keyboard.press('ControlOrMeta+A'); await page.keyboard.type(`Keyboard-${width}`);
      const saved = h.response(page, 'PUT', `/api/admin/products/${id}`); await page.getByRole('button', { name: '保存并更新官网', exact: true }).focus(); await page.keyboard.press('Enter');
      assert.equal((await (await saved).json()).product.series, `Keyboard-${width}`); await page.waitForFunction(() => !document.querySelector('#save-product').disabled);
      assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)); await h.screenshot(page, `editor-${width}.png`);
    }
  });
  await h.check('unsaved navigation and expired-session retry preserve the original save', async () => {
    await page.getByLabel('产品简介', { exact: true }).fill('重新登录后保留此内容');
    page.removeAllListeners('dialog'); const dialog = page.waitForEvent('dialog'); const navigate = page.getByRole('link', { name: '返回产品列表', exact: false }).click(); await (await dialog).dismiss(); await navigate;
    assert.equal(new URL(page.url()).pathname, '/admin/products/edit/'); page.on('dialog', dialog => dialog.accept());
    const sibling = await h.page(1440, page.context()); await sibling.goto(new URL('/admin/products/', h.base).href); await sibling.locator('#session-state').filter({ hasText: 'admin' }).waitFor();
    await sibling.getByRole('button', { name: '退出', exact: true }).click(); await sibling.waitForURL('**/admin/login/**');
    // Polling may already have cleared the expired session, so either the session refresh or the write can receive the real 401.
    const failed = page.waitForResponse(response => response.status() === 401 && ['/api/admin/session', `/api/admin/products/${id}`].includes(new URL(response.url()).pathname));
    await page.getByRole('button', { name: '保存并更新官网', exact: true }).click(); const first = await failed;
    assert.equal(await page.getByLabel('产品简介', { exact: true }).inputValue(), '重新登录后保留此内容');
    await h.login(sibling); const retry = h.response(page, 'PUT', `/api/admin/products/${id}`); await page.getByRole('button', { name: '重试保存', exact: true }).click(); const retried = await retry;
    if (first.request().method() === 'PUT') assert.equal(retried.request().headers()['idempotency-key'], first.request().headers()['idempotency-key']);
    assert(retried.request().headers()['idempotency-key']); assert.equal((await retried.json()).product.summary, '重新登录后保留此内容');
    await sibling.close(); await page.waitForFunction(() => !document.querySelector('#save-product').disabled);
  });
  await h.check('business deletion removes the record and remains observable until updated', async () => {
    const version = await h.remove(page, id); await h.waitForUpdate(page, id, version); await page.locator('#site-update').filter({ hasText: '产品已删除，官网已更新' }).waitFor();
    assert.equal((await h.request(page, `/api/admin/products/${id}`)).status(), 404);
  });
} catch (error) { failure = error; }
finally { await h.finish(failure, cleanupPage ?? page); }
