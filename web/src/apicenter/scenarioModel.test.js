import test from 'node:test'
import assert from 'node:assert/strict'
import {scenarioTargets,scenarioBlocker} from './scenarioModel.js'
import {caseDraft,casePayload} from './caseModel.js'
test('a scenario preserves node application identities through editing and serialization',()=>{
 const c=caseDraft({scope:'scenario',name:'跨应用',steps:[{id:'a',name:'订单',service:'orders',application_id:'app-a',request:{path:'/orders'}},{id:'b',name:'付款',service:'payments',application_id:'app-b',request:{path:'/payments'}}]})
 const p=casePayload(c);assert.equal(p.scope,'scenario');assert.deepEqual(p.steps.map(s=>[s.service,s.application_id]),[['orders','app-a'],['payments','app-b']]);c.steps[1].service='';assert.throws(()=>casePayload(c),e=>e.field==='service'&&e.stepIndex===1)
})
test('all scenario application targets and credentials must be valid before execution',()=>{
 const context={case:{steps:[{},{}]},applications:['orders','payments'].map(service=>({service,enabled:true,variables_digest:'vars-'+service,targets:[{key:'entry',fingerprint:'fp-'+service,credential_references_valid:true}]}))}
 const input={loading:false,error:'',context,environment:'test',caseID:1,selection:{orders:'entry',payments:'entry'},admin:false}
 assert.equal(scenarioBlocker(input),'');assert.deepEqual(scenarioTargets(context,input.selection),{orders:{key:'entry',fingerprint:'fp-orders',variables_digest:'vars-orders'},payments:{key:'entry',fingerprint:'fp-payments',variables_digest:'vars-payments'}})
 assert.match(scenarioBlocker({...input,selection:{orders:'entry'}}),/payments/)
 context.applications[1].targets[0].credential_references_valid=false;assert.match(scenarioBlocker(input),/认证变量/)
 context.applications[1].targets[0].credential_references_valid=true;context.protected=true;assert.match(scenarioBlocker(input),/管理员/)
 assert.equal(scenarioBlocker({...input,admin:true}),'')
 assert.match(scenarioBlocker({...input,environment:''}),/同一环境/)
})
