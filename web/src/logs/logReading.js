const javaPattern = /^\d{4}-\d\d-\d\d[ T]\d\d:\d\d:\d\d(?:[., ]\d{3})?\s+.*?\b(?:TRACE|DEBUG|INFO|WARN|ERROR|FATAL)\s+\[[^\]\r\n]+\]\s+[\w.$-]+\s*:\s*(.+)$/
const eventFields = /(?:^|\s)((?:event|outcome|reason|error|exception)=(?:"(?:[^"\\]|\\.)*"|[^\s]+))/gi
const timeFormat = new Intl.DateTimeFormat('zh-CN', {hour:'2-digit', minute:'2-digit', second:'2-digit', hourCycle:'h23', fractionalSecondDigits:3})

export function logTime(timestamp, full = false) {
  const date = new Date(timestamp)
  if (timestamp == null || !Number.isFinite(date.getTime())) return '—'
  return (full ? date.toLocaleDateString('sv-SE') + ' ' : '') + timeFormat.format(date)
}

export function logSummary(raw) {
  const firstLine = raw.split(/\r?\n/, 1)[0]
  if (firstLine.startsWith('{')) {
    try {
      const fields = JSON.parse(firstLine)
      const value = fields.message || fields.msg
      if (typeof value === 'string' && value.trim()) return value
    } catch {}
  }
  const body = javaPattern.exec(firstLine)?.[1] || firstLine
  if (/^event=\S/.test(body)) {
    const priority = [...body.matchAll(eventFields)].map(match => match[1])
    if (priority.length > 1) return [...priority, body.replace(eventFields, '').trim()].filter(Boolean).join(' ')
  }
  return body
}

export function queryPattern(query, literal = false) {
  const terms = []
  if (literal) terms.push(query)
  else {
    const tokens = query.match(/(?:[\w-]+:)?"[^"]*"|[()]|[^\s()]+/g) || []
    let excluded = false
    const groups = [false]
    for (const token of tokens) {
      if (token === '(') { groups.push(groups.at(-1) || excluded); excluded = false; continue }
      if (token === ')') { if (groups.length > 1) groups.pop(); excluded = false; continue }
      if (token === 'NOT' || token === '-' || /^(?!message:)[\w-]+:$/.test(token)) { excluded = true; continue }
      const skip = excluded || groups.at(-1)
      excluded = false
      if (skip || token === 'OR' || token === 'AND' || token.startsWith('-') || /^(?!message:)[\w-]+:/.test(token)) continue
      terms.push(token.replace(/^message:/, '').replace(/^"|"$/g, '').replace(/\*$/, ''))
    }
  }
  const escaped = [...new Set(terms.filter(Boolean))].sort((left,right) => right.length-left.length).map(term => term.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'))
  return escaped.length ? new RegExp('('+escaped.join('|')+')','iu') : null
}

export function logPreview(message, pattern) {
  const summary = logSummary(message)
  if (!pattern) return summary
  const text = pattern.test(summary) ? summary : message
  const match = pattern.exec(text)
  if (!match) return summary
  const start = Math.max(text.lastIndexOf('\n', match.index - 1) + 1, match.index - 48)
  const newline = text.indexOf('\n', match.index + match[0].length)
  const end = Math.min(newline === -1 ? text.length : newline, match.index + match[0].length + 200)
  return (start && text[start-1] !== '\n' ? '…' : '') + text.slice(start,end).trimEnd() + (end < text.length && text[end] !== '\n' ? '…' : '')
}

const rowKeys = new WeakMap()
let nextKey = 0
export function logRowKey(hit) {
  if (!rowKeys.has(hit)) rowKeys.set(hit, ++nextKey)
  return rowKeys.get(hit)
}
