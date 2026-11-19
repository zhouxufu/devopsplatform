export const changeKinds = {code:'代码',mysql:'SQL',nacos:'Nacos',redis:'Redis',manual:'人工操作',artifact:'制品','data-sync':'数据同步'}
export function overviewGroups(changes=[], {application='',kind='',query=''}={}) {
 const groups=new Map(),term=query.trim().toLocaleLowerCase()
 for(const change of changes){
  if(application&&change.service!==application||kind&&change.kind!==kind)continue
  // Read-only projection: keep every saved identity and the original ordering.
  // Two requirements/apps may share a branch; that does not merge their changes.
  if(term&&!JSON.stringify([change.id,change.title,change.service,change.reference,change.summary,change.sql,change.nacos,change.redis,change.owner]).toLocaleLowerCase().includes(term))continue
  const service=change.service||''
  if(!groups.has(service))groups.set(service,{service,changes:[]})
  groups.get(service).changes.push(change)
 }
 return [...groups.values()]
}
