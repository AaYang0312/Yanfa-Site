import test from 'node:test';
import assert from 'node:assert/strict';
import fixtures from './fixtures.json' with {type:'json'};
import {validateContract} from '../../src/generated/validate-contract.mjs';
const source=fixtures.find(f=>f.name==='published tool requires complete facts').data;
function product(count){
 const p={...structuredClone(source),name:'测试工具',model:'TEST',summary:'明确用途的产品介绍',applications:['产品目录所列用途']};
 p.images=Array.from({length:count},(_,i)=>({media_id:`m_image${i}`,label:'产品图片',description:''}));p.cover_media_id=count?p.images[0].media_id:null;return p;
}
test('published tools allow genuine missing photos and a complete 20-image gallery',()=>{
 for(const count of [0,14,20])assert.equal(validateContract('product.schema.json#/$defs/PublishedProduct',product(count)).images.length,count);
});
test('product image limit and cover ownership remain enforced',()=>{
 assert.throws(()=>validateContract('product.schema.json#/$defs/PublishedProduct',product(21)),e=>e.status===422);
 const p=product(14);p.cover_media_id='m_unrelated';assert.throws(()=>validateContract('product.schema.json#/$defs/PublishedProduct',p),e=>e.status===422);
});
