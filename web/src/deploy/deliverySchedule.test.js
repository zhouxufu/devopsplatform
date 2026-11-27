import test from 'node:test'
import assert from 'node:assert/strict'
import {canDispose,dependentNodes,focusStep,waitingSteps} from './deliverySchedule.js'

test('skipping preserves the dependent chain while a manual receipt satisfies the original edge',()=>{
 const nodes=[{id:'gis'},{id:'gateway',depends_on:['gis']},{id:'statistics'},{id:'verify',depends_on:['gateway']}]
 assert.deepEqual(dependentNodes(nodes,'gis').map(n=>n.id),['gateway','verify'])
 const steps=[{node_id:'gis',status:'skipped'},{node_id:'statistics',status:'failed'}]
 assert.equal(waitingSteps(steps,nodes[1])[0],steps[0]);assert.equal(focusStep(steps),1)
 steps[0].status='manual_done';assert.deepEqual(waitingSteps(steps,nodes[1]),[])
 assert.equal(canDispose({kind:'approval'}),false);assert.equal(canDispose({kind:'verify',verification:{mode:'manual'}}),false)
})
