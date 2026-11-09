import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import { createRequire } from 'node:module';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { build } from 'esbuild';

// Render the real components without needing a server or a browser installation.
const root = fileURLToPath(new URL('../', import.meta.url));
const temporary = await mkdtemp(join(tmpdir(), 'yuanfa-guide-presentation-'));
let ui;
try {
  const bundle = await build({
    stdin: {
      resolveDir: root,
      contents: [
        'export { GuideResultView, Requirements } from "./src/components/guide/product.tsx";',
        'export { MessageList } from "./src/components/guide/message-list.tsx";',
        'export { createElement } from "react";',
        'export { renderToStaticMarkup } from "react-dom/server";'
      ].join('\n')
    },
    bundle: true, platform: 'node', format: 'cjs', jsx: 'automatic', write: false
  });
  const entry = join(temporary, 'presentation.cjs');
  await writeFile(entry, bundle.outputFiles[0].text);
  ui = createRequire(import.meta.url)(entry);
} finally {
  await rm(temporary, { recursive: true, force: true });
}
const { createElement: h, renderToStaticMarkup: render, GuideResultView, MessageList, Requirements } = ui;
const requirements = overrides => ({ scene_ids: [], material: null, substrate: null, nail_type: null, fastener_length_mm: null, air_supply: null, load_bearing: null, long_nose: null, blower: null, ...overrides });
const product = overrides => ({
  product_id: 'fixture-f30', model: 'F30', name: '测试直钉枪', detail_url: '/products/fixture-f30/',
  image: { url: '/media/fixture.webp', label: 'F30 产品图', description: '', width: 64, height: 64 },
  match_level: 'needs_confirmation',
  matched_facts: [{ field: 'power_source', text: '已审核动力：pneumatic', sources: [{ public_label: '产品目录', public_url: '/catalog/' }] }],
  missing_fields: ['material', 'guide_profile.materials', 'guide_profile.power_source', 'guide_profile.required_requirements'],
  confirmation_notes: ['简介：机身小巧，便于握持。', '用途说明：木工作业'], ...overrides
});
const result = overrides => ({
  release_id: 'fixture-release', state: 'recommendations', confirmed_requirements: requirements(),
  conflicts: [], question: null, recommendations: [product()], selection: null,
  missing_fields: ['material', 'guide_profile.required_requirements'], contact_href: 'tel:123456', ...overrides
});
const props = value => ({ result: value, onSelect() {}, async onPurchase() {}, onAnswer() {} });
const resultHTML = value => render(h(GuideResultView, props(value)));

// Expand the hook-free result tree to exercise its action callbacks as well as its markup.
function* elements(node) {
  if (Array.isArray(node)) { for (const child of node) yield* elements(child); return; }
  if (!node || typeof node !== 'object') return;
  if (typeof node.type === 'function') { yield* elements(node.type(node.props)); return; }
  yield node;
  yield* elements(node.props?.children);
}

test('browsing shows one chat reply and compact cards without a verification checklist', () => {
  const reply = 'F30 是一款直钉枪，可先看看产品详情。';
  const value = result({ reply });
  const html = render(h('div', null,
    h(MessageList, { messages: [{ role: 'assistant', content: reply }], busy: false }),
    h(GuideResultView, props(value))));
  assert.equal(html.split(reply).length - 1, 1);
  assert.match(html, /F30 产品图/);
  assert.match(html, /href="\/products\/fixture-f30\/"[^>]*aria-label="查看详情"/);
  assert.match(html, /aria-label="选这款"/);
  assert.match(html, /<summary>资料来源<\/summary>/);
  assert.match(html, /href="\/catalog\/"/);
  assert.doesNotMatch(html, /guide_profile|materials|required_requirements|缺口|还需确认|尚缺|已审核条件匹配|简介：机身小巧|用途说明：木工作业|请补充或核实条件|已审核动力/);
  assert.doesNotMatch(resultHTML(result()), /还需确认|请补充或核实条件|以下条件/);
  assert.doesNotMatch(resultHTML(result({ recommendations: [product({ match_level: 'matched' })] })), /已审核条件匹配/);
});

