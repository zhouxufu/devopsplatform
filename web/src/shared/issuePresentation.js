// Presentation only: keep source identifiers unchanged in requests and snapshots.
export function sourcePerson(value, labels = {}) {
  const account = String(value || '').trim()
  if (!account || account.toLowerCase() === 'closed') return '—'
  return labels[account] || account
}
export const issueStatus = item => {
 const status=item?.external_status||item?.status
 if(item?.source==='manual')return ({open:'待处理',active:'处理中',resolved:item?.kind==='bug'?'待回归':'已完成',closed:'已关闭'})[status]||status||'—'
 return ({active:'激活',resolved:'已解决',closed:'已关闭',open:'进行中',draft:'草稿',ready:'已就绪',completed:'已完成'})[status]||status||'—'
}
export function issueDate(value) {
  if (!value || /^0{4}-0{2}-0{2}/.test(String(value))) return '—'
  const date = new Date(typeof value === 'number' ? value * 1000 : value)
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString('zh-CN', {hour12:false})
}
export function personOptions(accounts = [], labels = {}) {
  return [...new Set(accounts)].filter(value => sourcePerson(value) !== '—').map(value => ({value, label:labels[value] && labels[value] !== value ? `${labels[value]}（${value}）` : value}))
}
export const workSelectionKey = item => item.id ? String(item.id) : `${item.source}:${item.external_id}`
