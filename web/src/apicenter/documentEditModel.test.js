import test from 'node:test'
import assert from 'node:assert/strict'
import {parseDocumentText,patchDocumentText,documentTextFromResponse,interfaceURL,updateOperation,effectiveParameters,updateParameters,resolveLocal,editOperationJSON,formatValue,mergeDocumentTexts} from './documentEditModel.js'
const text='{"openapi":"3.0.3","info":{"title":"订单","version":"1"},"servers":[{"url":"https://api.example.com/{base}","variables":{"base":{"default":"v1"}}}],"paths":{"/orders/{id}":{"parameters":[{"name":"id","in":"path"}],"get":{"summary":"查询订单","responses":{"200":{"description":"成功","content":{"application/json":{"example":{"id":9223372036854775807}}}}}},"post":{"responses":{"200":{"description":"ok"}}}}},"components":{"schemas":{"User":{"type":"object"}}}}'
test('document edits preserve complete definitions, other operations, refs and large numeric examples',()=>{
 const changed=patchDocumentText(text,'get /orders/{id}','summary','查询订单详情')
 assert.match(changed,/9223372036854775807/)
 const doc=parseDocumentText(changed)
 assert.equal(doc.paths['/orders/{id}'].get.summary,'查询订单详情')
 assert.ok(doc.paths['/orders/{id}'].post)
 assert.ok(doc.components.schemas.User)
 assert.equal(patchDocumentText(changed,'get /orders/{id}','info.title','').includes('"title": ""'),true)
 assert.throws(()=>parseDocumentText(patchDocumentText(changed,'get /orders/{id}','info.title','')))
 assert.throws(()=>patchDocumentText(text,'get /missing','summary','oops'))
})
test('copy URL uses declared servers and keeps relative paths when no server is declared',()=>{
 const op={method:'GET',path:'/orders/{id}'}
 assert.equal(interfaceURL(parseDocumentText(text),op),'https://api.example.com/v1/orders/{id}')
 assert.equal(interfaceURL({paths:{}},op),'/orders/{id}')
})
test('raw API response extraction preserves full OpenAPI number precision',()=>{
 const raw='{"id":1,"document":'+text+'}'
 assert.match(documentTextFromResponse(raw),/9223372036854775807/)
 assert.throws(()=>parseDocumentText('{'))
})
test('visual edits isolate inherited parameters and shared schema copies',()=>{
 const doc=parseDocumentText(text),params=effectiveParameters(doc,'get /orders/{id}')
 params[0].description='路径 ID'
 const next=parseDocumentText(updateParameters(text,'get /orders/{id}',params))
 assert.equal(next.paths['/orders/{id}'].get.parameters[0].description,'路径 ID')
 assert.equal(next.paths['/orders/{id}'].parameters[0].description,undefined)
 assert.equal(next.paths['/orders/{id}'].post.parameters,undefined)

 assert.throws(()=>updateParameters(text,'get /orders/{id}',[]),/路径级参数/)
 assert.throws(()=>updateParameters(text,'get /orders/{id}',[{...params[0],name:'other'}]),/路径级参数/)
 const schema=resolveLocal({$ref:'#/components/schemas/User'},doc);schema.description='当前接口'
 assert.equal(doc.components.schemas.User.description,undefined)
 assert.throws(()=>resolveLocal({$ref:'#/missing'},doc))
 assert.throws(()=>resolveLocal({$ref:'https://example.test/schema'},doc))
 assert.match(updateOperation(text,'get /orders/{id}',o=>o.parameters=params),/9223372036854775807/)
})
test('advanced operation JSON preserves whole-document context and rejects invalid replacement',()=>{
 const doc=parseDocumentText(text),op=doc.paths['/orders/{id}'].get;op.description='增加说明'
 const next=editOperationJSON(text,'get /orders/{id}',formatValue(op))
 assert.match(next,/9223372036854775807/);assert.equal(parseDocumentText(next).paths['/orders/{id}'].get.description,'增加说明')
 assert.ok(parseDocumentText(next).paths['/orders/{id}'].post)
 assert.throws(()=>editOperationJSON(text,'get /orders/{id}','{}'))
})
test('three-way merge preserves disjoint edits and requires explicit conflicts, including delete and large numbers',()=>{
 const mine=patchDocumentText(text,'get /orders/{id}','summary','我的标题'),latest=patchDocumentText(text,'get /orders/{id}','description','别人补充的说明')
 let v=mergeDocumentTexts(text,mine,latest);assert.equal(v.conflicts.length,0);assert.match(v.text,/我的标题/);assert.match(v.text,/别人补充的说明/);assert.match(v.text,/9223372036854775807/)
 const competing=patchDocumentText(latest,'get /orders/{id}','summary','最新标题');v=mergeDocumentTexts(text,mine,competing);assert.equal(v.conflicts.length,1)
 const path=v.conflicts[0].path;v=mergeDocumentTexts(text,mine,competing,{[path]:'latest'});assert.equal(v.conflicts.length,0);assert.match(v.text,/最新标题/)
 const removed=updateOperation(text,'get /orders/{id}',o=>delete o.summary);v=mergeDocumentTexts(text,removed,competing);assert.equal(v.conflicts.length,1);assert.equal(v.conflicts[0].mine,'（删除）')
})
