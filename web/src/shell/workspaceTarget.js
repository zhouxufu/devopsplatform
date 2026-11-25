import {workspaceNavigation} from './navigation.js'

function route(hash){
 if(typeof hash!=='string'||!/^#\/(?!\/)/.test(hash)||hash.length>12000||/[\r\n\\]/.test(hash))return null
 try{const u=new URL(hash.slice(1),'https://workspace.invalid');return u.origin==='https://workspace.invalid'?u:null}catch{return null}
}
const canonical=u=>{const entries=[...u.searchParams].filter(([key])=>key!=='ws').sort(([a,x],[b,y])=>a.localeCompare(b)||x.localeCompare(y));return u.pathname+'?'+new URLSearchParams(entries)}
export function workspaceTarget(plugin,hash){
 const u=route(hash);if(!u)return null
 const q=u.searchParams
 if(plugin==='api-center'&&['/documents','/reports'].includes(u.pathname)){
  const scope=[q.get('detail_service')||q.get('service')||'',q.get('detail_environment')??q.get('environment')??'',q.get('detail_protocol')||q.get('protocol')||'http']
  if(scope[1]==='__unassigned')scope[1]=''
  const id=q.get(u.pathname==='/reports'?'report':'snapshot')||''
  if(id){
   if(u.pathname==='/reports'?!/^[a-f0-9]{32}$/.test(id):! /^[1-9]\d{0,18}$/.test(id))return null
   return {key:JSON.stringify([plugin,u.pathname,...scope,id]),detail:true,operation:q.get('operation')||'',scope,id,path:u.pathname}
  }
  return {key:JSON.stringify([plugin,canonical(u)]),detail:false,path:u.pathname,scope}
 }
 return {key:JSON.stringify([plugin,canonical(u)]),detail:!!(q.get('item')||q.get('run')||q.get('case')||q.get('task')||/\/\d+$/.test(u.pathname)),path:u.pathname}
}

// Menu entries resume the latest overview without resetting its filters. Linked
// records reuse only their full identity. A dirty/busy workspace is never navigated.
export function chooseWorkspace(workspaces,plugin,hash,{menu=false,forceNew=false,active=0,context}={}){
 if(forceNew)return null
 const target=workspaceTarget(plugin,hash);if(!target)return null
 const candidates=workspaces.filter(w=>w.plugin.name===plugin).sort((a,b)=>(b.id===active)-(a.id===active)||(b.lastActivated||0)-(a.lastActivated||0)||b.id-a.id)
 for(const w of candidates){
  const current=workspaceTarget(plugin,w.routeHash||w.hash);if(!current)continue
  if(!menu&&['logs','monitor','dbms','redis'].includes(plugin)&&context&&['service','environment'].some(key=>(w.context?.[key]||'')!==(context[key]||'')))continue
  if(menu){
   if(!current.detail&&!w.dirty&&!w.busy&&workspaceNavigation(w).id===workspaceNavigation({plugin:{name:plugin},hash}).id)return {workspace:w,navigate:false}
  }else if(current.key===target.key){
   const destination=route(hash),origin=route(w.routeHash||w.hash)
   const navigate=target.detail&&plugin==='api-center'&&target.path==='/documents'&&(target.operation!==current.operation||(destination.searchParams.get('tab')||'documents')!==(origin.searchParams.get('tab')||'documents')||destination.searchParams.get('base_id')!==origin.searchParams.get('base_id'))
   if(navigate&&(w.dirty||w.busy))continue
   let navigationHash=hash
   if(navigate){const u=origin;if(target.operation)u.searchParams.set('operation',target.operation);else u.searchParams.delete('operation');for(const key of ['tab','base_id']){const value=destination.searchParams.get(key);if(value)u.searchParams.set(key,value);else u.searchParams.delete(key)}navigationHash='#'+u.pathname+'?'+u.searchParams}
   return {workspace:w,navigate,hash:navigationHash}
  }
 }
 return null
}
