import test from 'node:test'
import assert from 'node:assert/strict'
import {deliveryChangePayload,deliveryEntryProgress,deliveryResourceOptions,deliveryNextWorkspace,savedDeliverySelection} from './deliveryEntry.js'
import {deliveryRows,expandDeliveryRows} from './deliveryContent.js'
import {validateDeliveryChange} from './deliveryValidation.js'
test('saving a new row preserves its selected identity without choosing an old or ambiguous title',()=>{
 const submitted={kind:'nacos',service:'orders',title:'配置'},old={...submitted,id:1},added={...submitted,id:5}
 assert.equal(savedDeliverySelection(submitted,[old,added],[1]),5)
 assert.equal(savedDeliverySelection({...submitted,id:1},[old,added],[1]),1)
 assert.equal(savedDeliverySelection(submitted,[old,added,{...added,id:6}],[1]),0)
 assert.equal(savedDeliverySelection(undefined,[old],[]),0)
 assert.equal(savedDeliverySelection({kind:'manual',title:'人工记录',service:''},[{id:8,kind:'manual',title:'人工记录'}]),8)
})

test('independent SQL validation ignores inactive Redis and Nacos form drafts but preserves raw SQL',()=>{
 const form={kind:'mysql',title:'SQL',service:'orders',sql:{resource_alias:'db',content:'-- 原文\nDROP TABLE menu',max_affected_rows:1},nacos:{entries:[]},redis:{commands:''}}
 const payload=deliveryChangePayload(form)
 assert.equal(validateDeliveryChange(payload,'standalone'),null)
 assert.equal(payload.sql.content,form.sql.content)
 assert.equal(payload.nacos,undefined);assert.equal(payload.redis,undefined)
 assert.deepEqual(form.redis,{commands:''})
})

test('readiness includes unchanged owned drafts without rewriting readonly or ready records',()=>{
 const changes=[{id:1,kind:'code',status:'draft',service:'api',reference:'feature/x',title:'实现'}, {id:2,kind:'code',status:'ready',service:'web',reference:'feature/x',title:'已就绪'}, {id:3,kind:'manual',status:'draft',service:'api',reference:'verify',title:'他人内容',owner:'other'}]
 const rows=deliveryRows(changes,c=>c.owner!=='other',[{name:'api'},{name:'web'}],42)
 assert.equal(expandDeliveryRows(rows,'Bug').length,0)
 const origins=[],items=expandDeliveryRows(rows,'Bug',origins,true)
 assert.deepEqual(items.map(x=>x.id),[1]);assert.deepEqual(origins,['existing-1'])
 assert.deepEqual(deliveryEntryProgress(rows).editable,1)
 assert.equal(deliveryEntryProgress(rows).canContinue,false)
 assert.equal(deliveryEntryProgress(rows.map(x=>({...x,status:'ready',readonly:true}))).canContinue,true)
 assert.equal(deliveryEntryProgress(rows.map(x=>({...x,status:'ready'})),true).canContinue,false)
 assert.equal(deliveryEntryProgress([]).canContinue,false)
 assert.match(deliveryEntryProgress([]).hint,/添加/)
 assert.doesNotMatch(deliveryEntryProgress([]).hint,/只读|维护人/)
})
test('resource options show useful descriptions or config filenames but submit stable aliases',()=>{
 const document={requirements:[{alias:'db',kind:'mysql',description:'业务字典库'},{alias:'settings',kind:'nacos',description:'按环境选择的 Nacos 配置'},{alias:'cache',kind:'redis'}],bindings:[{alias:'settings',environment:'dev',config:{data_id:'common.yaml'}},{alias:'settings',environment:'test',config:{data_id:'common.yaml'}}]}
 assert.deepEqual(deliveryResourceOptions(document,'mysql'),[{value:'db',label:'业务字典库 · db'}])
 assert.deepEqual(deliveryResourceOptions(document,'nacos'),[{value:'settings',label:'common.yaml · settings'}])
 assert.deepEqual(deliveryResourceOptions(undefined,'mysql'),[])
})
test('next-step navigation retains work item identity and never starts execution',()=>{
 assert.deepEqual(deliveryNextWorkspace('changes',[21,12]),{plugin:'deploy',hash:'#/orchestration?changes=21,12'})
 for(const ids of [[],[0],[1,'2&publish=1']])assert.equal(deliveryNextWorkspace('changes',ids),null)
 assert.deepEqual(deliveryNextWorkspace('delivery',42),{plugin:'deploy',hash:'#/orchestration?work_items=42'})
 assert.deepEqual(deliveryNextWorkspace('version','42'),{plugin:'research',hash:'#/versions?add_work_item=42'})
 for(const id of [0,-1,'42&publish=1','bad'])assert.equal(deliveryNextWorkspace('delivery',id),null)
 assert.equal(deliveryNextWorkspace('execute',42),null)
})
test('code drafts can omit the branch until ready',()=>{
 const change={kind:'code',service:'orders',reference:' '}
 assert.equal(validateDeliveryChange(change,'draft',false),null)
 assert.equal(validateDeliveryChange(change,'ready',true).field,'reference')
 assert.equal(validateDeliveryChange({...change,service:''},'draft',false).field,'services')
})
