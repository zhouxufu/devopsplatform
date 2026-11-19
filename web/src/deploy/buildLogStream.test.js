import test from 'node:test'
import assert from 'node:assert/strict'
import {createBuildLogStream} from './buildLogStream.js'

test('socket history replaces the earlier HTTP snapshot rather than repeating the build', () => {
  const stream = createBuildLogStream()
  const history = '\n▶ 流水线 #3\n\n━━ 拉取源码 ━━\n⬇ fetch'
  let shown = stream.receive(history, history + '\nHEAD is now at commit')
  shown = stream.receive(shown, '━━ Java 构建 ━━\n')
  assert.equal(shown, history + '\nHEAD is now at commit\n━━ Java 构建 ━━')
  assert.equal(shown.split('▶ 流水线').length - 1, 1)
})
test('reconnection replaces stale output and restores records missed while disconnected', () => {
  const stream = createBuildLogStream()
  let shown = stream.receive('', '\nstart')
  shown = stream.receive(shown, 'before disconnect\n')
  stream.opened()
  shown = stream.receive(shown, '\nstart\nbefore disconnect\nwhile disconnected')
  assert.equal(stream.receive(shown, 'finished\n'), '\nstart\nbefore disconnect\nwhile disconnected\nfinished')
})
test('legitimate repeated records and internal blank lines remain intact', () => {
  const stream = createBuildLogStream()
  let shown = stream.receive('', '')
  shown = stream.receive(shown, 'still running\n')
  shown = stream.receive(shown, 'still running\n')
  shown = stream.receive(shown, '\nnext stage\n\n')
  assert.equal(shown, '\nstill running\nstill running\n\nnext stage')
})
