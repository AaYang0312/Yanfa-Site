import test from 'node:test';
import assert from 'node:assert/strict';
import cases from './consumer-cases.json' with { type: 'json' };
import { ContractError, decodeContract, validateContract } from '../../src/generated/validate-contract.mjs';

for (const entry of cases) {
  test(entry.name, () => {
    if (entry.valid) {
      assert.deepEqual(decodeContract(entry.schema, entry.raw), entry.expected);
    } else {
      assert.throws(() => decodeContract(entry.schema, entry.raw), error => {
        assert.ok(error instanceof ContractError);
        assert.equal(error.status, 422);
        for (const field of entry.fields) assert.ok(error.error.fields.some(path => path === field || path.startsWith(`${field}.`)), `${field}: ${error.error.fields}`);
        return true;
      });
    }
  });
}

test('non-finite numbers never enter a validated candidate revision', () => {
  for (const number of [NaN, Infinity, -Infinity]) {
    assert.throws(() => validateContract('admin.schema.json#/$defs/CandidateInput', { expected_workspace_revision: number }), error => error.status === 422 && error.error.fields.includes('expected_workspace_revision'));
  }
});
