import {stringify as jsonStringify} from 'lossless-json'
export function resolveSchema(value,document,seen=new Set()){
 if(!value||typeof value!=='object')return {}
 if(!value.$ref)return value
 if(!value.$ref.startsWith('#/')||value.$ref.length>2048||seen.size>=32||seen.has(value.$ref))return value
 const next=new Set(seen).add(value.$ref)
 let result=document
 for(const key of value.$ref.slice(2).split('/').map(v=>v.replace(/~1/g,'/').replace(/~0/g,'~'))){if(!result||!Object.hasOwn(result,key))return value;result=result[key]}
 return resolveSchema(result,document,next)
}
export function operationParameters(operation,document){
 const combined=new Map()
 for(const value of [...(document.paths?.[operation.path]?.parameters||[]),...(operation.definition?.parameters||[])]){
  const p=resolveSchema(value,document)
  if(p.name&&p.in)combined.set(p.in+':'+p.name,p)
 }
 return [...combined.values()]
}
export function schemaMetadata(schema,document){
 const s=resolveSchema(schema,document),details=[]
 for(const [key,label] of [['format','格式'],['minimum','最小值'],['maximum','最大值'],['minLength','最短'],['maxLength','最长'],['pattern','规则'],['minItems','最少项'],['maxItems','最多项']])if(s[key]!==undefined)details.push(label+' '+String(s[key]))
 if(s.readOnly)details.push('只读');if(s.writeOnly)details.push('只写');if(s.deprecated)details.push('已废弃')
 return {defaultValue:s.default===undefined?'—':jsonStringify(s.default),enumValue:s.enum?jsonStringify(s.enum):'—',constraints:details.join(' · ')||'—'}
}
export function schemaRows(schema,document,depth=0,seen=new Set(),prefix='',budget={remaining:500},fieldPath=''){
 if(depth>5||!schema||seen.has(schema.$ref)||budget.remaining<=0)return []
 const next=new Set(seen);if(schema.$ref)next.add(schema.$ref)
 const s=resolveSchema(schema,document)
 if(s.type==='array')return schemaRows(s.items,document,depth+1,next,prefix,budget,fieldPath)
 const rows=[]
 for(const [field,value] of Object.entries(s.properties||{})){
  if(budget.remaining--<=0)break
  const v=resolveSchema(value,document),path=fieldPath+'/'+field.replace(/~/g,'~0').replace(/\//g,'~1'),children=schemaRows(value,document,depth+1,next,prefix+field+'.',budget,path)
  rows.push({key:prefix+field,field,field_path:path,type:v.type==='array'?`array<${resolveSchema(v.items,document).type||'object'}>`:v.type||v.$ref?.split('/').at(-1)||'object',required:s.required?.includes(field)?'是':'否',description:v.description||value.description||'—',...schemaMetadata(v,document),...(children.length?{children}: {})})
 }
 return rows
}
export function nodeOptions(providers,nodes=[]){
 const result=providers.flatMap(p=>(p.nodes||[]).map(n=>({value:`extension@${p.id}@${n.key}`,label:`${p.name} · ${n.name}`,disabled:p.state!=='running'})))
 for(const n of nodes){if(n.kind!=='extension'||!n.extension)continue;const value=`extension@${n.extension.provider}@${n.extension.node_key}`;if(!result.some(o=>o.value===value))result.push({value,label:`${n.name||n.extension.provider}（插件不可用）`,disabled:true})}
 return result
}

export function operationMatches(operation,query='',group=''){
 if(group&&!operation.tags?.includes(group))return false
 const text=[operation.path,operation.summary,operation.description,operation.method,operation.maintainer,operation.maintainer_name,...(operation.tags||[])].join(' ').toLowerCase()
 return String(query).trim().toLowerCase().split(/\s+/).filter(Boolean).every(term=>text.includes(term))
}
