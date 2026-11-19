import test from 'node:test'
import assert from 'node:assert/strict'
import {runtimeWorkspaceHash,safeWorkspaceHash,linkedWorkspaceContext,workspaceSnapshot} from './workspaceSession.js'
test('documentation and log handoffs survive runtime navigation without persisting execution scope',()=>{
 const input='#/testing?service=orders&snapshot=41&operation='+encodeURIComponent('get /orders/{id}')+'&token=private'
 const runtime=runtimeWorkspaceHash(input,'api-center'),q=new URL(runtime.slice(1),'http://fixture').searchParams
 assert.equal(q.get('snapshot'),'41');assert.equal(q.get('operation'),'get /orders/{id}');assert.equal(q.get('service'),'orders');assert.equal(q.has('token'),false)
 const saved=workspaceSnapshot([{id:1,plugin:{name:'api-center'},hash:runtime,context:{service:'orders'}}],1)
 assert.equal(saved.windows[0].hash,'#/testing?service=orders');assert.equal(saved.windows[0].context.service,'orders');assert.equal(JSON.stringify(saved).includes('snapshot'),false)
 const log=runtimeWorkspaceHash('#/search?service=orders&environment=test&request_id='+('a'.repeat(32))+'&from=1000&to=2000&password=secret','logs')
 assert.equal(new URL(log.slice(1),'http://fixture').searchParams.get('request_id'),'a'.repeat(32));assert.equal(safeWorkspaceHash(log),'#/search?service=orders&environment=test')
 assert.equal(runtimeWorkspaceHash('#//evil.example/x?operation=get%20/x','api-center'),'#/')
 assert.equal(runtimeWorkspaceHash('#/testing?operation='+encodeURIComponent('get //evil.example'),'api-center'),'#/testing')
})
test('linked context changes only the new window and rejects unbounded ranges',()=>{
 const original={service:'orders',environment:'test',range:[10,20],rangeMinutes:15}
 const child=linkedWorkspaceContext(original,{environment:'prod',range:[1000,2000],password:'secret'})
 assert.equal(original.environment,'test');assert.deepEqual(original.range,[10,20]);assert.equal(child.environment,'prod');assert.equal(child.rangeMinutes,0);assert.equal('password' in child,false)
 child.range[0]=555;assert.deepEqual(original.range,[10,20])
 assert.deepEqual(linkedWorkspaceContext(original,{range:[1,1e15]}).range,[10,20])
})
test('case identity is runtime context and never a persisted selected execution scope',()=>{
 const hash='#/test-execution?service=orders&case=42&token=secret'
 assert.ok(runtimeWorkspaceHash(hash,'api-center').includes('case=42'))
 assert.equal(runtimeWorkspaceHash(hash,'api-center').includes('secret'),false)
 assert.equal(safeWorkspaceHash(hash).includes('case='),false)
})
