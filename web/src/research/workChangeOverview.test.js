import test from 'node:test'
import assert from 'node:assert/strict'
import {overviewGroups} from './workChangeOverview.js'
test('overview preserves identities, applications, draft status and original payloads',()=>{
 const rows=[{id:1,kind:'code',service:'a',reference:'shared'},{id:2,kind:'code',service:'b',reference:'shared'},{id:3,kind:'mysql',service:'a',status:'draft',sql:{content:'-- comment\nUPDATE t SET value=2 WHERE id=1;'}},{id:4,kind:'manual',reference:'人工确认'}]
 const before=JSON.stringify(rows),groups=overviewGroups(rows)
 assert.deepEqual(groups.map(g=>g.changes.map(c=>c.id)),[[1,3],[2],[4]])
 assert.equal(groups[0].changes[1],rows[2]);assert.equal(JSON.stringify(rows),before)
 assert.deepEqual(overviewGroups(rows,{application:'a',kind:'mysql',query:'UPDATE t'}).flatMap(g=>g.changes.map(c=>c.id)),[3])
 assert.deepEqual(overviewGroups(rows,{query:'not found'}),[])
})
