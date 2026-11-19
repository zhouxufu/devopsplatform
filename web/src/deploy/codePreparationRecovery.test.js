import test from 'node:test'
import assert from 'node:assert/strict'
import {codePreparationRecovery as recovery} from './codePreparationRecovery.js'
const job={id:'same-task',state:'conflict',revision:7,lease_until:0}
test('conflict shows the action at the original node; no manual success is inferred',()=>{
 const result=recovery(job)
 assert.equal(result.needsRecovery,true)
 assert.equal(result.enabled,true)
 assert.equal(result.actionLabel,'验证合并并继续构建')
 assert.match(result.instruction,/目标分支并推送/)
 assert.equal(recovery({...job,verify_only:true}).actionLabel,'验证合并并继续')
})
test('lease boundary and protection provide actionable disabled reasons',()=>{
 assert.equal(recovery({...job,lease_until:101},{now:100000}).enabled,false)
 assert.match(recovery({...job,lease_until:101},{now:100000}).reason,/安全等待期/)
 assert.equal(recovery({...job,lease_until:101},{now:101000}).enabled,true)
 assert.match(recovery(job,{protectedScope:true}).reason,/勾选/)
 assert.equal(recovery(job,{protectedScope:true,scopeConfirmed:true}).enabled,true)
 assert.match(recovery(job,{canManage:false}).reason,/发起人或管理员/)
 assert.equal(recovery(job,{busy:true}).enabled,false)
})
test('active workers, completed and canceled tasks never invite duplicate recovery',()=>{
 for(const state of ['merging','building']){
  assert.equal(recovery({...job,state,lease_until:200},{now:100000}).needsRecovery,false)
  assert.equal(recovery({...job,state,lease_until:200},{now:200000}).needsRecovery,true)
 }
 for(const state of ['ready','canceled','preview'])assert.equal(recovery({...job,state}).enabled,false)
 assert.equal(recovery(null).enabled,false)
})
test('uncertain and failed states ask for reconciliation without claiming a conflict',()=>{
 assert.match(recovery({...job,state:'uncertain'}).instruction,/远端.*实际合并结果/)
 assert.match(recovery({...job,state:'failed'}).instruction,/现有构建记录/)
})
test('scope drift and unavailable review block resume with a concrete recovery path',()=>{
 const changed=recovery(job,{scopeReview:{changed:true}})
 assert.equal(changed.enabled,false);assert.match(changed.reason,/查看.*差异.*终止旧准备/)
 assert.equal(recovery(job,{scopeLoading:true}).enabled,false)
 assert.equal(recovery(job,{scopeError:'读取失败'}).enabled,false)
 assert.equal(recovery(job,{scopeReview:{changed:false}}).enabled,true)
})
