// Navigation is presentation only: retain separate credential stores and IDs.
export const primaryResourceSections = [
 {key:'environments',label:'环境',icon:'static'},
 {key:'hosts',label:'主机',icon:'resources'},
 {key:'connections',label:'服务连接',icon:'devtools'},
 {key:'runners',label:'构建执行器',icon:'build'}
]
export const sharedResourceSections = [
 {key:'resourceGroups',label:'共享资源'},
 {key:'groups',label:'主机组'},
 {key:'variables',label:'共享变量'}
]
export function resourceScope(plugin='platform',agentOnly=false){
 if(agentOnly)return ['hosts','audit']
 if(['logs','monitor'].includes(plugin))return ['hosts','environments','audit']
 if(plugin==='devtools')return ['environments','audit']
 if(['dbms','redis'].includes(plugin))return ['connections','environments','resourceGroups','audit']
 return ['hosts','connections','environments','runners','resourceGroups','groups','variables','audit']
}
export function resourceInventory(connections=[],hosts=[],kinds=[]){
 const allowed=new Set(kinds.map(k=>k.value))
 return [
  ...hosts.map(row=>({...row,category:'hosts',kind:'host',id:'host:'+row.name,endpoint:row.addr})),
  ...connections.filter(row=>allowed.has(row.kind)).map(row=>({...row,category:'connections',id:'connection:'+row.name}))
 ]
}
export function filterResourceInventory(rows,query='',kind=''){
 const term=query.trim().toLocaleLowerCase()
 return rows.filter(row=>(!kind||row.kind===kind)&&(!term||[row.name,row.kind,row.endpoint,row.environment,row.typeLabel,row.environmentLabel].some(v=>String(v||'').toLocaleLowerCase().includes(term))))
}
