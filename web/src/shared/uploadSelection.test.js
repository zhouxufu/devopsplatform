import test from 'node:test'
import assert from 'node:assert/strict'
import {uploadSelection} from './uploadSelection.js'
test('one multi-file selection is validated as one batch and never automatically posted by Arco',async()=>{
 const selections=[],choose=uploadSelection(files=>selections.push(files))
 const a={name:'a.png',size:8},b={name:'b.png',size:9}
 assert.equal(choose(a),false);assert.equal(choose(b),false)
 assert.equal(selections.length,0);await Promise.resolve();assert.deepEqual(selections,[[a,b]])
 choose(a);await Promise.resolve();assert.deepEqual(selections,[[a,b],[a]])
})
