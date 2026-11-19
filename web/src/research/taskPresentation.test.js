import test from 'node:test'
import assert from 'node:assert/strict'
import {taskActivityTime, taskActivityAge, taskActivityStamp} from './taskPresentation.js'

test('missing or invalid task timestamps never display NaN or an epoch date', () => {
  for (const value of [undefined, null, '', ' ', 0, '0', -1, 'invalid', NaN, Infinity, {}, true, 1e20]) {
    const task = {updated_at: value, at: value}
    assert.equal(taskActivityTime(task), null)
    assert.equal(taskActivityAge(task), '—')
    assert.equal(taskActivityStamp(task), '暂无活动时间')
  }
})

test('task activity prefers a valid update and falls back to the original activity', () => {
  assert.equal(taskActivityTime({updated_at: '1793282400', at: 1793280000}), 1793282400)
  assert.equal(taskActivityTime({updated_at: 'invalid', at: 1793280000}), 1793280000)
  assert.equal(taskActivityTime({updated_at: 0, at: 1793280000}), 1793280000)
})

test('task relative age handles minute, hour, day and future-clock boundaries', () => {
  const task = {at: 1793280000}, now = task.at * 1000
  for (const [delta, expected] of [[-60000, '刚刚'], [0, '刚刚'], [60000, '1 分钟前'], [3600000, '1 小时前'], [86400000, '1 天前']]) {
    assert.equal(taskActivityAge(task, now + delta), expected)
  }
})
