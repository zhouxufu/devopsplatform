import test from 'node:test'
import assert from 'node:assert/strict'
import {resolveSchema,schemaRows,nodeOptions,operationParameters} from './model.js'
test('document fields resolve local references with bounded recursion and stable unique keys',()=>{
 const doc={components:{schemas:{User:{type:'object',required:['id'],properties:{id:{type:'integer'},parent:{$ref:'#/components/schemas/User'}}}}}}
 const rows=schemaRows({type:'object',properties:{buyer:{$ref:'#/components/schemas/User'},seller:{$ref:'#/components/schemas/User'}}},doc)
 assert.equal(rows[0].children[0].required,'是');assert.notEqual(rows[0].children[0].key,rows[1].children[0].key)
 assert.equal(resolveSchema({$ref:'https://example.invalid/schema'},doc).$ref,'https://example.invalid/schema')
 assert.equal(resolveSchema({$ref:'#/__proto__/x'},doc).$ref,'#/__proto__/x')
 assert.ok(JSON.stringify(rows).length<5000)
})
test('workflow choices come from installed providers and preserve unavailable saved bindings',()=>{
 const providers=[{id:'smart-doc',name:'Smart-doc',state:'running',nodes:[{key:'api-documentation',name:'生成接口文档'}]},{id:'scan',name:'扫描',state:'running',nodes:[{key:'check',name:'只读检查'}]}]
 assert.equal(nodeOptions(providers).length,2)
 const saved=[{kind:'extension',name:'文档',extension:{provider:'smart-doc',node_key:'api-documentation'}}]
 assert.equal(nodeOptions([],saved)[0].disabled,true)
 assert.equal(nodeOptions(providers,saved).filter(o=>o.value==='extension@smart-doc@api-documentation').length,1)
})
test('shared schema references cannot expand into an unbounded tree',()=>{
 const schemas={Leaf:{type:'object',properties:{value:{type:'string'}}}}
 for(let i=4;i>=0;i--)schemas['L'+i]={type:'object',properties:Object.fromEntries(Array.from({length:100},(_,j)=>['f'+j,{$ref:'#/components/schemas/'+(i===4?'Leaf':'L'+(i+1))}]))}
 const rows=schemaRows({$ref:'#/components/schemas/L0'},{components:{schemas}})
 const count=items=>items.reduce((n,v)=>n+1+count(v.children||[]),0)
 assert.ok(count(rows)<=500)
})


test('document parameter view merges path refs with operation overrides and shows schema constraints',()=>{
 const doc={components:{parameters:{ID:{name:'id',in:'path',required:true,schema:{type:'string'}}}},paths:{'/orders/{id}':{parameters:[{$ref:'#/components/parameters/ID'},{name:'page',in:'query',schema:{type:'integer',default:1}}]}}}
 const op={path:'/orders/{id}',definition:{parameters:[{name:'page',in:'query',schema:{type:'integer',default:2}}]}}
 const rows=operationParameters(op,doc)
 assert.equal(rows.length,2);assert.equal(rows[0].name,'id');assert.equal(rows[1].schema.default,2)
 const fields=schemaRows({type:'object',properties:{status:{type:'string',enum:['open','closed'],default:'open',maxLength:10}}},doc)
 assert.equal(fields[0].defaultValue,'"open"');assert.match(fields[0].enumValue,/closed/);assert.match(fields[0].constraints,/10/)
})

test('function search matches words across summary, description, path and tags without requiring an application',async()=>{
 const {operationMatches}=await import('./model.js')
 const op={summary:'创建订房订单',description:'关联取消记录',path:'/API/orders',method:'POST',tags:['交易','订单']}
 assert.equal(operationMatches(op,'创建 订单'),true)
 assert.equal(operationMatches(op,'取消'),true)
 assert.equal(operationMatches(op,'api/ORDERS post','交易'),true)
 assert.equal(operationMatches(op,'创建','支付'),false)
 assert.equal(operationMatches(op,'不存在'),false)
 assert.equal(operationMatches(op,'%'),false)
})

 test('official tree row expansion keeps sibling objects and nested paths independent',async()=>{
  const {createTable,getCoreRowModel,getExpandedRowModel}=await import('@tanstack/vue-table')
  const data=schemaRows({type:'object',properties:{buyer:{type:'object',properties:{name:{type:'string'}}},seller:{type:'object',properties:{name:{type:'string'}}}}},{})
  let expanded={}
  const table=createTable({data,columns:[{accessorKey:'field'}],getRowId:r=>r.key,getSubRows:r=>r.children,getCoreRowModel:getCoreRowModel(),getExpandedRowModel:getExpandedRowModel(),state:{expanded},onExpandedChange:update=>{expanded=typeof update==='function'?update(expanded):update;table.setOptions(old=>({...old,state:{...old.state,expanded}}))}})
  const keys=()=>table.getRowModel().rows.map(r=>r.id)
  assert.deepEqual(keys(),['buyer','seller'])
  table.getRow('buyer').toggleExpanded();assert.deepEqual(keys(),['buyer','buyer.name','seller'])
  table.getRow('seller').toggleExpanded();assert.equal(table.getRow('seller.name').depth,1)
  table.getRow('buyer').toggleExpanded();assert.deepEqual(keys(),['buyer','seller','seller.name'])
 })
