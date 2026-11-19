import test from 'node:test'
import assert from 'node:assert/strict'
import {splitSQLNode} from './deliveryStages.js'
test('SQL stages partition exact records and preserve dependencies',()=>{
 const nodes=[{id:'sql',kind:'mysql',change_ids:[1,2,3],depends_on:['earlier']},{id:'service',kind:'deploy'}]
 assert.equal(splitSQLNode(nodes,0,[2],'after'),true)
 assert.deepEqual(nodes.map(n=>n.change_ids),[[1,3],[2],undefined])
 assert.equal(nodes[1].sql_group,'sql')
 assert.deepEqual(nodes[1].depends_on,['earlier'])
 assert.equal(splitSQLNode(nodes,0,[3],'last'),true)
 assert.equal(nodes[1].sql_group,'sql')
 assert.deepEqual(nodes.filter(n=>n.kind==='mysql').flatMap(n=>n.change_ids).sort(),[1,2,3])
})
test('invalid split does not change the draft',()=>{
 for(const selection of [[],[1,2],[999]]){
  const nodes=[{id:'sql',kind:'mysql',change_ids:[1,2]}],before=structuredClone(nodes)
  assert.equal(splitSQLNode(nodes,0,selection,'after'),false)
  assert.deepEqual(nodes,before)
 }
})
