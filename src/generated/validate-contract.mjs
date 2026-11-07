import Ajv2020 from 'ajv/dist/2020.js';
import addFormats from 'ajv-formats';
import documents from './schema-documents.json' with { type: 'json' };

const base = 'https://yuanfa.example/schemas/';
const ajv = new Ajv2020({ allErrors: true, strict: false, strictNumbers: true, validateFormats: true });
addFormats(ajv);
const sources = new Map(documents.map(doc => [doc.$id, doc]));
for (const doc of documents) ajv.addSchema(doc);
export class ContractError extends Error {
  constructor(status, code, fields) {
    super(code);
    this.status = status;
    this.error = { code, message: status === 400 ? 'JSON 格式不合法。' : '字段或引用不合法。', fields };
  }
}
const join = (path, key) => path ? `${path}.${key}` : String(key);
function resolve(schema, document) {
  if (!schema.$ref) return [schema, document];
  const url = new URL(schema.$ref, document.$id);
  const doc = sources.get(url.origin + url.pathname);
  const target = url.hash.slice(2).split('/').reduce((value, key) => value[key], doc);
  return [target, doc];
}
function normalize(schema, value, document) {
  if (value === null) return value;
  // A reference adds the target's rules; its siblings stay in this document.
  if (schema.$ref) {
    const [target, targetDocument] = resolve(schema, document);
    value = normalize(target, value, targetDocument);
  }
  for (const keyword of ['allOf', 'anyOf', 'oneOf']) {
    for (const branch of schema[keyword] ?? []) value = normalize(branch, value, document);
  }
  if (schema['x-trim'] && typeof value === 'string') return value.replace(/^\p{White_Space}+|\p{White_Space}+$/gu, '');
  if (Array.isArray(value) && schema.items) return value.map(item => normalize(schema.items, item, document));
  if (value && typeof value === 'object' && schema.properties) {
    for (const [key, child] of Object.entries(schema.properties)) {
      if (Object.hasOwn(value, key)) value[key] = normalize(child, value[key], document);
    }
  }
  return value;
}
function invariants(schema, value, document, path, fields) {
  if (value === null) return;
  if (schema.$ref) {
    const [target, targetDocument] = resolve(schema, document);
    invariants(target, value, targetDocument, path, fields);
  }
  for (const branch of schema.allOf ?? []) invariants(branch, value, document, path, fields);
  if (schema.anyOf) {
    const branch = schema.anyOf.find(x => x.type !== 'null');
    if (branch) invariants(branch, value, document, path, fields);
  }
  if (schema.oneOf) {
    const branch = schema.oneOf.find(x => ajv.getSchema(new URL(x.$ref, document.$id).href)(value));
    if (branch) invariants(branch, value, document, path, fields);
  }
  const fail = key => fields.push(key ? join(path, key) : path || '$');
  if (schema['x-safe-relative-path'] && value.split('/').some(x => x === '.' || x === '..')) fail();
  if (schema['x-ordered-range']) {
    const [lo, hi] = schema['x-ordered-range'];
    if (value[lo] > value[hi]) fail(hi);
  }
  if (schema['x-history-max-length'] && (value.messages ?? []).reduce((n, x) => n + [...x.content].length, 0) > schema['x-history-max-length']) fail('messages');
  if (schema['x-cover-membership']) {
    if (value.cover_media_id !== null && !value.images.some(x => x.media_id === value.cover_media_id)) fail('cover_media_id');
  }
  if (schema['x-selection-missing-fields'] && value.state === 'selected' && JSON.stringify(value.missing_fields) !== JSON.stringify(value.selection.missing_fields)) fail('missing_fields');
  if (schema['x-active-membership'] && !value.successful_release_ids.includes(value.release_id)) fail('successful_release_ids');
  if (schema['x-max-pixels'] && value.width * value.height > schema['x-max-pixels']) { fail('width'); fail('height'); }
  if (schema['x-product-version'] && value.version !== value.product.version) fail('version');
  if (schema['x-release-consistency']) {
    if (value.product && value.product.release_id !== value.release_id) fail('product.release_id');
    for (const key of ['items', 'products']) (value[key] ?? []).forEach((item, index) => {
      if (item.release_id !== value.release_id) fail(`${key}.${index}.release_id`);
    });
  }
  if (schema['x-catalog-references']) {
    const productIDs = new Set(value.products.map(x => x.id));
    const seriesIDs = new Set(value.legacy_series.map(x => x.id));
    const slugs = new Set(); const models = new Set();
    for (let i = 0; i < value.legacy_series.length; i++) {
      const series = value.legacy_series[i];
      if (slugs.has(series.slug)) fail(`legacy_series.${i}.slug`);
      slugs.add(series.slug);
      if (series.product_ids.some(x => !productIDs.has(x))) fail(`legacy_series.${i}.product_ids`);
    }
    for (let i = 0; i < value.products.length; i++) {
      const product = value.products[i];
      if (slugs.has(product.slug)) fail(`products.${i}.slug`);
      slugs.add(product.slug);
      const model = product.model.toLowerCase();
      if (model && models.has(model)) fail(`products.${i}.model`);
      if (model) models.add(model);
      if (product.legacy_series_ids.some(x => !seriesIDs.has(x))) fail(`products.${i}.legacy_series_ids`);
      for (const series of value.legacy_series) {
        if (product.legacy_series_ids.includes(series.id) !== series.product_ids.includes(product.id)) fail(`products.${i}.legacy_series_ids`);
      }
    }
  }
  if (Array.isArray(value)) {
    const key = schema['x-unique-by'];
    if (key) {
      const seen = new Set();
      value.forEach((item, index) => {
        if (item[key] === null) return; // multiple new server-assigned channel IDs
        if (seen.has(item[key])) fail(`${index}.${key}`);
        seen.add(item[key]);
      });
    }
    if (schema.items) value.forEach((item, index) => invariants(schema.items, item, document, join(path, index), fields));
  } else if (schema.properties) {
    for (const [key, child] of Object.entries(schema.properties)) {
      if (Object.hasOwn(value, key)) invariants(child, value[key], document, join(path, key), fields);
    }
  }
}

export function validateContract(schema, value) {
  const uri = new URL(schema, base).href;
  const validator = ajv.getSchema(uri);
  if (!validator) throw new Error(`Unknown contract: ${schema}`);
  const sourceURL = new URL(uri);
  const source = sources.get(sourceURL.origin + sourceURL.pathname);
  value = normalize({ $ref: uri }, value, source);
  if (!validator(value)) {
    const fields = validator.errors.map(error => {
      const path = error.instancePath.slice(1).split('/').map(x => x.replaceAll('~1', '/').replaceAll('~0', '~')).join('.');
      return join(path, error.params.missingProperty ?? error.params.additionalProperty ?? '').replace(/\.$/, '') || '$';
    });
    throw new ContractError(422, 'VALIDATION_FAILED', [...new Set(fields)].sort());
  }
  const url = new URL(uri); const doc = sources.get(url.origin + url.pathname);
  const fields = [];
  invariants({ $ref: uri }, value, doc, '', fields);
  if (fields.length) throw new ContractError(422, 'VALIDATION_FAILED', [...new Set(fields)].sort());
  return value;
}
export function decodeContract(schema, json) {
  let value;
  try { value = JSON.parse(json); } catch { throw new ContractError(400, 'INVALID_JSON', []); }
  return validateContract(schema, value);
}
