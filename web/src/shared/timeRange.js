export const DEFAULT_RECENT_MINUTES = 15
export const RECENT_PRESETS = [15, 60, 360, 1440, 4320, 10080]

export function recentRange(minutes = DEFAULT_RECENT_MINUTES, now = Date.now()) {
  const value = Number(minutes)
  const safeMinutes = Number.isFinite(value) && value > 0 ? value : DEFAULT_RECENT_MINUTES
  return [now - safeMinutes * 60000, now]
}

export function validRange(value) {
  return Array.isArray(value) && value.length === 2 && value.every(Number.isFinite) && value[1] > value[0]
}

export function inferRecentMinutes(value, now = Date.now(), endToleranceMs = 30000) {
  if (!validRange(value) || Math.abs(now - value[1]) > endToleranceMs) return 0
  const duration = value[1] - value[0]
  return RECENT_PRESETS.find(minutes => Math.abs(duration - minutes * 60000) < 2000) || 0
}

export function restoreTimeRange(saved = {}, now = Date.now()) {
  const explicit = Number(saved.rangeMinutes)
  if (Number.isFinite(explicit) && explicit > 0) {
    return { range: recentRange(explicit, now), rangeMinutes: explicit }
  }
  if (validRange(saved.range)) {
    const inferred = inferRecentMinutes(saved.range, now, 10 * 60000)
    if (inferred) return { range: recentRange(inferred, now), rangeMinutes: inferred }
    // Old recent ranges were stored as absolute timestamps. Once expired they
    // must reopen at the current time instead of silently hiding new data.
    if (now - saved.range[1] >= 10 * 60000) {
      return { range: recentRange(DEFAULT_RECENT_MINUTES, now), rangeMinutes: DEFAULT_RECENT_MINUTES }
    }
    return { range: saved.range, rangeMinutes: 0 }
  }
  return { range: recentRange(DEFAULT_RECENT_MINUTES, now), rangeMinutes: DEFAULT_RECENT_MINUTES }
}

export function resolveTimeRange(value, rangeMinutes, now = Date.now()) {
  return Number(rangeMinutes) > 0 ? recentRange(rangeMinutes, now) : value
}
