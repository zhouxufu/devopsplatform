import test from 'node:test'
import assert from 'node:assert/strict'
import {emptyRequest,operationDraft} from './testingModel.js'
import {requestDraft,composeEditorRequest,switchHeaderMode,formatJSON,pathParameterKeys} from './requestEditorModel.js'
import {caseDraft,casePayload} from './caseModel.js'

test('both request editors keep table and JSON headers, disabled drafts and duplicate validation',()=>{
 const d=requestDraft({...emptyRequest(),headers:{'X-Test':'yes'}})
 d.headerRows.push({key:'disabled',enabled:false,name:'X-Draft',value:'later'})
 switchHeaderMode(d,'json');assert.deepEqual(JSON.parse(d.headersJSON),{'X-Test':'yes'})
 d.headersJSON='{"X-Test":"updated","Content-Type":"application/json"}'
 switchHeaderMode(d,'table');assert.equal(d.headerRows.at(-1).enabled,false)
 assert.deepEqual(composeEditorRequest(d).headers,{'X-Test':'updated','Content-Type':'application/json'})
 d.headerRows.push({enabled:true,name:'x-test',value:'duplicate'})
 assert.throws(()=>composeEditorRequest(d),e=>e.field==='headers'&&e.message.includes('重复'))
 d.headerRows.pop();switchHeaderMode(d,'json');d.headersJSON='[]'
 assert.throws(()=>switchHeaderMode(d,'table'),/键值/);assert.equal(d.headerMode,'json');assert.equal(d.headersJSON,'[]')
})
test('case steps compose the same path and query editor without persisting UI data',()=>{
 const c=caseDraft({name:'查询',steps:[{name:'步骤',request:{...emptyRequest(),path:'/orders/{id}?from=old'}}]})
 c.steps[0].pathValues.id='a/b';c.steps[0].queryRows=[{name:'filter',value:'{{var.FILTER}}',enabled:true},{name:'skipped',value:'ignored',enabled:false}]
 const p=casePayload(c)
 assert.equal(p.steps[0].request.path,'/orders/a%2Fb?from=old&filter={{var.FILTER}}')
 for(const key of ['headerMode','headerRows','queryRows','pathValues'])assert.equal(key in p.steps[0],false)
 const reopened=caseDraft(p);assert.equal(casePayload(reopened).steps[0].request.path,p.steps[0].request.path)
})
test('drafts use declared successful status and parameter override identity',()=>{
 for(const status of ['201','204']){
  const doc={paths:{'/orders/{id}':{parameters:[{name:'id',in:'path',example:'old'},{name:'q',in:'query',required:true,schema:{default:'base'}}]}}}
  const op={method:'POST',path:'/orders/{id}',definition:{parameters:[{name:'id',in:'path',example:'new'}],responses:{[status]:{description:'ok'},'400':{description:'invalid'}}}}
  const d=operationDraft(op,doc)
  assert.equal(d.request.assertions[0].expected,status);assert.equal(d.pathValues.id,'new');assert.equal(d.query.length,1)
 }
 assert.deepEqual(operationDraft({method:'GET',path:'/',definition:{responses:{default:{description:'other'}}}},{}).request.assertions,[])
 const d=requestDraft(emptyRequest());d.queryRows=[{name:'id',value:'前缀 {{step.order_id}} /',enabled:true}]
 assert.equal(composeEditorRequest(d).path,'/?id=%E5%89%8D%E7%BC%80+{{step.order_id}}+%2F')
})
test('formatting JSON preserves large numbers, types and invalid drafts',()=>{
 assert.match(formatJSON('{"id":9007199254740993123,"float":2.370,"bool":false}'),/9007199254740993123/)
 assert.match(formatJSON('{"float":2.370}'),/2\.370/)
 assert.throws(()=>formatJSON('{invalid'))
 assert.deepEqual(pathParameterKeys('/orders/{id}/{{step.order_id}}?q={{var.Q}}'),['id'])
 const draft=requestDraft({...emptyRequest(),path:'/orders/{id}'});draft.pathValues.id='{{step.order_id}}';assert.equal(composeEditorRequest(draft).path,'/orders/{{step.order_id}}')
})
