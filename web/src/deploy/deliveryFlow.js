const labels={draft:'草稿',blocked:'需要处理',pending_approval:'待审批',waiting_confirmation:'待人工确认',approved:'待执行',running:'执行中',success:'执行完成',failed:'执行失败',canceled:'已终止',rejected:'已拒绝',uncertain:'结果待核对',timed_out:'超时待核对',waiting_next_batch:'待批次确认',waiting_manual:'待手动发布'}
export function deliveryStateLabel(run={}){
 if(run.status==='success'&&(run.preparation||run.plan?.preparation_request||run.stage?.includes('前置')))return '准备完成 · 尚未发布'
 if(run.status==='blocked')return run.current_kind?.startsWith('code-')||run.kind?.startsWith('code-')||run.steps?.some(s=>s.kind?.startsWith('code-')&&s.status==='blocked')?'代码准备待处理':'需要处理'
 return labels[run.status]||'正在核对状态'
}
export function deliveryFlow(run,phase=1){
 const stages=['选择交付内容','确认交付范围','准备代码与制品','发布预检与审批','执行与验证']
 if(!run)return {stages,index:phase>=3?3:0,label:'交付准备',next:'在当前页面选择范围和环境，预览确认后继续。'}
 const prep=!!(run.preparation||run.plan?.preparation_request),step=run.steps?.find(s=>!['success','skipped'].includes(s.status))
 let index=prep?step?.kind?.startsWith('code-')||step?.kind==='verify'?2:run.status==='success'?3:1:run.status==='pending_approval'||run.status==='approved'?3:4
 let next=run.next_action||(run.status==='blocked'?'在下方处理当前阻塞；原任务和已完成结果保留。':prep&&run.status==='success'?'准备已完成，在本页继续预检并提交发布审批。':prep?'当前节点状态会自动更新；需要人工确认时在下方处理。':run.status==='success'?'本次执行已完成；当前运行健康仍以实际检测为准。':'在下方核对当前节点并处理，状态自动更新。')
 return {stages,index,label:deliveryStateLabel(run),next,complete:!prep&&run.status==='success'}
}
export const deliveryHash=record=>record.preparation?'#/orchestration?preparation='+(record.run_id||record.id):'#/orchestration?run='+(record.run_id||record.id)
