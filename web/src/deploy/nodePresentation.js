export const nodeKinds = {extension:'插件节点',verify:'独立验证',script:'交付脚本',approval:'人工审核',deploy:'发布应用',manual:'人工确认',mysql:'SQL / 数据同步变更',nacos:'Nacos 配置',redis:'Redis 命令',xxl_job:'定时任务','code-prepare':'自动合并并构建','code-verify':'核对人工合入并构建'}
export const nodeKindLabel = node => nodeKinds[node.kind] || node.kind || '未选择类型'
export function nodeTypeWarning(node) {
  return /审核|审批|会签|或签/.test(node.name || '') && node.kind !== 'approval'
    ? `名称包含审核，但实际类型是“${nodeKindLabel(node)}”，不会作为审批节点执行。请核对节点类型。` : ''
}
