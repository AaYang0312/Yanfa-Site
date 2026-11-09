import test from 'node:test';
import assert from 'node:assert/strict';
import { validateContract } from '../../src/generated/validate-contract.mjs';
const requirements = { scene_ids: [], material: null, substrate: null, nail_type: null, fastener_length_mm: null, air_supply: null, load_bearing: null, long_nose: null, blower: null };
const proposal = () => ({ intent: 'clarify', requirements: structuredClone(requirements), candidate_product_ids: [], selected_product_id: null, selection_reference: null, reply: '你准备做什么活儿？' });
const proposalSchema = 'guide.schema.json#/$defs/GuideModelProposal';

test('natural clarification carries no accidental products or selection', () => {
  assert.deepEqual(validateContract(proposalSchema, proposal()), proposal());
  for (const mutate of [p => { delete p.reply; }, p => { p.reply = ' \n '; }, p => { p.reply = '好'.repeat(2001); }, p => { p.candidate_product_ids = ['p_example']; }, p => { p.selected_product_id = 'p_example'; }]) {
    const value = proposal(); mutate(value);
    assert.throws(() => validateContract(proposalSchema, value));
  }
});

test('natural reply is optional for existing consultation fixtures', () => {
  const value = proposal(); value.intent = 'consult'; delete value.reply;
  assert.deepEqual(validateContract(proposalSchema, value), value);
});

test('browser accepts a complete clarification result with a natural reply', () => {
  const result = { release_id: 'r_conversation', state: 'clarifying', confirmed_requirements: requirements, conflicts: [], question: null, recommendations: [], selection: null, missing_fields: [], contact_href: 'tel:12345', reply: '你准备做什么活儿？' };
  assert.deepEqual(validateContract('guide.schema.json#/$defs/GuideResult', result), result);
});
