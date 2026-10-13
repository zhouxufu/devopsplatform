// UI validation identifies an editor row, not a flattened database record.
// Server validation remains authoritative (SQL grammar, permissions, conflicts).
const text = value => String(value ?? '').trim()
const resourceName = /^[A-Za-z0-9_.-]+$/
const nacosKey = /^[A-Za-z0-9_-]+(?:\.[A-Za-z0-9_-]+)*$/
export function validateDeliveryChange(change, rowKey) {
  const fail = (field, message, entry) => ({rowKey, field, message, entry})
  if(change.kind==='code'){
    if(!text(change.service))return fail('services','请选择至少一个影响应用')
    if(!text(change.reference))return fail('reference','请填写关联分支')
  }
  if(['mysql','nacos','redis'].includes(change.kind)){
    if(!text(change.service))return fail('service','请选择应用')
    const payload=change.sql||change.nacos||change.redis
    if(!resourceName.test(text(payload?.resource_alias)))return fail('resource','请选择逻辑资源')
  }
  if(change.sql){
    if(!text(change.sql.content))return fail('sql','请填写 SQL 脚本')
    if(!Number.isInteger(Number(change.sql.max_affected_rows))||Number(change.sql.max_affected_rows)<1||Number(change.sql.max_affected_rows)>10000)return fail('max_affected_rows','允许影响行数须为 1–10000 的整数')
  }
  if(change.redis&&!text(change.redis.commands))return fail('commands','请填写 Redis 命令')
  if(change.nacos){
    const n=change.nacos
    if(!text(n.group))return fail('group','请填写默认 Group')
    for(const [field,label] of [['data_id','Data ID'],['group','Group']])if(new TextEncoder().encode(text(n[field])).length>255||/[\r\n\0]/.test(text(n[field])))return fail(field,`${label} 不能超过 255 个字符或包含换行`)
    if(!['yaml','properties','json'].includes(n.format))return fail('format','请选择 YAML、properties 或 JSON 格式')
    if(!n.entries?.length||n.entries.length>200)return fail('entries','请添加 1–200 个配置项')
    const seen=new Map()
    for(const [entry,e] of n.entries.entries()){
      const key=text(e.key),prefix=`第 ${entry+1} 个配置项：`
      if(!key)return fail('key',prefix+'请填写配置项名称',entry)
      if(!nacosKey.test(key))return fail('key',prefix+'名称使用字母、数字、短横线、下划线和点',entry)
      if(seen.has(key))return fail('key',prefix+`与第 ${seen.get(key)+1} 项重复`,entry)
      if([...seen.keys()].some(k=>k.startsWith(key+'.')||key.startsWith(k+'.')))return fail('key',prefix+'不能同时修改父路径和子路径',entry)
      if(!['set','delete'].includes(e.action))return fail('action',prefix+'请选择设置值或删除项',entry)
      if(new TextEncoder().encode(String(e.value??'')).length>65536)return fail('value',prefix+'值不能超过 64 KiB',entry)
      seen.set(key,entry)
    }
  }
  return null
}

export function locateDeliveryServerError(message, changes, origins) {
  const match=String(message).match(/^第\s*(\d+)\s*项[：:]/)
  if(!match)return null // Network, permission and optimistic-lock errors have no field location.
  const index=Number(match[1])-1,c=changes[index],rowKey=origins[index]
  if(!c||!rowKey)return null
  let field=({code:'reference',mysql:'sql',nacos:'entries',redis:'commands'})[c.kind]||'reference'
  if(/应用不存在/.test(message))field=c.kind==='code'?'services':'service'
  else if(/未声明.*资源|资源别名|请选择.*资源/.test(message))field='resource'
  else if(/影响行数/.test(message))field='max_affected_rows'
  else if(/Data ID/.test(message))field='data_id'
  else if(/Group/.test(message))field='group'
  const entryMatch=String(message).match(/第\s*(\d+)\s*个配置项/)
  return {rowKey,field:entryMatch?(/期望值/.test(message)?'value':/设置值或删除项/.test(message)?'action':'key'):field,entry:entryMatch?Number(entryMatch[1])-1:undefined,message:String(message)}
}
