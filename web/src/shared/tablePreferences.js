// Presentation only: never store records, filters, selections, SQL or query values.
export function cleanTablePreferences(value,columns){
 const byId=new Map(columns.map(c=>[c.id,c])),valid=id=>typeof id==='string'&&byId.has(id)
 const list=value=>Array.isArray(value)?value.slice(0,100):[]
 const hidden=[...new Set(list(value?.hidden).filter(id=>valid(id)&&!byId.get(id).required))]
 const widths=list(value?.widths).flatMap(item=>{if(typeof item!=='string')return [];const at=item.lastIndexOf('=');const id=item.slice(0,at),n=Number(item.slice(at+1));return valid(id)&&Number.isFinite(n)?[id+'='+Math.round(Math.max(byId.get(id).minSize||64,Math.min(Math.min(4096,byId.get(id).maxSize||720),n)))]:[]})
 const left=[...new Set(list(value?.left).filter(valid))],right=[...new Set(list(value?.right).filter(id=>valid(id)&&!left.includes(id)))]
 return {hidden,widths,left,right}
}
export function defaultTablePreferences(columns){return cleanTablePreferences({hidden:columns.filter(c=>c.defaultHidden).map(c=>c.id),widths:[],left:columns.filter(c=>c.pin==='left').map(c=>c.id),right:columns.filter(c=>c.pin==='right').map(c=>c.id)},columns)}
export function tableSchemaKey(columns){let hash=2166136261;for(const char of columns.join('\u0000'))hash=Math.imul(hash^char.charCodeAt(0),16777619);return (hash>>>0).toString(36)}
