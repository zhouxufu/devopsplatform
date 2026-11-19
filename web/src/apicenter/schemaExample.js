import {resolveSchema} from './model.js'
import {stringify} from 'lossless-json'
export const jsonText=value=>stringify(value,null,2)??''
// Documentation-only example; never sent to an endpoint or presented as a live response.
export function schemaExample(schema,document,{example}={}){
 if(example!==undefined)return {value:example,inferred:false,truncated:false}
 let remaining=500,truncated=false
 const walk=(node,seen=new Set(),depth=0)=>{
  if(depth>12||remaining--<=0){truncated=true;return null}
  if(node?.$ref&&seen.has(node.$ref)){truncated=true;return null}
  const next=new Set(seen);if(node?.$ref)next.add(node.$ref)
  const s=resolveSchema(node,document)
  if(s.$ref){truncated=true;return null}
  if(s.example!==undefined)return s.example
  if(s.default!==undefined)return s.default
  if(s.enum?.length)return s.enum[0]
  if(s.type==='array')return s.items?[walk(s.items,next,depth+1)]:[]
  if(s.type==='object'||s.properties){const out={};for(const [key,value] of Object.entries(s.properties||{})){if(remaining<=0){truncated=true;break}Object.defineProperty(out,key,{value:walk(value,next,depth+1),enumerable:true,configurable:true,writable:true})}return out}
  if(s.allOf?.length){const out={};for(const variant of s.allOf){const value=walk(variant,next,depth+1);if(value&&typeof value==='object'&&!Array.isArray(value))for(const [key,v] of Object.entries(value))Object.defineProperty(out,key,{value:v,enumerable:true,configurable:true,writable:true})}return out}
  if(s.oneOf?.length||s.anyOf?.length)return walk((s.oneOf||s.anyOf)[0],next,depth+1)
  return s.type==='integer'||s.type==='number'?0:s.type==='boolean'?false:s.type==='null'?null:''
 }
 return {value:walk(schema),inferred:true,get truncated(){return truncated}}
}
