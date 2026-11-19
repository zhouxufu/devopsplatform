import test from 'node:test'
import assert from 'node:assert/strict'
import {closeQuerySession} from './querySessions.js'
test('closing an inactive query preserves active target, draft and results by identity',()=>{
 const sessions=[{id:1},{id:2,query:{database:'orders',sql:'SELECT id FROM orders'},result:{rows:[1]}},{id:3}]
 for(const id of [1,3]){const next=closeQuerySession(sessions,id,2);assert.equal(next.sessions[next.index],sessions[1]);assert.equal(sessions.length,3)}
})
test('closing the active query chooses the adjacent tab and never removes the final placeholder',()=>{
 const sessions=[{id:1},{id:2},{id:3}]
 assert.equal(closeQuerySession(sessions,2,2).sessions[1],sessions[2])
 assert.equal(closeQuerySession(sessions,3,3).sessions[1],sessions[1])
 assert.equal(closeQuerySession(sessions,99,1),null)
 assert.equal(closeQuerySession([sessions[0]],1,1),null)
})
