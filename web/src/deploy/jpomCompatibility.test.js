import test from 'node:test'
import assert from 'node:assert/strict'
import {jpomEnvironmentAllowed,selectJavaMode,jpomConfigurationError} from './jpomCompatibility.js'
test('overwrite compatibility is only offered for unprotected development/test',()=>{
 const environments=[{name:'d',purpose:'development'},{name:'t',purpose:'test'},{name:'p',purpose:'production'},{name:'s',purpose:'staging'},{name:'protected-test',purpose:'test',protected:true}]
 for(const name of ['d','t'])assert.equal(jpomEnvironmentAllowed(environments,name),true)
 for(const name of ['p','s','protected-test','missing'])assert.equal(jpomEnvironmentAllowed(environments,name),false)
 assert.equal(jpomEnvironmentAllowed(environments,'t',true),false)
})
test('selecting overwrite mode preserves runtime settings and uses SSH explicitly',()=>{
 const previous={java_mode:'managed',java_version:'8',java_args:'--profile=dev',jpom_project_id:'original-id',deployment_transport:'agent',deployment_agent_id:'old',deployment_agent_ids:'old2',custom:'retained'}
 const next=selectJavaMode(previous,'jpom');assert.equal(next.java_mode,'jpom');assert.equal(next.java_args,previous.java_args);assert.equal(next.java_version,'8');assert.equal(next.custom,'retained');assert.equal(next.deployment_transport,undefined);assert.equal(previous.deployment_transport,'agent')
 const managed=selectJavaMode(next,'managed');assert.equal(managed.health_failure_action,'rollback');assert.equal(managed.jpom_project_id,'original-id')
})
test('wrong project, directory-style JAR and relative logs are actionable fields',()=>{
 assert.equal(jpomConfigurationError({}).field,'Jpom 项目 ID')
 assert.equal(jpomConfigurationError({jpom_project_id:'original',jpom_jar_name:'../app.jar'}).field,'Jpom JAR 文件名')
 assert.equal(jpomConfigurationError({jpom_project_id:'original',jpom_jar_name:'app.jar',jpom_log_file:'relative.log'}).field,'Jpom 日志文件')
 assert.equal(jpomConfigurationError({jpom_project_id:'estate-bus',jpom_jar_name:'service-bus-hzfc-0.0.1.jar'}),null)
})

test('detected configuration changes only Java draft fields and preserves variables/policies',async()=>{
 const {applyJpomDetection}=await import('./jpomCompatibility.js')
 const form={environment:'test',remote_dir:'/managed',hosts:['one'],runtime_env_text:'KEEP=secret',deploy_config:{java_mode:'managed',deployment_transport:'agent',java_port:'8080',custom:'keep'}}
 const result={status:'matched',candidate:{remote_dir:'/original/jar',deploy_config:{java_mode:'jpom',jpom_project_id:'native',jpom_jar_name:'original.jar',java_version:'8',java_args:'--profile=test',untrusted:'ignored'}}}
 const next=applyJpomDetection(form,result)
 assert.equal(next.remote_dir,'/original/jar');assert.equal(next.runtime_env_text,form.runtime_env_text);assert.deepEqual(next.hosts,form.hosts);assert.equal(next.deploy_config.deployment_transport,undefined);assert.equal(next.deploy_config.untrusted,undefined);assert.equal(next.deploy_config.custom,'keep');assert.equal(form.remote_dir,'/managed')
 assert.equal(applyJpomDetection(form,{status:'ambiguous'}),null)
})
test('late detection cannot overwrite edits, another environment, or a closed editor',async()=>{
 const {createJpomDetector}=await import('./jpomCompatibility.js')
 for(const action of ['edit','environment','close','cancel']){
  let snapshot=JSON.stringify({form:{environment:'test',deploy_config:{}},mode:'direct'}),resolve;let applied=0;const notices=[],states=[]
  const detector=createJpomDetector({current:()=>snapshot,read:()=>new Promise(r=>resolve=r),apply:()=>applied++,notify:m=>notices.push(m),busy:v=>states.push(v)})
  const pending=detector.detect()
  if(action==='cancel')detector.cancel();else snapshot=action==='close'?null:JSON.stringify({form:{environment:action==='environment'?'dev':'test',edited:true}})
  resolve({status:'matched',candidate:{remote_dir:'/jar',deploy_config:{java_mode:'jpom',jpom_project_id:'native',jpom_jar_name:'original.jar'}}});await pending
  assert.equal(applied,0);assert.deepEqual(notices,[]);assert.equal(states.at(-1),false)
 }
})
test('detection failures preserve drafts, expose login errors and allow retry',async()=>{
 const {createJpomDetector}=await import('./jpomCompatibility.js')
 const notices=[];let applied=0,fail=true
 const detector=createJpomDetector({current:()=>JSON.stringify({form:{deploy_config:{}},mode:'direct'}),read:async()=>{if(fail)throw Error('登录已过期');return {status:'not_found',message:'未发现实例'}},apply:()=>applied++,notify:m=>notices.push(m),busy:()=>{}})
 await detector.detect();fail=false;await detector.detect();assert.equal(applied,0);assert.deepEqual(notices,['登录已过期','未发现实例'])
})
test('automatic probes target migration evidence and leave ordinary managed apps and manual choices alone',async()=>{
 const {shouldAutoDetectJpom}=await import('./jpomCompatibility.js')
 assert.equal(shouldAutoDetectJpom({remote_dir:'/opt/orders',deploy_config:{java_mode:'managed'}},'orders'),false)
 assert.equal(shouldAutoDetectJpom({remote_dir:'/old/.devopsplatform/orders',deploy_config:{java_mode:'managed'}},'orders'),true)
 assert.equal(shouldAutoDetectJpom({remote_dir:'/old/.devopsplatform/other',deploy_config:{java_mode:'managed'}},'orders'),false)
 assert.equal(shouldAutoDetectJpom({remote_dir:'/old/.devopsplatform/orders',deploy_config:{java_mode:'managed',java_detection:'manual'}},'orders'),false)
 assert.equal(shouldAutoDetectJpom({remote_dir:'/old/jar',deploy_config:{java_mode:'custom'}},'orders'),true)
})
