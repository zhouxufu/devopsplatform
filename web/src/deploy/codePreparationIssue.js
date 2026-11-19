// Only readiness errors may offer this repair route. Other errors keep their original message.
export function codePreparationIssue(message) {
  const match=String(message||'').match(/^代码变更 #([1-9]\d*) (?:尚未就绪|仍为草稿，尚未标记就绪|当前状态无法确认为已就绪)(?:[。；，].*)?$/)
  if(!match)return null
  const id=Number(match[1])
  if(!Number.isSafeInteger(id))return null
  return {
    id,
    message:message===`代码变更 #${id} 尚未就绪`?`代码变更 #${id} 尚未确认就绪。请打开变更核对内容并标记就绪，再回到此页重新检查来源。`:message,
    hash:`#/changes?tab=changes&item=${id}`
  }
}
