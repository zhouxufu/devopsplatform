// Presentation/navigation state only: never persist request drafts or execution scope.
export function historyTab(source){return source==='case'?'caseHistory':'manualHistory'}
export function isHistoryTab(tab){return ['manualHistory','caseHistory'].includes(tab)}
export function initialConsoleTab(query,recordsOnly=false){return recordsOnly?'caseHistory':query.view==='history'?historyTab(query.source):'request'}
export function reportReturnTab(tab,run,recordsOnly=false){return isHistoryTab(tab)?tab:recordsOnly?'caseHistory':historyTab(run?.source||(run?.case_id||run?.suite_id?'case':'manual'))}
export function requestName(request){if(request.mode==='dubbo'&&request.rpc)return `DUBBO ${request.rpc.interface}#${request.rpc.method}(${(request.rpc.parameter_types||[]).join(',')})`.slice(0,180);return `${request.method||'GET'} ${(request.path||'/').split(/[?#]/,1)[0]||'/'}`.slice(0,180)}
export function contextBlocker({service,loading,error,context,environment,target,admin}){
 if(!service)return {message:'请选择应用',action:''}
 if(loading)return {message:'正在加载环境和测试目标…',action:''}
 if(error)return {message:'环境与目标读取失败，请重试',action:'refresh'}
 if(!context.environments?.length)return {message:'当前应用尚未配置环境',action:'environment'}
 if(!environment)return {message:'请选择执行环境',action:''}
 if(!context.enabled)return {message:'当前环境未启用接口测试',action:'profile'}
 if(!context.targets?.length)return {message:'当前环境暂无可用访问地址，请配置环境入口或应用机器端口',action:'profile'}
 if(!target)return {message:'请选择测试目标',action:''}
 const env=context.environments.find(e=>e.name===environment)
 if(env?.protected&&!admin)return {message:'受保护环境仅管理员可执行接口测试',action:''}
 return null
}
