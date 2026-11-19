// Metadata timestamps use Unix seconds; source ISO dates and log milliseconds
// have separate formatters and must not be silently reinterpreted here.
export function formatUnixTime(value, options = {}) {
  if (typeof value !== 'number' && typeof value !== 'string') return '—'
  const seconds = Number(value)
  if (!Number.isFinite(seconds) || seconds <= 0) return '—'
  const date = new Date(seconds * 1000)
  return Number.isFinite(date.getTime()) ? date.toLocaleString('zh-CN', {hour12: false, ...options}) : '—'
}
