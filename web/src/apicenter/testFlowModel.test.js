import test from 'node:test'
import assert from 'node:assert/strict'
import {caseDraft,casePayload,emptyCase,emptyStep} from './caseModel.js'
import {connectSteps,flowReferences} from './testFlowModel.js'
import {caseReferences} from './collectionModel.js'
import {linearGraph,connectGraph,disconnectGraph,validateGraph} from './testFlowModel.js'
test('node identity survives request editing and saving; layout is not executable data',()=>{
 const c=emptyCase();c.name='回归';c.steps[0].name='创建';c.steps.push({...emptyStep(),name:'查询'})
 const ids=c.steps.map(s=>s.id);assert.equal(new Set(ids).size,2)
 const payload=casePayload(caseDraft(casePayload(c)));assert.deepEqual(payload.steps.map(s=>s.id),ids);assert.equal('positions' in payload,false)
})
test('connecting reorders only the explicit target and preserves IDs without mutating original',()=>{
 const steps=['a','b','c','d'].map(id=>({id}))
 assert.deepEqual(connectSteps(steps,'a','d').map(s=>s.id),['a','d','b','c'])
 assert.deepEqual(steps.map(s=>s.id),['a','b','c','d'])
 assert.throws(()=>connectSteps(steps,'a','a'),/不同/)
})
test('connections cannot silently move consumers before extracted values',()=>{
 const c=emptyCase();c.name='依赖';c.steps[0].name='开始'
 c.steps.push({...emptyStep(),name:'创建',extractions:[{name:'order',source:'json',path:'/id'}]})
 c.steps.push({...emptyStep(),name:'查询',request:{...emptyStep().request,path:'/orders/{{step.order}}'}})
 assert.deepEqual(flowReferences(c.steps),[{from:c.steps[1].id,to:c.steps[2].id,name:'order'}])
 assert.throws(()=>casePayload({...c,steps:connectSteps(c.steps,c.steps[0].id,c.steps[2].id)}),/尚未由前面/)
})
test('condition and join payloads preserve graph semantics without executable request defaults',()=>{
 const c=emptyCase();c.name='条件';c.steps=[{...emptyStep(),id:'condition',kind:'condition',name:'判断',condition:{variable:'data.flag',operator:'equals',expected:{type:'boolean',value:'true'}}},{...emptyStep(),id:'a',name:'真路径'},{...emptyStep(),id:'b',name:'假路径'},{...emptyStep(),id:'join',name:'汇合',kind:'join'}]
 c.graph={entry:'condition',concurrency:2,edges:[{from:'condition',to:'a',branch:'true'},{from:'condition',to:'b',branch:'false'},{from:'a',to:'join'},{from:'b',to:'join'}]}
 c.data_rows=[{name:'true',values:{flag:{type:'boolean',value:'true'}}}]
 const payload=casePayload(c);assert.equal(payload.steps[0].kind,'condition');assert.deepEqual(payload.steps[0].request,{});assert.equal(payload.graph.edges[0].branch,'true');assert.deepEqual(casePayload(caseDraft(payload)).graph,payload.graph);assert.throws(()=>casePayload({...c,data_rows:[]}),/数据列/);assert.deepEqual(caseReferences({steps:[{condition:{variable:'case.flag'},request:{}}]}),['flag'])
 assert.throws(()=>validateGraph(c.steps,{...c.graph,edges:[...c.graph.edges,{from:'join',to:'condition'}]}),/循环|入口/)
})
test('parallel outgoing edges accumulate and condition reconnection replaces only one route',()=>{
 const steps=[{id:'p',kind:'parallel'},{id:'a'},{id:'b'}],base={entry:'p',concurrency:2,edges:[]}
 const graph=connectGraph(connectGraph(base,steps,'p','a'),steps,'p','b');assert.equal(graph.edges.length,2)
 const condition=[{id:'c',kind:'condition'},{id:'a'},{id:'b'},{id:'d'}]
 const changed=connectGraph({entry:'c',concurrency:1,edges:[{from:'c',to:'a',branch:'true'},{from:'c',to:'b',branch:'false'}]},condition,'c','d','true')
 assert.equal(changed.edges.find(e=>e.branch==='false').to,'b');assert.equal(changed.edges.find(e=>e.branch==='true').to,'d')
 assert.equal(linearGraph([{id:'a'},{id:'b'}]).edges[0].to,'b')
})
test('disconnecting a legacy path preserves nodes and variable definitions, and requires reconnecting before save',()=>{
 const c=emptyCase();c.name='订单回归';c.steps[0].name='创建订单';c.steps[0].extractions=[{name:'order',source:'json',path:'/id'}]
 c.steps.push({...emptyStep(),name:'查询订单',request:{...emptyStep().request,path:'/orders/{{step.order}}'}})
 const original=casePayload(c),edge=linearGraph(c.steps).edges[0]
 const graph=disconnectGraph(linearGraph(c.steps),edge)
 assert.throws(()=>casePayload({...c,graph}),/接入|入口|终点/)
 const saved=casePayload({...c,graph:connectGraph(graph,c.steps,edge.from,edge.to)})
 assert.deepEqual(saved.steps,original.steps)
 assert.equal(graph.edges.length,0)
})
test('disconnecting a condition route preserves the other branch and rejects incomplete execution graphs',()=>{
 const steps=[{id:'c',name:'条件',kind:'condition'},{id:'a',name:'真路径'},{id:'b',name:'假路径'},{id:'j',name:'汇合',kind:'join'}]
 const graph={entry:'c',concurrency:2,edges:[{from:'c',to:'a',branch:'true'},{from:'c',to:'b',branch:'false'},{from:'a',to:'j'},{from:'b',to:'j'}]}
 const draft=disconnectGraph(graph,graph.edges[0])
 assert.deepEqual(draft.edges,graph.edges.slice(1));assert.equal(graph.edges.length,4)
 assert.throws(()=>validateGraph(steps,draft),/真\/假|接入/)
 assert.doesNotThrow(()=>validateGraph(steps,connectGraph(draft,steps,'c','a','true')))
})
