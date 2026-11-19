// Task APIs use Unix seconds. Missing activity is unknown, never an elapsed date.
export function taskActivityTime(task) {
  for (const value of [task?.updated_at, task?.at]) {
    if (typeof value !== 'number' && typeof value !== 'string') continue
    const seconds = Number(value)
    if (Number.isFinite(seconds) && seconds > 0 && Number.isFinite(new Date(seconds * 1000).getTime())) return seconds
  }
  return null
}

export function taskActivityAge(task, now = Date.now()) {
  const seconds = taskActivityTime(task)
  if (seconds === null || !Number.isFinite(now)) return '—'
  const minutes = Math.max(0, Math.floor((now - seconds * 1000) / 60000))
  if (minutes < 1) return '刚刚'
  if (minutes < 60) return minutes + ' 分钟前'
  if (minutes < 1440) return Math.floor(minutes / 60) + ' 小时前'
  return Math.floor(minutes / 1440) + ' 天前'
}

export function taskActivityStamp(task) {
  const seconds = taskActivityTime(task)
  return seconds === null ? '暂无活动时间' : new Date(seconds * 1000).toLocaleString('zh-CN', {hour12: false})
}
