// Values stay only in the current editor draft; never use view/session memory.
export function editRuntimeVariable(raw, key) {
  const text = String(raw || '')
  let offset = 0
  for (const line of text.split('\n')) {
    const separator = line.indexOf('=')
    if (separator > 0 && line.slice(0, separator).trim() === key) {
      return {text, start: offset + separator + 1, end: offset + line.length}
    }
    offset += line.length + 1
  }
  const next = text + (text && !text.endsWith('\n') ? '\n' : '') + key + '='
  return {text: next, start: next.length, end: next.length}
}

export function runtimeEnvironmentUpdate({runtime_env_text: raw = '', runtime_env_delete_keys: removed = [], runtime_env_clear: clear = false}) {
  const patch = Object.create(null)
  for (const [index, original] of String(raw).split(/\r?\n/).entries()) {
    const line = original.trim()
    if (!line || line.startsWith('#')) continue
    const separator = original.indexOf('=')
    if (separator < 1) throw new Error(`第 ${index + 1} 行应为 KEY=value`)
    const key = original.slice(0, separator).trim()
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(key)) throw new Error(`第 ${index + 1} 行变量名格式错误`)
    if (Object.hasOwn(patch, key)) throw new Error(`第 ${index + 1} 行变量名重复：${key}`)
    const value = original.slice(separator + 1)
    if (/[\x00\r]/.test(value)) throw new Error(`第 ${index + 1} 行变量值包含不支持的字符`)
    patch[key] = value
  }
  if (clear) {
    if (Object.keys(patch).length || removed.length) throw new Error('清空全部变量时不能同时新增、更新或单独删除，请选择一种操作')
    return {runtime_env: {}}
  }
  for (const key of removed) {
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(key)) throw new Error('待删除的变量名格式错误')
    if (Object.hasOwn(patch, key)) throw new Error(`变量 ${key} 已标记删除，请撤销删除后再更新`)
    patch[key] = null
  }
  return Object.keys(patch).length ? {runtime_env_patch: patch} : {}
}
