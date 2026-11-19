const groupNames = {nacos:'Nacos 配置',mysql:'MySQL 变更',redis:'Redis 命令',manual:'人工处理'}

export function resourceContentGroups(changes = []) {
  const groups = new Map()
  for (const change of changes) {
    if (['code','artifact'].includes(change.kind)) continue
    if (!groups.has(change.kind)) groups.set(change.kind, {kind:change.kind,label:groupNames[change.kind] || change.kind || '其他变更',changes:[]})
    groups.get(change.kind).changes.push(change)
  }
  return [...groups.values()]
}

export function sqlContentSelection(change, environment) {
  const sql = change.sql
  if (!sql) return {label:'缺少 SQL 内容',content:'',missing:true}
  const override = sql.environment_overrides?.find(item => item.environment === environment)
  if (override) return {label:override.skip?'本环境无需执行':'本环境专用 SQL',content:override.skip?'':override.content || '',skip:!!override.skip,reason:override.reason || ''}
  return {label:sql.data_sync?'数据同步 SQL':'默认 SQL',content:sql.content || ''}
}

export function resourceContentSummary(change, environment) {
  if (change.kind === 'nacos') return change.nacos?.restore?'恢复历史配置快照':`${change.nacos?.entries?.length || 0} 个配置项`
  if (change.kind === 'mysql') return sqlContentSelection(change,environment).label
  if (change.kind === 'redis') return `${String(change.redis?.commands || '').split(/\r?\n/).filter(line => line.trim()).length} 行命令`
  return change.summary || change.reference || '展开查看处理说明'
}

export function nodeContentTargets(node, changes = [], applications = []) {
  if (node.kind === 'deploy') return {applications:node.service?applications.filter(name => name === node.service):applications,changeIds:[]}
  const ids = node.change_ids || []
  return {applications:[],changeIds:changes.filter(change =>
    change.kind === node.kind && (!node.service || node.service === change.service) &&
    (ids.length ? ids.includes(change.id) : ['mysql','nacos','redis'].includes(node.kind))
  ).map(change => change.id)}
}

// Keep the reviewed target/diff for this change only; never mix another node's changes into a card.
export function resourceChangePreview(preview, change) {
  const id = change.id
  if (change.kind === 'nacos') return (preview?.nacos_plans || []).map(plan => ({...plan,configs:(plan.configs || []).filter(item => item.change_id === id)})).filter(plan => plan.configs.length)
  if (change.kind === 'redis') return (preview?.redis_plans || []).map(plan => ({...plan,changes:(plan.changes || []).filter(item => item.change_id === id)})).filter(plan => plan.changes.length)
  if (change.kind === 'mysql') return (preview?.sql_plans || []).map(plan => ({...plan,
    statements:(plan.statements || []).filter(item => item.change_id === id),
    script_selections:(plan.script_selections || []).filter(item => item.change_id === id),
    applied:(plan.applied || []).filter(item => item.change_id === id)
  })).filter(plan => plan.statements.length || plan.script_selections.length || plan.applied.length)
  return []
}
