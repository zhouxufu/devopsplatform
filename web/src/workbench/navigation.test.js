import test from 'node:test'
import assert from 'node:assert/strict'
import {workbenchTabHash} from './navigation.js'
import {selectedNavigation} from '../shell/navigation.js'
test('DBMS/Redis internal navigation stays aligned with official menu routes',()=>{
 for(const plugin of ['dbms','redis'])for(const tab of ['query','operations','history']){
  const hash=workbenchTabHash(tab,'#query')
  assert.ok(hash.startsWith('#/'))
  assert.equal(selectedNavigation(plugin,hash),selectedNavigation(plugin,'#/'+tab))
 }
 assert.equal(selectedNavigation('dbms',workbenchTabHash('sync','#history')),'sync')
})
test('unchanged context is preserved, while changing workbench area removes old execution scope',()=>{
 assert.equal(workbenchTabHash('sync','#/sync?work_item=7'),'#/sync?work_item=7')
 assert.equal(workbenchTabHash('query','#/sync?work_item=7'),'#/query')
 assert.equal(workbenchTabHash('history','#history'),'#/history')
 assert.equal(workbenchTabHash('invalid','#/sync'),'#/query')
})
