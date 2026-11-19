import {parse,stringify,isLosslessNumber} from 'lossless-json'

const sensitive=/authorization|cookie|password|passwd|secret|token|api[-_]?key/i
const escape=value=>String(value).replaceAll('~','~0').replaceAll('/','~1')
export function responseDocument(text){
 try{
  const value=parse(text),flat=[];let limited=false
  function visit(value,path,name,depth=0,masked=false){
   if(flat.length>=500||depth>16){limited=true;return null}
   const scalar=value===null||typeof value!=='object'||isLosslessNumber(value),kind=isLosslessNumber(value)?'number':value===null?'null':Array.isArray(value)?'array':typeof value
   masked=masked||sensitive.test(name)||(typeof value==='string'&&value.includes('[REDACTED]'))
   const raw=scalar?stringify(value):kind==='array'?`[${value.length} 项]`:'{…}'
   const row={key:path,path,name,kind,value:raw.length>512?raw.slice(0,512)+'…':raw,expected:scalar?raw:'',canAssert:scalar&&!masked&&new TextEncoder().encode(raw).length<=4096&&path.length<=512,canExtract:scalar&&value!==null&&value!==''&&!masked&&new TextEncoder().encode(typeof value==='string'?value:raw).length<=8192&&path.length<=512}
   flat.push(row)
   if(!scalar){row.children=[];for(const [key,item] of Object.entries(value)){const child=visit(item,path+'/'+escape(key),key,depth+1,masked);if(child)row.children.push(child);if(flat.length>=500){limited=true;break}}if(!row.children.length)delete row.children}
   return row
  }
  const root=visit(value,'','$')
  return {rows:root?[root]:[],flat,formatted:stringify(value,null,2),limited,valid:true}
 }catch{return {rows:[],flat:[],formatted:text,limited:false,valid:false}}
}
export function fieldAssertion(row){if(!row.canAssert)throw Error('该字段不能生成断言');return {type:'json',path:row.path,expected:row.expected}}
export function fieldExtraction(row,name){if(!row.canExtract)throw Error('该字段不能提取为步骤变量');if(!/^[A-Za-z_][A-Za-z0-9_]{0,63}$/.test(name))throw Error('变量名须以字母或下划线开头，最多 64 字符');return {name,source:'json',path:row.path}}
export function reportFieldDraft(caseDefinition,run,{index,kind,path,name,event,select}){
 if(run.case_id!==caseDefinition.id||run.case_revision!==caseDefinition.revision)throw Error('用例版本已变化，不能直接套用旧报告字段；请在当前步骤中核对后配置')
 const step=caseDefinition.steps?.[index],record=run.steps?.[index]
 if(!Number.isInteger(index)||index<0||!step||!record?.result||record.result.truncated)throw Error('报告步骤不存在、响应已截断或不是普通 JSON 响应')
 const isSSE=record.request?.mode==='sse',sse=isSSE?selectedSSE(record.result.events||[],event,select):null
 if(isSSE&&(!event||!sse))throw Error('SSE 事件不存在，请重新选择')
 const row=responseDocument(isSSE?sse.data:record.result.body||'').flat.find(r=>r.path===path)
 if(!row)throw Error('响应字段不存在')
 let value
 if(kind==='extraction'){
  value=fieldExtraction(row,name||'response_value')
  if(caseDefinition.steps.flatMap(s=>s.extractions||[]).some(v=>v.name===value.name))throw Error('该变量名已存在，请在响应提取中调整')
  if((step.extractions||[]).length>=10)throw Error('每步最多提取 10 个变量')
 }else if(kind==='assertion'){
  if(step.request.assertions.length>=30)throw Error('每步最多 30 条断言')
  value=fieldAssertion(row)
 }else throw Error('未知的报告字段操作')
 if(isSSE)value={...value,...(kind==='assertion'?{type:'sse_json'}:{source:'sse'}),event,select}
 return {index,kind,value}
}
export function runSummary(run){
 const steps=run.steps||[],checks=steps.length?steps.flatMap(s=>s.result?.checks||[]):run.result?.checks||[]
 return {total:steps.length,passed:steps.filter(s=>s.status==='success').length,skipped:steps.filter(s=>s.status==='skipped').length,checks:checks.length,failedChecks:checks.filter(c=>!c.passed).length,firstFailure:steps.findIndex(s=>['failed','unknown','canceled'].includes(s.status))}
}
export const checkType=type=>({status:'HTTP 状态码',json:'JSON 字段',header:'响应头',contains:'正文包含',sse_count:'SSE 事件数',regex:'正文正则',json_regex:'字段正则',json_range:'数值范围',duration:'耗时上限',schema:'JSON Schema',sse_json:'SSE 字段',sse_contains:'SSE 数据包含'})[type]||type
export function sseSelection(events,index){
 if(!Number.isInteger(index)||!events[index])throw Error('SSE 事件不存在')
 const event=events[index].event||'message',count=events.slice(0,index+1).filter(e=>(e.event||'message')===event).length
 return {event,select:count===1?'first':'index:'+(count-1)}
}
export function selectedSSE(events,event,selection='last'){
 const matches=events.filter(e=>!event||(e.event||'message')===event)
 if(selection==='first')return matches[0]
 if(selection.startsWith('index:'))return matches[Number(selection.slice(6))]
 return matches.at(-1)
}

export function eventSelectionLabel(selection){return selection==='first'?'第一条匹配':selection?.startsWith('index:')?'第 '+(Number(selection.slice(6))+1)+' 条匹配':'最后一条匹配'}
// A Table renders rows explicitly. Keep expansion separate from the response
// objects so filtering or inspecting a field never mutates the saved report.
export function expandedResponseRows(rows,expanded){
 const keys=new Set(expanded||[]),out=[]
 function visit(items,depth){for(const item of items||[]){out.push({...item,depth});if(keys.has(item.key))visit(item.children,depth+1)}}
 visit(rows,0)
 return out
}
