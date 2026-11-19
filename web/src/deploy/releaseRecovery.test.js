import test from 'node:test'
import assert from 'node:assert/strict'
import {blockingRelease,canRecheckJavaRelease,originalReviewDeploys,autoRecheckBusy,autoRecheckText} from './releaseRecovery.js'
test('recovery targets original application environment and deployment identity',()=>{
 const order={id:26,number:6,service:'bus',environment:'dev',artifact_id:21,status:'needs_review',deploy_ids:[26],target_snapshot:{target_type:'ssh-jar',deploy_config:{java_mode:'managed'}}}
 assert.equal(blockingRelease([order],'bus','prod'),null)
 assert.equal(blockingRelease([{...order,status:'success'},order],'bus','dev'),order)
 assert.equal(canRecheckJavaRelease(order),true)
 assert.equal(canRecheckJavaRelease({...order,target_snapshot:{...order.target_snapshot,deploy_config:{java_mode:'managed',deployment_agent_id:'agent'}}}),false)
 const d={id:26,service:'bus',environment:'dev',artifact_id:21,status:'needs_review'}
 assert.deepEqual(originalReviewDeploys(order,[d,{...d,id:27},{...d,environment:'prod'},{...d,artifact_id:22}]),[d])
 assert.equal(canRecheckJavaRelease({...order,status:'success'}),false)
})

test('automatic observation exposes actionable state and only disables a live check',()=>{
 assert.equal(autoRecheckBusy({state:'checking',lease_until:200},100),true)
 assert.equal(autoRecheckBusy({state:'checking',lease_until:200},201),false)
 assert.match(autoRecheckText({state:'checking',attempts:2}),/第 2 次/)
 assert.match(autoRecheckText({state:'manual',message:'原制品不匹配'}),/需人工核对.*原制品不匹配/)
 assert.match(autoRecheckText({state:'scheduled',next_at:180,message:'仍在启动'}),/等待自动核对.*仍在启动/)
})
