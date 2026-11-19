export const quoteKey = key => JSON.stringify(String(key))
// Results and approved browser queries belong to the selected database as well
// as the connection; never restore them into another schema on the same server.
export const queryContext = q => JSON.stringify([q.environment,q.connection||'',q.database||'',q.service||'',q.resource_alias||''])
export const quoteIdentifier = name => {
  if (!/^[A-Za-z_][A-Za-z0-9_]{0,63}$/.test(name)) throw Error('此表名暂不支持快捷浏览，请使用数据库客户端查看')
  return '`' + name + '`'
}
export function redisContentCommand(key, type, cursor = '0', offset = 0) {
  const k = quoteKey(key)
  switch (type) {
    case 'string': return `GET ${k}`
    case 'hash': return `HSCAN ${k} ${cursor} COUNT 50`
    case 'set': return `SSCAN ${k} ${cursor} COUNT 50`
    case 'zset': return `ZSCAN ${k} ${cursor} COUNT 50`
    case 'list': return `LRANGE ${k} ${offset} ${offset + 49}`
    default: return ''
  }
}
// Never discard a batch then advance its cursor. On reaching the display cap,
// further scanning stops; the UI explicitly states the result is incomplete.
export function appendKeys(existing, incoming, limit = 1000) {
  const unique = [...new Set([...existing, ...incoming.map(String)])]
  const bounded = boundItems(unique, limit)
  return {keys: bounded.items, capped: bounded.capped}
}
function boundItems(items, limit = 1000) {
  const out = [], encoder = new TextEncoder()
  let bytes = 0
  for (const item of items) {
    const size = encoder.encode(JSON.stringify(item)).byteLength
    if (out.length >= limit || bytes + size > 2 * 1024 * 1024) return {items: out, capped: true}
    out.push(item); bytes += size
  }
  return {items: out, capped: out.length >= limit}
}
export function appendRedisValues(type, existing, incoming) {
  let merged
  if (type === 'hash' || type === 'zset') {
    merged = [...new Map([...redisValues(type, existing), ...redisValues(type, incoming)].map(row => [row.name, row.value]))]
  } else if (type === 'set') merged = [...new Set([...existing, ...incoming])]
  else merged = [...existing, ...incoming]
  const bounded = boundItems(merged)
  return {values: type === 'hash' || type === 'zset' ? bounded.items.flat() : bounded.items, capped: bounded.capped}
}
export function redisValues(type, value) {
  const values = Array.isArray(value) ? value : []
  if (type === 'hash' || type === 'zset') {
    const out = []
    for (let i = 0; i < values.length; i += 2) out.push({name: String(values[i]), value: values[i + 1]})
    return out
  }
  return values.map((v, i) => ({name: String(i), value: v}))
}
export {prettyValue} from '../shared/prettyValue.js'
