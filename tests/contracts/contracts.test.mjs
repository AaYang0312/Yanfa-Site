import test from 'node:test';
import assert from 'node:assert/strict';
import fixtures from './fixtures.json' with { type: 'json' };
import { ContractError, decodeContract, validateContract } from '../../src/generated/validate-contract.mjs';

for (const fixture of fixtures) {
  test(fixture.name, () => {
    if (fixture.valid) {
      const result = validateContract(fixture.schema, structuredClone(fixture.data));
      if (fixture.name === 'unknown requirements preserve all nulls') assert.equal(result.air_supply, null);
      if (fixture.name === 'explicit false remains false') assert.equal(result.air_supply, false);
      if (fixture.name === 'trim material') assert.equal(result.material, '木材');
    } else {
      assert.throws(() => validateContract(fixture.schema, structuredClone(fixture.data)), error => {
        assert.ok(error instanceof ContractError);
        assert.equal(error.status, 422);
        for (const field of fixture.fields) assert.ok(error.error.fields.some(path => path === field || path.startsWith(`${field}.`)), `${field}: ${error.error.fields}`);
        return true;
      });
    }
  });
}
test('malformed JSON remains 400 instead of field error', () => {
  assert.throws(() => decodeContract('guide.schema.json#/$defs/GuideRequirements', '{'), error => error.status === 400 && error.error.code === 'INVALID_JSON');
});
