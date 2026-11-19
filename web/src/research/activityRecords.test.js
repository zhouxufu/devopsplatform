import test from 'node:test'
import assert from 'node:assert/strict'
import {mergeActivityRecords,localActivityContent} from './activityRecords.js'
const id='a'.repeat(32),next='b'.repeat(32)
const local={id:1,actor:'platform-user',source_actor:'source-user',operation_id:id,kind:'assignTo',content:'请处理',at:1001}
const action={actor:'source-user',action:'assigned',comment:`<p>请处理</p><br>[delivery-operation:${id}]`,date:'2026-10-23 07:57:48',history:[{field:'assignedTo',old:'qa',new:'dev'}]}
test('local change activities show stable identifiers and Chinese summaries, preserving unknown users',()=>{
 assert.equal(mergeActivityRecords([local],{}, {'platform-user':'周续福'})[0].actor,'周续福')
 assert.equal(localActivityContent({kind:'change-created',content:JSON.stringify({kind:'mysql',change_id:6,title:'建表',status:'draft'})}),'SQL #6 · 建表 · 草稿')
 assert.equal(localActivityContent({kind:'delivery-ownership',content:'{"mode":"follow","work_item_id":24}'}),'变更：随工作项 #24 交付')
 assert.equal(localActivityContent({kind:'release-intent',content:'{"release_required":null}'}),'上线意图待确认')
 assert.equal(localActivityContent({kind:'comment',content:'原始评论'}),'原始评论')
 assert.equal(localActivityContent({kind:'change-created',content:'null'}),'详情暂不可读，可展开查看原始记录')
})
test('one verified operation displays the source record with its fields and name',()=>{
 const result=mergeActivityRecords([local],{actions:{7:action},users:{'source-user':'来源姓名'}})
 assert.equal(result.length,1);assert.equal(result[0].actor,'来源姓名');assert.equal(result[0].source,true)
 assert.equal(result[0].history.length,1);assert.equal(result[0].content,'<p>请处理</p>')
})
test('genuine repeated comments remain separate, regardless of identical text or time',()=>{
 const result=mergeActivityRecords([local,{...local,id:2,operation_id:next}],{actions:{7:action,8:{...action,comment:action.comment.replace(id,next)}}})
 assert.equal(result.length,2);assert(result.every(a=>a.source))
 assert.equal(mergeActivityRecords([local],{actions:{7:{...action,comment:'请处理'}}}).length,2)
 assert.equal(mergeActivityRecords([local],{actions:{7:action,8:action}}).length,2)
})
test('missing source, mismatched identity or action never suppresses an audit record',()=>{
 assert.equal(mergeActivityRecords([local],{}).length,1)
 for(const a of [{...action,actor:'other'},{...action,action:'resolved'}])assert.equal(mergeActivityRecords([local],{actions:{7:a}}).length,2)
 assert.equal(mergeActivityRecords([{...local,operation_id:''}],{actions:{7:action}}).length,2)
})
test('source edited records corresponding to comments keep comment semantics and rich attachments',()=>{
 const result=mergeActivityRecords([{...local,kind:'comment'}],{actions:{7:{...action,action:'edited',comment:`<p>请处理<img src="/file/1.png"></p>[delivery-operation:${id}]`}}})
 assert.equal(result.length,1);assert.equal(result[0].kind,'comment');assert.match(result[0].content,/<img/)
})

test('manual item activity summaries tolerate malformed history and label transitions',()=>{
 assert.equal(localActivityContent({kind:'work-item-status',content:'{"status":["open","active"]}'}),'状态：待处理 → 处理中')
 assert.equal(localActivityContent({kind:'work-item-assigned',content:'{"assigned_to":["","bob"]}'}),'负责人：未指派 → bob')
 assert.equal(localActivityContent({kind:'work-item-updated',content:'{"priority":["3","2"],"severity":["2","1"]}'}),'更新了：优先级、严重程度')
 for(const content of ['null','[]','invalid'])assert.equal(localActivityContent({kind:'work-item-updated',content}),'工作项信息已更新')
})
