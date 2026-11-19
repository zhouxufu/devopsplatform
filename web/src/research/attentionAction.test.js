import test from 'node:test'
import assert from 'node:assert/strict'
import {attentionSelectionKey, canAttend, submitAttention} from './attentionAction.js'
const task=(id,event='first')=>({id,title:id,attention_key:id,attention_event:event,attention_revision:0,read_only:true,can_ignore:true})
test('execution readonly does not disable explicit personal capability; event and revision bind selection',()=>{
 const t=task('delivery:1');assert.equal(canAttend(t,'ignore'),true)
 assert.equal(canAttend({...t,can_ignore:false},'ignore'),false)
 assert.equal(canAttend({...t,attention_event:''},'ignore'),false)
 assert.notEqual(attentionSelectionKey(t),attentionSelectionKey({...t,attention_event:'new'}))
 assert.notEqual(attentionSelectionKey(t),attentionSelectionKey({...t,attention_revision:1}))
})
test('one frozen batch preserves partial and missing results without replay',async()=>{
 const a=task('delivery:1'),b=task('delivery:2'),c=task('delivery:3');let calls=0
 const result=await submitAttention([a,b,c,a,{...a,can_ignore:false}],'ignore','暂不跟进这些异常','request-1',async request=>{
  calls++;assert.equal(request.items.length,3);assert.deepEqual(request.items[0],{id:'delivery:1',event_digest:'first',revision:0})
  a.attention_event='changed';return {results:[{id:'delivery:1',event_digest:'first',success:true},{id:'delivery:2',event_digest:'first',success:false,error:'事件已变化'}]}
 })
 assert.equal(calls,1);assert.equal(result.succeeded[0].attention_event,'first');assert.deepEqual(result.failed.map(t=>t.id),['delivery:2','delivery:3'])
 assert.match(result.failed[1].error,/未确认/)
 await assert.rejects(submitAttention([b],'ignore','暂不处理此异常','request-2',async()=>{throw Error('连接断开')}),/断开/)
})
