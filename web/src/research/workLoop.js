export const loopStages=[['development','开发与变更'],['preparation','代码与制品'],['delivery','环境交付'],['regression','回归测试'],['writeback','状态回写'],['acceptance','人工验收']]
export const loopStateLabels={developing:'待开发',running:'自动推进中',blocked:'需要处理',waiting_human:'等待指定人员',paused:'已暂停',canceled:'授权已撤销',awaiting_acceptance:'待验收',completed:'已完成'}
export function loopCanAdvance(task){return !!task&&!task.paused&&!task.revoked&&task.state==='running'&&task.stage!=='development'}
export function loopCurrent(task){return task?.iterations?.at(-1)||null}
export function loopScopeRows(grant){return Object.entries(grant?.policies||{}).map(([app,p])=>({app,target:p.code_policy?.target_branch||'未配置',mode:p.code_policy?.mode==='integration'?'公共分支':'独立分支',variant:p.build_variant?.name||'默认构建方案'}))}

// Candidates come from this work item's active deliverable changes, never the
// global application catalogue. Deriving candidates does not expand a grant.
export function loopRelatedApplications(changes=[]){
 return [...new Set(changes.filter(c=>['code','artifact'].includes(c.kind)&&['draft','ready'].includes(c.status)&&typeof c.service==='string'&&c.service.trim()).map(c=>c.service))]
}
export function loopApplicationRows(changes=[],services=[],selected=[],related=loopRelatedApplications(changes)){
 const names=new Map(services.map(s=>[s.name,s.display_name||s.name]))
 return [...new Set([...related,...selected])].map(name=>({name,label:names.get(name)||name,related:related.includes(name)}))
}
