import test from 'node:test'
import assert from 'node:assert/strict'
import {availableDailyWork,dailyWorkGroups,taskActionLabel} from './dailyWork.js'
import {runtimeWorkspaceHash} from './workspaceSession.js'

test('daily tasks expose testing and preserve exact navigation without starting execution',()=>{
 const plugins=['research','deploy','api-center','logs','monitor','dbms','redis'].map(name=>({name}))
 const groups=availableDailyWork(plugins)
 assert.equal(groups.length,3)
 for(const item of groups.flatMap(group=>group.items)){
  assert.equal(runtimeWorkspaceHash(item.hash,item.plugin),item.hash)
  const query=new URL(item.hash.slice(1),'http://fixture').searchParams
  assert(!query.has('start'));assert(!query.has('case'));assert(!query.has('run'))
 }
 assert.equal(groups.find(g=>g.id==='test').items.find(i=>i.label==='用例执行记录').hash,'#/test-cases?tab=records')
})
test('disabled plugins remove their task entries without inventing user roles',()=>{
 const before=JSON.stringify(dailyWorkGroups)
 assert.deepEqual(availableDailyWork([]),[])
 const groups=availableDailyWork([{name:'logs'}])
 assert.deepEqual(groups.map(g=>g.id),['operate'])
 assert.deepEqual(groups[0].items.map(i=>i.plugin),['logs'])
 assert.equal(JSON.stringify(dailyWorkGroups),before)
})
test('home task actions describe review and inspection rather than performing them',()=>{
 assert.equal(taskActionLabel({kind:'approval'}),'审核内容')
 assert.equal(taskActionLabel({kind:'exception',status:'uncertain'}),'核对异常')
 assert.equal(taskActionLabel({kind:'manual'}),'查看确认项')
 assert.equal(taskActionLabel({kind:'future-kind'}),'查看任务')
})
