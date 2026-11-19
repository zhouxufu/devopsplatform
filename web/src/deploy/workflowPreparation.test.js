import test from 'node:test'
import assert from 'node:assert/strict'
import {preparationIdentity} from './workflowPreparation.js'
const base={template_id:1,source_type:'work_items',work_item_ids:[7,8],environment:'test',artifact_sources:{orders:'build',billing:'existing'}}
test('preparation identity ignores Go/JS map key insertion order',()=>{
 assert.equal(preparationIdentity(base),preparationIdentity({...base,artifact_sources:{billing:'existing',orders:'build'}}))
})
test('scope, workflow, environment and artifact mode changes invalidate preparation',()=>{
 for(const change of [{template_id:2},{work_item_ids:[9]},{selected_change_ids:[12]},{environment:'prod'},{scene:'hotfix'},{artifact_sources:{orders:'existing',billing:'existing'}}])assert.notEqual(preparationIdentity(base),preparationIdentity({...base,...change}))
})
test('transient preflight and result fields do not break restoration',()=>{
 assert.equal(preparationIdentity(base),preparationIdentity({...base,preparation_run_id:123,code_preparation_id:'result',artifacts:{orders:42},preflight_token:'do-not-compare',reason:'updated release reason'}))
})
