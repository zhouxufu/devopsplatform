import {platformPage,platformPageFromHash} from './platformPages.js'
// Window layout/navigation only. Never serialize editors, credentials, approval
// digests, query cursors or selected delivery scope into shell preferences.
const short = (value, max=160) => typeof value==='string' ? value.slice(0,max) : ''
export function safeWorkspaceHash(value){
 if(typeof value!=='string'||!value.startsWith('#/')||value.length>2000||/%(?:2f|5c|2e)/i.test(value.split('?')[0])||value.includes('\\'))return '#/'
 try{
  const url=new URL(value.slice(1),'https://workspace.invalid')
  if(url.origin!=='https://workspace.invalid'||!/^\/[\w/%.-]*$/.test(url.pathname)||/%(?:2f|5c|2e)/i.test(url.pathname))return '#/'
  const query=new URLSearchParams()
  for(const key of ['view','quick','tab','mine','return','task','run','preparation','release','start','area','item','service','environment']){
   const v=url.searchParams.get(key)
   if(key==='area'&&!['runs','templates'].includes(v))continue
   if(key==='item'&&!/^[1-9]\d{0,18}$/.test(v||''))continue
   if(v&&/^[\w:.-]{1,160}$/.test(v))query.set(key,v)
  }
  return '#'+url.pathname+(query.size?'?'+query:'')
 }catch{return '#/'}
}
export function windowContext(value={}){
 if(!value||typeof value!=='object')value={}
 const range=Array.isArray(value.range)&&value.range.length===2&&value.range.every(Number.isFinite)?value.range:[0,0]
 return {service:short(value.service),environment:short(value.environment,64),range:[...range],rangeMinutes:Number.isFinite(value.rangeMinutes)&&value.rangeMinutes>=0&&value.rangeMinutes<=1440?value.rangeMinutes:15}
}
export function workspaceSnapshot(workspaces,active){
 const windows=workspaces.slice(0,30).map(w=>({plugin:w.plugin.name,name:/^lp-workspace-[\w-]{1,80}$/.test(w.el?.name||w.name||'')?w.el?.name||w.name:'',title:short(w.title,60),hash:safeWorkspaceHash(w.hash),context:windowContext(w.context)}))
 return {version:1,windows,active:workspaces.findIndex(w=>w.id===active)}
}
export function restoreWorkspaceSnapshot(value,plugins,actor={}){
 if(value?.version!==1||!Array.isArray(value.windows))return {windows:[],active:-1}
 const windows=[],names=new Set();let active=-1
 value.windows.slice(0,30).forEach((w,index)=>{
  const plugin=plugins.find(p=>p.name===w?.plugin&&p.name!=='devtools')
  if(!plugin)return
  if(plugin.name==='platform'&&!platformPage(platformPageFromHash(w.hash)?.id,actor,plugins))return
  const name=/^lp-workspace-[\w-]{1,80}$/.test(w.name||'')&&!names.has(w.name)?w.name:''
  if(name)names.add(name)
  if(index===value.active)active=windows.length
  windows.push({plugin,name,title:short(w.title,60),hash:safeWorkspaceHash(w.hash),context:windowContext(w.context)})
 })
 return {windows,active}
}
export const workspaceStorageKey=actor=>'lp:windows:v1:'+encodeURIComponent(actor||'local')

