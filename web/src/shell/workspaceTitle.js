import {workspaceNavigation} from './navigation.js'
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
 if(peers.length===1)return base
 const scope=workspaceScope(workspace)
 const same=peers.filter(w=>!w.title&&workspaceScope(w)===scope)
 const withEnvironment=['logs','monitor'].includes(workspace.plugin.name)&&same.length>1&&workspace.context?.environment
 const detail=[scope===base?'':scope,withEnvironment?workspace.context.environment:''].filter(Boolean).join(' · ')
 const duplicates=withEnvironment?same.filter(w=>w.context?.environment===workspace.context.environment):same
 return base+(detail?' · '+detail:'')+(duplicates.length>1?' · '+(duplicates.indexOf(workspace)+1):'')
}
