// Presentation/navigation only. Environment preflight stays in orchestration.
// Editors keep inactive type drafts in memory. Only validate/send the active type.
export function deliveryChangePayload(form){
 return {...form,sql:form.kind==='mysql'?form.sql:undefined,nacos:form.kind==='nacos'?form.nacos:undefined,redis:form.kind==='redis'?form.redis:undefined,xxl_job:form.kind==='xxl_job'?form.xxl_job:undefined}
}
export function savedDeliverySelection(submitted,saved,knownIDs=[]){
 if(!submitted)return 0
 if(submitted.id)return submitted.id
 const known=new Set(knownIDs)
 const candidates=(saved||[]).filter(c=>!known.has(c.id)&&c.kind===submitted.kind&&(c.service||'')===(submitted.service||'')&&c.title===submitted.title.trim())
 return candidates.length===1?candidates[0].id:0 // Never guess between concurrent/duplicate additions.
}
export function deliveryEntryProgress(rows,dirty=false){
 const editable=rows.filter(row=>!row.readonly),ready=rows.filter(row=>row.status==='ready')
 return {editable:editable.length,ready:ready.length,total:rows.length,canContinue:!dirty&&!!rows.length&&ready.length===rows.length,
  hint:!rows.length?'添加本次涉及的代码、SQL 或配置变更':dirty?'保存草稿可稍后继续；校验并就绪后进入交付':editable.length?'填写完成后可直接校验并就绪':rows.length&&ready.length===rows.length?'内容已就绪，可发起交付或纳入版本':'仍有只读草稿待维护人补齐'}
}
export function deliveryResourceOptions(document,kind){
 return (document?.requirements||[]).filter(r=>r.kind===kind).map(r=>{
  const targets=[...new Set((document.bindings||[]).filter(b=>b.alias===r.alias).map(b=>b.config?.data_id).filter(Boolean))]
  const description=r.description&&!/^(Nacos 动态数据源|Nacos spring\.|已拉取源码|用户提供|按环境选择)/.test(r.description)?r.description:''
  const name=r.display_name||description||(targets.length===1?targets[0]:r.alias)
  return {value:r.alias,label:name===r.alias?r.alias:`${name} · ${r.alias}`}
 })
}
export function deliveryNextWorkspace(action,id){
 if(action==='changes'){
  if(!Array.isArray(id)||!id.length||!id.every(value=>Number.isSafeInteger(value)&&value>0))return null
  return {plugin:'deploy',hash:'#/orchestration?changes='+[...new Set(id)].join(',')}
 }
 if(!Number.isSafeInteger(Number(id))||Number(id)<1)return null
 if(action==='delivery')return {plugin:'deploy',hash:'#/orchestration?work_items='+Number(id)}
 if(action==='version')return {plugin:'research',hash:'#/versions?add_work_item='+Number(id)}
 return null
}
