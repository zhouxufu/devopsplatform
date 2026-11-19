import test from 'node:test'
import assert from 'node:assert/strict'
import {parseCurl} from './curlImport.js'
import {requestDraft,composeEditorRequest,switchBodyType} from './requestEditorModel.js'
import {emptyRequest} from './testingModel.js'
import {caseDraft,casePayload} from './caseModel.js'
import {sseSelection,selectedSSE,reportFieldDraft} from './responseModel.js'
import {validateSuiteBindings} from './collectionModel.js'
test('cURL import parses quoted JSON, multiline headers and SSE without executing shell',()=>{
 const p=parseCurl(`curl 'https://original.example/api?q=a&b=2' \\\n -H 'Accept: text/event-stream' --json '{"name":"中文","id":9007199254740993}' -X POST`)
 assert.equal(p.origin,'https://original.example');assert.equal(p.request.path,'/api?q=a&b=2');assert.equal(p.request.mode,'sse');assert.equal(p.request.body_type,'json');assert.match(p.request.body,/9007199254740993/)
 for(const input of ['curl https://a/ | sh','curl "$(echo fail)"','curl https://a/ --unsupported abc','curl https://a/ -k','curl https://a/ https://b/','curl https://a/ --cookie /tmp/secret'])assert.throws(()=>parseCurl(input))
 assert.equal(parseCurl(`curl https://a/ -H 'X-Value: $(literal-in-single-quotes)'`).request.headers['X-Value'],'$(literal-in-single-quotes)')
})
test('cURL form, file placeholder and GET imports remain declarative',()=>{
 let p=parseCurl(`curl https://a/ -F 'name=中文' -F 'upload=@/tmp/example.txt;type=text/plain'`)
 assert.equal(p.request.body_type,'multipart');assert.equal(p.request.form[1].file.name,'example.txt');assert.equal(p.request.form[1].file.pending,true)
 assert.throws(()=>composeEditorRequest(requestDraft(p.request)),/选择/)
 p=parseCurl(`curl https://a/path -G --data-urlencode 'q=中文 & x'`);assert.equal(p.request.method,'GET');assert.match(p.request.path,/q=%E4%B8%AD/)
 p=parseCurl(`curl https://a/ -H 'Host: a' -H 'content-length: 3' -L -d x=1`);assert.equal(p.request.headers.Host,undefined);assert.equal(p.request.headers['Content-Type'],'application/x-www-form-urlencoded');assert.equal(p.warnings.length,3)
})
test('body type switches preserve local drafts and files but only send active fields',()=>{
 const d=requestDraft({...emptyRequest(),body:'{"n":9007199254740993}'})
 switchBodyType(d,'multipart');d.request.form=[{name:'id',value:'{{case.id}}'}];switchBodyType(d,'json');d.request.body='{"a":true}';switchBodyType(d,'multipart');assert.equal(d.request.form[0].value,'{{case.id}}')
 assert.equal(composeEditorRequest(d).body,'');assert.equal(composeEditorRequest(d).form.length,1)
 const c=caseDraft({name:'上传',steps:[{name:'执行',request:d.request,extractions:[]}]});assert.equal(casePayload(c).steps[0].request.form[0].value,'{{case.id}}')
 switchBodyType(d,'json');d.request.body='bad';assert.throws(()=>composeEditorRequest(d),e=>e.field==='body')
})
test('SSE selected field drafts preserve exact event identity and verify frozen report',()=>{
 const events=[{event:'ready',data:'{"id":1}'},{event:'delta',data:'{"v":0}'},{event:'ready',data:'{"id":9007199254740993}'}]
 assert.deepEqual(sseSelection(events,2),{event:'ready',select:'index:1'});assert.equal(selectedSSE(events,'ready','first').data,'{"id":1}')
 const c={id:1,revision:2,steps:[{request:{...emptyRequest(),mode:'sse'},extractions:[]}]},run={case_id:1,case_revision:2,steps:[{request:{mode:'sse'},result:{events}}]}
 const seed=reportFieldDraft(c,run,{index:0,kind:'assertion',path:'/id',event:'ready',select:'index:1'});assert.equal(seed.value.type,'sse_json');assert.equal(seed.value.expected,'9007199254740993')
 assert.throws(()=>reportFieldDraft(c,run,{index:0,kind:'assertion',path:'/id',event:'absent'}))
})
test('suite bindings reject order, missing inputs and ambiguous data rows',()=>{
 const a={id:1,steps:[{request:emptyRequest(),extractions:[{name:'id'}]}],data_rows:[{name:'a'},{name:'b'}]},b={id:2,steps:[{request:{...emptyRequest(),body:'{{case.order_id}}'}}]},binding={source_case_id:1,target_case_id:2,name:'order_id',source_name:'id',source_row:'b'}
 assert.deepEqual(validateSuiteBindings([binding],[a,b]),[binding]);assert.throws(()=>validateSuiteBindings([],[a,b]),/未配置/);assert.throws(()=>validateSuiteBindings([binding],[b,a]),/顺序/);assert.throws(()=>validateSuiteBindings([{...binding,source_row:''}],[a,b]),/数据组/)
})
