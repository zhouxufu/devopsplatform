import test from 'node:test'
import assert from 'node:assert/strict'
import {toast,dismissToast,confirmAction,promptAction,pendingConfirmation,finishConfirmation} from './shadcnFeedback.js'

test('leave confirmation returns the selected outcome and clears the active request',async()=>{
 const cancel=confirmAction('保留环境草稿？')
 assert.equal(pendingConfirmation.value.content,'保留环境草稿？')
 finishConfirmation(false)
 assert.equal(await cancel,false)
 assert.equal(pendingConfirmation.value,null)
 const accept=confirmAction('放弃环境草稿？')
 finishConfirmation(true)
 finishConfirmation(false)
 assert.equal(await accept,true)
 assert.equal(pendingConfirmation.value,null)
})
test('only one confirmation is active; a second request does not replace the first',async()=>{
 const first=confirmAction('关闭当前工作区？')
 const second=confirmAction('其他请求')
 assert.equal(await second,false)
 assert.equal(pendingConfirmation.value.content,'关闭当前工作区？')
 finishConfirmation(true)
 assert.equal(await first,true)
})
test('reason prompts retain entered text on acceptance and distinguish cancellation',async()=>{
 const accepted=promptAction('填写回退原因')
 finishConfirmation(true,'核对日志后恢复上一版本')
 assert.equal(await accepted,'核对日志后恢复上一版本')
 const canceled=promptAction('再次填写')
 finishConfirmation(false)
 assert.equal(await canceled,null)
})

test('environment notice IDs dismiss through the notification API when a target becomes available',t=>{
 const originalFrame=globalThis.requestAnimationFrame
 globalThis.requestAnimationFrame=callback=>{callback();return 1}
 t.after(()=>{if(originalFrame)globalThis.requestAnimationFrame=originalFrame;else delete globalThis.requestAnimationFrame})
 const notice=toast('Temporary environment notice','warning',{duration:6000})
 assert.ok(typeof notice==='number'||typeof notice==='string')
 assert.doesNotThrow(()=>dismissToast(notice))
 assert.doesNotThrow(()=>dismissToast(undefined))
 assert.doesNotThrow(()=>dismissToast(null))
 assert.doesNotThrow(()=>dismissToast(0))
})
