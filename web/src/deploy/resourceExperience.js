export const resourceCheckLabels={passed:'核验通过',failed:'核验失败',stale:'配置已变化',unbound:'未绑定',unchecked:'未核验',unsupported:'暂不支持',not_applicable:'此环境未使用',pending_source:'源配置待完善',missing_config:'引用配置缺失'}
export const resourceSourceLabels={declared:'源码已声明',not_applicable:'此环境未使用',pending_source:'源配置待完善',missing_config:'引用配置缺失'}
export function resourceUnboundState(requirement,environment){const state=requirement?.sources?.[environment]?.state;return ['not_applicable','pending_source','missing_config'].includes(state)?state:'unbound'}
export const resourceCheckKey=row=>JSON.stringify([row.environment,row.alias])
export function resourceConnectionStatus(connection,transient){
 if(transient==='testing')return '测试中…'
 if(connection.last_check)return resourceCheckLabels[connection.last_check.status]||'未核验'
 return transient==='ok'?'本次测试通过':transient==='fail'?'本次测试失败':'未测试'
}
export function nacosReferenceKey(item){return JSON.stringify([item.environment||'',item.connection||'',item.group,item.data_id])}
export function scopedNacosReference(item,environment,connection){return {...item,environment,connection:connection||item.connection||''}}
export function resourceSaveBody(draft){
 const {checks,error,own_requirements,own_bindings,...rest}=draft
 return {...rest,composition_version:2,own_requirements:draft.requirements||[],own_bindings:draft.bindings||[]}
}
export function resourceDraftDiff(baseline,current){
 const before=baseline||{},after=current||{},changes=[]
 const list=value=>[
  ...(value.bindings||[]).map(b=>({...b,key:resourceCheckKey(b)})),
  ...(value.nacos_configs||[]).map(b=>({...b,key:resourceCheckKey(b),config:{data_id:b.data_id,group:b.group}})),
 ]
 const a=new Map(list(before).map(b=>[b.key,b])),b=new Map(list(after).map(b=>[b.key,b]))
 const target=row=>row?JSON.stringify([row.connection||'',row.config||{}]):''
 for(const key of new Set([...a.keys(),...b.keys()]))if(target(a.get(key))!==target(b.get(key)))changes.push({environment:(b.get(key)||a.get(key)).environment,alias:(b.get(key)||a.get(key)).alias,before:a.get(key),after:b.get(key)})
 const declaration=value=>JSON.stringify([value.requirements||[],value.resource_groups||[],value.group_revisions||{}])
 return {bindings:changes,declarationsChanged:declaration(before)!==declaration(after)}
}
export function resourceEnvironmentCounts(draft,environments,groups=[]){
 const req=[...(draft.requirements||[]),...groups.flatMap(g=>g.requirements||[])]
 const bindings=[...(draft.bindings||[]),...groups.flatMap(g=>g.bindings||[]),...(draft.nacos_configs||[])]
 const allAliases=new Set([...req.map(r=>r.alias),...(draft.nacos_configs||[]).map(r=>r.alias)])
 const checks=new Map((draft.checks||[]).map(c=>[resourceCheckKey(c),c]))
 return environments.map(env=>{const rows=bindings.filter(b=>b.environment===env.name),bound=new Set(rows.map(b=>b.alias)),states={unbound:0,not_applicable:0,pending_source:0,missing_config:0};for(const alias of allAliases)if(!bound.has(alias))states[resourceUnboundState(req.find(r=>r.alias===alias),env.name)]++;return {...env,declared:allAliases.size,bound:bound.size,missing:states.unbound,...states,unchecked:rows.filter(b=>!['passed','failed','stale'].includes(checks.get(resourceCheckKey(b))?.status)).length,passed:rows.filter(b=>checks.get(resourceCheckKey(b))?.status==='passed').length,failed:rows.filter(b=>['failed','stale'].includes(checks.get(resourceCheckKey(b))?.status)).length}})
}

