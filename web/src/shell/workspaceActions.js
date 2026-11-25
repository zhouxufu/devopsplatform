// Workspaces are ordered exactly as displayed; the fixed home tab is not included.
export function workspaceCloseTargets(workspaces,anchorID,action){
 if(action==='all')return workspaces.map(w=>w.id)
 const index=workspaces.findIndex(w=>w.id===anchorID)
 if(anchorID===0)return action==='right'?workspaces.map(w=>w.id):[]
 if(index<0)return []
 if(action==='left')return workspaces.slice(0,index).map(w=>w.id)
 if(action==='right')return workspaces.slice(index+1).map(w=>w.id)
 if(action==='other')return workspaces.filter((w,i)=>i!==index).map(w=>w.id)
 return action==='current'?[anchorID]:[]
}
export function nextWorkspaceAfterClose(workspaces,active,ids){
 const removed=new Set(ids)
 if(!removed.has(active))return active
 const index=workspaces.findIndex(w=>w.id===active)
 return workspaces.slice(index+1).find(w=>!removed.has(w.id))?.id||workspaces.slice(0,index).reverse().find(w=>!removed.has(w.id))?.id||0
}
