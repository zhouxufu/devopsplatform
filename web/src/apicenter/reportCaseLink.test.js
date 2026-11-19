import test from 'node:test'
import assert from 'node:assert/strict'
import {reportCaseLink} from './reportCaseLink.js'
import {runtimeWorkspaceHash,safeWorkspaceHash} from '../shell/workspaceSession.js'

test('report maintenance points to the frozen case identity and leaves the report unchanged',()=>{
 const run={case_id:42,case_revision:1,service:'orders',case_snapshot:{id:42,revision:1,service:'orders'},steps:[{status:'failed'}]}
 const before=JSON.stringify(run),link=reportCaseLink(run)
 assert.deepEqual(link,{id:42,revision:1,hash:'#/test-cases?service=orders&case=42'})
 assert.equal(runtimeWorkspaceHash(link.hash,'api-center'),link.hash)
 assert.equal(safeWorkspaceHash(link.hash),'#/test-cases?service=orders')
 assert.equal(JSON.stringify(run),before)
})
test('suite reports choose the first failing unit, or the explicitly selected unit',()=>{
 const run={service:'orders',suite_id:5,steps:[{status:'success'},{status:'unknown'}],units:[{start:0,end:1,case_snapshot:{id:10,revision:2,service:'orders'}},{start:1,end:2,case_snapshot:{id:20,revision:4,service:'payments'}}]}
 assert.equal(reportCaseLink(run).hash,'#/test-cases?service=payments&case=20')
 assert.equal(reportCaseLink(run,0).id,10)
 assert.equal(reportCaseLink({...run,steps:[{status:'success'},{status:'success'}]}),null)
 assert.equal(reportCaseLink(run,2),null)
 assert.equal(reportCaseLink(run,-1),null)
})
test('cross-application reports preserve scenario scope without borrowing the current application',()=>{
 assert.equal(reportCaseLink({scenario:true,case_id:7,service:'orders',case_revision:3}).hash,'#/test-cases?scope=scenario&case=7')
 assert.equal(reportCaseLink({case_snapshot:{id:9,scope:'scenario',revision:2}}).hash,'#/test-cases?scope=scenario&case=9')
})
test('manual, invalid and unattributed reports cannot fabricate a case link',()=>{
 for(const run of [null,{}, {case_id:-1,service:'orders'},{case_id:1.5,service:'orders'},{case_id:1},{case_id:Infinity,service:'orders'}])assert.equal(reportCaseLink(run),null)
})
