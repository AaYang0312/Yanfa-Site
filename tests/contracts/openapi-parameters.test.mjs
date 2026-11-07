import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { validateContract } from '../../src/generated/validate-contract.mjs';

const api = JSON.parse(await readFile(new URL('../../schemas/http-api.openapi.yaml', import.meta.url), 'utf8'));
const operation = api.paths['/api/catalog/products'].get;
function consumeParameter(name, value) {
  const parameter = operation.parameters.find(item => item.in === 'query' && item.name === name);
  return validateContract(parameter.schema.$ref, value);
}

for (const material of [' 木材 ', '\u0085木材\u3000', ` ${'木'.repeat(100)} `]) {
  test(`OpenAPI material consumer normalizes ${JSON.stringify(material)}`, () => {
    const result = consumeParameter('material', material);
    assert.equal(result, material.replace(/^\p{White_Space}+|\p{White_Space}+$/gu, ''));
    assert.equal(validateContract('public-catalog.schema.json#/$defs/CatalogQuery', { material }).material, result);
  });
}
for (const material of ['', '\u0085\u3000\t', '木'.repeat(101)]) {
  test(`OpenAPI material consumer rejects ${JSON.stringify(material)}`, () => {
    assert.throws(() => consumeParameter('material', material), error => error.status === 422);
    assert.throws(() => validateContract('public-catalog.schema.json#/$defs/CatalogQuery', { material }), error => error.status === 422 && error.error.fields.includes('material'));
  });
}
for (const raw of ['5e-324', '1.5', '1.7976931348623157e308']) {
  test(`OpenAPI length consumer preserves ${raw}`, () => {
    const number = JSON.parse(raw);
    assert.equal(consumeParameter('fastener_length_mm', number), number);
    assert.equal(validateContract('public-catalog.schema.json#/$defs/CatalogQuery', { fastener_length_mm: number }).fastener_length_mm, number);
  });
}
for (const raw of ['0', '1e-999', '1e309', '-1', 'null']) {
  test(`OpenAPI length consumer rejects ${raw}`, () => {
    const number = JSON.parse(raw);
    assert.throws(() => consumeParameter('fastener_length_mm', number), error => error.status === 422);
    assert.throws(() => validateContract('public-catalog.schema.json#/$defs/CatalogQuery', { fastener_length_mm: number }), error => error.status === 422 && error.error.fields.includes('fastener_length_mm'));
  });
}
test('OpenAPI limit consumer preserves bounds and rejects out-of-range values', () => {
  assert.equal(consumeParameter('limit', 1), 1);
  assert.equal(consumeParameter('limit', 100), 100);
  for (const number of [0, 101, Infinity, Number.MAX_SAFE_INTEGER + 1]) assert.throws(() => consumeParameter('limit', number), error => error.status === 422);
});
