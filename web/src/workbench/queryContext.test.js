import test from 'node:test'
import assert from 'node:assert/strict'
import {queryContext} from './browserModel.js'
test('browser results and approval restoration cannot cross databases, environments or aliases',()=>{
 const q={environment:'test',connection:'instance',database:'orders',sql:'SELECT id FROM orders'}
 assert.notEqual(queryContext(q),queryContext({...q,database:'billing'}))
 assert.notEqual(queryContext(q),queryContext({...q,environment:'prod'}))
 assert.equal(queryContext(q),queryContext({...q,sql:'SELECT name FROM orders'}))
 assert.equal(queryContext({...q,database:undefined}),queryContext({...q,database:''}))
 const app={environment:'test',service:'orders',resource_alias:'db'}
 assert.notEqual(queryContext(app),queryContext({...app,resource_alias:'audit'}))
 assert.notEqual(queryContext(app),queryContext({...app,service:'billing'}))
})
