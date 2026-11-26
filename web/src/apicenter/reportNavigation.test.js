import test from 'node:test'
import assert from 'node:assert/strict'
import {reportListLocation} from './reportNavigation.js'
test('return from a direct detail always targets the list and removes detail ownership only',()=>{
 const source={report:'r4',detail_service:'bus',detail_environment:'dev',detail_protocol:'http',actor:'alice',page:'2',service:'orders',protocol:'http',start:'2026-11-01',state:'saved',mine:'1',end:''}
 const target=reportListLocation(source)
 assert.equal(target.path,'/reports')
 assert.deepEqual(target.query,{actor:'alice',page:'2',service:'orders',protocol:'http',start:'2026-11-01',state:'saved',mine:'1'})
 assert.equal(source.report,'r4')
})
test('no prior list entry is required to return from a refreshed detail',()=>{
 assert.deepEqual(reportListLocation({report:'r4',page:'1'}),{path:'/reports',query:{page:'1'}})
})
