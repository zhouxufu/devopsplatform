import test from 'node:test'
import assert from 'node:assert/strict'
import {resourceDocumentEditable,resourceConnectionStatus,nacosReferenceKey,resourceSaveBody,resourceEnvironmentCounts,resourceEnvironmentComparison,rebaseResourceDraft,mergeDiscoveredResources} from './resourceExperience.js'
const binding=(alias,environment,connection)=>({alias,environment,connection,config:{database:'app'}})
const draft=()=>({revision:3,requirements:[{alias:'db',kind:'mysql'},{alias:'cache',kind:'redis'}],bindings:[binding('db','dev','dev-db'),binding('db','test','test-db')],nacos_configs:[],resource_groups:[]})
test('catalog identity keeps environment and connection independent',()=>{
 const a={environment:'dev',connection:'nacos',group:'COMMON',data_id:'app.yaml'}
 assert.notEqual(nacosReferenceKey(a),nacosReferenceKey({...a,environment:'test'}))
 assert.notEqual(nacosReferenceKey(a),nacosReferenceKey({...a,connection:'other'}))
})
test('save contains revision and explicit own mappings, never check evidence',()=>{
 const input={...draft(),checks:[{status:'passed'}],error:'diagnostic',own_bindings:[binding('db','prod','must-not-replace')]}
 const saved=resourceSaveBody(input)
 assert.equal(saved.revision,3);assert.equal(saved.checks,undefined);assert.equal(saved.error,undefined)
 assert.deepEqual(saved.own_bindings,input.bindings)
})
test('three-way merge preserves remote unrelated edits and requires per-target conflict choice',()=>{
 const baseline=draft(),local=structuredClone(baseline),remote=structuredClone(baseline)
 local.bindings[0].connection='local-dev';remote.bindings[1].connection='new-test';remote.revision=4
 let result=rebaseResourceDraft(baseline,local,remote)
 assert.equal(result.conflicts.length,0);assert.equal(result.draft.revision,4)
 assert.deepEqual(result.draft.bindings.map(b=>b.connection),['local-dev','new-test'])
 remote.bindings[0].connection='remote-dev'
 result=rebaseResourceDraft(baseline,local,remote)
 assert.equal(result.conflicts.length,1)
 const id=result.conflicts[0].id
 result=rebaseResourceDraft(baseline,local,remote,{[id]:'local'})
 assert.equal(result.conflicts.length,0);assert.deepEqual(result.draft.bindings.map(b=>b.connection),['local-dev','new-test'])
 assert.equal(baseline.bindings[0].connection,'dev-db')
})
test('three-way merge handles local deletion and remote addition without dropping either',()=>{
 const baseline=draft(),local=draft(),remote=draft();local.bindings.shift();remote.bindings.push(binding('cache','test','redis'))
 const result=rebaseResourceDraft(baseline,local,remote)
 assert.equal(result.conflicts.length,0);assert.deepEqual(result.draft.bindings.map(b=>b.connection),['test-db','redis'])
})
test('selected discovery candidates only modify their exact application draft targets',()=>{
 const original=draft(),candidate={applyable:true,alias:'db',kind:'mysql',environment:'dev',requirement:{alias:'db',kind:'mysql'},binding:binding('db','dev','detected')}
 const merged=mergeDiscoveredResources(original,[candidate,{...candidate,applyable:false,environment:'prod',binding:binding('db','prod','blocked')}])
 assert.deepEqual(merged.bindings.map(b=>b.connection),['test-db','detected'])
 assert.equal(original.bindings[0].connection,'dev-db');assert.equal(merged.bindings.some(b=>b.environment==='prod'),false)
 assert.throws(()=>mergeDiscoveredResources(original,[{...candidate,kind:'redis'}]),/其他类型/)
})
test('saved declarations and actual bindings are distinct from verified evidence',()=>{
 const input=draft();input.checks=[{alias:'db',environment:'dev',status:'passed'},{alias:'db',environment:'test',status:'failed'}]
 const counts=resourceEnvironmentCounts(input,[{name:'dev'},{name:'test'}])
 assert.deepEqual(counts.map(e=>[e.declared,e.bound,e.passed,e.failed]),[[2,1,1,0],[2,1,0,1]])
 assert.equal(resourceConnectionStatus({secret_configured:true}),'未测试')
 assert.equal(resourceConnectionStatus({last_check:{status:'stale'}},'ok'),'配置已变化')
 assert.equal(resourceConnectionStatus({last_check:{status:'passed'}},'testing'),'测试中…')
})
test('merging a local target change invalidates server check evidence',()=>{
 const baseline=draft(),local=draft(),remote=draft()
 remote.checks=[{alias:'db',environment:'dev',status:'passed'},{alias:'db',environment:'test',status:'passed'}]
 local.bindings[0].connection='different'
 const result=rebaseResourceDraft(baseline,local,remote)
 assert.deepEqual(result.draft.checks.map(c=>c.status),['stale','passed'])
 assert.equal(remote.checks[0].status,'passed')
})

