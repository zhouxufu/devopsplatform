import test from 'node:test'
import assert from 'node:assert/strict'
import {reportRelation,documentDestination,reportTime} from './reportModel.js'
test('report success remains independent from current document relation',()=>{
 const report={state:'saved',snapshot_id:37,current_id:38}
 assert.equal(reportRelation(report),'已有更新 #38')
 assert.equal(report.state,'saved')
 assert.match(reportRelation({...report,state:'historical'}),/匹配历史 #37/)
 assert.equal(reportRelation({...report,state:'unchanged',snapshot_id:38}),'当前文档')
 assert.equal(reportTime(0),'未记录')
})
test('document links retain application environment protocol and exact snapshot',()=>{
 const r={service:'orders',environment:'dev',protocol:'dubbo',snapshot_id:37,current_id:38}
 const original=new URL(documentDestination(r,{operation:'method/path'}).slice(1),'http://fixture')
 assert.equal(original.searchParams.get('service'),'orders')
 assert.equal(original.searchParams.get('environment'),'dev')
 assert.equal(original.searchParams.get('protocol'),'dubbo')
 assert.equal(original.searchParams.get('snapshot'),'37')
 assert.equal(original.searchParams.get('operation'),'method/path')
 assert.match(documentDestination(r,{current:true}),/snapshot=38/)
 assert.equal(documentDestination({...r,snapshot_id:0}),'')
})
