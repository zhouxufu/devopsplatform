import {test} from 'node:test'
import assert from 'node:assert/strict'
import {sourcePerson,issueDate,issueStatus,personOptions,workSelectionKey} from './issuePresentation.js'
import {nodeTypeWarning,nodeKindLabel} from '../deploy/nodePresentation.js'
test('source sentinels never become people, including translated sentinels',()=>{
 assert.equal(sourcePerson('Closed',{Closed:'已关闭'}),'—')
 assert.equal(sourcePerson('alice',{alice:'张三'}),'张三')
 assert.deepEqual(personOptions(['alice','closed','Closed','','alice'],{alice:'张三'}),[{value:'alice',label:'张三（alice）'}])
})
test('source date/status formatting and source-scoped selection identity',()=>{
 assert.equal(issueDate('0000-00-00 00:00:00'),'—');assert.equal(issueDate('invalid'),'—')
 assert.equal(issueStatus({status:'open',external_status:'resolved'}),'已解决')
 assert.notEqual(workSelectionKey({source:'zentao:a',external_id:'7'}),workSelectionKey({source:'zentao:b',external_id:'7'}))
 assert.equal(workSelectionKey({id:7}),'7')
})
test('node wording cannot masquerade as approval behavior',()=>{
 assert(nodeTypeWarning({name:'开发负责人审核',kind:'deploy'}).includes('不会作为审批节点'))
 assert.equal(nodeTypeWarning({name:'开发负责人审核',kind:'approval'}),'')
 assert.equal(nodeTypeWarning({name:'配置变更',kind:'nacos'}),'')
 assert.equal(nodeKindLabel({kind:'approval'}),'人工审核')
})
