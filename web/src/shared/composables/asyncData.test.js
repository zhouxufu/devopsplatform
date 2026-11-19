import {test} from 'node:test'
import assert from 'node:assert/strict'
import {effectScope} from 'vue'
import {useAsyncData, requestList} from './asyncData.js'
const deferred = () => {let resolve, reject; const promise = new Promise((a,b) => {resolve=a;reject=b}); return {promise,resolve,reject}}
test('scope change clears old data and ignores a slower previous response', async () => {
  const scope = effectScope(), state = scope.run(() => useAsyncData([]))
  await state.load(async () => ['test'])
  const slow = deferred(); let signal
  const first = state.load(s => {signal=s;return slow.promise})
  assert.deepEqual(state.data.value, [])
  await state.load(async () => ['prod'])
  assert.equal(signal.aborted, true)
  slow.resolve(['test-old']); await first
  assert.deepEqual(state.data.value, ['prod']); assert.equal(state.loading.value,false)
  scope.stop()
})
test('failed request clears loading, retains a retryable error and can recover', async () => {
  const scope=effectScope(), state=scope.run(() => useAsyncData([]))
  await state.load(async () => {throw new Error('offline')})
  assert.equal(state.error.value,'offline'); assert.equal(state.loading.value,false)
  await state.load(async () => ['recovered'])
  assert.equal(state.error.value,''); assert.deepEqual(state.data.value,['recovered']); scope.stop()
})
test('unmount aborts requests and prevents late state changes', async () => {
  const scope=effectScope(), state=scope.run(() => useAsyncData([])), pending=deferred();let signal
  const request=state.load(s=>{signal=s;return pending.promise});scope.stop()
  pending.resolve(['late']);await request
  assert.equal(signal.aborted,true);assert.deepEqual(state.data.value,[]);assert.equal(state.loading.value,false)
})
test('HTTP failures and wrong list shapes are errors, not empty results', async () => {
  const original=globalThis.fetch
  try {
    globalThis.fetch=async()=>new Response(JSON.stringify({error:'no access'}),{status:403})
    await assert.rejects(requestList('/example'),/no access/)
    globalThis.fetch=async()=>new Response('{}')
    await assert.rejects(requestList('/example'),/列表响应格式错误/)
    globalThis.fetch=async()=>new Response('null')
    assert.deepEqual(await requestList('/example'),[])
  } finally {globalThis.fetch=original}
})
