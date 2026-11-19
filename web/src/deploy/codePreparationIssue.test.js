import test from 'node:test'
import assert from 'node:assert/strict'
import {codePreparationIssue} from './codePreparationIssue.js'

test('readiness error routes to the exact change and preserves the concrete server reason',()=>{
  const message='代码变更 #17 仍为草稿，尚未标记就绪。请先核对变更内容并标记就绪，再重新检查代码来源。'
  assert.deepEqual(codePreparationIssue(message),{id:17,message,hash:'#/changes?tab=changes&item=17'})
})
test('older servers gain an actionable explanation without assuming an unreported status',()=>{
  const issue=codePreparationIssue('代码变更 #1 尚未就绪')
  assert.equal(issue.id,1)
  assert.match(issue.message,/核对内容并标记就绪/)
  assert.doesNotMatch(issue.message,/草稿/)
})
test('unrelated, malformed and unsafe errors never generate a repair link',()=>{
  for(const message of ['Git 认证失败 #1','代码变更 #1 发生合并冲突','代码变更 #0 尚未就绪','代码变更 #-1 尚未就绪','代码变更 #9007199254740992 尚未就绪','代码变更 #1/other 尚未就绪','其他错误：代码变更 #1 尚未就绪','',null])assert.equal(codePreparationIssue(message),null)
})
