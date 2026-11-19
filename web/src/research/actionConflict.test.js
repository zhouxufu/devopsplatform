import {test} from 'node:test'
import assert from 'node:assert/strict'
import {refreshActionConflict} from './actionConflict.js'

const detail={item:{id:5,title:'他人修改的标题',metadata_revision:3},remote:{bug:{status:'active',assignedTo:'alice'}},source_identity:'personal'}
test('conflict refresh keeps user fields and performs reads only',async()=>{
 const calls=[],updated=[]
 const draft={action:'assignTo',expected_digest:'old',operation_id:'same-prewrite-id',assigned_to:'qa',comment:'我的说明',attachments:['upload1'],keep_following:true,title:'草稿标题',description:'草稿正文',metadata:{revision:2,release_required:false,no_release_reason:'重复'}}
 const read=async path=>{calls.push(path);return path.includes('action-options')?{digest:'new',users:{qa:'测试'}}:detail}
 const result=await refreshActionConflict(5,draft,read,v=>updated.push(v))
 assert.equal(calls.length,2);assert.equal(updated[0],detail)
 assert.equal(result.form.expected_digest,'new');assert.equal(result.form.metadata.revision,3)
 for(const key of ['operation_id','assigned_to','comment','attachments','keep_following','title','description'])assert.deepEqual(result.form[key],draft[key])
 assert.equal(result.form.metadata.no_release_reason,'重复')
 assert.equal(draft.expected_digest,'')
})
test('refresh failure invalidates stale digest without losing drafts',async()=>{
 const draft={action:'resolve',expected_digest:'stale',comment:'说明',attachments:['image'],resolved_build:'trunk'},updates=[]
 await assert.rejects(refreshActionConflict(5,draft,async path=>{if(path.includes('action-options'))throw Error('网络异常');return detail},v=>updates.push(v)),/网络异常/)
 assert.equal(draft.expected_digest,'');assert.equal(draft.comment,'说明');assert.deepEqual(draft.attachments,['image']);assert.equal(updates.length,1)
})
test('public fallback cannot renew a personal write snapshot',async()=>{
 let reads=0
 await assert.rejects(refreshActionConflict(5,{action:'assignTo'},async()=>{reads++;return {...detail,source_identity:'public'}}),/本人来源账号/)
 assert.equal(reads,1)
})
test('comment reload uses edit options without replaying the comment',async()=>{
 const calls=[]
 await refreshActionConflict(5,{action:'comment',comment:'文本'},async path=>{calls.push(path);return path.includes('action-options')?{digest:'new'}:detail})
 assert.equal(calls[1],'research/work-items/5/action-options?action=edit')
})
