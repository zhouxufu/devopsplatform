import test from 'node:test'
import assert from 'node:assert/strict'
import {bugAPI,evidenceLogInput,evidenceLogHash,evidenceContext} from './bugEvidence.js'
import {runtimeWorkspaceHash,safeWorkspaceHash} from '../shell/workspaceSession.js'
test('evidence navigation retains runtime scope without saving execution selections',()=>{
 const regression='#/test-execution?service=orders&environment=test&bug=42&case=9&evidence_revision=3&target=host:node-1:8080&token=secret'
 const runtime=runtimeWorkspaceHash(regression,'api-center'),q=new URL(runtime.slice(1),'http://fixture').searchParams
 assert.equal(q.get('bug'),'42');assert.equal(q.get('case'),'9');assert.equal(q.get('target'),'host:node-1:8080');assert.equal(q.get('evidence_revision'),'3');assert.equal(q.has('token'),false)
 const saved=safeWorkspaceHash(runtime);assert.equal(saved.includes('bug='),false);assert.equal(saved.includes('target='),false);assert.equal(saved.includes('case='),false)
 const suite=runtimeWorkspaceHash('#/test-execution?suite=12&evidence_suite_revision=7&bug=42','api-center');assert.match(suite,/suite=12/);assert.match(suite,/evidence_suite_revision=7/)
})
test('logs retain source, bounded time and real trace; absence remains a time search',()=>{
 const hit={service:'orders',environment:'test',host:'test-host',ts:1_792_000_000_000,message:'ERROR fixture',trace_id:'trace-fixture-123'}
 const input=evidenceLogInput(hit,'splunk');assert.equal(input.log.source,'splunk');assert.equal(input.log.excerpt,hit.message)
 const hash=evidenceLogHash({service:'orders',log:input.log}),runtime=runtimeWorkspaceHash(hash,'logs'),q=new URL(runtime.slice(1),'http://fixture').searchParams
 assert.equal(q.get('trace_id'),hit.trace_id);assert.equal(q.get('log_source'),'splunk');assert.equal(q.get('host'),hit.host);assert.equal(Number(q.get('to'))-Number(q.get('from')),120000)
 assert.deepEqual(evidenceContext(runtime),{service:'orders',environment:'test',range:[hit.ts-60000,hit.ts+60000]})
 assert.equal(safeWorkspaceHash(runtime).includes('trace_id'),false)
 delete input.log.trace_id;const plain=evidenceLogHash({log:input.log});assert.equal(new URL(plain.slice(1),'http://fixture').searchParams.get('trace_id'),'')
 assert.equal(runtimeWorkspaceHash('#/search?evidence=1&from=1&to=9999999999999','logs').includes('evidence='),false)
})
test('association never treats malformed responses as success or replays a write',async()=>{
 const payload={request_key:'original-request-001',kind:'test',run_id:1},calls=[]
 const fetcher=async(url,options)=>{calls.push({url,options});return {ok:true,json:async()=>({})}}
 await assert.rejects(bugAPI('bug-evidence/link',payload,{fetcher}),/结果尚未确认/)
 assert.equal(calls.length,1);assert.deepEqual(JSON.parse(calls[0].options.body),payload)
 await assert.rejects(bugAPI('bug-evidence/link',payload,{fetcher:async()=>({ok:true,json:async()=>{throw Error('html')}})}),/关联结果尚未确认/)
 assert.deepEqual(await bugAPI('bug-evidence/link',payload,{fetcher:async()=>({ok:true,json:async()=>({id:7,work_item_id:8})})}),{id:7,work_item_id:8})
})
test('timeout aborts a hung association once and preserves the submission identity',async()=>{
 let calls=0
 const fetcher=(_url,{signal})=>new Promise((resolve,reject)=>{calls++;signal.addEventListener('abort',()=>reject(signal.reason),{once:true})})
 await assert.rejects(bugAPI('bug-evidence/link',{request_key:'original-request-001'},{fetcher,timeoutMS:5}),/结果尚未确认/)
 assert.equal(calls,1)
})