test('invalid parsed references can be repaired without allowing corrupt or changing documents to be overwritten',()=>{
 assert.equal(resourceDocumentEditable({composition_version:2,error:'请选择环境',nacos_configs:[{alias:'old',environment:''}]}),true)
 assert.equal(resourceDocumentEditable({composition_version:2,error:'资源组不存在',resource_groups:['removed']}),true)
 assert.equal(resourceDocumentEditable({composition_version:2,error:'应用资源配置损坏'}),false)
 assert.equal(resourceDocumentEditable({error:'应用资源正在变化',requirements:[],bindings:[]}),false)
})

test('source observations classify only unbound targets and survive draft merge and serialization',()=>{
 const input={requirements:[{alias:'unused',kind:'mysql',sources:{dev:{state:'not_applicable'}}},{alias:'pending',kind:'mysql',sources:{dev:{state:'pending_source'}}},{alias:'missing',kind:'nacos',sources:{dev:{state:'missing_config'}}},{alias:'legacy',kind:'mysql'}],bindings:[],checks:[]}
 let count=resourceEnvironmentCounts(input,[{name:'dev'}])[0]
 assert.deepEqual([count.missing,count.not_applicable,count.pending_source,count.missing_config],[1,1,1,1])
 assert.deepEqual(resourceEnvironmentComparison(input,[{name:'dev'}]).map(row=>row.targets[0].status),['not_applicable','pending_source','missing_config','unbound'])
 const candidate={can_declare:true,alias:'pending',kind:'mysql',environment:'test',requirement:{alias:'pending',kind:'mysql',sources:{test:{state:'declared',file:'bootstrap.yml',digest:'hmac:proof'}}}}
 const merged=mergeDiscoveredResources(input,[candidate])
 assert.equal(merged.bindings.length,0)
 assert.equal(merged.requirements[1].sources.dev.state,'pending_source')
 assert.equal(input.requirements[1].sources.test,undefined)
 assert.equal(resourceSaveBody(merged).own_requirements[1].sources.test.digest,'hmac:proof')
 input.bindings=[binding('unused','dev','broken')];input.checks=[{alias:'unused',environment:'dev',status:'failed'}]
 count=resourceEnvironmentCounts(input,[{name:'dev'}])[0]
 assert.equal(count.failed,1);assert.equal(count.not_applicable,0)
 assert.equal(resourceEnvironmentComparison(input,[{name:'dev'}])[0].targets[0].status,'failed')
 const remote=structuredClone(merged);remote.revision=2;remote.requirements[1].sources.dev={state:'declared',file:'remote.yml'}
 const local=structuredClone(merged);local.requirements[1].sources.dev={state:'not_applicable'}
 assert.equal(rebaseResourceDraft(merged,local,remote).conflicts.length,1)
})
