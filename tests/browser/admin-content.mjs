import assert from 'node:assert/strict';
import { mkdir, readFile, stat, writeFile } from 'node:fs/promises';
import { resolve, join } from 'node:path';
import { pathToFileURL } from 'node:url';

const args = process.argv.slice(2);
function option(name) { const index = args.indexOf(name); return index === -1 ? null : args[index + 1]; }
const base = new URL(option('--base') ?? process.env.QA_URL);
const out = resolve(option('--out') ?? process.env.QA_OUTPUT);
assert.equal(base.hostname, '127.0.0.1', 'V08 requires its isolated loopback origin');
assert(process.env.SMOKE_ADMIN_PASSWORD_FILE, 'manager 0600 password file is required');
assert.equal((await stat(process.env.SMOKE_ADMIN_PASSWORD_FILE)).mode & 0o777, 0o600);
const password = (await readFile(process.env.SMOKE_ADMIN_PASSWORD_FILE, 'utf8')).trimEnd();
const { chromium } = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE ?? resolve('node_modules/playwright-core/index.mjs')).href);
await mkdir(out, { recursive: true, mode: 0o700 });
const report = { success: false, base: base.origin, checks: [], requests: [], screenshots: [], errors: [], states: {} };
const browserServer = await chromium.launchServer({ executablePath: process.env.BROWSER_PATH, headless: true, args: ['--no-sandbox'] });
const browser = await chromium.connect(browserServer.wsEndpoint());
report.browser_pid = browserServer.process().pid;
const contexts = [];
const suffix = `${Date.now()}-${Math.random().toString(16).slice(2, 9)}`;
const slug = `qa-admin-${suffix}`;
async function context(width = 1440) {
  const result = await browser.newContext({ viewport: { width, height: 1000 } }); contexts.push(result); return result;
}
function track(page) {
  page.on('pageerror', error => report.errors.push(error.message));
  page.on('requestfinished', async request => {
    const url = new URL(request.url()); if (!url.pathname.startsWith('/api/admin/') || request.method() === 'GET') return;
    const response = await request.response(); const headers = await request.allHeaders();
    report.requests.push({ method: request.method(), path: url.pathname, origin: headers.origin, csrf_present: Boolean(headers['x-csrf-token']), if_match: headers['if-match'] ?? null, status: response.status(), etag: response.headers().etag ?? null });
  });
  page.on('dialog', dialog => dialog.accept());
}
async function login(page) {
  await page.goto(new URL('/admin/', base).href);
  await page.waitForURL('**/admin/login/**');
  await page.getByLabel('用户名', { exact: true }).fill('admin');
  await page.getByLabel('密码', { exact: true }).fill(password);
  await Promise.all([page.waitForURL('**/admin/'), page.getByRole('button', { name: '登录', exact: true }).click()]);
  await page.locator('#session-state').filter({ hasText: 'admin' }).waitFor();
}
function response(page, method, path, status) {
  return page.waitForResponse(value => new URL(value.url()).pathname === path && value.request().method() === method && value.status() === status);
}
async function save(page, id, status = 200) {
  const [result] = await Promise.all([
    response(page, id ? 'PUT' : 'POST', id ? `/api/admin/products/${id}` : '/api/admin/products', status),
    page.getByRole('button', { name: '保存草稿', exact: true }).click(),
  ]);
  return result.json();
}
async function screenshot(page, name) {
  await page.screenshot({ path: join(out, name), fullPage: true }); report.screenshots.push(name);
}
async function check(name, run) {
  await run(); report.checks.push(name); console.log(`PASS ${name}`);
  await writeFile(join(out, 'admin-content.json'), JSON.stringify(report, null, 2), { mode: 0o600 });
}
async function upload(page, file, expected) {
  await page.getByLabel('上传图片', { exact: true }).setInputFiles(file);
  const [result] = await Promise.all([
    response(page, 'POST', '/api/admin/media', expected),
    page.getByRole('button', { name: '上传并选择', exact: true }).click(),
  ]);
  return result.json();
}
let id;
try {
  const admin = await context(); const page = await admin.newPage(); track(page); await login(page);
  await check('P01 incomplete draft created entirely through /admin', async () => {
    await page.getByRole('link', { name: '产品', exact: true }).click();
    await page.getByRole('link', { name: '新建草稿', exact: true }).click();
    await page.getByLabel('固定路径 slug', { exact: true }).fill(slug);
    const created = await save(page, null, 201); id = created.product.id;
    assert.equal(created.product.visibility, 'unlisted'); assert.equal(created.product.name, ''); assert.equal(created.product.images.length, 0);
    report.states.initial = created; await page.getByText('草稿已保存；尚未公开。', { exact: true }).waitFor();
  });
  await check('all display fields and series membership editable', async () => {
    await page.getByLabel('条目类型', { exact: true }).selectOption('tool');
    await page.getByLabel('产品名称', { exact: true }).fill('隔离验收工具 <纯文本>');
    await page.getByLabel('型号', { exact: true }).fill(`QA-${suffix}`);
    await page.getByLabel('分类', { exact: true }).selectOption('staple');
    await page.getByLabel('展示系列名称', { exact: true }).fill('隔离验收系列');
    await page.getByLabel('排序值', { exact: true }).fill('73');
    await page.getByLabel('目标上下架状态', { exact: true }).selectOption('listed');
    await page.getByLabel('产品简介', { exact: true }).fill('仅用于隔离验收：客户资料未验，不构成产品事实。');
    await page.getByLabel('用途（每行一项）', { exact: true }).fill('验收用途一\n验收用途二');
    await page.getByLabel('特点（每行一项）', { exact: true }).fill('验收特点');
    await page.getByLabel('说明（每行一项）', { exact: true }).fill('验收说明');
    await page.getByRole('group', { name: '展示场景', exact: true }).getByLabel('家装木工').check();
    await page.getByRole('group', { name: '旧系列归属', exact: true }).getByLabel('F30 系列', { exact: true }).check();
    await page.getByLabel('展示枪钉类型', { exact: true }).selectOption('staple');
    await page.getByLabel('展示紧固件规格', { exact: true }).fill('隔离规格 15–45 mm');
    await page.getByLabel('展示加长枪嘴标签', { exact: true }).check();
    await page.getByLabel('展示吹尘标签', { exact: true }).check();
  });
  let png; let jpg;
  await check('P05 upload, captions, cover and ordered gallery', async () => {
    await page.locator('#media-picker summary').click();
    png = await upload(page, resolve('tests/fixtures/media/valid.png'), 201);
    jpg = await upload(page, resolve('tests/fixtures/media/valid.jpg'), 201);
    let rows = page.locator('.image-row'); assert.equal(await rows.count(), 2);
    await rows.nth(0).getByLabel('图片标题', { exact: true }).fill('正面图');
    await rows.nth(0).getByLabel('图片说明', { exact: true }).fill('正面说明');
    await rows.nth(1).getByLabel('图片标题', { exact: true }).fill('侧面图');
    await rows.nth(1).getByLabel('图片说明', { exact: true }).fill('侧面说明');
    await rows.nth(1).getByRole('button', { name: '上移', exact: true }).click();
    await rows.nth(1).getByLabel('用作封面', { exact: true }).check();
    assert.equal(await rows.nth(0).getAttribute('data-image-id'), jpg.media_id);
    assert.equal(await rows.nth(1).getAttribute('data-image-id'), png.media_id);
    await page.locator('#media-picker summary').click();
  });
  await check('all guide facts, unknowns and provenance editable', async () => {
    await page.getByRole('group', { name: '选型适用场景', exact: true }).getByLabel('家装木工').check();
    for (const [label, value] of [['适用材料', '验收木材'], ['适用基材', '验收木方'], ['不适用材料', '验收金属'], ['不适用基材', '验收混凝土'], ['关键限制', '仅为隔离夹具'], ['咨询提示', '请核实客户资料']]) await page.getByLabel(`选型${label}（每行一项）`, { exact: true }).fill(value);
    await page.getByLabel('选型动力要求', { exact: true }).selectOption('pneumatic');
    await page.getByLabel('选型紧固件类型', { exact: true }).selectOption('staple');
    for (const label of ['选型适用承重', '选型加长枪嘴', '选型吹尘功能']) await page.getByLabel(label, { exact: true }).selectOption('false');
    await page.getByLabel('已知长度范围', { exact: true }).check();
    await page.getByLabel('最小长度 mm', { exact: true }).fill('15');
    await page.getByLabel('最大长度 mm', { exact: true }).fill('45');
    const required = page.getByRole('group', { name: '用途必需条件（需单独审核）', exact: true });
    await required.getByLabel('材料', { exact: true }).check(); await required.getByLabel('气源', { exact: true }).check();
    await page.getByRole('button', { name: '添加来源', exact: true }).click();
    const source = page.locator('.source-row');
    await source.getByLabel('来源 ID', { exact: true }).fill('src_qa_text');
    await source.getByLabel('来源类型', { exact: true }).selectOption('product_text');
    await source.getByLabel('来源引用', { exact: true }).fill('/summary');
    await source.getByLabel('公开来源标签', { exact: true }).fill('隔离测试资料');
    await source.getByLabel('公开来源 HTTPS 地址（可空）', { exact: true }).fill('https://docs.example.test/qa');
  });
  await check('G09 multiple channels add edit toggle reorder and remove', async () => {
    assert.equal(await page.locator('#channel-empty').isVisible(), true);
    for (let index = 0; index < 3; index++) {
      await page.getByRole('button', { name: '添加购买渠道', exact: true }).click();
      const row = page.locator('.channel-row').nth(index);
      await row.getByLabel('平台', { exact: true }).fill(`隔离平台${index + 1}`);
      await row.getByLabel('店铺名称', { exact: true }).fill(`隔离店铺${index + 1}`);
      await row.getByLabel('商品详情 HTTPS 地址', { exact: true }).fill(`https://item.example.test/products/qa-${suffix}-${index + 1}`);
      if (index === 1) { await row.getByLabel('型号对应范围', { exact: true }).selectOption('listing_with_variants'); await row.getByLabel('SKU 选择提示', { exact: true }).fill('到达商城后选择验收规格'); }
      await row.getByLabel('启用此渠道', { exact: true }).check();
    }
    await page.locator('.channel-row').nth(2).getByRole('button', { name: '删除购买渠道', exact: true }).click();
    await page.locator('.channel-row').nth(1).getByRole('button', { name: '上移', exact: true }).click();
    await page.locator('.channel-row').nth(1).getByLabel('启用此渠道', { exact: true }).uncheck();
    const result = await save(page, id); report.states.full = result;
    const p = result.product;
    assert.equal(p.name, '隔离验收工具 <纯文本>'); assert.equal(p.kind, 'tool'); assert.equal(p.category_id, 'staple'); assert.equal(p.series, '隔离验收系列'); assert.deepEqual(p.legacy_series_ids, ['s_f30']);
    assert.deepEqual(p.applications, ['验收用途一', '验收用途二']); assert.deepEqual(p.features, ['验收特点']); assert.deepEqual(p.notes, ['验收说明']); assert.deepEqual(p.scene_ids, ['home-woodworking']); assert.equal(p.nail_type, 'staple'); assert.equal(p.fastener_text, '隔离规格 15–45 mm'); assert.equal(p.long_nose, true); assert.equal(p.blower, true); assert.equal(p.visibility, 'listed'); assert.equal(p.sort_order, 73);
    assert.deepEqual(p.images.map(image => [image.media_id, image.label, image.description]), [[jpg.media_id, '侧面图', '侧面说明'], [png.media_id, '正面图', '正面说明']]); assert.equal(p.cover_media_id, png.media_id);
    assert.deepEqual(p.guide_profile.fastener_length_mm, { min: 15, max: 45 }); assert.equal(p.guide_profile.load_bearing, false); assert.equal(p.guide_profile.power_source, 'pneumatic'); assert.deepEqual(p.guide_profile.required_requirements, ['material', 'air_supply']);
    assert.equal(p.purchase_options[0].platform, '隔离平台2'); assert.equal(p.purchase_options[0].match_scope, 'listing_with_variants'); assert.equal(p.purchase_options[0].enabled, true); assert.equal(p.purchase_options[1].enabled, false); assert(p.purchase_options.every(option => option.id));
    await page.getByText('草稿已保存；尚未公开。', { exact: true }).waitFor();
    for (const selector of ['.image-row', '.source-row', '.channel-row']) {
      assert.equal(await page.locator(selector).first().getByRole('button', { name: '上移', exact: true }).isDisabled(), true);
      assert.equal(await page.locator(selector).last().getByRole('button', { name: '下移', exact: true }).isDisabled(), true);
    }
  });
  await check('current-version review SHA matches all 14 fact types', async () => {
    const fields = Object.keys({ scene_ids: 1, materials: 1, substrates: 1, excluded_materials: 1, excluded_substrates: 1, power_source: 1, nail_type: 1, fastener_length_mm: 1, load_bearing: 1, long_nose: 1, blower: 1, limitations: 1, consultation_notes: 1, required_requirements: 1 });
    for (const field of fields) {
      const row = page.locator(`.review-row[data-fact="${field}"]`);
      await row.getByLabel('审核依据：隔离测试资料', { exact: true }).check();
      const wait = response(page, 'POST', `/api/admin/products/${id}/guide-review`, 200);
      await row.getByRole('button', { name: '确认此事实已核实', exact: true }).click();
      const result = await (await wait).json(); const review = result.product.guide_profile.reviews.find(item => item.field === field);
      assert.equal(review.reviewed_by, 'admin'); assert(review.reviewed_at.endsWith('Z'));
      report.states.reviewed = result;
    }
    assert.equal(report.states.reviewed.product.guide_profile.reviews.length, fields.length);
    await screenshot(page, 'desktop-reviewed.png');
  });
  await check('source edits invalidate reviews and unknown facts cannot be reviewed', async () => {
    await page.getByLabel('产品简介', { exact: true }).fill('隔离来源已更新：旧审核必须失效。');
    await page.getByLabel('选型适用承重', { exact: true }).selectOption('');
    const result = await save(page, id); assert.deepEqual(result.product.guide_profile.reviews, []);
    assert.equal(result.product.guide_profile.load_bearing, null);
    assert.equal(await page.locator('.review-row[data-fact="load_bearing"]').getByRole('button').count(), 0);
    assert.match(await page.locator('.review-row[data-fact="load_bearing"]').textContent(), /未知值不能审核/);
  });
  await check('P06 invalid image failures preserve existing gallery and media', async () => {
    await page.locator('#media-picker summary').click();
    const before = await page.locator('.image-row').count();
    for (const [file, code] of [
      [{ name: 'forged.jpg', mimeType: 'image/jpeg', buffer: await readFile('tests/fixtures/media/valid.png') }, 422],
      [{ name: 'broken.png', mimeType: 'image/png', buffer: Buffer.from('not an image') }, 422],
      [resolve('tests/fixtures/media/animated.png'), 422],
      [{ name: 'large.png', mimeType: 'image/png', buffer: Buffer.alloc(10 * 1024 * 1024 + 1) }, 413],
      [resolve('tests/fixtures/media/too-wide.png'), 413],
    ]) { await upload(page, file, code); assert.equal(await page.locator('.image-row').count(), before); }
    const referenced = page.locator(`.media-card[data-media-id="${png.media_id}"]`);
    const denied = response(page, 'DELETE', `/api/admin/media/${png.media_id}`, 409);
    await referenced.getByRole('button', { name: '删除素材', exact: true }).click(); await denied;
    assert.equal(await referenced.count(), 1);
    await page.locator('#media-picker summary').click();
  });
  await check('P03 two independent sessions preserve local edits on 412', async () => {
    const second = await context(); const other = await second.newPage(); track(other); await login(other);
    await other.goto(new URL(`/admin/products/edit/?id=${id}`, base).href); await other.getByLabel('产品简介', { exact: true }).waitFor();
    await other.getByLabel('产品简介', { exact: true }).fill('第二会话保留的本地编辑');
    await page.getByLabel('产品简介', { exact: true }).fill('第一会话已保存的服务器编辑'); await save(page, id);
    await save(other, id, 412);
    await other.locator('#conflict').waitFor({ state: 'visible' });
    assert.equal(await other.getByLabel('产品简介', { exact: true }).inputValue(), '第二会话保留的本地编辑');
    assert.match(await other.locator('#conflict').textContent(), /第一会话已保存的服务器编辑/);
    assert.match(await other.locator('#conflict').textContent(), /第二会话保留的本地编辑/);
    const count = report.requests.filter(item => item.path === `/api/admin/products/${id}` && item.method === 'PUT').length;
    await other.getByRole('button', { name: '保留本地，继续比较', exact: true }).click();
    assert.equal(report.requests.filter(item => item.path === `/api/admin/products/${id}` && item.method === 'PUT').length, count);
    await screenshot(other, 'conflict-local-preserved.png');
    await other.getByRole('button', { name: '已比较，将本地编辑转到此服务器版本', exact: true }).click();
    assert.equal(await other.getByLabel('产品简介', { exact: true }).inputValue(), '第二会话保留的本地编辑');
    const merged = await save(other, id); assert.equal(merged.product.summary, '第二会话保留的本地编辑');
    await page.getByLabel('产品简介', { exact: true }).fill('过时的本地内容'); await save(page, id, 412);
    await page.getByRole('button', { name: '采用服务器版本并丢弃本地', exact: true }).click();
    assert.equal(await page.getByLabel('产品简介', { exact: true }).inputValue(), '第二会话保留的本地编辑');
  });
  await check('draft data and images denied to anonymous visitor', async () => {
    const anonymous = await context(); const guest = await anonymous.newPage();
    const product = await guest.goto(new URL(`/api/admin/products/${id}`, base).href); assert.equal(product.status(), 401);
    const image = await guest.goto(new URL(`/api/admin/media/${png.media_id}/file`, base).href); assert.equal(image.status(), 401);
    const directory = await guest.goto(new URL('/media/private.png', base).href); assert.equal(directory.status(), 404);
    // T08's baseline precedes publication: the actual Go site refuses to serve
    // any draft. The development Astro server is only an admin/API proxy.
    const backend = new URL(process.env.ADMIN_API_TARGET);
    const publicPage = await guest.goto(new URL(`/products/${slug}/`, backend).href); assert.equal(publicPage.status(), 503);
    assert(!(await guest.textContent('body')).includes('隔离验收工具'));
    report.states.anonymous = { product: product.status(), image: image.status(), private_directory: directory.status(), prepublication_page: publicPage.status() };
  });
  await check('O01 390px and 320px keyboard editing no overflow', async () => {
    for (const width of [390, 320]) {
      await page.setViewportSize({ width, height: 900 });
      await page.getByLabel('展示系列名称', { exact: true }).focus();
      await page.keyboard.press('ControlOrMeta+A'); await page.keyboard.type(`键盘编辑-${width}`);
      await page.keyboard.press('Tab'); assert.equal(await page.getByLabel('排序值', { exact: true }).evaluate(el => el === document.activeElement), true);
      await page.getByLabel('展示吹尘标签', { exact: true }).focus(); await page.keyboard.press('Space');
      const wait = response(page, 'PUT', `/api/admin/products/${id}`, 200);
      await page.getByRole('button', { name: '保存草稿', exact: true }).focus(); await page.keyboard.press('Enter');
      const result = await (await wait).json(); assert.equal(result.product.series, `键盘编辑-${width}`);
      const size = await page.evaluate(() => ({ width: innerWidth, scroll: document.documentElement.scrollWidth })); assert.equal(size.width, width); assert(size.scroll <= width, `overflow at ${width}`);
      await screenshot(page, `mobile-${width}.png`);
    }
  });
  await check('search filters, dedicated review, media after pagination and existing selection', async () => {
    const extra = await admin.newPage(); track(extra);
    await extra.goto(new URL(`/admin/products/?q=${encodeURIComponent(`QA-${suffix}`)}`, base).href);
    await extra.locator('.product-card').waitFor();
    await extra.getByLabel('分类', { exact: true }).selectOption('staple');
    await extra.getByLabel('目标上下架', { exact: true }).selectOption('listed');
    await Promise.all([extra.waitForResponse(r => r.url().includes('/api/admin/products?') && r.url().includes('category=staple') && r.status() === 200), extra.getByRole('button', { name: '搜索', exact: true }).click()]);
    assert.equal(await extra.locator('.product-card').count(), 1);
    assert.match(await extra.locator('.product-card').textContent(), /有未发布修改/);
    await extra.goto(new URL(`/admin/guide-reviews/?q=${encodeURIComponent(`QA-${suffix}`)}`, base).href);
    await extra.locator('.product-card a').click(); await extra.locator('.review-row').first().waitFor();
    assert.match(extra.url(), /guide-reviews\/review/); assert.match(await extra.locator('#product-meta').textContent(), new RegExp(id));
    const [firstResponse] = await Promise.all([
      extra.waitForResponse(r => new URL(r.url()).pathname === '/api/admin/media' && r.status() === 200),
      extra.goto(new URL('/admin/media/', base).href),
    ]);
    const firstList = await firstResponse.json(); const initialAfter = firstList.after;
    assert.equal(new URL(firstResponse.url()).searchParams.get('after'), null);
    await extra.locator('.media-card').first().waitFor();
    if (firstList.next_cursor) {
      const [nextResponse] = await Promise.all([
        extra.waitForResponse(r => new URL(r.url()).pathname === '/api/admin/media' && new URL(r.url()).searchParams.has('cursor') && r.status() === 200),
        extra.getByRole('button', { name: '加载更多素材', exact: true }).click(),
      ]);
      assert.equal(new URL(nextResponse.url()).searchParams.get('after'), null);
    }
    const [emptyResponse] = await Promise.all([
      extra.waitForResponse(r => new URL(r.url()).pathname === '/api/admin/media' && r.status() === 200),
      extra.goto(new URL(`/admin/media/?after=${encodeURIComponent(initialAfter)}`, base).href),
    ]);
    assert.deepEqual((await emptyResponse.json()).items, []);
    const imageData = await extra.evaluate(text => {
      const canvas = document.createElement('canvas'); canvas.width = 220; canvas.height = 40;
      const ctx = canvas.getContext('2d'); ctx.fillStyle = '#315e85'; ctx.fillRect(0, 0, 220, 40); ctx.fillStyle = '#fff'; ctx.fillText(text, 4, 24);
      return canvas.toDataURL('image/png').split(',')[1];
    }, suffix);
    const fresh = await upload(extra, { name: 'qa-private.png', mimeType: 'image/png', buffer: Buffer.from(imageData, 'base64') }, 201);
    const [refreshed] = await Promise.all([
      extra.waitForResponse(r => new URL(r.url()).pathname === '/api/admin/media' && r.status() === 200),
      extra.getByRole('button', { name: '刷新素材', exact: true }).click(),
    ]);
    assert.equal(new URL(refreshed.url()).searchParams.get('after'), initialAfter);
    assert.deepEqual((await refreshed.json()).items.map(item => item.media_id), [fresh.media_id]);
    await extra.goto(new URL(`/admin/media/?product_id=${encodeURIComponent(id)}&after=${encodeURIComponent(initialAfter)}`, base).href);
    const chosenID = fresh.media_id;
    await extra.locator(`.media-card[data-media-id="${chosenID}"]`).getByRole('button', { name: '选择此图', exact: true }).click();
    await extra.waitForURL('**/admin/products/edit/**');
    await extra.locator(`.image-row[data-image-id="${chosenID}"]`).waitFor();
    const associated = await save(extra, id); assert(associated.product.images.some(image => image.media_id === chosenID));
    report.states.existing_media = { media_id: chosenID, fixed_after: initialAfter };
    await screenshot(extra, 'existing-media-associated.png');
    await extra.close(); await page.bringToFront();
  });
  await check('delete never-published incomplete draft through browser', async () => {
    const extra = await admin.newPage(); track(extra); await extra.goto(new URL('/admin/products/new/', base).href);
    await extra.getByLabel('固定路径 slug', { exact: true }).fill(`${slug}-delete`);
    const created = await save(extra, null, 201);
    await Promise.all([
      response(extra, 'DELETE', `/api/admin/products/${created.product.id}`, 200),
      extra.getByRole('button', { name: '删除未公开草稿', exact: true }).click(),
    ]);
    await extra.waitForURL('**/admin/products/');
    await extra.getByLabel('搜索名称或型号', { exact: true }).fill(`${slug}-delete`);
    await Promise.all([
      extra.waitForResponse(r => r.url().includes('/api/admin/products?') && r.url().includes(encodeURIComponent(`${slug}-delete`)) && r.status() === 200),
      extra.getByRole('button', { name: '搜索', exact: true }).click(),
    ]);
    await extra.getByText('没有符合条件的产品。', { exact: true }).waitFor();
    await extra.close(); await page.bringToFront();
  });
  await check('unsaved navigation guard and expired-session local retention', async () => {
    await page.getByLabel('产品简介', { exact: true }).fill('会话失效后仍保留的本地内容');
    page.removeAllListeners('dialog'); let guarded = false;
    page.once('dialog', async dialog => { guarded = true; await dialog.dismiss(); });
    await page.getByRole('link', { name: '产品', exact: true }).click(); assert(guarded); assert.match(page.url(), /products\/edit/);
    page.on('dialog', dialog => dialog.accept());
    const sibling = await admin.newPage(); track(sibling); await sibling.goto(new URL('/admin/', base).href); await sibling.locator('#session-state').filter({ hasText: 'admin' }).waitFor();
    await sibling.getByRole('button', { name: '退出', exact: true }).click(); await sibling.waitForURL('**/admin/login/');
    await page.bringToFront();
    await save(page, id, 401);
    assert.equal(await page.getByLabel('产品简介', { exact: true }).inputValue(), '会话失效后仍保留的本地内容');
    assert.match(await page.locator('#session-state').textContent(), /会话已失效/);
    await screenshot(page, 'expired-local-preserved.png');
  });
  await check('exact page Origin, CSRF and strong If-Match on all writes', async () => {
    for (const request of report.requests) {
      assert.equal(request.origin, base.origin);
      if (!request.path.endsWith('/login')) assert.equal(request.csrf_present, true);
      if (request.method === 'PUT' || request.path.endsWith('/guide-review')) assert.match(request.if_match, /^"[1-9][0-9]*"$/);
    }
    assert.deepEqual(report.errors, []);
  });
  report.success = true; report.product_id = id;
} catch (error) {
  report.failure = { name: error.name, message: error.message, stack: error.stack };
  report.failure_pages = [];
  for (const [contextIndex, value] of contexts.entries()) for (const [pageIndex, failedPage] of value.pages().entries()) {
    try {
      report.failure_pages.push({ url: failedPage.url(), text: (await failedPage.locator('body').innerText()).slice(-12000), controls: await failedPage.locator('input,button,textarea,select').evaluateAll(elements => elements.filter(el => el.disabled || !el.checkValidity()).map(el => ({ tag: el.tagName, label: el.closest('label')?.textContent, disabled: el.disabled, message: el.validationMessage, value: el.type === 'password' ? undefined : el.value }))) });
      await failedPage.screenshot({ path: join(out, `failure-${contextIndex}-${pageIndex}.png`), fullPage: true });
    } catch { /* A closed page has no remaining surface to capture. */ }
  }
  console.error(error.stack); process.exitCode = 1;
} finally {
  await Promise.all(contexts.map(value => value.close())); await browser.close(); await browserServer.close();
  await writeFile(join(out, 'admin-content.json'), JSON.stringify(report, null, 2), { mode: 0o600 });
}