test('only stated conditions get concise Chinese gap hints; aliases are deduplicated and false remains known', () => {
  const html = resultHTML(result({
    reply: '可以进一步确认这款的使用条件。',
    confirmed_requirements: requirements({ material: '木材', fastener_length_mm: 20, air_supply: false }),
    recommendations: [product({
      missing_fields: ['guide_profile.materials', 'guide_profile.excluded_materials', 'guide_profile.substrates', 'guide_profile.fastener_length_mm', 'guide_profile.power_source', 'guide_profile.blower', 'guide_profile.required_requirements'],
      matched_facts: []
    })]
  }));
  assert.match(html, /还需确认：材料适配、钉长范围、气源要求。/);
  assert.equal(html.split('材料适配').length - 1, 1);
  assert.doesNotMatch(html, /基材适配|吹尘功能|guide_profile|fastener_length_mm|required_requirements/);
});

test('JSON selection retains compatibility facts, purchase variants, sources and callback', async () => {
  const value = result({
    state: 'selected', recommendations: [],
    confirmed_requirements: requirements({ long_nose: true }),
    selection: product({
      matched_facts: [{ field: 'long_nose', text: '已审核满足必需条件：long_nose', sources: [{ public_label: '产品说明', public_url: null }] }],
      missing_fields: [], purchase_status: 'available', purchase_options: [{
        purchase_option_id: 'fixture-variant', platform: '测试商城', store_name: '工具店',
        match_scope: 'listing_with_variants', sku_hint: '请选择 F30 / 20 mm',
        destination_host: 'example.test', handoff_url: '/api/public/purchase/fixture'
      }]
    })
  });
  const html = resultHTML(value);
  assert.match(html, /已选型号：F30/);
  assert.match(html, /已审核满足必需条件：加长枪嘴要求/);
  assert.match(html, /请选择 F30 \/ 20 mm/);
  assert.match(html, /aria-label="所选型号商城入口"/);
  assert.match(html, /产品说明/);
  assert.doesNotMatch(html, /<p>测试直钉枪<\/p>|简介：机身小巧|用途说明：木工作业/);
  assert.doesNotMatch(html, /long_nose|aria-label="选这款"|重复的产品长说明/);
  let purchased;
  let prevented = false;
  const tree = GuideResultView({ ...props(value), onPurchase: async href => { purchased = href; } });
  const link = [...elements(tree)].find(node => node.type === 'a' && node.props['data-guide-purchase']);
  link.props.onClick({ preventDefault() { prevented = true; } });
  assert.equal(prevented, true);
  assert.equal(purchased, '/api/public/purchase/fixture');
});

test('JSON conflicts stay visible in Chinese and identical spoken conflicts are not repeated', () => {
  const conflict = '已审核资料不满足必需条件：load_bearing';
  const value = result({ state: 'no_match', conflicts: [conflict], recommendations: [] });
  assert.match(resultHTML(value), /已审核资料不满足必需条件：承重用途/);
  assert.doesNotMatch(resultHTML(value), /load_bearing|guide_profile|以下条件/);
  assert.doesNotMatch(resultHTML({ ...value, reply: conflict }), /yg-conflicts|选型结果说明/);
  assert.match(resultHTML({ ...value, reply: '这款不太合适。' }), /承重用途/);
});

test('clarification can have no cards; repeated question text is omitted but answer options still work', () => {
  const question = { field: 'material', text: '主要用在什么材料上？', options: ['木材', '板材'] };
  const value = result({ state: 'clarifying', recommendations: [], reply: '我帮你选。主要用在什么材料上？', question });
  const html = resultHTML(value);
  assert.match(html, /role="group" aria-label="主要用在什么材料上？"/);
  assert.doesNotMatch(html, /<p>主要用在什么材料上？<\/p>|yg-product|以下条件|guide_profile/);
  assert.match(resultHTML({ ...value, reply: '可以。' }), /<p>主要用在什么材料上？<\/p>/);
  assert.doesNotMatch(resultHTML({ ...value, question: { ...question, options: [] } }), /yg-question/);
  assert.doesNotMatch(resultHTML({ ...value, question: null }), /yg-question|以下条件/);
  let answered;
  const tree = GuideResultView({ ...props(value), onAnswer: text => { answered = text; } });
  [...elements(tree)].find(node => node.type === 'button' && node.props.children === '木材').props.onClick();
  assert.equal(answered, '木材');
});

