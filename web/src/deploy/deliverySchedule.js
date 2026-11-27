export function canDispose(node) {
  return !!node && node.kind !== 'approval' && !node.approvers?.length && !node.monitor && !(node.kind === 'verify' && node.verification?.mode === 'manual')
}
export function dependentNodes(nodes, id) {
  const affected = new Set([id])
  return nodes.filter(node => {
    if (!node.depends_on?.some(dep => affected.has(dep))) return false
    affected.add(node.id)
    return true
  })
}
export function focusStep(steps, statusOf = step => step.status) {
  for (const states of [['failed','interrupted','uncertain','needs_review'], ['executing','running','waiting','queued','waiting_next_batch'], ['pending_target','pending','reviewing']]) {
    const index = steps.findIndex(step => states.includes(statusOf(step)))
    if (index >= 0) return index
  }
  return 0
}
export function waitingSteps(steps, node) {
  return steps.filter(step => node?.depends_on?.includes(step.node_id) && !['success','manual_done'].includes(step.status))
}
