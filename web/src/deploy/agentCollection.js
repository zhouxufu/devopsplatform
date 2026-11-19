// Send an ordinary first-line regex, so existing Agents can use the preset
// through configuration delivery without a binary upgrade.
export const JAVA_MULTILINE = String.raw`^\s*\[?(\d{4}[-/]\d{2}[-/]\d{2}[ T]|\d{2}:\d{2}:\d{2})`

export const MULTILINE_MODES = [
  { value: 'single', label: '单行日志（每行一条）' },
  { value: 'java', label: 'Java 异常堆栈合并（时间戳首行）' },
  { value: 'custom', label: '自定义首行正则' },
]

export function collectEditor(input = {}) {
  const c = input && typeof input === 'object' ? input : {}
  return {
    ...c,
    application_id: typeof c.application_id === 'string' ? c.application_id : '',
    service: typeof c.service === 'string' ? c.service : '',
    paths_text: (Array.isArray(c.paths) ? c.paths : []).filter(path => typeof path === 'string').join('\n'),
    multiline: c.multiline || '',
    // Do not silently change existing single-line or custom configurations.
    multiline_mode: c.multiline === JAVA_MULTILINE ? 'java' : c.multiline ? 'custom' : 'single',
  }
}

export function selectCollectApplication(c, application) {
  // Only recommend a preset when first selecting the application for a new item.
  if (!c.application_id && !c.multiline && c.multiline_mode === 'single' && application?.artifact_type === 'jar') {
    c.multiline_mode = 'java'
  }
  c.application_id = application?.application_id || ''
}

export function collectMultiline(c) {
  if (c.multiline_mode === 'java') return JAVA_MULTILINE
  if (c.multiline_mode === 'custom') return (c.multiline || '').trim()
  return ''
}

// Return an editor location, not just a generic save error. Do not interpret
// Agent's Go/RE2 expressions with the browser's different RegExp grammar.
export function validateCollections(items = []) {
  if (!items.length) return {index: 0, field: 'application_id', message: '请添加至少一个日志采集项'}
  for (const [index, item] of items.entries()) {
    if (!item?.application_id) return {index, field: 'application_id', message: `采集项 ${index + 1}：请选择平台应用`}
    if (!item.paths_text?.split('\n').some(path => path.trim())) return {index, field: 'paths_text', message: `采集项 ${index + 1}：请填写日志绝对路径`}
    if (item.multiline_mode === 'custom' && !collectMultiline(item)) return {index, field: 'multiline', message: `采集项 ${index + 1}：请填写首行正则，或改用单行日志`}
  }
  return null
}
