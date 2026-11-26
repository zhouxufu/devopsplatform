import test from 'node:test'
import assert from 'node:assert/strict'
import {reactive} from 'vue'
import {emptyCase,emptyStep,caseDraft,casePayload,recordSource} from './caseModel.js'
import {emptyRequest} from './testingModel.js'
test('twenty-step cases preserve generated UUID columns and numeric extractions',()=>{
 const draft=emptyCase()
 draft.name='多步骤分享'
 draft.steps=Array.from({length:20},(_,index)=>({...emptyStep(),name:`步骤 ${index+1}`}))
 draft.data_rows=[{name:'分享',values:{id:{type:'uuid',value:''}}}]
 draft.steps[0].extractions=[{name:'fee',source:'json',path:'/fee',format:'finite_number_text'}]
 draft.steps[1].request.path='/share/{{data.id}}'
 const saved=casePayload(draft)
 assert.deepEqual(casePayload(caseDraft(saved)),saved)
 draft.steps.push({...emptyStep(),name:'超出上限'})
 assert.throws(()=>casePayload(draft),/1–20/)
})
test('legacy cases become one step with original identity and revision',()=>{const v=caseDraft({id:7,revision:3,name:'查询订单',request:{...emptyRequest(),path:'/orders/7'}});assert.equal(v.revision,3);assert.equal(v.steps.length,1);assert.equal(v.steps[0].request.path,'/orders/7');assert.equal(v.priority,'P2')})
test('case authoring compiles ordered independent requests, without leaking UI state',()=>{const draft=reactive(emptyCase());draft.name='订单场景';draft.module='订单';draft.preconditions='订单已存在';draft.test_data='编号 7';draft.steps[0].name='查询订单';draft.steps[0].headerMode='json';draft.steps[0].headersJSON='{"X-Test":"yes"}';draft.steps.push({...emptyStep(),name:'查询状态'});const payload=casePayload(draft);assert.equal(payload.steps.length,2);assert.equal(payload.steps[0].request.headers['X-Test'],'yes');assert.equal(payload.steps[1].request.headers['X-Test'],undefined);assert.equal('headersJSON' in payload.steps[0],false);assert.equal(payload.preconditions,'订单已存在');assert.equal(draft.steps[0].request.headers['X-Test'],undefined)})
test('invalid steps point to their position and old records classify by saved case association',()=>{const c=emptyCase();assert.throws(()=>casePayload(c),/用例名称/);c.name='test';assert.throws(()=>casePayload(c),/步骤 1/);c.steps[0].name='test';c.steps[0].headerMode='json';c.steps[0].headersJSON='[]';assert.throws(()=>casePayload(c),/步骤 1/);assert.equal(recordSource({case_id:3}),'case');assert.equal(recordSource({case_id:0,source:'case'}),'manual')})
test('response extraction survives editing and step references stay templates',()=>{const c=emptyCase();c.name='创建并查询';c.steps[0].name='创建';c.steps[0].extractions=[{name:'order_id',source:'json',path:'/data/id'}];c.steps.push({...emptyStep(),name:'查询',request:{...emptyRequest(),path:'/orders/{{step.order_id}}?id={{step.order_id}}',assertions:[{type:'json',path:'/data/id',expected:'{{step.order_id}}'}]}});const p=casePayload(c);assert.equal(p.steps[1].request.path,c.steps[1].request.path);assert.deepEqual(caseDraft(p).steps[0].extractions,c.steps[0].extractions);p.steps[1].extractions=[{name:'order_id',source:'json',path:'/id'}];assert.throws(()=>casePayload(caseDraft(p)),/重复/);c.steps.reverse();assert.throws(()=>casePayload(c),/前面的步骤/);assert.equal(c.steps[1].extractions.length,1)})
test('invalid variable references focus their own field and source types validate',()=>{const c=emptyCase();c.name='变量检查';c.steps[0].name='请求';c.steps[0].request.body='{{step.not_yet}}';assert.throws(()=>casePayload(c),e=>e.stepIndex===0&&e.field==='body');c.steps[0].request.body='';c.steps[0].extractions=[{name:'last',source:'sse',path:'/id'}];assert.throws(()=>casePayload(c),/请求类型/);c.steps[0].request.mode='sse';assert.equal(casePayload(c).steps[0].extractions[0].source,'sse')})
test('document association belongs to each step and survives drafts without replacing requests',()=>{const c=emptyCase();c.name='关联';c.steps[0].name='查询';c.steps[0].document_id=7;c.steps[0].operation_id='get /orders/{id}';c.steps[0].request.path='/orders/8';const p=casePayload(c),again=casePayload(caseDraft(p));assert.equal(again.steps[0].document_id,7);assert.equal(again.steps[0].operation_id,'get /orders/{id}');assert.equal(again.steps[0].request.path,'/orders/8')})
