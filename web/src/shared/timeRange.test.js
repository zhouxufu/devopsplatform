import test from 'node:test'
import assert from 'node:assert/strict'
import { inferRecentMinutes, recentRange, resolveTimeRange, restoreTimeRange } from './timeRange.js'

test('relative range always advances to the current time', () => {
  assert.deepEqual(resolveTimeRange([1, 2], 15, 2_000_000), recentRange(15, 2_000_000))
})

test('old absolute recent range migrates to a current sliding window', () => {
  const now = 2_000_000
  const old = [now - 30 * 60000, now - 15 * 60000]
  assert.deepEqual(restoreTimeRange({ range: old }, now), { range: recentRange(15, now), rangeMinutes: 15 })
})

test('custom time range remains fixed', () => {
  const now = 2_000_000
  const custom = [now - 7 * 60000, now - 2 * 60000]
  assert.deepEqual(restoreTimeRange({ range: custom }, now), { range: custom, rangeMinutes: 0 })
})

test('picker presets are recognized only when ending near now', () => {
  const now = 2_000_000
  assert.equal(inferRecentMinutes([now - 60 * 60000, now], now), 60)
  assert.equal(inferRecentMinutes([now - 120 * 60000, now - 60 * 60000], now), 0)
})
