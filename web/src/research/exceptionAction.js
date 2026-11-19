export function exceptionCommentError(comment) {
  const size = [...String(comment || '').trim()].length
  if (size < 6) return `请填写处理结果或忽略原因（至少 6 个字符，当前 ${size} 个）`
  if (new TextEncoder().encode(comment).length > 8000) return '处理说明过长，请缩短后提交'
  return ''
}
