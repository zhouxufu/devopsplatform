import test from 'node:test'
import assert from 'node:assert/strict'
import {deliveryPreparation} from './deliveryPreparation.js'
test('unread and failed preparation is unknown even when stale ready content exists',()=>{
 const summary={changes:[{status:'ready'}]}
 assert.equal(deliveryPreparation(null).state,'loading')
 assert.equal(deliveryPreparation(summary,{loading:true}).total,null)
 assert.deepEqual(deliveryPreparation(summary,{error:'offline'}),{state:'error',label:'读取失败',total:null,ready:null})
})
test('content readiness never infers deployment from historical or unknown statuses',()=>{
 assert.equal(deliveryPreparation({changes:[]}).state,'empty')
 assert.equal(deliveryPreparation({changes:[{status:'ready'},{status:'draft'}]}).state,'draft')
 assert.equal(deliveryPreparation({changes:[{status:'completed'}]}).state,'draft')
 assert.equal(deliveryPreparation({changes:[{status:'future-status'}]}).state,'draft')
 const summary={changes:[{status:'ready'}]},before=JSON.stringify(summary)
 assert.equal(deliveryPreparation(summary).label,'内容已就绪')
 assert.equal(JSON.stringify(summary),before)
})
