// Shared by the workflow node and inbox: a lease is never permission to replay.
export function codePreparationRecovery(job, {now=Date.now(), busy=false, protectedScope=false, scopeConfirmed=false, canManage=true,scopeReview=null,scopeLoading=false,scopeError=''}={}) {
 const state=job?.state
 const recoverable=['conflict','uncertain','failed','merging','building'].includes(state)
 const leaseUntil=Number(job?.lease_until||0)*1000
 const needsRecovery=recoverable&&(!['merging','building'].includes(state)||leaseUntil<=now)
 let reason=''
 if(!canManage)reason='请由本次交付发起人或管理员处理。'
 else if(scopeLoading)reason='正在核对交付范围，请等待差异结果。'
 else if(scopeError)reason='范围差异尚未读取成功，请重新核对差异后再恢复。'
 else if(scopeReview?.changed)reason='交付范围已变化。请查看下方具体差异，终止旧准备后按当前范围重新预览和确认。'
 else if(busy)reason='正在核对任务，请等待本次操作返回。'
 else if(!recoverable)reason='当前任务无需恢复，请核对最新状态。'
 else if(leaseUntil>now)reason='执行器仍在工作或安全等待期内，请等待后核对状态。'
 else if(protectedScope&&!scopeConfirmed)reason='请先勾选确认本次受保护环境的代码范围。'
 const title=state==='conflict'?'代码合并冲突，等待处理':state==='uncertain'?'推送结果待核对':state==='failed'?'代码准备失败，等待处理':needsRecovery?'代码任务中断，等待核对':''
 const instruction=state==='conflict'?'在本地解决下方冲突，将冻结源提交合入指定目标分支并推送，然后在此验证并继续。':state==='uncertain'?'先核对远端目标分支的实际合并结果，再恢复原任务。':state==='failed'?'核对失败原因和现有构建记录后，恢复原任务。':'核对原任务的远端合并结果和现有构建记录后继续。'
 return {needsRecovery,reason,title,instruction,actionLabel:job?.verify_only?'验证合并并继续':'验证合并并继续构建',enabled:needsRecovery&&!reason}
}
