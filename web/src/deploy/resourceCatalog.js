import {resourceKindLabels} from './resourceConnections.js'
export const resourceTitles={connections:'资源连接',hosts:'主机',groups:'主机组',variables:'变量组',audit:'审计日志'}
export const auditResourceLabels={connection:'资源连接',host:'主机','host-group':'主机组','variable-group':'变量组',runner:'构建 Runner',environment:'环境','resource-group':'资源组',pipeline:'构建流水线',release:'发布单','delivery-run':'交付单','release-version':'发布版本','work-item':'工作项'}
export const auditActionLabels={save:'保存',create:'创建',delete:'删除',test:'连接测试',approve:'同意',reject:'拒绝',start:'启动',stop:'停止',retry:'重试',cancel:'取消',continue:'继续',complete:'完成',fail:'失败',timeout:'超时',uncertain:'结果待核对',rollback:'回滚',configure:'配置'}
export const auditResourceLabel=value=>auditResourceLabels[value]||value||'—'
export const auditActionLabel=value=>auditActionLabels[value]||value||'—'
export function publicResourceFields(kind,row){
  if(kind==='connections')return [row.name,resourceKindLabels[row.kind],row.endpoint]
  if(kind==='hosts')return [row.name,row.addr,row.user,row.environment]
  if(kind==='groups')return [row.name,row.environment,row.description,...(row.hosts||[])]
  if(kind==='variables')return [row.name,row.description,...(row.keys||[])]
  return [row.actor,row.action,auditActionLabel(row.action),row.resource_type,auditResourceLabel(row.resource_type),row.resource_id]
}
export function filterCatalog(kind,rows,{query='',filter='',start='',end=''}={},environments=[]){
 const term=query.trim().toLocaleLowerCase(),from=start?new Date(start).getTime():null,to=end?new Date(end).getTime():null
 return rows.filter(row=>{
  if(filter&&(kind==='connections'?row.kind!==filter:kind==='audit'?row.resource_type!==filter:row.environment!==filter))return false
  if(kind==='audit'&&((from!==null&&!(row.created_at*1000>=from))||(to!==null&&!(row.created_at*1000<=to))))return false
  const fields=publicResourceFields(kind,row)
  if(row.environment)fields.push(environments.find(env=>env.name===row.environment)?.display_name)
  return !term||fields.some(value=>String(value??'').toLocaleLowerCase().includes(term))
 })
}
export function parseVariableLines(raw){
 const values={}
 for(const [index,line] of String(raw||'').split('\n').entries()){
  if(!line.trim())continue
  const equal=line.indexOf('='),key=line.slice(0,equal).trim()
  // Error text deliberately excludes the supplied line, which can contain secrets.
  if(equal<1||!key)throw new Error(`第 ${index+1} 行格式错误，请使用 KEY=value`)
  if(Object.hasOwn(values,key))throw new Error(`第 ${index+1} 行变量名重复，请检查`)
  Object.defineProperty(values,key,{value:line.slice(equal+1),enumerable:true,writable:true,configurable:true})
 }
 return values
}
