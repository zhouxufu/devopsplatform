export const visibilityOptions=[{value:'private',label:'仅自己'},{value:'team',label:'团队可用'}]
export function copyCase(value){return {...JSON.parse(JSON.stringify(value)),id:0,revision:0,owner:'',can_edit:true,visibility:'private',name:value.name+'（副本）'}}
const modulePath=value=>(value||'').split('/').map(part=>part.trim()).filter(Boolean).join('/')
export function moduleTree(cases){
 const roots=[],nodes=new Map()
 for(const path of [...new Set(cases.map(row=>modulePath(row.module)).filter(Boolean))].sort()){
  let value='',parent
  for(const label of path.split('/')){
   value=value?value+'/'+label:label
   if(!nodes.has(value)){
    const node={value,label}
    const siblings=parent?(parent.children??=[]):roots
    siblings.push(node)
    nodes.set(value,node)
   }
   parent=nodes.get(value)
  }
 }
 return roots
}
export function searchModuleTree(nodes,keyword){
 const query=keyword.trim().toLowerCase(),expanded=[]
 if(!query)return {items:nodes,expanded}
 function search(items){
  return items.flatMap(node=>{
   const children=search(node.children||[])
   if(!children.length&&!node.value.toLowerCase().includes(query))return []
   if(children.length)expanded.push(node.value)
   return [{...node,children:children.length?children:undefined}]
  })
 }
 return {items:search(nodes),expanded}
}
export function filterCases(cases,{search='',module='',visibility='',priority='',owner=''}={}){const q=search.trim().toLowerCase();return cases.filter(c=>{const path=modulePath(c.module);return (!module||path===module||path.startsWith(module+'/'))&&(!visibility||c.visibility===visibility)&&(!priority||c.priority===priority)&&(!owner||c.owner===owner)&&(!q||[c.name,c.module,c.id,c.owner].some(v=>String(v??'').toLowerCase().includes(q)))})}
export function suitePlan(suite,cases){const selected=(suite?.case_ids||[]).map(id=>cases.find(c=>c.id===id));return {selected,missing:selected.some(c=>!c),units:selected.reduce((sum,c)=>sum+(c?Math.max(1,c.data_rows?.length||0):0),0),steps:selected.reduce((sum,c)=>sum+(c?(c.steps?.length||1)*Math.max(1,c.data_rows?.length||0):0),0),revisions:Object.fromEntries(selected.filter(Boolean).map(c=>[String(c.id),c.revision]))}}
export function validateDataRows(rows,steps){if(rows.length*steps.length>100)throw Object.assign(Error('数据组与步骤合计超过 100 步，请拆分用例'),{field:'data'});if(rows.length>20)throw Object.assign(Error('最多 20 组数据'),{field:'data'});const names=new Set();for(const row of rows){if(!row.name?.trim()||names.has(row.name))throw Object.assign(Error('数据组名称不能为空或重复'),{field:'data'});names.add(row.name);const values=Object.entries(row.values||{});if(!values.length||values.length>20)throw Object.assign(Error('每组需要 1–20 个数据列'),{field:'data'});for(const [key,cell] of values){if(!/^[A-Za-z_][A-Za-z0-9_]{0,63}$/.test(key)||typeof cell?.value!=='string'||new TextEncoder().encode(cell.value).length>8192)throw Object.assign(Error('请补齐数据组 '+row.name+' 的 '+key),{field:'data'});if(cell.type==='number'&&!/^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?$/.test(cell.value)||cell.type==='boolean'&&!['true','false'].includes(cell.value)||cell.type==='uuid'&&cell.value!==''||!['number','boolean','string','null','uuid'].includes(cell.type))throw Object.assign(Error('数据组 '+row.name+' 的 '+key+' 类型不匹配'),{field:'data'})}}
 for(const [i,step] of steps.entries()){const fields=[step.condition?.variable?.startsWith('data.')?'{{'+step.condition.variable+'}}':'',step.request.path,step.request.body,...(step.request.form||[]).map(f=>f.value),...Object.values(step.request.headers||{}),...step.request.assertions.flatMap(a=>[a.path,a.expected])];for(const field of fields){const text=String(field||'');if(text.replace(/\{\{data\.([A-Za-z_][A-Za-z0-9_]{0,63})\}\}/g,'').includes('{{data.'))throw Object.assign(Error('数据变量格式无效'),{stepIndex:i,field:'path'});for(const m of text.matchAll(/\{\{data\.([A-Za-z_][A-Za-z0-9_]{0,63})\}\}/g)){if(!rows.length||rows.some(row=>!row.values[m[1]]))throw Object.assign(Error('数据列 '+m[1]+' 未配置，请补齐所有数据组'),{field:'data'})}}}
 return rows.map(row=>({name:row.name,values:Object.fromEntries(Object.entries(row.values).map(([k,c])=>[k,{type:c.type,value:c.value}]))}))}
export function caseReferences(c){const names=new Set();for(const step of c?.steps||[]){const r=step.request;if(step.condition?.variable?.startsWith('case.'))names.add(step.condition.variable.slice(5));for(const field of [r?.path,r?.body,...(r?.form||[]).map(f=>f.value),...Object.values(r?.headers||{}),...(r?.assertions||[]).flatMap(a=>[a.path,a.expected,a.event])])for(const m of String(field||'').matchAll(/\{\{case\.([A-Za-z_][A-Za-z0-9_]{0,63})\}\}/g))names.add(m[1])}return [...names]}
export function validateSuiteBindings(bindings,selected){
 if(bindings.length>40)throw Error('最多 40 个用例变量关联')
 const seen=new Set()
 for(const b of bindings){const source=selected.findIndex(c=>c?.id===b.source_case_id),target=selected.findIndex(c=>c?.id===b.target_case_id),key=b.target_case_id+':'+b.name;if(source<0||target<0||source>=target)throw Error('变量来源须为排在目标用例前面的用例，请检查顺序');if(!/^[A-Za-z_][A-Za-z0-9_]{0,63}$/.test(b.name)||seen.has(key))throw Error('用例变量名称无效或重复');seen.add(key);const c=selected[source];if(!c.steps.flatMap(s=>s.extractions||[]).some(e=>e.name===b.source_name))throw Error('来源用例没有这个提取变量');if(c.data_rows?.length>1&&!b.source_row||b.source_row&&!c.data_rows?.some(r=>r.name===b.source_row))throw Error('请选择有效的来源数据组')}
 for(const c of selected.filter(Boolean))for(const name of caseReferences(c))if(!seen.has(c.id+':'+name))throw Error('TC-'+c.id+' 的 {{case.'+name+'}} 尚未配置来源')
 return bindings
}
