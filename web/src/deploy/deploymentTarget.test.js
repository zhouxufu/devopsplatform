import test from 'node:test'
import assert from 'node:assert/strict'
import {deploymentTargetReady,buildDeployBlocker} from './deploymentTarget.js'
test('manual publication requires instructions but no invented host resources',()=>{
 const target={target_type:'manual',protected:true,require_reason:true,deploy_config:{manual_instructions:'核对审核制品后按现有流程发布'}}
 assert.equal(deploymentTargetReady(target),true)
 assert.match(buildDeployBlocker(target,'变更'),/审批人/)
 target.approvers='reviewer';assert.match(buildDeployBlocker(target),/发布原因/)
 assert.equal(buildDeployBlocker(target,'变更'),'')
 assert.equal(deploymentTargetReady({...target,deploy_config:{manual_instructions:'  '}}),false)
 assert.equal(deploymentTargetReady({target_type:'docker-host',host_group:'g',hosts:[]}),false)
 assert.equal(deploymentTargetReady({target_type:'docker-host',host_group:'g',hosts:['h']}),true)
})

test('registered direct host targets are ready across application list, build continuation and build/publish dialogs',()=>{
 const direct={target_type:'docker-host',hosts:['registered-test-host'],protected:true,require_reason:true,approvers:'reviewer'}
 assert.equal(deploymentTargetReady(direct),true)
 assert.match(buildDeployBlocker(direct,''),/发布原因/)
 assert.equal(buildDeployBlocker(direct,'verified change'),'')
 assert.match(buildDeployBlocker({...direct,approvers:''},'reason'),/审批人/)
 assert.equal(deploymentTargetReady({...direct,hosts:[]}),false)
})
