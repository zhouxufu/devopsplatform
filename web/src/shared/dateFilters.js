export function localDateValue(date, type = 'date') {
  const pad = n => String(n).padStart(2, '0')
  const day = [date.getFullYear(), pad(date.getMonth() + 1), pad(date.getDate())].join('-')
  return type === 'datetime' ? day + 'T' + [date.getHours(), date.getMinutes(), date.getSeconds()].map(pad).join(':') : day
}

// Calendar days rather than fixed 24-hour intervals, including local DST days.
// A date-only consumer still expands its end boundary to 23:59:59.
export function historyDateRange(days, type = 'datetime', now = new Date()) {
  const end = new Date(now), start = new Date(now)
  start.setDate(start.getDate() - days + 1)
  start.setHours(0, 0, 0, 0)
  return [start, end]
}

export function validDateBounds(start, end) {
  if (start && !Number.isFinite(new Date(start).getTime())) return false
  if (end && !Number.isFinite(new Date(end).getTime())) return false
  return !start || !end || new Date(start).getTime() <= new Date(end).getTime()
}
