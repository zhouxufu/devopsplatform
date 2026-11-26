import test from 'node:test'
import assert from 'node:assert/strict'
import {listPageSizes,paginationState} from './pagination.js'
test('ordinary lists use one size policy and recover obsolete saved sizes',()=>{
 assert.deepEqual(listPageSizes,[20,50,100])
 assert.deepEqual(paginationState(154),{size:20,pages:8,page:1})
 assert.deepEqual(paginationState(154,3,50),{size:50,pages:4,page:3})
 assert.deepEqual(paginationState(154,9,100),{size:100,pages:2,page:2})
 assert.equal(paginationState(154,1,15).size,20)
})
test('empty and reduced lists retain a valid page boundary',()=>{
 assert.deepEqual(paginationState(0,4,50),{size:50,pages:1,page:1})
 assert.deepEqual(paginationState(21,7,20),{size:20,pages:2,page:2})
})
