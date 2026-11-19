import test from 'node:test'
import assert from 'node:assert/strict'
import {deliveryEditorGroups,changedDeliveryRowKeys} from './deliveryEditorList.js'

test('mixed change kinds group by application without duplicating a multi-application code row',()=>{
 const rows=[{key:'sql',kind:'mysql',service:'ai'},{key:'code',kind:'code',services:['ai']},{key:'multi',kind:'code',services:['ai','bus','ai']},{key:'note',kind:'manual',service:''}]
 const before=JSON.stringify(rows),groups=deliveryEditorGroups(rows)
 assert.deepEqual(groups.map(g=>g.rows.map(r=>r.key)),[['sql','code'],['multi'],['note']])
 assert.equal(groups[1].label,'跨应用变更');assert.equal(groups[2].label,'公共 / 人工事项')
 assert.equal(groups.flatMap(g=>g.rows).length,rows.length);assert.equal(JSON.stringify(rows),before)
})
test('search covers every kind and does not change the source rows or selected draft',()=>{
 const rows=[{key:'sql',kind:'mysql',service:'AI',sql:{resource_alias:'shared-db'}},{key:'code',kind:'code',services:['bus'],reference:'feature/SUMMARY'}]
 assert.equal(deliveryEditorGroups(rows,'SHARED-DB')[0].rows[0],rows[0])
 assert.equal(deliveryEditorGroups(rows,'summary')[0].rows[0],rows[1])
 assert.deepEqual(deliveryEditorGroups(rows,'missing'),[]);assert.equal(rows.length,2)
})
test('only modified editable drafts are counted; readonly rows and deleted unsaved rows do not inflate save state',()=>{
 const initial=[{key:'old',kind:'mysql',sql:{content:'SELECT 1'}},{key:'ready',readonly:true,title:'ready'}]
 const rows=structuredClone(initial)
 assert.equal(changedDeliveryRowKeys(rows,initial).size,0)
 rows[0].sql.content='SELECT 2';rows[1].title='server-owned';rows.push({key:'new',kind:'manual',reference:'record'})
 assert.deepEqual([...changedDeliveryRowKeys(rows,initial)],['old','new'])
 rows.pop();rows[0].sql.content='SELECT 1';assert.equal(changedDeliveryRowKeys(rows,initial).size,0)
})
