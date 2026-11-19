import test from 'node:test'
import assert from 'node:assert/strict'
import {environmentHostOptions,selectedDeploymentHosts} from './deploymentHosts.js'
const hosts=[{name:'test-a',environment:'test',addr:'a'},{name:'test-b',environment:'test',addr:'b'},{name:'prod-a',environment:'prod',addr:'c'}]
test('direct targets need no group and preserve explicit ordering',()=>{
 assert.equal(environmentHostOptions(hosts,'test').length,2)
 assert.deepEqual(selectedDeploymentHosts({environment:'test',target_type:'docker-host',host_group:'stale',hosts:['test-b','test-a']},'direct',[],hosts),{host_group:'',hosts:['test-b','test-a']})
})
test('direct hosts reject stale/cross environment/duplicate/empty selections',()=>{
 for(const selected of [[],['prod-a'],['unknown'],['test-a','test-a']])assert.throws(()=>selectedDeploymentHosts({environment:'test',hosts:selected},'direct',[],hosts))
})
test('group remains optional and authoritative, cloud/manual clear machine bindings',()=>{
 const form={environment:'test',target_type:'docker-host',host_group:'pool',hosts:['prod-a']}
 assert.deepEqual(selectedDeploymentHosts(form,'group',[{name:'pool',environment:'test',hosts:['test-a']}],hosts),{host_group:'pool',hosts:['test-a']})
 assert.throws(()=>selectedDeploymentHosts(form,'group',[{name:'pool',environment:'prod',hosts:['prod-a']}],hosts))
 assert.deepEqual(selectedDeploymentHosts({...form,target_type:'manual'},'direct',[],hosts),{host_group:'',hosts:[]})
})
