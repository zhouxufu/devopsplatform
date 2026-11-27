import {parse,stringify} from 'lossless-json'
import {composeRequest} from './testingModel.js'

export const assertionOptions=[{value:'status',label:'HTTP 状态码等于'},{value:'json',label:'JSON 字段等于'},{value:'contains',label:'响应正文包含'},{value:'header',label:'响应头等于'},{value:'sse_count',label:'SSE 事件数至少'},{value:'regex',label:'响应正文匹配正则'},{value:'json_regex',label:'JSON 字段匹配正则'},{value:'json_range',label:'JSON 数值范围'},{value:'duration',label:'响应耗时不超过（ms）'},{value:'schema',label:'JSON Schema 校验'},{value:'sse_json',label:'SSE 事件字段等于'},{value:'sse_contains',label:'SSE 事件数据包含'}]
export const extractionSources=[{value:'json',label:'JSON 响应正文'},{value:'finite_number_text',label:'JSON 数值转文本'},{value:'header',label:'响应头'},{value:'sse',label:'SSE 匹配事件'}]
export function setExtractionSource(extraction, value){
 extraction.source=value==='finite_number_text'?'json':value
 if(value==='finite_number_text')extraction.format=value
 else delete extraction.format
}
export function requestDraft(request,extra={}){
 const draft={request:JSON.parse(JSON.stringify(request)),headersJSON:JSON.stringify(request.headers||{},null,2),pathValues:{},queryRows:[],...extra}
 initializeHeaders(draft)
 return draft
}
export function parseHeaders(text){
 let value
 try{value=JSON.parse(text||'{}')}catch{throw Error('请求头 JSON 格式无效')}
 if(!value||Array.isArray(value)||typeof value!=='object'||Object.values(value).some(v=>typeof v!=='string'))throw Error('请求头须为字符串键值 JSON 对象')
 headersFromRows(Object.entries(value).map(([name,value])=>({name,value,enabled:true})))
 return value
}
export function headersFromRows(rows){
 const seen=new Set(),pairs=[]
 for(const row of rows){
  if(!row.enabled)continue
  const name=row.name.trim()
  if(!name&&!row.value)continue
  if(!name)throw Error('请填写请求头名称')
  const key=name.toLowerCase()
  if(seen.has(key))throw Error('请求头名称重复：'+name)
  seen.add(key);pairs.push([name,row.value])
 }
 return Object.fromEntries(pairs)
}
export function editorHeaders(draft){return draft.headerMode==='table'?headersFromRows(draft.headerRows||[]):parseHeaders(draft.headersJSON)}
export function initializeHeaders(draft){
 if(draft.headerMode)return
 try{draft.headerRows=Object.entries(parseHeaders(draft.headersJSON)).map(([name,value],key)=>({key,name,value,enabled:true}));draft.headerMode='table'}catch{draft.headerMode='json'}
}
export function switchHeaderMode(draft,mode){
 if(mode===draft.headerMode)return
 const value=editorHeaders(draft)
 draft.headersJSON=JSON.stringify(value,null,2)
 if(mode==='table')draft.headerRows=[...Object.entries(value).map(([name,value],key)=>({key:'active-'+key,name,value,enabled:true})),...(draft.headerRows||[]).filter(r=>!r.enabled||!r.name.trim()).map((r,i)=>({...r,key:'inactive-'+i}))]
 draft.headerMode=mode
}
export function composeEditorRequest(draft){
 try{if(draft.uploading)throw Object.assign(Error('文件正在读取，请稍后再保存或发送'),{field:'body'});return validateRequestBody(composeRequest(draft.request,JSON.stringify(editorHeaders(draft)),draft.pathValues||{},draft.queryRows||[]))}catch(e){
  e.field=e.message.includes('请求头')?'headers':e.message.includes('路径参数')?'parameters':e.field||'path';throw e
 }
}
export function formatJSON(text){return stringify(parse(text),null,2)}
export const pathParameterKeys=path=>[...new Set([...String(path||'').matchAll(/\{\{[^{}]*\}\}|\{([^{}]+)\}/g)].map(v=>v[1]).filter(Boolean))]
export function switchBodyType(draft,type){
 const r=draft.request,old=r.body_type||'';if(old===type)return
 draft.bodyDrafts||={};draft.bodyDrafts[old]={body:r.body||'',form:r.form||[],file:r.file}
 const saved=draft.bodyDrafts[type]||{body:'',form:[]};r.body_type=type;r.body=saved.body;r.form=saved.form;delete r.file;if(saved.file)r.file=saved.file
}
export function validateRequestBody(r){
 const size=text=>new TextEncoder().encode(text||'').length,structured=['urlencoded','multipart','binary'].includes(r.body_type)
 if(r.body_type==='json'){try{if(r.body)parse(r.body)}catch{throw Object.assign(Error('JSON 正文格式无效'),{field:'body'})}}
 let total=structured?0:size(r.body)
 for(const row of r.form||[]){if(!row.name?.trim()||/[\r\n\0]/.test(row.name)||row.name.includes('{{'))throw Object.assign(Error('请填写有效表单字段名'),{field:'body'});total+=size(row.name)+size(row.value);if(row.file){if(row.file.pending||!row.file.name||!row.file.data_base64&&row.file.data_base64!=='')throw Object.assign(Error('请选择表单文件'),{field:'body'});total+=atob(row.file.data_base64||'').length}}
 if(r.body_type==='binary'){if(r.file?.pending||!r.file?.name)throw Object.assign(Error('请选择二进制文件'),{field:'body'});total+=atob(r.file.data_base64||'').length}
 if(total>256*1024)throw Object.assign(Error('正文、表单或文件超过 256 KiB'),{field:'body'})
 return r
}

export function eventSelectionOptions(current){return [{value:"first",label:"第一条匹配"},{value:"last",label:"最后一条匹配"},...(current?.startsWith("index:")?[{value:current,label:"第 "+(Number(current.slice(6))+1)+" 条匹配"}]:[])]}