test('a natural reply replaces the card fact checklist while the select action remains', () => {
  const material = { field: 'materials', text: '适用于木材。', sources: [] };
  const length = { field: 'fastener_length_mm', text: '钉长 20 mm 在范围内。', sources: [] };
  const value = result({
    reply: '适用于木材。', confirmed_requirements: requirements({ material: '木材', fastener_length_mm: 20 }),
    recommendations: [product({ matched_facts: [material, length], missing_fields: [] })]
  });
  assert.doesNotMatch(resultHTML(value), /适用于木材/);
  assert.doesNotMatch(resultHTML(value), /钉长 20 mm 在范围内。/);
  let selected;
  const tree = GuideResultView({ ...props(value), onSelect: id => { selected = id; } });
  [...elements(tree)].find(node => node.type === 'button' && node.props['aria-label'] === '选这款').props.onClick();
  assert.equal(selected, 'fixture-f30');
});

test('busy copy is conversational while the live log and explicit false conditions remain accessible', () => {
  const html = render(h(MessageList, { messages: [{ role: 'user', content: '看看 F30' }], busy: true }));
  assert.match(html, /role="log"[^>]*aria-live="polite"/);
  assert.match(html, /<p role="status">正在整理回复…<\/p>/);
  assert.match(html, /您：/);
  assert.doesNotMatch(render(h(MessageList, { messages: [], busy: false })), /role="status"/);
  const conditions = render(h(Requirements, { requirements: requirements({ air_supply: false }) }));
  assert.match(conditions, /现场压缩空气气源/);
  assert.match(conditions, /<dd>否<\/dd>/);
});

test('a question owns its condition hint so cards only show other relevant gaps', () => {
  const value = result({
    state: 'selected', recommendations: [],
    confirmed_requirements: requirements({ material: '木材', air_supply: false }),
    question: { field: 'air_supply', text: '现场有压缩空气吗？', options: ['有气源', '没有气源'] },
    selection: product({
      matched_facts: [], missing_fields: ['air_supply', 'guide_profile.power_source', 'guide_profile.materials'],
      purchase_status: 'unavailable', purchase_options: []
    })
  });
  const html = resultHTML(value);
  assert.match(html, /<p>现场有压缩空气吗？<\/p>/);
  assert.match(html, />没有气源<\/button>/);
  assert.match(html, /还需确认：材料适配。/);
  assert.doesNotMatch(html, /还需确认：[^<]*气源|guide_profile|商城映射/);
  assert.doesNotMatch(html, /暂无商城链接|yg-purchase|所选型号商城入口/);
});


test('live drafts replace waiting copy and disappear when the completed message is committed', () => {
  const messages = [{ role: 'user', content: '介绍一下 F30' }];
  const draft = '这款适合木工';
  const preview = render(h(MessageList, { messages, busy: true, draft }));
  assert.match(preview, /data-guide-draft/);
  assert.match(preview, /role="log"[^>]*aria-live="polite"/);
  assert.equal(preview.split(draft).length - 1, 1);
  assert.doesNotMatch(preview, /正在整理回复|yg-welcome/);
  const complete = render(h(MessageList, { messages: [...messages, { role: 'assistant', content: draft }], busy: false, draft }));
  assert.equal(complete.split(draft).length - 1, 1);
  assert.doesNotMatch(complete, /data-guide-draft|正在回复/);
  const cancelled = render(h(MessageList, { messages, busy: false, draft }));
  assert.doesNotMatch(cancelled, /data-guide-draft|这款适合木工/);
});

test('selected cards with no purchase options never render an empty purchase area or a subtitle', () => {
  for (const purchase_status of ['available', 'unavailable']) {
    const html = resultHTML(result({ state: 'selected', recommendations: [], selection: product({ purchase_status, purchase_options: [] }) }));
    assert.match(html, /已选型号：F30/);
    assert.match(html, /aria-label="查看详情"/);
    assert.match(html, /<summary>资料来源<\/summary>/);
    assert.doesNotMatch(html, /<p>测试直钉枪<\/p>|简介：|用途说明：|暂无商城链接|yg-purchase/);
  }
});
