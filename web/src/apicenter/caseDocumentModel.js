import {parse,stringify} from 'lossless-json'
import {operationDraft} from './testingModel.js'
import {operationParameters} from './model.js'
import {requestDraft} from './requestEditorModel.js'

export function hasCaseRequest(step){
 const r=step.request||{}
 return !!(step.name?.trim()||r.path&&r.path!=='/'||r.method&&r.method!=='GET'||r.body||r.form?.length||r.file||step.extractions?.length||Object.keys(r.headers||{}).length||(step.headerRows||[]).some(r=>r.name||r.value)||(step.headersJSON||'{}').replace(/\s/g,'')!=='{}'||(step.queryRows||[]).some(r=>r.name||r.value)||Object.values(step.pathValues||{}).some(Boolean)||r.assertions?.some(a=>a.type!=='status'||a.expected!=='200'))
}
export function documentStep(step,row,text,{scenario=false,service=''}={}){
 const data=JSON.parse(text),snapshot=data.snapshot||{}
 if(!Number.isSafeInteger(row.snapshot_id)||row.snapshot_id<1||data.id!==row.snapshot_id||snapshot.service!==row.service||row.environment!==undefined&&snapshot.environment!==row.environment||row.application_id&&snapshot.application_id!==row.application_id||!scenario&&row.service!==service)throw Error('接口文档归属不一致，请刷新后重新选择')
 const operation=data.operations?.find(o=>o.id===row.id)
 if(!operation)throw Error('此接口不在所选文档快照中，请重新选择')
 const contract=parse(text).document
 if(!contract?.paths&&!contract?.dubbo)throw Error('接口文档定义不完整')
 const definition=contract.paths?.[operation.path]?.[operation.method.toLowerCase()]||operation.definition
 const selected={...operation,definition},seed=operationDraft(selected,contract)
 for(const p of operationParameters(selected,contract))if(p.in==='header')seed.request.headers[p.name]=String(p.example??p.schema?.example??p.schema?.default??'')
 const mime=seed.request.headers['Content-Type']||''
 if(mime.includes('json'))seed.request.body_type='json'
 if(['application/x-www-form-urlencoded','multipart/form-data'].includes(mime)){
  const sample=seed.request.body?parse(seed.request.body):{}
  seed.request.body_type=mime==='multipart/form-data'?'multipart':'urlencoded';seed.request.body=''
  seed.request.form=Object.entries(sample).map(([name,value])=>({name,value:value&&typeof value==='object'&&!value.isLosslessNumber?stringify(value):String(value??'')}))
  if(seed.request.body_type==='multipart')delete seed.request.headers['Content-Type']
 }
 return {...step,kind:operation.protocol==='dubbo'?'dubbo':'http',...requestDraft(seed.request,{pathValues:seed.pathValues,queryRows:seed.query}),name:operation.summary||operation.path,document_id:data.id,operation_id:operation.id,service:scenario?row.service:'',application_id:scenario?snapshot.application_id:'',extractions:[],bodyDrafts:{},uploading:false}
}
