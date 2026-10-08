// logs /api/services 为 { service: todayCount }；兼容未来直接返回数组的形式。
export function serviceNames(data) {
  if (Array.isArray(data)) {
    return data.map(x => typeof x === 'string' ? x : x?.name).filter(Boolean).sort()
  }
  return Object.keys(data || {}).sort()
}
