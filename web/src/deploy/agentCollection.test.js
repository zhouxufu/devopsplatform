import test from 'node:test'
import assert from 'node:assert/strict'
import { JAVA_MULTILINE, collectEditor, collectMultiline, selectCollectApplication } from './agentCollection.js'
import { LOG_LEVELS } from '../logs/levels.js'

test('only the four common log levels are selectable', () => {
  assert.deepEqual(LOG_LEVELS, ['DEBUG', 'INFO', 'WARN', 'ERROR'])
})

test('Java preset identifies common timestamp headers but not exception continuations', () => {
  const start = new RegExp(JAVA_MULTILINE)
  for (const line of [
    '2026-09-16 10:24:37.025 [Dubbo] ERROR Failed to get metadata',
    '[2026-09-16 10:24:37,025] ERROR failed',
    '2026-09-16T10:24:37.025+08:00 ERROR failed',
    '2026/09/16 10:24:37 ERROR failed',
    '  10:24:37.025 [Dubbo] ERROR failed',
  ]) assert.ok(start.test(line), line)
  for (const line of [
    'org.apache.dubbo.rpc.RpcException: Failed to invoke remote method',
    '\tat org.apache.dubbo.rpc.protocol.dubbo.DubboInvoker.doInvoke(DubboInvoker.java:158)',
    'Caused by: java.net.ConnectException: Connection refused',
    '\tSuppressed: java.io.IOException: closed',
    '\t... 12 more',
    '',
  ]) assert.equal(start.test(line), false, line)
})

test('existing modes survive editing and saving without silent changes', () => {
  for (const [pattern, mode] of [['', 'single'], [JAVA_MULTILINE, 'java'], [String.raw`^\d{4}-`, 'custom']]) {
    const c = collectEditor({ application_id: 'orders', paths: ['/var/log/orders.log'], multiline: pattern })
    assert.equal(c.multiline_mode, mode)
    assert.equal(collectMultiline(c), pattern)
    selectCollectApplication(c, { application_id: 'orders', artifact_type: 'jar' })
    assert.equal(collectMultiline(c), pattern)
    assert.equal(collectEditor({ multiline: collectMultiline(c) }).multiline_mode, mode)
  }
})

test('new Java collection recommends merging; other application types stay single-line', () => {
  for (const kind of ['jar', 'static', 'image']) {
    const c = collectEditor()
    selectCollectApplication(c, { application_id: 'new-app', artifact_type: kind })
    assert.equal(c.multiline_mode, kind === 'jar' ? 'java' : 'single')
    assert.equal(collectMultiline(c), kind === 'jar' ? JAVA_MULTILINE : '')
  }
})

test('switching to single-line clears the outgoing regex, custom text is preserved while switching', () => {
  const c = collectEditor({ multiline: '^START' })
  c.multiline_mode = 'java'
  assert.equal(collectMultiline(c), JAVA_MULTILINE)
  c.multiline_mode = 'single'
  assert.equal(collectMultiline(c), '')
  c.multiline_mode = 'custom'
  assert.equal(collectMultiline(c), '^START')
})

test('collectEditor tolerates legacy null collection entries', () => {
  assert.deepEqual(collectEditor(null), {
    application_id: '', service: '', paths_text: '', multiline: '', multiline_mode: 'single',
  })
  assert.equal(collectEditor({ paths: null }).paths_text, '')
})
