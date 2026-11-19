import test from 'node:test'
import assert from 'node:assert/strict'
import {responseDocument,fieldAssertion,fieldExtraction,runSummary,reportFieldDraft,expandedResponseRows} from './responseModel.js'

test('response fields preserve JSON Pointer escaping, numeric precision and assertion types',()=>{
 const d=responseDocument('{"a/b":{"~id":9007199254740993123},"string":"123","false":false,"nil":null,"empty":""}')
 const row=d.flat.find(r=>r.path==='/a~1b/~0id')
 assert.equal(fieldAssertion(row).expected,'9007199254740993123');assert.equal(fieldExtraction(row,'order_id').path,'/a~1b/~0id')
 assert.equal(fieldAssertion(d.flat.find(r=>r.path==='/string')).expected,'"123"')
 assert.equal(d.flat.find(r=>r.path==='/false').canExtract,true)
 assert.equal(d.flat.find(r=>r.path==='/nil').canAssert,true);assert.equal(d.flat.find(r=>r.path==='/nil').canExtract,false)
 assert.equal(d.flat.find(r=>r.path==='/empty').canExtract,false)
 assert.match(d.formatted,/9007199254740993123/)
})
test('masked fields, recursive sensitive paths and invalid responses cannot seed tests',()=>{
 const d=responseDocument('{"token":"masked","nested":{"password":{"part":"value"}},"id":"[REDACTED]","safe":0}')
 for(const path of ['/token','/nested/password/part','/id']){
  const r=d.flat.find(r=>r.path===path);assert.equal(r.canAssert,false);assert.equal(r.canExtract,false);assert.throws(()=>fieldAssertion(r))
 }
 assert.equal(d.flat.find(r=>r.path==='/safe').canExtract,true)
 assert.equal(responseDocument('{incomplete').valid,false)
 assert.throws(()=>fieldExtraction(d.flat.find(r=>r.path==='/safe'),'1bad'),/变量名/)
})
test('response exploration is bounded and report failure location preserves skipped semantics',()=>{
 const d=responseDocument(JSON.stringify(Object.fromEntries(Array.from({length:1000},(_,i)=>['field'+i,i]))))
 assert.equal(d.limited,true);assert.ok(d.flat.length<=500)
 const summary=runSummary({steps:[{status:'success',result:{checks:[{passed:true}]}},{status:'failed',result:{checks:[{passed:false}]}},{status:'skipped',result:{checks:[]}}]})
 assert.deepEqual(summary,{total:3,passed:1,skipped:1,checks:2,failedChecks:1,firstFailure:1})
})
test('report actions only derive drafts from the matching frozen revision and never modify history',()=>{
 const definition={id:7,revision:2,steps:[{request:{assertions:[]},extractions:[]}]}
 const run={id:11,case_id:7,case_revision:2,steps:[{request:{mode:'http'},result:{body:'{"id":9007199254740993123,"token":"[REDACTED]"}'}}]}
 const before=JSON.stringify([definition,run]),selection={index:0,kind:'extraction',path:'/id',name:'order_id'}
 assert.deepEqual(reportFieldDraft(definition,run,selection).value,{name:'order_id',source:'json',path:'/id'})
 assert.equal(reportFieldDraft(definition,run,{...selection,kind:'assertion'}).value.expected,'9007199254740993123')
 assert.equal(JSON.stringify([definition,run]),before)
 assert.throws(()=>reportFieldDraft({...definition,revision:3},run,selection),/版本已变化/)
 assert.throws(()=>reportFieldDraft(definition,{...run,case_id:8},selection),/版本已变化/)
 assert.throws(()=>reportFieldDraft(definition,run,{...selection,path:'/token'}),/不能提取/)
 run.steps[0].result.truncated=true;assert.throws(()=>reportFieldDraft(definition,run,selection),/截断/)
})

test('response tree expansion preserves hierarchy without mutating report data',()=>{
 const rows=[{key:'root',children:[{key:'child',children:[{key:'leaf'}]}]},{key:'sibling'}]
 const original=JSON.stringify(rows)
 assert.deepEqual(expandedResponseRows(rows,[]).map(x=>[x.key,x.depth]),[['root',0],['sibling',0]])
 assert.deepEqual(expandedResponseRows(rows,['root']).map(x=>[x.key,x.depth]),[['root',0],['child',1],['sibling',0]])
 assert.deepEqual(expandedResponseRows(rows,['root','child']).map(x=>[x.key,x.depth]),[['root',0],['child',1],['leaf',2],['sibling',0]])
 assert.equal(JSON.stringify(rows),original)
})
