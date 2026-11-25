import test from 'node:test'
import assert from 'node:assert/strict'
import {hasInlineLogContext, updateWorkspaceContext} from './workspaceContext.js'

test('only log search owns inline context, including query links', () => {
  for (const hash of ['#/', '#/search', '#/search?trace_id=abc']) assert.equal(hasInlineLogContext({plugin:{name:'logs'},hash}),true)
  for (const hash of ['#/live', '#/status', '#/subs']) assert.equal(hasInlineLogContext({plugin:{name:'logs'},hash}),false)
  assert.equal(hasInlineLogContext({plugin:{name:'monitor'},hash:'#/search'}),false)
})

test('context updates preserve window isolation and copy the chosen range', () => {
  const original={service:'orders',environment:'dev',range:[100,200],rangeMinutes:15}
  const other=structuredClone(original), context=structuredClone(original), range=[300,400]
  assert.equal(updateWorkspaceContext(context,{service:'housing',environment:'test',range,rangeMinutes:0}),true)
  range[0]=999
  assert.deepEqual(context,{service:'housing',environment:'test',range:[300,400],rangeMinutes:0})
  assert.deepEqual(other,original)
  assert.equal(updateWorkspaceContext(context,{...context}),false)
})

test('invalid times do not replace a valid context and unknown payload is ignored', () => {
  const context={service:'orders',environment:'dev',range:[100,200],rangeMinutes:15}, before=structuredClone(context)
  for(const update of [{range:[200,100],rangeMinutes:0},{range:[0,1],rangeMinutes:0},{range:[100,Infinity],rangeMinutes:15},{range:[100,200],rangeMinutes:1441},{service:null,environment:42,token:'secret'}])assert.equal(updateWorkspaceContext(context,update),false)
  assert.deepEqual(context,before)
})
