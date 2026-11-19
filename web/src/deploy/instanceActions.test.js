import test from 'node:test'
import assert from 'node:assert/strict'
import {instanceActionPayload,instanceStateText} from './instanceActions.js'
const row={host:'test-a',state:'running',controllable:true,fingerprint:'server-frozen-identity'}
const pending={action:'restart',confirmed:true,reason:'维护',requestId:'test-request-123456'}
test('instance actions bind reviewed identity and reuse request ID rather than form overrides',()=>{
 const body=instanceActionPayload(row,{protected:true}, {...pending,fingerprint:'forged'})
 assert.equal(body.fingerprint,row.fingerprint);assert.equal(body.request_id,pending.requestId);assert.equal(body.confirm_protected,true)
 assert.deepEqual(instanceActionPayload(row,{protected:true},pending),body)
})
test('unmanaged, stale, unconfirmed and protected instance actions fail before sending',()=>{
 for(const [r,p] of [[{...row,controllable:false},pending],[{...row,state:'stopped'},pending],[row,{...pending,confirmed:false}],[row,{...pending,reason:''}],[row,{...pending,requestId:'bad'}]])assert.throws(()=>instanceActionPayload(r,{protected:true},p))
 assert.equal(instanceStateText('unknown'),'状态待核对')
})

test('Docker query failure is distinct from an unreachable SSH host',()=>{
 assert.equal(instanceStateText('inspection-failed'),'读取失败')
 assert.equal(instanceStateText('unreachable'),'无法连接')
})
