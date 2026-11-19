import test from 'node:test'
import assert from 'node:assert/strict'
import {resourceScope,primaryResourceSections,sharedResourceSections,resourceInventory,filterResourceInventory} from './resourceWorkspace.js'
import {newResourceBinding} from './applicationResourceDraft.js'
test('hosts remain a distinct workspace and reuse stays optional',()=>{
 assert.deepEqual(primaryResourceSections.map(s=>s.key),['environments','hosts','connections','runners'])
 assert.deepEqual(sharedResourceSections.map(s=>s.key),['resourceGroups','groups','variables'])
 assert.deepEqual(resourceScope('logs',true),['hosts','audit'])
 assert.ok(!resourceScope('dbms').includes('runners'))
 assert.ok(!resourceScope('monitor').includes('groups'))
})
test('unified list retains type-specific identity and never searches secrets',()=>{
 const rows=resourceInventory([{name:'same',kind:'mysql',endpoint:'db:3306',password:'secret',config:{database:'private_db'}},{name:'other',kind:'redis'}],[{name:'same',addr:'host',environment:'test'}],[{value:'mysql'}])
 assert.deepEqual(rows.map(r=>r.id),['host:same','connection:same'])
 assert.equal(filterResourceInventory(rows,'','host')[0].category,'hosts')
 assert.equal(filterResourceInventory(rows,'secret').length,0)
 assert.equal(filterResourceInventory(rows,'private_db').length,0)
 assert.equal(filterResourceInventory(rows,'db:3306','mysql').length,1)
})
test('new binding suggests only one unambiguous matching target and never infers schema',()=>{
 const r={alias:'db',kind:'mysql'},connections=[{name:'one',kind:'mysql',config:{database:'old_default'}}]
 assert.deepEqual(newResourceBinding(r,[],connections,[{name:'test'}]),{alias:'db',environment:'test',connection:'one',config:{}})
 assert.equal(newResourceBinding(r,[],[...connections,{name:'two',kind:'mysql'}],[{name:'test'},{name:'prod'}]).connection,'')
 assert.equal(newResourceBinding(r,[{alias:'db',environment:'test'}],connections,[{name:'test'},{name:'prod'}]).environment,'prod')
 assert.equal(newResourceBinding(r,[],[{name:'cache',kind:'redis'}],[]).connection,'')
})
