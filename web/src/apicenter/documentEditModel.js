import {parse,stringify} from 'lossless-json'
const methods=new Set(['get','put','post','delete','options','head','patch','trace'])
export function parseDocumentText(text,validate=true){
 const doc=parse(text)
 if(validate&&(!doc||typeof doc!=='object'||!/^3\.[01]\./.test(doc.openapi||'')||!doc.info?.title||!doc.info?.version||!doc.paths||typeof doc.paths!=='object'||Array.isArray(doc.paths)))throw Error('请填写有效的 OpenAPI 3.0/3.1 文档，包含标题、版本和 paths')
 return doc
}
export function documentTextFromResponse(text){return stringify(parse(text).document,null,2)}
export function documentOperations(doc){return Object.entries(doc.paths).flatMap(([path,item])=>Object.keys(item||{}).filter(method=>methods.has(method)).map(method=>({value:method+' '+path,label:method.toUpperCase()+' '+path,method,path}))) }
export function operationAt(doc,key){const split=String(key).indexOf(' ');const method=key.slice(0,split),path=key.slice(split+1);if(!methods.has(method)||!Object.hasOwn(doc.paths,path)||(!doc.paths[path]||!Object.hasOwn(doc.paths[path],method)))return null;return doc.paths[path][method]}
export function patchDocumentText(text,key,field,value){
 const doc=parseDocumentText(text,false)
 if(field.startsWith('info.')){
  const name=field.slice(5)
  if(name==='contact'){doc.info.contact={...(doc.info.contact||{}),name:value}}
  else if(['title','version','description'].includes(name))doc.info[name]=value
  else throw Error('不支持的文档字段')
 }else{
  const op=operationAt(doc,key);if(!op)throw Error('请选择存在的接口')
  if(!['summary','description','x-maintainer','tags'].includes(field))throw Error('不支持的接口字段')
  if(value===''&&['x-maintainer','tags'].includes(field))delete op[field]
  else op[field]=field==='tags'?String(value).split(/[,，]/).map(v=>v.trim()).filter(Boolean):value
 }
 return stringify(doc,null,2)
}
export function interfaceURL(doc,operation){
 const item=doc?.paths?.[operation?.path]||{},def=item[String(operation?.method).toLowerCase()]||{}
 const server=(def.servers||item.servers||doc?.servers||[])[0]
 if(!server?.url)return operation?.path||''
 const base=server.url.replace(/\{([^}]+)\}/g,(all,name)=>server.variables?.[name]?.default??all)
 return base.replace(/\/$/,'')+'/'+String(operation.path||'').replace(/^\//,'')
}

export function updateOperation(text,key,update){const doc=parseDocumentText(text,false),op=operationAt(doc,key);if(!op)throw Error('请选择存在的接口');update(op,doc);return stringify(doc,null,2)}
export function resolveLocal(value,doc){
 let v=value,seen=new Set()
 while(v?.$ref?.startsWith('#/')&&!seen.has(v.$ref)){
  seen.add(v.$ref);const target=v.$ref.slice(2).split('/').reduce((o,k)=>o?.[k.replace(/~1/g,'/').replace(/~0/g,'~')],doc)
  if(!target||typeof target!=='object')throw Error('本地引用不存在：'+v.$ref)
  const {$ref,...siblings}=v;v={...target,...siblings}
 }
 if(v?.$ref)throw Error('循环或外部引用请使用高级 JSON 编辑')
 return parse(stringify(v||{}))
}
export function effectiveParameters(doc,key){const path=key.slice(key.indexOf(' ')+1),op=operationAt(doc,key),params=new Map();for(const p of [...(doc.paths[path]?.parameters||[]),...(op?.parameters||[])]){const v=resolveLocal(p,doc);params.set(v.in+':'+v.name,v)}return [...params.values()]}
export function inheritedParameter(doc,key,param){const path=key.slice(key.indexOf(' ')+1);return (doc.paths[path]?.parameters||[]).some(p=>{const v=resolveLocal(p,doc);return v.in===param.in&&v.name===param.name})}
export function updateParameters(text,key,params){return updateOperation(text,key,(op,doc)=>{const path=key.slice(key.indexOf(' ')+1);for(const p of doc.paths[path]?.parameters||[]){const v=resolveLocal(p,doc);if(!params.some(n=>n.name===v.name&&n.in===v.in))throw Error('路径级参数不能仅在当前接口删除或改名；请在完整 JSON 中核对同路径所有接口后调整')}op.parameters=params})}
export function editOperationJSON(text,key,value){const next=parse(value);if(!next||typeof next!=='object'||Array.isArray(next)||!next.responses)throw Error('接口定义需要 responses');return updateOperation(text,key,op=>{for(const k of Object.keys(op))delete op[k];Object.assign(op,next)})}
export function formatJSON(text){return stringify(parse(text),null,2)}
export function formatValue(value){return value===undefined?'':stringify(value,null,2)}
export function parseValue(text){return parse(text)}

// Arrays are atomic: guessing how to merge parameter/allOf array positions can
// silently change API semantics. Only disjoint object edits merge automatically.
export function mergeDocumentTexts(baseText,mineText,latestText,choices={}){
 const missing=Symbol('missing'),conflicts=[]
 const same=(a,b)=>a===missing||b===missing?a===b:stringify(a)===stringify(b)
 const object=v=>v!==missing&&v!==null&&typeof v==='object'&&!Array.isArray(v)&&!v.isLosslessNumber
 function merge(base,mine,latest,path){
  if(same(mine,latest)||same(base,latest))return mine
  if(same(base,mine))return latest
  if(object(base)&&object(mine)&&object(latest)){const out={};for(const key of new Set([...Object.keys(base),...Object.keys(mine),...Object.keys(latest)])){const v=merge(Object.hasOwn(base,key)?base[key]:missing,Object.hasOwn(mine,key)?mine[key]:missing,Object.hasOwn(latest,key)?latest[key]:missing,path+'/'+key.replace(/~/g,'~0').replace(/\//g,'~1'));if(v!==missing)out[key]=v}return out}
  if(choices[path]==='mine')return mine;if(choices[path]==='latest')return latest
  conflicts.push({path,mine:mine===missing?'（删除）':stringify(mine,null,2),latest:latest===missing?'（删除）':stringify(latest,null,2)})
  return mine
 }
 const result=merge(parseDocumentText(baseText),parseDocumentText(mineText),parseDocumentText(latestText),'')
 return {text:stringify(result,null,2),conflicts}
}
