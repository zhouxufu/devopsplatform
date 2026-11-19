export function scenarioTargets(context,selection){
 return Object.fromEntries((context.applications||[]).map(app=>{const target=(app.targets||[]).find(t=>t.key===selection[app.service]);return [app.service,{key:target?.key||'',fingerprint:target?.fingerprint||'',variables_digest:app.variables_digest}]}))
}
export function scenarioBlocker({loading,error,context,environment,caseID,selection,admin}){
 if(loading)return '正在读取场景及所有应用目标'
 if(error)return error
 if(!caseID)return '请选择已保存的业务场景'
 if(!environment)return '所有调用应用需绑定同一环境，请先配置应用环境'
 if(context.protected&&!admin)return '受保护环境需要管理员确认'
 if(!context.applications?.length)return '请刷新场景目标'
 for(const app of context.applications){
  const label=app.display_name||app.service
  if(!app.enabled)return label+' 的接口测试已停用'
  const target=app.targets?.find(t=>t.key===selection[app.service])
  if(!target)return '请选择 '+label+' 的执行入口'
  if(!target.credential_references_valid)return label+' 的认证变量不可用，请检查应用环境配置'
 }
 const c=context.case
 if(c&&Math.max(1,c.data_rows?.length||0)*(c.steps?.length||0)>100)return '单次场景最多 100 步，请拆分数据组'
 return ''
}
