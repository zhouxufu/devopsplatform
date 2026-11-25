import test from 'node:test'
import assert from 'node:assert/strict'
import {parse} from 'lossless-json'
import {operationDraft} from './testingModel.js'
import {caseDraft,casePayload} from './caseModel.js'
import {documentStep} from './caseDocumentModel.js'
import {parseDocumentText} from './documentEditModel.js'
const text='{"dubbo":"1.0","info":{"title":"RPC","version":"1"},"methods":[{"interface":"example.House","method":"get","parameters":[{"name":"id","java_type":"java.lang.Long","example":9223372036854775806}],"returns":{"java_type":"java.lang.String"}}]}'
const doc=parse(text),op={id:'dubbo:fixture',protocol:'dubbo',path:'example.House#get(java.lang.Long)',method:'DUBBO',rpc:JSON.parse(text).methods[0]}
test('native document seed preserves long precision and RPC identity through case payload',()=>{
 assert.equal(parseDocumentText(text).dubbo,'1.0')
 const seed=operationDraft(op,doc);assert.match(seed.request.body,/9223372036854775806/)
 const c=caseDraft({name:'RPC',steps:[{id:'rpc',kind:'dubbo',name:'查询',target_key:'endpoint:rpc',request:seed.request,extractions:[]}]})
 const saved=casePayload(c);assert.equal(saved.steps[0].kind,'dubbo');assert.equal(saved.steps[0].target_key,'endpoint:rpc');assert.equal(saved.steps[0].request.rpc.interface,'example.House');assert.match(saved.steps[0].request.body,/9223372036854775806/)
})
test('document picker accepts Dubbo and preserves lossless method example',()=>{
 const response='{"id":12,"snapshot":{"service":"orders","environment":"test"},"operations":'+JSON.stringify([op])+',"document":'+text+'}'
 const step=documentStep({}, {id:op.id,snapshot_id:12,service:'orders',environment:'test'},response,{service:'orders'})
 assert.equal(step.kind,'dubbo');assert.equal(step.operation_id,op.id);assert.match(step.request.body,/9223372036854775806/)
})

import {requestName} from './consoleModel.js'
test('RPC quick-call name preserves method identity',()=>{
 assert.equal(requestName({mode:'dubbo',rpc:{interface:'example.House',method:'get',parameter_types:['java.lang.Long']}}),'DUBBO example.House#get(java.lang.Long)')
})
