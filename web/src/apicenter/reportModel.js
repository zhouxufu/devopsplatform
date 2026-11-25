export const reportStates={preview:'待确认',processing:'处理中',saved:'已保存',unchanged:'内容一致',historical:'匹配历史',failed:'失败',uncertain:'结果待核对',cancelled:'已取消',expired:'已过期'}
export const reportSources={mcp:'MCP',file:'文件导入',build:'构建生成',edit:'文档编辑'}
export function reportRelation(report){
 if(!report?.snapshot_id)return '未更新文档'
 if(report.state==='historical')return `匹配历史 #${report.snapshot_id} · 当前 #${report.current_id}`
 return report.snapshot_id===report.current_id?'当前文档':`已有更新 #${report.current_id}`
}
export function reportTime(value){return value>0?new Date(value*1000).toLocaleString('zh-CN',{hour12:false}):'未记录'}
export function documentDestination(report,{current=false,operation=''}={}){
 const id=current?report.current_id:report.snapshot_id
 if(!id||!report.service||report.environment==null)return ''
 return '#/documents?'+new URLSearchParams({service:report.service,environment:report.environment||'__unassigned',protocol:report.protocol||'http',snapshot:String(id),...(operation?{operation}:{})})
}
export function openDocumentWorkspace(hash){if(!hash)return;if(window.parent!==window)window.parent.postMessage({type:'workspace.open',plugin:'api-center',hash},location.origin);else window.open(location.pathname+hash,'_blank','noopener')}
