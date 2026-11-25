import test from 'node:test'
import assert from 'node:assert/strict'
import {agentForHost,hostForAgent} from './agentHostIdentity.js'

const host={name:'worker',environment:'dev'}, other={name:'worker',environment:'test'}
test('persistent Agent id opens its current resource host; legacy ids still work',()=>{
 const agent={id:'agent_123',name:'dev/worker',online:true}
 assert.equal(agentForHost(host,[agent]),agent)
 assert.equal(hostForAgent(agent.id,[host,other],[agent]),host)
 assert.equal(hostForAgent('test/worker',[host,other],[]),other)
 assert.equal(hostForAgent('agent_missing',[host,other],[agent]),null)
})
test('ambiguous names, old aliases and conflicting installed identities cannot retarget a host',()=>{
 const agents=[{id:'agent_1',name:'dev/worker'},{id:'agent_2',name:'dev/worker'}]
 assert.equal(agentForHost(host,agents),null)
 assert.equal(hostForAgent('agent_1',[host],agents),null)
 assert.equal(agentForHost(host,agents,{agent_id:'agent_2'}),agents[1])
 assert.equal(hostForAgent('agent_2',[host],agents,{worker:{agent_id:'agent_2'}}),host)
 assert.equal(hostForAgent('agent_1',[host],agents,{worker:{agent_id:'agent_2'}}),null)
 assert.equal(hostForAgent('agent_old',[host],[{id:'agent_old',name:'dev/new-name',aliases:['dev/worker']}]),null)
})