// Runtime-only context carries links between tools. Persisted window snapshots
// still use safeWorkspaceHash and never save document/operation execution scope.
export function runtimeWorkspaceHash(value,plugin){
 const safe=safeWorkspaceHash(value)
 if(typeof value!=='string'||!value.startsWith('#/')||value.length>12000)return safe
 try{
  const url=new URL(value.slice(1),'https://workspace.invalid'),path='#'+url.pathname
  if(url.origin!=='https://workspace.invalid')return safe
  if(safeWorkspaceHash(path)!==path)return safe
  const query=new URLSearchParams(safe.split('?')[1]||''),input=url.searchParams
  for(const key of ['service','environment']){const v=input.get(key);if(v&&/^[A-Za-z0-9_.-]{1,191}$/.test(v))query.set(key,v)}
  if(plugin==='deploy'&&url.pathname==='/orchestration'){
   for(const key of ['work_item','version','preparation']){const id=input.get(key);if(/^[1-9]\d{0,15}$/.test(id||''))query.set(key,id)}
   for(const key of ['work_items','changes']){const ids=input.get(key);if(ids&&/^[1-9]\d{0,15}(,[1-9]\d{0,15}){0,499}$/.test(ids))query.set(key,ids)}
  }
  if(plugin==='research'&&/^\/work\/\d+$/.test(url.pathname)&&input.get('action')==='preview')query.set('action','preview')
  if(plugin==='api-center'&&['/documents','/reports','/testing','/test-cases','/test-execution'].includes(url.pathname)){
   for(const key of ['protocol','detail_protocol']){const v=input.get(key);if(['http','dubbo'].includes(v))query.set(key,v)}
   for(const key of ['detail_service','detail_environment']){const v=input.get(key);if(v!==null&&/^[A-Za-z0-9_.-]{0,191}$/.test(v))query.set(key,v)}
   const report=input.get('report');if(url.pathname==='/reports'&&/^[a-f0-9]{32}$/.test(report||''))query.set('report',report)
   for(const key of ['bug','suite','evidence_revision','evidence_suite_revision']){const v=input.get(key);if(/^[1-9]\d{0,18}$/.test(v||''))query.set(key,v)}
   const target=input.get('target');if(target&&/^[A-Za-z0-9_.:-]{1,191}$/.test(target))query.set('target',target)
   const caseID=input.get('case');if(/^[1-9]\d{0,18}$/.test(caseID||''))query.set('case',caseID)
   const id=input.get('snapshot');if(/^[1-9]\d{0,18}$/.test(id||''))query.set('snapshot',id)
   const op=input.get('operation');if(op&&(/^(get|post|put|patch|delete|head|options|trace) \/(?!\/)[^\r\n\\]{0,8192}$/.test(op)||input.get('protocol')==='dubbo'&&op.length<=8192&&!/[\r\n\\]/.test(op)))query.set('operation',op)
  }
  if(plugin==='logs'&&url.pathname==='/search'){
   if(input.get('evidence')==='1'){
    const from=Number(input.get('from')),to=Number(input.get('to'))
    if(Number.isFinite(from)&&Number.isFinite(to)&&from>0&&to>from&&to-from<=86400000){
     query.set('evidence','1');query.set('from',String(from));query.set('to',String(to))
     const trace=input.get('trace_id');if(/^[A-Za-z0-9_-]{8,128}$/.test(trace||''))query.set('trace_id',trace)
     const source=input.get('log_source');if(/^[A-Za-z0-9_.-]{1,80}$/.test(source||''))query.set('log_source',source)
     const host=input.get('host');if(host&&host.length<=191&&!/[\r\n\x00]/.test(host))query.set('host',host)
    }
   }
   const id=input.get('request_id');if(/^[a-f0-9]{32}$/.test(id||'')){query.set('request_id',id);const from=Number(input.get('from')),to=Number(input.get('to'));if(Number.isFinite(from)&&Number.isFinite(to)&&from>0&&to>from&&to-from<=86400000){query.set('from',String(from));query.set('to',String(to))}}
  }
  if(plugin==='monitor'&&url.pathname==='/trace'&&/^[a-f0-9]{32}$/.test(input.get('trace')||''))query.set('trace',input.get('trace'))
  return path+(query.size?'?'+query:'')
 }catch{return safe}
}
export function linkedWorkspaceContext(source={},overrides={}){
 const next=windowContext(source)
 if(!overrides||typeof overrides!=='object')return next
 for(const key of ['service','environment'])if(typeof overrides[key]==='string')next[key]=overrides[key]
 const range=overrides.range
 if(Array.isArray(range)&&range.length===2&&range.every(Number.isFinite)&&range[0]>0&&range[1]>range[0]&&range[1]-range[0]<=86400000){next.range=[...range];next.rangeMinutes=0}
 return windowContext(next)
}
