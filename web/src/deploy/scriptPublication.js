export function scriptPublicationError(config={}){
 if(!config.script?.trim())return '请填写发布脚本'
 if(!config.script_runner)return '请选择脚本执行 Runner'
 const seconds=Number(config.script_timeout_seconds)
 if(!Number.isInteger(seconds)||seconds<1||seconds>3600)return '脚本发布超时须为 1–3600 秒'
 let args;try{args=JSON.parse(config.script_arguments||'[]')}catch{return '脚本参数须为 JSON 字符串数组'}
 if(!Array.isArray(args)||args.length>30||args.some(v=>typeof v!=='string'||v.includes('\0')||v.length>4096))return '脚本参数须为不超过 30 项的字符串数组'
 return ''
}
export function scriptReconciliationPayload(order,draft={}){
 const state=order?.script_publication
 if(!state?.needs_reconciliation)throw Error('发布单不在脚本结果待核对状态')
 if(!draft.confirm_stopped)throw Error('请先确认原脚本已结束，并核对外部执行状态')
 if(!draft.comment?.trim())throw Error('请填写外部状态核对依据')
 if(!['success','failed'].includes(draft.result))throw Error('请选择核对结果')
 return {deploy_id:state.deploy_id,artifact_id:order.artifact_id,digest:state.artifact_digest,config_digest:state.config_digest,result:draft.result,comment:draft.comment.trim(),confirm_stopped:true}
}
