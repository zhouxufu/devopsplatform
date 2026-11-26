import {parse,isLosslessNumber} from 'lossless-json'

const own=(v,k)=>Object.hasOwn(v,k)
const object=v=>v!==null&&typeof v==='object'&&!isLosslessNumber(v)
const decode=part=>part.replaceAll('~1','/').replaceAll('~0','~')
const labels={operation:'接口',responses:'响应',requestBody:'请求体',content:'内容',schema:'字段定义',properties:'字段',items:'数组项',parameters:'参数',pathParameters:'路径参数',example:'示例',examples:'示例',description:'说明',type:'类型',required:'必填',summary:'功能名称',dependencies:'引用定义'}
export function fieldPath(path){return String(path||'').split('/').filter(Boolean).map(decode).map(p=>labels[p]||p).join(' / ')||'接口定义'}
function read(raw,present){
 if(!present)return {present:false}
 try{return {present:true,value:parse(raw)}}catch{return {present:true,value:raw,incomplete:true}}
}
function text(v,present){
 if(!present)return '（不存在）'
 if(v===null)return 'null'
 if(isLosslessNumber(v))return v.toString()
 if(typeof v==='string')return v===''?'（空字符串）':/^(null|true|false|-?\d+(\.\d+)?)$/.test(v)?JSON.stringify(v):v
 if(Array.isArray(v))return '[]'
 if(object(v))return '{}'
 return String(v)
}
// Presentation only: preserve the backend's immutable comparison paths and values.
export function describeFieldChanges(fields=[],upstreamTruncated=false){
 const rows=[],limit=300
 let truncated=upstreamTruncated,visited=0
 function walk(path,b,bp,a,ap,depth=0){
  if(++visited>5000||rows.length>=limit){truncated=true;return}
  if(bp&&ap&&!object(b)&&!object(a)&&text(b,true)===text(a,true)&&typeof b===typeof a)return
  const bo=bp&&object(b),ao=ap&&object(a)
  if(depth>=32&&(bo||ao))truncated=true
  const compatible=(!bp||bo)&&(!ap||ao)&&(!bp||!ap||Array.isArray(b)===Array.isArray(a))
  if(!compatible&&(bo||ao)&&depth<32){
   walk(path,b,bp,undefined,false,depth+1)
   walk(path,undefined,false,a,ap,depth+1)
   return
  }
  if(compatible&&(bo||ao)&&depth<32){
   const keys=[...new Set([...(bo?Object.keys(b):[]),...(ao?Object.keys(a):[])])]
   if(keys.length){for(const key of keys){const nb=bo&&own(b,key),na=ao&&own(a,key);walk([...path,Array.isArray(b)||Array.isArray(a)?`[${key}]`:key],nb?b[key]:undefined,nb,na?a[key]:undefined,na,depth+1);if(rows.length>=limit||visited>5000){truncated=true;break}}return}
   if(bp&&ap)return
  }
  const kind=!bp?'新增':!ap?'删除':'修改'
  rows.push({path:path.join(' / '),kind,before:text(b,bp),after:text(a,ap)})
 }
 for(const f of fields){
  const b=read(f.before,f.before_present),a=read(f.after,f.after_present)
  if(b.incomplete||a.incomplete){
   if(rows.length<limit)rows.push({path:fieldPath(f.path),kind:!b.present?'新增':!a.present?'删除':'修改',before:text(b.value,b.present),after:text(a.value,a.present),incomplete:true})
   truncated=true
  }else walk([fieldPath(f.path)],b.value,b.present,a.value,a.present)
 }
 return {rows,truncated}
}
