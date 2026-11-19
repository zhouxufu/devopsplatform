import test from 'node:test'
import assert from 'node:assert/strict'
import {exceptionCommentError as error} from './exceptionAction.js'
test('short comments explain the formerly silent disabled button',()=>{
  assert.match(error('1111'),/至少 6.*当前 4/)
  assert.match(error('   '),/当前 0/)
  assert.equal(error('已核对暂不处理'),'')
})
test('uses server-compatible Unicode character and UTF-8 byte limits',()=>{
  assert.match(error('😀😀😀😀'),/当前 4/)
  assert.equal(error('😀😀😀😀😀😀'),'')
  assert.match(error('文'.repeat(2667)),/过长/)
})
