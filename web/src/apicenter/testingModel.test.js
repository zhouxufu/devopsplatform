import test from 'node:test'
import assert from 'node:assert/strict'
import {reactive} from 'vue'
import {emptyRequest,operationDraft,composeRequest,running,statusLabel} from './testingModel.js'
test('document test drafts reuse definition, parameters and SSE without choosing a document server',()=>{
 const doc={servers:[{url:'https://untrusted.invalid'}],components:{schemas:{Order:{type:'object',properties:{count:{type:'integer'},name:{type:'string'}}}}},paths:{'/orders/{id}':{parameters:[{name:'id',in:'path',example:'订单/1'}]}}}
 const draft=operationDraft({method:'POST',path:'/orders/{id}',sse:true,definition:{parameters:[{name:'debug',in:'query',required:true,schema:{default:'yes'}}],requestBody:{content:{'application/json':{schema:{$ref:'#/components/schemas/Order'}}}}}},doc)
 assert.equal(draft.request.mode,'sse');assert.equal(draft.request.headers['Content-Type'],'application/json');assert.deepEqual(JSON.parse(draft.request.body),{count:0,name:''})
 assert.equal(composeRequest(draft.request,'{}',draft.pathValues,draft.query).path,'/orders/%E8%AE%A2%E5%8D%95%2F1?debug=yes')
 assert.equal(JSON.stringify(draft).includes('untrusted.invalid'),false)
})
test('request composition accepts reactive Vue drafts without mutation and rejects invalid headers and missing path values',()=>{
 const draft=reactive({...emptyRequest(),path:'/orders/{id}?v=1'})
 assert.throws(()=>composeRequest(draft,'{}',{},[]),/路径参数/)
 const out=composeRequest(draft,'{"X-App":"orders"}',{id:'a/b'},[{enabled:true,name:'q',value:'空 格'}])
 assert.equal(out.path,'/orders/a%2Fb?v=1&q=%E7%A9%BA+%E6%A0%BC');assert.equal(draft.path,'/orders/{id}?v=1');assert.deepEqual(out.headers,{'X-App':'orders'})
 assert.throws(()=>composeRequest(draft,'[]',{id:'1'},[]));assert.throws(()=>composeRequest(draft,'{"x":42}',{id:'1'},[]))
 assert.equal(running('unknown'),false);assert.equal(statusLabel('unknown'),'结果待核对')
})
test('log links preserve report scope and a bounded historical time window',async()=>{
 const {logTarget}=await import('./testingModel.js'),now=1800000000000
 const run={service:'orders',environment:'test',started_at:now/1000-300,ended_at:now/1000-299,result:{trace_id:'a'.repeat(32)}}
 const q=new URL(logTarget(run,now).slice(1),'http://fixture').searchParams
 assert.equal(q.get('service'),'orders');assert.equal(q.get('environment'),'test');assert.equal(q.get('request_id'),'a'.repeat(32))
 assert.ok(Number(q.get('to'))<=now);assert.ok(Number(q.get('to'))-Number(q.get('from'))<24*3600000)
})
test('intranet HTTP can generate stable request keys without secure-context randomUUID',async()=>{
 const {randomUUID}=await import('../shared/uuid.js'),descriptor=Object.getOwnPropertyDescriptor(globalThis,'crypto')
 try{Object.defineProperty(globalThis,'crypto',{configurable:true,value:{getRandomValues:array=>{array.fill(21);return array}}});assert.match(randomUUID(),/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/)}finally{Object.defineProperty(globalThis,'crypto',descriptor)}
})
