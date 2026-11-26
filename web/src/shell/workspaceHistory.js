const key='devopsWorkspace'
// Browser history carries only a window identity. Drafts, credentials, selected
// records and execution scope remain inside their owning live workspace.
export function createWorkspaceHistory(host,{activeName,hasWorkspace,activate,canRestore=()=>true}){
 let ready=false
 const marker=state=>state?.[key]?.version===1&&typeof state[key].name==='string'?state[key].name:null
 function record({replace=false}={}){
  if(!ready||!canRestore())return
  const name=activeName()||''
  if(!replace&&marker(host.history.state)===name)return
  host.history[replace?'replaceState':'pushState']({...host.history.state,[key]:{version:1,name}},'')
 }
 function restore(state){
  if(!ready||!canRestore())return
  const name=marker(state);if(name===null)return
  activate(name===''||hasWorkspace(name)?name:'')
  record({replace:true})
 }
 const onPop=event=>restore(event.state)
 host.addEventListener('popstate',onPop)
 return {
  start(){ready=true;const name=marker(host.history.state);if(name!==null&&canRestore())activate(name===''||hasWorkspace(name)?name:'');record({replace:true})},
  record,
  fromFrame(name){if(ready&&canRestore()&&marker(host.history.state)===name&&hasWorkspace(name))activate(name)},
  dispose(){ready=false;host.removeEventListener('popstate',onPop)},
 }
}
