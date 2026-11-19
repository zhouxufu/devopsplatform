import test from 'node:test'
import assert from 'node:assert/strict'
import {applicationResourceDraft} from './applicationResourceDraft.js'
test('legacy group migrates references, never copies inherited members into own resources',()=>{
 const draft=applicationResourceDraft({resource_group:'common',group_revision:4,requirements:[{alias:'db'}],bindings:[{alias:'db'}]})
 assert.deepEqual(draft.resource_groups,['common']);assert.deepEqual(draft.group_revisions,{common:4});assert.deepEqual(draft.requirements,[]);assert.deepEqual(draft.bindings,[])
})
test('composed draft separates effective and own members and retains Nacos selections',()=>{
 const draft=applicationResourceDraft({composition_version:2,resource_groups:['a','b'],requirements:[{alias:'inherited'}],own_requirements:[{alias:'own'}],own_bindings:[{alias:'own',config:{}}],nacos_configs:[{data_id:'bus.yml'}]})
 assert.equal(draft.requirements[0].alias,'own');assert.equal(draft.nacos_configs[0].data_id,'bus.yml');assert.equal(draft.resource_groups.length,2)
})
test('editing schemas on a shared instance keeps per-environment drafts and source records independent',()=>{
 const original={bindings:[{alias:'db',environment:'test',connection:'instance',config:{database:'orders_test'}},{alias:'db',environment:'prod',connection:'instance',config:{database:'orders_prod'}}]}
 const first=applicationResourceDraft(original),second=applicationResourceDraft(original)
 first.bindings[0].config.database='another_test'
 assert.equal(first.bindings[1].config.database,'orders_prod')
 assert.equal(second.bindings[0].config.database,'orders_test')
 assert.equal(original.bindings[0].config.database,'orders_test')
 assert.equal(applicationResourceDraft({bindings:[{connection:'legacy'}]}).bindings[0].config.database,undefined)
})
