import test from 'node:test'
import assert from 'node:assert/strict'
import {defaultDeploymentTarget,deploymentTargetOptions,switchDeploymentTarget,artifactKindMatchesTarget} from './environmentDeployment.js'
test('ordinary applications retain one automatic default and no extra setup',()=>{
 for(const [artifact_type,target] of Object.entries({jar:'ssh-jar',image:'docker-host',static:'ssh-static','java-bundle':'ssh-java-bundle'})){
  const service={artifact_type};assert.equal(defaultDeploymentTarget(service),target)
  assert.deepEqual(deploymentTargetOptions(service).map(o=>o.value),[target,'aliyun-sae','script','manual'])
 }
})
test('Java images can choose Java or Docker per environment without changing build type',()=>{
 for(const build_tool of ['maven','gradle'])assert.deepEqual(deploymentTargetOptions({artifact_type:'image',build_tool}).slice(0,2).map(o=>o.value),['docker-host','ssh-jar'])
 assert.equal(deploymentTargetOptions({artifact_type:'image'},[{kind:'jar'}])[1].value,'ssh-jar')
})
test('changing driver isolates mode-specific fields, preserves drafts and host scope',()=>{
 const form={target_type:'docker-host',remote_dir:'/',start_cmd:'',stop_cmd:'',health_url:'http://localhost:8080',hosts:['dev-node'],environment:'dev',runtime_env_text:'KEEP=draft',deploy_config:{network:'host',mounts:'private-docker-mount'}}
 const drafts={};switchDeploymentTarget(form,'ssh-jar',drafts)
 assert.equal(form.remote_dir,'');assert.equal(form.deploy_config.java_mode,'managed');assert.equal(form.deploy_config.network,undefined)
 form.remote_dir='/apps/orders';form.deploy_config.java_version='8';form.deploy_config.deploy_transport='agent'
 switchDeploymentTarget(form,'docker-host',drafts);assert.equal(form.remote_dir,'/');assert.equal(form.deploy_config.network,'host');assert.equal(form.deploy_config.deploy_transport,undefined)
 switchDeploymentTarget(form,'ssh-jar',drafts);assert.equal(form.remote_dir,'/apps/orders');assert.equal(form.deploy_config.java_version,'8');assert.deepEqual(form.hosts,['dev-node']);assert.equal(form.environment,'dev');assert.equal(form.runtime_env_text,'KEEP=draft')
 switchDeploymentTarget(form,'ssh-jar',drafts);assert.equal(form.remote_dir,'/apps/orders')
})
test('artifact picker follows selected environment kind, server still verifies provenance',()=>{
 const svc={artifact_type:'image'}
 for(const kind of ['oci-image','docker-image-archive']){
  assert.equal(artifactKindMatchesTarget({kind},{target_type:'docker-host'},svc),true)
  assert.equal(artifactKindMatchesTarget({kind},{target_type:'ssh-jar'},svc),false)
 }
 assert.equal(artifactKindMatchesTarget({kind:'jar'},{target_type:'ssh-jar'},svc),true)
 assert.equal(artifactKindMatchesTarget({kind:'jar'},{target_type:'docker-host'},svc),false)
 assert.equal(artifactKindMatchesTarget({kind:'jar'},{target_type:'manual',deploy_config:{manual_artifact_kind:'jar'}},svc),true)
 assert.equal(artifactKindMatchesTarget({kind:'jar'},null,svc),true)
})
