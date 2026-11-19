import test from 'node:test'
import assert from 'node:assert/strict'
import {loopCanAdvance,loopScopeRows,loopCurrent,loopRelatedApplications,loopApplicationRows} from './workLoop.js'
test('自动推进只处理仍有效的运行中任务',()=>{
 const task={state:'running',stage:'delivery',paused:false,revoked:false}
 assert.equal(loopCanAdvance(task),true)
 for(const state of ['blocked','waiting_human','developing','awaiting_acceptance','completed'])assert.equal(loopCanAdvance({...task,state}),false)
 assert.equal(loopCanAdvance({...task,paused:true}),false)
 assert.equal(loopCanAdvance({...task,revoked:true}),false)
})
test('展示授权目标公共分支，不把源提交当成目标',()=>{
 assert.deepEqual(loopScopeRows({policies:{bus:{code_policy:{mode:'integration',target_branch:'master-dev-optimize'},build_variant:{name:'dev'}}}}),[{app:'bus',target:'master-dev-optimize',mode:'公共分支',variant:'dev'}])
 const old={number:1,state:'success'},current={number:2,state:'developing'}
 assert.equal(loopCurrent({iterations:[old,current]}),current)
 assert.deepEqual(old,{number:1,state:'success'})
})
test('应用候选只来自有效代码和制品变更，按稳定标识去重',()=>{
 const changes=[{service:'bus',kind:'code',status:'ready'},{service:'bus',kind:'code',status:'draft'},{service:'job',kind:'artifact',status:'ready'},{service:'old',kind:'code',status:'archived'},{service:'canceled',kind:'code',status:'canceled'},{service:'sql-only',kind:'mysql',status:'ready'}]
 assert.deepEqual(loopRelatedApplications(changes),['bus','job'])
 assert.deepEqual(loopRelatedApplications(),[])
})
test('刷新候选和目录不扩大原授权、不丢失目录中已缺失的原授权应用',()=>{
 const grantApplications=['bus','removed-from-catalog'],before=[...grantApplications]
 const rows=loopApplicationRows([{service:'new-related',kind:'code',status:'ready'}],[{name:'bus',display_name:'总平台'},{name:'unrelated',display_name:'无关应用'}],grantApplications)
 assert.deepEqual(rows,[{name:'new-related',label:'new-related',related:true},{name:'bus',label:'总平台',related:false},{name:'removed-from-catalog',label:'removed-from-catalog',related:false}])
 assert.deepEqual(grantApplications,before)
})
