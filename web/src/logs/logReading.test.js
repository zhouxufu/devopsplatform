import test from 'node:test'
import assert from 'node:assert/strict'
import {logPreview, logRowKey, logSummary, queryPattern} from './logReading.js'

test('summarizes a gateway event while preserving its outcome, remaining fields and original evidence', () => {
  const raw = '2026-11-27 08:50:43 916 traceId= spanId= tl= WARN [elastic-90] .f.GatewayFilter : event=gateway_token_validation service=gateway outcome=INVALID'
  assert.equal(logPreview(raw,null),'event=gateway_token_validation outcome=INVALID service=gateway')
  assert.equal(logSummary('08:50 unrelated prefix: important text'),'08:50 unrelated prefix: important text')
  assert.equal(logSummary('event=other path=/order status=500'),'event=other path=/order status=500')
  assert.equal(logSummary('{"message":"order rejected","level":"WARN"}'),'order rejected')
})

test('finds terms in the raw stack or its prefix when the summary does not contain them', () => {
  const raw = 'request failed\n    at client.fetch()\nCaused by: Connection timeout on host'
  const pattern = queryPattern('level:(ERROR OR WARN) NOT heartbeat message:"Connection timeout"')
  assert.equal(logPreview(raw,pattern),'Caused by: Connection timeout on host')
  assert.equal(queryPattern('env:dev NOT (timeout OR fail) -secret'),null)
  assert.ok(queryPattern('time*').test('TimeoutException'))
  assert.ok(queryPattern('a.b [slow]',true).test('a.b [slow]'))
  assert.equal(logPreview('x'.repeat(500)+' timeout',queryPattern('timeout')).includes('timeout'),true)
})

test('row identities survive prepending and distinguish identical records', () => {
  const hit = {ts:1,message:'same'}
  const previous = logRowKey(hit)
  assert.notEqual(logRowKey({...hit}),previous)
  assert.equal(logRowKey(hit),previous)
})
