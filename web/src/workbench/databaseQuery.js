import {quoteIdentifier} from './browserModel.js'

export const filterOperators = ['=', '!=', '>', '>=', '<', '<=', 'LIKE', 'NOT LIKE', 'IN', 'NOT IN', 'IS NULL', 'IS NOT NULL']
export const emptyDataOptions = () => ({conditions: [], logic: 'AND', where: '', filterMode: 'fields', fields: [], order: '', direction: 'ASC', size: 100})
export const copyOptions = value => JSON.parse(JSON.stringify(value))

export function sqlLiteral(value) {
  const text = String(value ?? '')
  if (text.length > 4096) throw Error('单个条件值最多 4096 个字符')
  // Avoid MySQL SQL_MODE-dependent backslash quoting. Normal values remain
  // readable; values containing backslashes use an explicit UTF-8 conversion.
  if (text.includes('\\')) return 'CONVERT(0x' + [...new TextEncoder().encode(text)].map(b => b.toString(16).padStart(2, '0')).join('') + ' USING utf8mb4)'
  return "'" + text.replaceAll("'", "''") + "'"
}

export function tableDataSQL(table, schema, options, offset = 0) {
  const available = new Set(schema.map(row => row.field))
  const column = field => {
    if (!available.has(field)) throw Error('字段已变化，请刷新表结构后重新选择：' + field)
    return quoteIdentifier(field)
  }
  const literal = (field, value, operator) => {
    const type = schema.find(row => row.field === field)?.type || ''
    if (/^(tinyint|smallint|mediumint|int|integer|bigint|decimal|numeric|float|double|real|bit)\b/i.test(type) && !operator.includes('LIKE')) {
      const number = String(value).trim()
      if (!/^[-+]?\d+(?:\.\d+)?(?:[eE][-+]?\d+)?$/.test(number)) throw Error('数字字段 ' + field + ' 请输入有效数值')
      return number // Keep the token text; never round 64-bit IDs through Number.
    }
    return sqlLiteral(value)
  }
  if (!Number.isInteger(options.size) || options.size < 1 || options.size > 1000 || !Number.isInteger(offset) || offset < 0 || offset > 10000) throw Error('分页范围无效')
  if (options.fields.length > 50) throw Error('一次最多选择 50 个字段')
  const projection = options.fields.length ? options.fields.map(column).join(', ') : '*'
  let where = ''
  if (options.filterMode === 'sql') {
    where = options.where.trim().replace(/^where\s+/i, '')
    if (where.length > 4096) throw Error('WHERE 条件最多 4096 个字符')
    if (where) where = '(' + where + ')'
  } else {
    if (options.conditions.length > 10 || !['AND', 'OR'].includes(options.logic)) throw Error('最多添加 10 条条件')
    where = options.conditions.map(condition => {
      const field = column(condition.field), op = condition.operator
      if (!filterOperators.includes(op)) throw Error('筛选运算无效')
      if (op === 'IS NULL' || op === 'IS NOT NULL') return '(' + field + ' ' + op + ')'
      if (op === 'IN' || op === 'NOT IN') {
        const values = String(condition.value).split(',').map(v => v.trim())
        if (values.length > 100 || values.some(v => !v)) throw Error('IN 条件请填写 1–100 个值，以逗号分隔')
        return '(' + field + ' ' + op + ' (' + values.map(value => literal(condition.field, value, op)).join(', ') + '))'
      }
      return '(' + field + ' ' + op + ' ' + literal(condition.field, condition.value, op) + ')'
    }).join(' ' + options.logic + ' ')
  }
  const order = options.order ? [options.order] : schema.filter(row => row.key_type === 'PRI').map(row => row.field)
  if (!['ASC', 'DESC'].includes(options.direction)) throw Error('排序方向无效')
  return 'SELECT ' + projection + ' FROM ' + quoteIdentifier(table) + (where ? ' WHERE ' + where : '') + (order.length ? ' ORDER BY ' + order.map(column).join(', ') + ' ' + options.direction : '') + ' LIMIT ' + options.size + ' OFFSET ' + offset
}
