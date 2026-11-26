import {workspaceNavigation} from './navigation.js'
import {workspaceTarget} from './workspaceTarget.js'
const withoutPageLabel=(value,base)=>value===base?'':value.startsWith(base+' · ')?value.slice(base.length+3):value
// Labels describe a plugin's actual page/target, never another plugin's context.
export function workspaceScope(workspace){
 if(workspace.label)return workspace.label
 const name=workspace.plugin.name
 if(['logs','monitor'].includes(name))return workspace.context?.service||workspace.context?.environment||''
 return ''
}
export function workspaceTitle(workspace,workspaces){
 if(workspace.title)return workspace.title
 const navigation=workspaceNavigation(workspace),base=navigation.label
 const peers=workspaces.filter(w=>workspaceNavigation(w).id===navigation.id)
 if(workspace.plugin.name==='api-center'){
  const target=workspaceTarget('api-center',workspace.hash)
  if(target?.detail){
   const label=withoutPageLabel(workspace.label||'',base)
   const fallback=[target.scope[0],target.scope[1]&&!target.scope[1].startsWith('__')?target.scope[1]:'',target.path==='/documents'?'文档 #'+target.id:'上报 '+target.id.slice(0,8)].filter(Boolean).join(' · ')
   const detail=label.includes(target.path==='/documents'?'文档 #':' · R')?label:fallback
   const same=peers.filter(w=>!w.title&&workspaceTarget('api-center',w.hash)?.key===target.key)
   return base+' · '+detail+(same.length>1?' · 窗口 '+(same.indexOf(workspace)+1):'')
  }
 }
 if(peers.length===1)return base
 const scope=withoutPageLabel(workspaceScope(workspace),base)
 const same=peers.filter(w=>!w.title&&withoutPageLabel(workspaceScope(w),base)===scope)
 const withEnvironment=['logs','monitor'].includes(workspace.plugin.name)&&same.length>1&&workspace.context?.environment
 const detail=[scope===base?'':scope,withEnvironment?workspace.context.environment:''].filter(Boolean).join(' · ')
 const duplicates=withEnvironment?same.filter(w=>w.context?.environment===workspace.context.environment):same
 return base+(detail?' · '+detail:'')+(duplicates.length>1?' · '+(duplicates.indexOf(workspace)+1):'')
}