const entryKey=(field,row)=>field==='requirements'?row.alias:resourceCheckKey(row)
const entryValue=(field,row)=>row==null?'':JSON.stringify(field==='requirements'?[row.alias,row.kind,!!row.required,row.description||'',Object.entries(row.sources||{}).sort()]:field==='bindings'?[row.environment,row.alias,row.connection||'',row.config||{}]:[row.environment,row.alias,row.connection||'',row.group,row.data_id])
export function rebaseResourceDraft(baseline,local,remote,choices={}){
 const draft={...remote},conflicts=[]
 for(const field of ['requirements','bindings','nacos_configs']){
  const maps=[baseline,local,remote].map(value=>new Map((value?.[field]||[]).map(row=>[entryKey(field,row),row])))
  draft[field]=[]
  for(const key of new Set(maps.flatMap(m=>[...m.keys()]))){
   const [before,mine,theirs]=maps.map(m=>m.get(key)),b=entryValue(field,before),a=entryValue(field,mine),r=entryValue(field,theirs)
   let chosen
   if(a===b)chosen=theirs
   else if(r===b||a===r)chosen=mine
   else{const id=field+':'+key,choice=choices[id];if(!['local','remote'].includes(choice))conflicts.push({id,field,alias:(mine||theirs||before)?.alias,environment:(mine||theirs||before)?.environment,local:mine,remote:theirs});chosen=choice==='local'?mine:theirs}
   if(chosen)draft[field].push({...chosen,...(chosen.config?{config:{...chosen.config}}:{})})
  }
 }
 const shared=value=>JSON.stringify([value?.resource_groups||[],value?.group_revisions||{}]),b=shared(baseline),a=shared(local),r=shared(remote)
 if(a!==b){if(r!==b&&a!==r&&!choices.shared)conflicts.push({id:'shared',field:'shared',alias:'共享资源',local:local.resource_groups,remote:remote.resource_groups});if(r===b||choices.shared==='local'){draft.resource_groups=[...(local.resource_groups||[])];draft.group_revisions={...local.group_revisions}}}
 const targets=value=>new Map([...(value?.bindings||[]),...(value?.nacos_configs||[]).map(r=>({...r,config:{group:r.group,data_id:r.data_id}}))].map(b=>[resourceCheckKey(b),JSON.stringify([b.connection||'',b.config||{}])]))
 const previousTargets=targets(remote),mergedTargets=targets(draft)
 draft.checks=(remote.checks||[]).map(c=>previousTargets.get(resourceCheckKey(c))!==mergedTargets.get(resourceCheckKey(c))?{...c,status:'stale',message:'合并后的目标已变化，请重新核验'}:{...c})
 return {draft,conflicts}
}
export function mergeDiscoveredResources(draft,candidates){
 const next={...draft,requirements:(draft.requirements||[]).map(r=>({...r})),bindings:(draft.bindings||[]).map(b=>({...b,config:{...b.config}})),nacos_configs:(draft.nacos_configs||[]).map(r=>({...r}))}
 for(const item of candidates){
  if((!item.applyable&&!item.can_declare)||!item.requirement)continue
  const requirement=next.requirements.find(r=>r.alias===item.alias),catalog=next.nacos_configs.find(r=>r.alias===item.alias)
  if(requirement&&requirement.kind!==item.kind)throw Error('资源 '+item.alias+' 已存在其他类型，请先核对别名')
  if(catalog){if(item.kind!=='nacos')throw Error('资源别名与 Nacos 目录引用冲突');if(item.applyable&&item.binding){next.nacos_configs=next.nacos_configs.filter(r=>!(r.alias===item.alias&&r.environment===item.environment));next.nacos_configs.push({alias:item.alias,environment:item.environment,connection:item.connection,data_id:item.data_id,group:item.group})}continue}
  if(!requirement)next.requirements.push({...item.requirement,sources:{...item.requirement.sources}})
  else if(item.requirement.sources)requirement.sources={...requirement.sources,...item.requirement.sources}
  if(!item.applyable||!item.binding)continue
  next.bindings=next.bindings.filter(b=>!(b.environment===item.environment&&b.alias===item.alias));next.bindings.push({...item.binding,config:{...item.binding.config}})
 }
 return next
}

export function resourceEnvironmentComparison(draft,environments,groups=[]){
 const requirements=[...(draft.requirements||[]),...groups.flatMap(g=>g.requirements||[])]
 const bindings=[...(draft.bindings||[]),...groups.flatMap(g=>g.bindings||[]),...(draft.nacos_configs||[]).map(r=>({...r,config:{group:r.group,data_id:r.data_id}}))]
 const aliases=[...new Set([...(draft.requirements||[]).map(r=>r.alias),...groups.flatMap(g=>g.requirements||[]).map(r=>r.alias),...(draft.nacos_configs||[]).map(r=>r.alias)])]
 const checks=new Map((draft.checks||[]).map(c=>[resourceCheckKey(c),c]))
 return aliases.map(alias=>({alias,targets:environments.map(e=>{const binding=bindings.find(b=>b.alias===alias&&b.environment===e.name);return {environment:e.name,alias,binding,status:binding?checks.get(resourceCheckKey(binding))?.status||'unchecked':resourceUnboundState(requirements.find(r=>r.alias===alias),e.name)}})}))
}

// A parsed document with invalid references can be repaired in-place. A failed
// read/corrupt document must never be replaced by an apparently empty draft.
export function resourceDocumentEditable(value){
 if(!value.error)return true
 return value.composition_version===2&&['own_requirements','own_bindings','nacos_configs','resource_groups'].some(field=>Array.isArray(value[field]))
}
