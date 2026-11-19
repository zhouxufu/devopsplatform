import test from 'node:test'
import assert from 'node:assert/strict'
import {manualRecoveryBlocker} from './manualRecovery.js'
test('manual recovery accepts short and long evidence when the artifact is confirmed',()=>{
 for(const comment of ['已完成','好','OK','核对'.repeat(6000)])assert.equal(manualRecoveryBlocker({comment,confirmArtifact:true}),'')
})
test('manual recovery explains every missing prerequisite without a length requirement',()=>{
 assert.match(manualRecoveryBlocker(),/填写处理结果.*勾选/)
 assert.equal(manualRecoveryBlocker({comment:'已完成'}),'请勾选已核对本次审核制品')
 assert.equal(manualRecoveryBlocker({comment:' \n ',confirmArtifact:true}),'请填写处理结果与核对依据')
 assert.equal(manualRecoveryBlocker({busy:true,comment:'已完成',confirmArtifact:true}),'正在记录处理结果，请稍候')
})
