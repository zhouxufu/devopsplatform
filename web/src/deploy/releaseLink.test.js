import test from 'node:test'
import assert from 'node:assert/strict'
import {readReleaseLink} from './releaseLink.js'
test('exact release deep link loads beyond current page without substituting another application',async()=>{
 const calls=[];assert.equal((await readReleaseLink('22','orders',async path=>{calls.push(path);return {id:22,service:'orders'}})).id,22);assert.deepEqual(calls,['api/releases/22'])
 await assert.rejects(readReleaseLink('22','orders',async()=>({id:22,service:'other'})),/不匹配/)
 await assert.rejects(readReleaseLink('22','orders',async()=>({id:23,service:'orders'})),/不匹配/)
 for(const id of ['-1','1/other','HEAD','999999999999999999999'])await assert.rejects(readReleaseLink(id,'orders',async()=>assert.fail('bad ID reached network')),/无效/)
})
