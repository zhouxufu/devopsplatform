import test from 'node:test'
import assert from 'node:assert/strict'
import {scriptPublicationError,scriptReconciliationPayload} from './scriptPublication.js'
import {deploymentTargetReady} from './deploymentTarget.js'
import {selectedDeploymentHosts} from './deploymentHosts.js'

test('script publication validates literal arguments and requires an execution runner',()=>{
 const config={script:'echo "$1"',script_runner:'local',script_timeout_seconds:'600',script_arguments:'["x; $(touch nope)"]'}
 assert.equal(scriptPublicationError(config),'')
 for(const patch of [{script:''},{script_runner:''},{script_timeout_seconds:'0'},{script_timeout_seconds:'1.5'},{script_arguments:'{"arg":"x"}'},{script_arguments:'[42]'}])assert.ok(scriptPublicationError({...config,...patch}))
 assert.equal(deploymentTargetReady({target_type:'script',deploy_config:config}),true)
 assert.equal(deploymentTargetReady({target_type:'script',deploy_config:{script:'echo x'}}),false)
 assert.deepEqual(selectedDeploymentHosts({target_type:'script',hosts:['obsolete']},'direct'),{host_group:'',hosts:[]})
 assert.equal(deploymentTargetReady({target_type:'ssh-jar',hosts:['ordinary-host']}),true)
})

test('reconciliation binds the original script and artifact without issuing another publication',()=>{
 const order={id:31,artifact_id:9,script_publication:{needs_reconciliation:true,deploy_id:17,artifact_digest:'sha256:frozen',config_digest:'frozen-script'}}
 const draft={result:'failed',comment:' checked external job state ',confirm_stopped:true}
 assert.deepEqual(scriptReconciliationPayload(order,draft),{deploy_id:17,artifact_id:9,digest:'sha256:frozen',config_digest:'frozen-script',result:'failed',comment:'checked external job state',confirm_stopped:true})
 for(const change of [{confirm_stopped:false},{comment:'  '},{result:'retry'}])assert.throws(()=>scriptReconciliationPayload(order,{...draft,...change}))
 assert.throws(()=>scriptReconciliationPayload({...order,script_publication:{...order.script_publication,needs_reconciliation:false}},draft))
})
