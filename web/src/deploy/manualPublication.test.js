import test from 'node:test'
import assert from 'node:assert/strict'
import {manualPublicationPayload as payload} from './manualPublication.js'
const now=Date.parse('2026-10-27T12:00:00+08:00')
const order={id:10,status:'waiting_manual',artifact_id:12,started_at:now/1000-3600,manual_publication:{artifact_id:12,digest:'sha256:reviewed'}}
const draft={confirmed:true,result:'success',performed_at:'2026-10-27T03:30:00Z',comment:'  核对制品后发布，接口检查正常  '}
test('manual result is bound to the displayed reviewed artifact, not editable draft identity',()=>{
 assert.deepEqual(payload(order,{...draft,artifact_id:999,digest:'forged'},now),{artifact_id:12,digest:'sha256:reviewed',result:'success',performed_at:now/1000-1800,comment:'核对制品后发布，接口检查正常'})
 assert.equal(payload(order,{...draft,result:'failed'},now).result,'failed')
})
test('manual publication rejects approval bypass, wrong identities and incomplete actual results',()=>{
 for(const status of ['pending_approval','success','canceled'])assert.throws(()=>payload({...order,status},draft,now))
 assert.throws(()=>payload({...order,artifact_id:13},draft,now))
 for(const change of [{confirmed:false},{result:'waiting_manual'},{comment:''},{comment:'中'.repeat(3000)},{performed_at:'invalid'},{performed_at:'2026-10-27T01:00:00Z'},{performed_at:'2026-10-28T04:00:00Z'}])assert.throws(()=>payload(order,{...draft,...change},now))
})
