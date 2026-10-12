import test from 'node:test'
import assert from 'node:assert/strict'
import { historyDateRange, localDateValue, validDateBounds } from './dateFilters.js'
test('history presets use inclusive local calendar days', () => {
  const now = new Date(2026, 9, 12, 15, 21, 34)
  for (const [days, value] of [[1, '2026-10-12'], [7, '2026-10-06'], [30, '2026-09-13']]) {
    const [start, end] = historyDateRange(days, 'date', now)
    assert.equal(localDateValue(start), value)
    assert.equal(localDateValue(end), '2026-10-12')
    assert.equal(end.getTime(), now.getTime())
    assert.equal(start.getHours(), 0)
  }
  assert.equal(localDateValue(now, 'datetime'), '2026-10-12T15:21:34')
})
test('optional bounds and clearing remain valid, inverted or malformed bounds do not', () => {
  for (const pair of [['', ''], ['2026-10-06', ''], ['', '2026-10-06'], ['2026-10-06', '2026-10-06']]) assert(validDateBounds(...pair))
  assert(!validDateBounds('2026-10-07', '2026-10-06'))
  assert(!validDateBounds('bad', ''))
  assert(!validDateBounds('', 'bad'))
})
