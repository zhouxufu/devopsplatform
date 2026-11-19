export const runnerKindLabel = kind => kind === 'local' ? '平台本机' : kind || '未指定'
export const runnerLabels = value => [...new Set(String(value || '').split(/[,，]/).map(label => label.trim()).filter(Boolean))]

// Only search public presentation fields, never arbitrary configuration.
export function filterRunners(rows, query = '', status = '') {
  const term = query.trim().toLocaleLowerCase()
  return rows.filter(row => (!status || (status === 'enabled' ? row.enabled === true : row.enabled === false)) &&
    (!term || [row.name, runnerKindLabel(row.kind), ...runnerLabels(row.labels)].some(value => String(value || '').toLocaleLowerCase().includes(term))))
}

export function runnerPayload(form) {
  return {name: String(form.name || '').trim(), kind: 'local', labels: runnerLabels(form.labels).join(','), max_parallel: form.max_parallel, enabled: form.enabled === true}
}
