import test from 'node:test'
import assert from 'node:assert/strict'
import {formatUnixTime} from './datePresentation.js'

test('missing and malformed metadata dates never show epoch or Invalid Date', () => {
  for (const value of [undefined, null, '', ' ', 0, '0', -1, NaN, Infinity, 'invalid', true, {}, 1e20]) assert.equal(formatUnixTime(value), '—')
})

test('Unix-second metadata accepts numeric strings and preserves display options', () => {
  const seconds = 1793280000
  assert.equal(formatUnixTime(seconds), formatUnixTime(String(seconds)))
  const options = {month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit'}
  assert.equal(formatUnixTime(seconds, options), new Date(seconds * 1000).toLocaleString('zh-CN', {hour12: false, ...options}))
})
