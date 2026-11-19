import test from 'node:test'
import assert from 'node:assert/strict'
import {emptyStep} from './caseModel.js'
import {documentStep,hasCaseRequest} from './caseDocumentModel.js'
import {composeEditorRequest} from './requestEditorModel.js'
const row={snapshot_id:7,application_id:'app-orders',service:'orders',environment:'test',id:'post /orders/{id}'}
function response(){
 const operation={
  parameters:[{name:'mode',in:'query',required:true,example:'fast',schema:{type:'string'}},{name:'X-Tenant',in:'header',required:true,example:'team',schema:{type:'string'}}],
  requestBody:{content:{'application/json':{example:{title:'test'}}}},responses:{201:{description:'created'}},
 }
 const document={openapi:'3.0.3',info:{title:'Orders',version:'1'},paths:{'/orders/{id}':{parameters:[{name:'id',in:'path',required:true,example:'9',schema:{type:'string'}}],post:operation}}}
 return JSON.stringify({id:7,snapshot:{id:7,application_id:'app-orders',service:'orders',environment:'test'},operations:[{id:row.id,method:'POST',path:'/orders/{id}',summary:'创建订单'}],document})
}
test('document selection compiles an editable request and freezes the precise application and snapshot',()=>{
 const step={...emptyStep(),name:'原节点',action:'操作说明',expected:'预期说明',extractions:[{name:'old',source:'json',path:'/id'}]}
 const v=documentStep(step,row,response(),{scenario:true});const r=composeEditorRequest(v)
 assert.equal(v.id,step.id);assert.equal(v.name,'创建订单');assert.equal(v.service,'orders');assert.equal(v.application_id,'app-orders');assert.equal(v.document_id,7);assert.equal(v.operation_id,row.id);assert.equal(r.path,'/orders/9?mode=fast');assert.equal(r.method,'POST');assert.equal(r.headers['X-Tenant'],'team');assert.equal(r.body_type,'json');assert.equal(JSON.parse(r.body).title,'test');assert.equal(r.assertions[0].expected,'201');assert.deepEqual(v.extractions,[]);assert.equal(v.action,'操作说明');assert.equal(v.expected,'预期说明');assert.equal(step.name,'原节点')
})
test('same path from another application/environment or snapshot cannot replace a node',()=>{
 const step=emptyStep()
 assert.throws(()=>documentStep(step,{...row,service:'payments'},response(),{scenario:true}),/归属/)
 assert.throws(()=>documentStep(step,{...row,environment:'prod'},response(),{scenario:true}),/归属/)
 assert.throws(()=>documentStep(step,{...row,snapshot_id:8},response(),{scenario:true}),/归属/)
 assert.throws(()=>documentStep(step,row,response(),{service:'payments'}),/归属/)
 assert.throws(()=>documentStep(step,{...row,id:'get /missing'},response(),{scenario:true}),/不在/)
 assert.equal(documentStep(step,row,response(),{service:'orders'}).service,'')
})
test('document examples retain large JSON numbers and the blank-node check preserves request drafts',()=>{
 const step=emptyStep();assert.equal(hasCaseRequest(step),false)
 step.headersJSON='invalid';assert.equal(hasCaseRequest(step),true)
 step.headersJSON='{}';step.queryRows=[{name:'draft',value:'x',enabled:false}];assert.equal(hasCaseRequest(step),true)
 const raw=response().replace('"title":"test"','"id":9007199254740993123');const v=documentStep(emptyStep(),row,raw,{scenario:true});assert.match(v.request.body,/9007199254740993123/);assert.doesNotMatch(v.request.body,/isLosslessNumber/)
})
