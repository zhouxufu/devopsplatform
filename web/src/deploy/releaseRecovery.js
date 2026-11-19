import {activeReleaseStatuses} from './releasePolling.js'
export function blockingRelease(orders,service,environment){
 return (orders||[]).find(o=>o.service===service&&o.environment===environment&&activeReleaseStatuses.includes(o.status))||null
}
export function canRecheckJavaRelease(order){
 const t=order?.target_snapshot
 return order?.status==='needs_review'&&t?.target_type==='ssh-jar'&&t?.deploy_config?.java_mode==='managed'&&!t?.deploy_config?.deployment_agent_id&&!t?.deploy_config?.deployment_agent_ids&&t?.deploy_config?.deployment_transport!=='agent'
}
export function originalReviewDeploys(order,deploys){
 const ids=new Set(order?.deploy_ids||[])
 return (deploys||[]).filter(d=>ids.has(d.id)&&d.service===order.service&&d.environment===order.environment&&d.artifact_id===order.artifact_id&&d.status==='needs_review')
}

export function autoRecheckBusy(state,now=Date.now()/1000){
 return state?.state==='checking'&&Number(state.lease_until)>now
}
export function autoRecheckText(state){
 if(!state)return '等待后台安排自动核对'
 if(state.state==='manual')return '需人工核对 · '+(state.message||'自动核对未能确认原发布结果')
 if(state.state==='checking')return '正在自动核对 · 第 '+state.attempts+' 次'
 if(state.state==='scheduled')return '等待自动核对'+(state.next_at?' · 下次 '+new Date(state.next_at*1000).toLocaleTimeString('zh-CN'):'')+(state.message?' · '+state.message:'')
 return state.message||'原发布结果已确认'
}
