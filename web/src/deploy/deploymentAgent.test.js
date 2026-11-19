import test from 'node:test'
import assert from 'node:assert/strict'
import {agentTransport,changeAgentTransport,eligibleDeploymentAgents} from './deploymentAgent.js'

test('ordinary JAR keeps default transport and existing runtime fields',()=>{
 const original={java_mode:'managed',java_version:'8',health_failure_action:'rollback'}
 assert.equal(agentTransport(original),false)
 assert.deepEqual(changeAgentTransport(original,'ssh'),original)
 const agent=changeAgentTransport(original,'agent');assert.equal(agentTransport(agent),true);assert.equal(agent.health_failure_action,'rollback');assert.deepEqual(original,{java_mode:'managed',java_version:'8',health_failure_action:'rollback'})
})
test('changing transport clears stale single or frozen host bindings only',()=>{
 const old={deployment_agent_id:'A',deployment_agent_ids:'{"h":"A"}',java_bundle_jar:'lib/gis.jar',health_failure_action:'keep'}
 const agent=changeAgentTransport(old,'agent');assert.deepEqual(agent,{deployment_transport:'agent',java_bundle_jar:'lib/gis.jar',health_failure_action:'keep'})
 assert.equal(agentTransport(changeAgentTransport(agent,'ssh')),false)
})
test('optional Agent entry needs unique registration for every selected host',()=>{
 const agents=[{host:'a',enabled:true},{host:'b',enabled:true}]
 assert.equal(eligibleDeploymentAgents(agents,['a','b']),true)
 assert.equal(eligibleDeploymentAgents(agents,['a','missing']),false)
 assert.equal(eligibleDeploymentAgents([...agents,{host:'a',enabled:true}],['a']),false)
 assert.equal(eligibleDeploymentAgents(agents,[]),false)
})
