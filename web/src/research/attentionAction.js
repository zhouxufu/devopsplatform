// Personal disposition is bound to the observed event and revision, never execution state.
export const attentionSelectionKey = task => JSON.stringify([task.attention_key, task.attention_event, task.attention_revision || 0])
export const canAttend = (task, action) => !!task?.attention_key && !!task?.attention_event && task['can_' + action] === true
export const attentionItem = task => ({id: task.attention_key, event_digest: task.attention_event, revision: task.attention_revision || 0})
export async function submitAttention(tasks, action, reason, requestID, send) {
  const frozen = [...new Map(tasks.filter(t => canAttend(t, action)).map(t => [attentionSelectionKey(t), {...t}])).values()]
  if (!frozen.length) throw Error('所选记录不支持此操作，请刷新后重新选择')
  const result = await send({action, reason, request_id: requestID, items: frozen.map(attentionItem)})
  const succeeded = [], failed = []
  for (const task of frozen) {
    const row = result.results?.find(r => r.id === task.attention_key && r.event_digest === task.attention_event)
    if (row?.success === true) succeeded.push(task)
    else failed.push({...task, error: row?.error || '操作结果未确认，请核对处理记录'})
  }
  return {succeeded, failed}
}
