// Format JSON text lexically, so 64-bit IDs and decimal tokens keep their exact
// spelling. Parsing and serializing the value would round numbers in JavaScript.
export function prettyValue(value) {
  if (value === null) return 'NULL'
  if (typeof value !== 'string') return JSON.stringify(value, null, 2)
  const input = value.trim()
  if (!input.startsWith('{') && !input.startsWith('[')) return value
  try { JSON.parse(input) } catch { return value }
  let out = '', depth = 0, quoted = false, escaped = false
  const line = () => '\n' + '  '.repeat(depth)
  for (let i = 0; i < input.length; i++) {
    if (out.length > 2 * 1024 * 1024) return value
    const ch = input[i]
    if (quoted) {
      out += ch
      if (escaped) escaped = false
      else if (ch === '\\') escaped = true
      else if (ch === '"') quoted = false
    } else if (ch === '"') { quoted = true; out += ch }
    else if (ch === '{' || ch === '[') {
      out += ch
      let next = i + 1
      while (/\s/.test(input[next] || '') && next < input.length) next++
      if ((ch === '{' && input[next] === '}') || (ch === '[' && input[next] === ']')) { out += input[next]; i = next }
      else { depth++; if (depth > 32) return value; out += line() }
    } else if (ch === '}' || ch === ']') { depth--; out += line() + ch }
    else if (ch === ',') out += ',' + line()
    else if (ch === ':') out += ': '
    else if (!/\s/.test(ch)) out += ch
  }
  return out.length > 2 * 1024 * 1024 ? value : out
}
