import test from 'node:test';
import assert from 'node:assert/strict';
import { decodeToolResult, productEditLink } from './admin-helpers.mjs';
test('machine data comes from structuredContent even when conversational text is not JSON', () => {
  const envelope = { product: { id: 'p_fixture' }, version: 2, workspace_revision: 5 };
  const text = '产品草稿已保存，尚未更新官网。/admin/products/edit/?id=p_fixture';
  assert.deepEqual(decodeToolResult({ structuredContent: envelope, content: [{ type: 'text', text }] }), { value: envelope, error: false, text });
  assert.equal(productEditLink(text, 'http://127.0.0.1:18080', 'p_fixture').href, 'http://127.0.0.1:18080/admin/products/edit/?id=p_fixture');
});
test('structured business failures stay failures and unsafe handoffs are rejected', () => {
  const result = decodeToolResult({ isError: true, structuredContent: { error: { code: 'FORBIDDEN' } }, content: [] });
  assert(result.error); assert.equal(result.value.error.code, 'FORBIDDEN');
  assert.throws(() => decodeToolResult({ content: [{ type: 'text', text: '{}' }] }));
  assert.throws(() => productEditLink('https://other.example/admin/products/edit/?id=p_fixture', 'http://127.0.0.1:18080', 'p_fixture'));
  assert.throws(() => productEditLink('/admin/products/edit/?id=p_other', 'http://127.0.0.1:18080', 'p_fixture'));
});
