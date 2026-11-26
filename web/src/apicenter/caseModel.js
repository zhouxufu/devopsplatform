import {randomUUID} from '../shared/uuid.js'
export const maxCaseSteps = 20
import {validateDataRows} from './collectionModel.js'
import {validateGraph} from './testFlowModel.js'
import {emptyRequest} from './testingModel.js'
import {requestDraft,composeEditorRequest} from './requestEditorModel.js'
export const emptyStep=()=>({id:'node-'+randomUUID(),service:'',application_id:'',document_id:0,operation_id:'',name:'',action:'',expected:'',...requestDraft(emptyRequest()),extractions:[]})
export const emptyCase=()=>({id:0,revision:0,document_id:0,visibility:'private',data_rows:[],name:'',module:'',priority:'P2',preconditions:'',test_data:'',steps:[emptyStep()]})
export function caseDraft(value){
 const v=JSON.parse(JSON.stringify(value)),steps=v.steps?.length?v.steps:[{name:'接口验证',action:'',expected:'',request:v.request||emptyRequest()}]
 return {...emptyCase(),...v,steps:steps.map(s=>({...emptyStep(),...s,...requestDraft(s.request?{...s.request,assertions:s.request.assertions||[]}:emptyRequest())}))}
}
export function casePayload(draft){
 if(!draft.name.trim())throw Object.assign(Error('请填写用例名称'),{field:'name'})
 if(!draft.steps.length||draft.steps.length>maxCaseSteps)throw Error(`用例需要 1–${maxCaseSteps} 个步骤`)
 const available=new Set()
 const steps=draft.steps.map((step,i)=>{
  if(!step.name.trim())throw Object.assign(Error('请填写步骤 '+(i+1)+' 的名称'),{stepIndex:i,field:'name'})
  try{
   if(step.kind&&!['http','dubbo'].includes(step.kind))return {id:step.id,target_key:step.target_key||'',kind:step.kind,name:step.name,action:step.action,expected:step.expected,request:{},extractions:[],...(step.kind==='condition'?{condition:step.condition}: {})}
   if(draft.scope==='scenario'&&!step.service)throw Object.assign(Error('请选择调用应用'),{field:'service'})
   const request=composeEditorRequest(step),extractions=(step.extractions||[]).map(e=>({name:e.name.trim(),source:e.source,path:e.path,...(e.format?{format:e.format}:{}),...(e.source==='sse'?{event:e.event||'',select:e.select||'last'}:{})}))
   const fields=[['path',request.path],['body',request.body],...(request.form||[]).map(f=>['body',f.value]),...Object.values(request.headers).map(v=>['headers',v]),...request.assertions.flatMap(a=>[['checks',a.path],['checks',a.expected]])]
   for(const [field,value] of fields){const text=String(value||'');if(text.replace(/\{\{step\.([A-Za-z_][A-Za-z0-9_]{0,63})\}\}/g,'').includes('{{step.'))throw Object.assign(Error('步骤变量格式无效'),{field});for(const m of text.matchAll(/\{\{step\.([A-Za-z_][A-Za-z0-9_]{0,63})\}\}/g))if(!draft.graph&&!available.has(m[1]))throw Object.assign(Error('变量 '+m[1]+' 尚未由前面的步骤提取'),{field})}
   if(extractions.length>10)throw Error('每步最多提取 10 个变量')
   for(const e of extractions){
    if(!/^[A-Za-z_][A-Za-z0-9_]{0,63}$/.test(e.name)||available.has(e.name))throw Error('提取变量名无效或重复')
    if(!['json','header','sse'].includes(e.source)||e.path.length>512)throw Error('提取来源或路径无效')
    if(e.source==='header'?!/^[A-Za-z0-9!#$%&'*+.^_`|~-]+$/.test(e.path):((e.path!==''&&!e.path.startsWith('/'))||/~(?![01])/.test(e.path)))throw Error('提取字段请使用 JSON Pointer，响应头请填写名称')
    if((e.source==='sse'&&request.mode!=='sse')||(e.source==='json'&&request.mode==='sse'))throw Error('提取来源与请求类型不一致')
    available.add(e.name)
   }
   return {...(draft.scope==='scenario'?{service:step.service,application_id:step.application_id||''}:{}),id:step.id,kind:request.mode==='dubbo'?'dubbo':'http',document_id:Number(step.document_id)||0,operation_id:step.operation_id||'',name:step.name,action:step.action,expected:step.expected,target_key:step.target_key||'',request,extractions}
  }
  catch(e){throw Object.assign(Error('步骤 '+(i+1)+'：'+e.message),{stepIndex:i,field:e.field|| (e.message.includes('请求头')?'headers':e.message.includes('变量')||e.message.includes('提取')?'extractions':'path')})}
 })
 if(draft.graph)validateGraph(steps,draft.graph)
 const data_rows=validateDataRows(draft.data_rows||[],steps.map(s=>({...s,request:{headers:{},assertions:[],...s.request}})))
 return {...(draft.scope==='scenario'?{scope:'scenario'}:{}),...(draft.graph?{graph:draft.graph}:{}),data_rows,...Object.fromEntries(['definition_version','visibility','id','revision','document_id','name','module','priority','preconditions','test_data'].map(k=>[k,draft[k]])),steps}
}
export function recordSource(run){return run.case_id>0||run.suite_id>0?'case':'manual'}
