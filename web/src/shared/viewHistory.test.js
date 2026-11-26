import test from 'node:test'
import assert from 'node:assert/strict'
import {createViewNavigation} from './viewHistory.js'
test('fast normalization cannot replace an uncommitted detail history entry',async()=>{
 const calls=[],pending=[]
 const request=method=>target=>{calls.push([method,target]);return new Promise(resolve=>pending.push(resolve))}
 const navigate=createViewNavigation({push:request('push'),replace:request('replace')})
 const first=navigate('/detail',true),normalized=navigate('/detail?resolved=1')
 assert.deepEqual(calls,[['push','/detail'],['push','/detail?resolved=1']])
 pending[0]();await first;pending[1]();await normalized
 const refresh=navigate('/detail?resolved=1&label=ready');assert.equal(calls.at(-1)[0],'replace');pending[2]();await refresh
})
