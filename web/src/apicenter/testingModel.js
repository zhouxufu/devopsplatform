import {dubboMethod} from './dubboModel.js'
import {stringify} from 'lossless-json'
import {resolveSchema,operationParameters} from './model.js'
export const emptyRequest=()=>({method:'GET',path:'/',headers:{},body:'',mode:'http',timeout_seconds:30,max_events:20,assertions:[{type:'status',path:'',expected:'200'}]})
export function operationDraft(operation,document){
 const request=emptyRequest(),definition=operation.definition||{}
 if(operation.protocol==='dubbo'||operation.rpc){const m=dubboMethod(operation,document);return {request:{...request,mode:'dubbo',method:'DUBBO',path:operation.path,rpc:{interface:m.interface,method:m.method,group:m.group||'',version:m.version||'',parameter_types:m.parameters.map(p=>p.java_type)},body_type:'json',body:stringify(m.parameters.map(p=>p.example??sampleSchema(p.schema,document)??null),null,2),assertions:[{type:'rpc_success',path:'',expected:'true'}]},pathValues:{},query:[]}}

 request.method=operation.method;request.path=operation.path;request.mode=operation.sse?'sse':'http'
 const success=Object.keys(definition.responses||{}).filter(v=>/^2\d\d$/.test(v)).sort()
 request.assertions=success.length?[{type:'status',path:'',expected:success.includes('200')?'200':success[0]}]:[]
 const content=resolveSchema(definition.requestBody,document).content||{},mime=Object.keys(content)[0]
 if(mime){request.headers['Content-Type']=mime;const media=content[mime],sample=media.example??Object.values(media.examples||{})[0]?.value??sampleSchema(media.schema,document);if(sample!==undefined)request.body=typeof sample==='string'?sample:stringify(sample,null,2)}
 const parameters=operationParameters(operation,document)
 return {request,pathValues:Object.fromEntries(parameters.filter(p=>p.in==='path').map(p=>[p.name,String(p.example??p.schema?.example??'')])),query:parameters.filter(p=>p.in==='query').map((p,i)=>({key:i,name:p.name,value:String(p.example??p.schema?.default??''),enabled:p.required===true}))}
}
function sampleSchema(schema,document,depth=0){if(depth>4)return undefined;const s=resolveSchema(schema,document);if(s.example!==undefined)return s.example;if(s.default!==undefined)return s.default;if(s.enum?.length)return s.enum[0];if(s.type==='array')return [sampleSchema(s.items,document,depth+1)??null];if(s.properties)return Object.fromEntries(Object.entries(s.properties).slice(0,50).map(([k,v])=>[k,sampleSchema(v,document,depth+1)??null]));return {string:'',integer:0,number:0,boolean:false}[s.type]}
export function composeRequest(request,headersJSON,pathValues,query){
 const headers=JSON.parse(headersJSON||'{}');if(!headers||Array.isArray(headers)||typeof headers!=='object'||Object.values(headers).some(v=>typeof v!=='string'))throw Error('请求头须为字符串键值 JSON 对象')
 if(request.mode==='dubbo')return {...JSON.parse(JSON.stringify(request)),headers}
 const encode=(value,query=false)=>String(value).split(/(\{\{(?:var|step|data|case)\.[A-Za-z_][A-Za-z0-9_]*\}\})/g).map(part=>/^\{\{(?:var|step|data|case)\.[A-Za-z_][A-Za-z0-9_]*\}\}$/.test(part)?part:query?encodeURIComponent(part).replaceAll('%20','+'):encodeURIComponent(part)).join('')
 let path=request.path.replace(/\{\{[^{}]*\}\}|\{([^{}]+)\}/g,(_,name)=>{if(name===undefined)return _;if(!String(pathValues[name]??'').trim())throw Error('请填写路径参数 '+name);return encode(pathValues[name])})
 const extra=query.filter(row=>row.enabled&&row.name.trim()).map(row=>encodeURIComponent(row.name)+'='+encode(row.value,true))
 if(extra.length)path+=(path.includes('?')?'&':'?')+extra.join('&')
 return {...JSON.parse(JSON.stringify(request)),path,headers}
}
export const running=status=>['queued','running'].includes(status)
export const statusLabel=status=>({queued:'排队中',running:'执行中',success:'通过',failed:'未通过',unknown:'结果待核对',canceled:'已停止',pending:'待执行',skipped:'未执行'})[status]||status
export function logTarget(run,now=Date.now()){
 const from=Math.max(1,run.started_at*1000-60000),to=Math.max(from+1,Math.min(now,(run.ended_at||Math.floor(now/1000))*1000+60000))
 return '#/search?'+new URLSearchParams({request_id:run.result.trace_id,service:run.service,environment:run.environment,from:String(from),to:String(to)})
}
