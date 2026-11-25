// Log search owns its complete filter form; other pages retain the shell bar.
export function hasInlineLogContext(workspace) {
  if (workspace?.plugin?.name !== 'logs') return false
  const path = String(workspace.hash || '#/').slice(1).split('?')[0]
  return path === '/' || path === '/search'
}

export function updateWorkspaceContext(context, update) {
  let changed = false
  for (const key of ['service', 'environment']) {
    if (typeof update[key] === 'string' && update[key] !== context[key]) {
      context[key] = update[key]
      changed = true
    }
  }
  const range = update.range, minutes = update.rangeMinutes
  if (Array.isArray(range) && range.length === 2 && range.every(Number.isFinite) && range[0] > 0 && range[1] > range[0] && Number.isFinite(minutes) && minutes >= 0 && minutes <= 1440) {
    if (range.some((value, index) => value !== context.range?.[index]) || minutes !== context.rangeMinutes) {
      context.range = [...range]
      context.rangeMinutes = minutes
      changed = true
    }
  }
  return changed
}
