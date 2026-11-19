import test from 'node:test'
import assert from 'node:assert/strict'
import {inboxAttention,safeSourceUrl,verifySourceOperation,sourceActionLabel} from './sourceOperation.js'
test('archived personal selection does not leak into actionable inbox; owned selection stays available',()=>{
 for(const preference of ['active','all','archived']){assert.equal(inboxAttention('todo',preference),'active');assert.equal(inboxAttention('done',preference),'all');assert.equal(inboxAttention('owned',preference),preference)}
})
test('verification reads original operation; uncertain or failed reads cannot become a receipt',async()=>{
 const calls=[],op={id:'frozen-op',request:{action:'comment',comment:'keep original'}}
 const send=async(path,body)=>{calls.push({path,body});return {...op,state:'uncertain',message:'尚无证据'}}
 const result=await verifySourceOperation(op,send);assert.equal(result.verified,false);assert.equal(result.message,'尚无证据');assert.deepEqual(calls,[{path:'research/operations/frozen-op/reconcile',body:{}}]);assert.equal(op.request.comment,'keep original')
 await assert.rejects(verifySourceOperation(op,async()=>({id:'other',state:'success'})),/不一致/)
 await assert.rejects(verifySourceOperation(op,async()=>{throw Error('登录过期')}),/登录过期/)
 assert.equal((await verifySourceOperation(op,async()=>({...op,state:'success'}))).verified,true)
})
test('source navigation rejects script URLs and embedded credentials',()=>{assert.equal(safeSourceUrl('javascript:alert(1)'),'');assert.equal(safeSourceUrl('https://user:pass@example.invalid/'),'');assert.equal(safeSourceUrl('https://example.invalid/bug-view-7.html'),'https://example.invalid/bug-view-7.html');assert.equal(sourceActionLabel('comment'),'发表评论')})
