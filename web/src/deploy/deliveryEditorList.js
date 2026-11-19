// Group only the navigation. Never duplicate/reorder the rows submitted to APIs.
export function deliveryEditorGroups(rows, query='') {
 const groups=new Map(),search=query.trim().toLocaleLowerCase()
 for(const row of rows){
  const services=[...new Set((row.kind==='code'?(row.services||[]):[row.service]).filter(Boolean))]
  if(search&&![row.title,row.reference,...services,(row.sql||row.nacos||row.redis)?.resource_alias].some(v=>String(v||'').toLocaleLowerCase().includes(search)))continue
  const key=services.length>1?'multi:'+services.slice().sort().join('\0'):services[0]||(row.kind==='manual'?'unassigned:manual':'unassigned:application')
  if(!groups.has(key))groups.set(key,{key,label:services.length>1?'跨应用变更':services[0]||(row.kind==='manual'?'公共 / 人工事项':'待选择应用'),rows:[]})
  groups.get(key).rows.push(row)
 }
 return [...groups.values()]
}

export function changedDeliveryRowKeys(rows, initialRows=[]) {
 const initial=new Map(initialRows.map(row=>[row.key,JSON.stringify(row)]))
 return new Set(rows.filter(row=>!row.readonly&&initial.get(row.key)!==JSON.stringify(row)).map(row=>row.key))
}
