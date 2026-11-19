import test from 'node:test'
import assert from 'node:assert/strict'
import {filterRunners, runnerKindLabel, runnerLabels, runnerPayload} from './resourceRunners.js'

const rows = [{name:'demo-build',kind:'local',labels:'demo, java21',enabled:true}, {name:'local',kind:'local',labels:'darwin,local',enabled:false,secret:'hidden-value'}]
test('Runner search and status use public fields without inferring health', () => {
  assert.equal(runnerKindLabel('local'), '平台本机')
  assert.equal(runnerKindLabel('future-runner'), 'future-runner')
  assert.deepEqual(filterRunners(rows, 'JAVA21').map(row => row.name), ['demo-build'])
  assert.equal(filterRunners(rows, '平台本机').length, 2)
  assert.deepEqual(filterRunners(rows, '', 'disabled').map(row => row.name), ['local'])
  assert.equal(filterRunners(rows, 'hidden-value').length, 0)
  assert.equal(rows[0].enabled, true)
})
test('Runner write payload only contains supported fields; labels are normalized', () => {
  assert.deepEqual(runnerLabels('java21, docker，java21, '), ['java21','docker'])
  assert.deepEqual(runnerPayload({name:' demo ',kind:'ssh',labels:'a，b,a',max_parallel:2,enabled:true,secret:'hidden'}), {name:'demo',kind:'local',labels:'a,b',max_parallel:2,enabled:true})
})
