import test from 'node:test'
import assert from 'node:assert/strict'
import {deliveryFlow,deliveryStateLabel,deliveryHash} from './deliveryFlow.js'
test('准备完成只进入预检，不表示发布成功',()=>{
 const run={id:8,status:'success',preparation:true}
 assert.equal(deliveryFlow(run).index,3)
 assert.equal(deliveryFlow(run).complete,false)
 assert.equal(deliveryStateLabel(run),'准备完成 · 尚未发布')
 assert.match(deliveryHash(run),/preparation=8/)
 assert.match(deliveryHash({run_id:9}),/run=9/)
})
test('阻塞显示在原代码节点，审批与执行有独立阶段',()=>{
 const blocked={status:'blocked',plan:{preparation_request:{}},steps:[{kind:'manual',status:'success'},{kind:'code-prepare',status:'blocked'}]}
 assert.equal(deliveryFlow(blocked).index,2)
 assert.equal(deliveryStateLabel(blocked),'代码准备待处理')
 assert.equal(deliveryFlow({status:'pending_approval'}).index,3)
 assert.equal(deliveryFlow({status:'running'}).index,4)
 assert.equal(deliveryFlow({status:'success'}).complete,true)
})
test('准备草稿不把表单步骤误标为范围已确认或代码已完成',()=>{
 assert.equal(deliveryFlow(null,1).index,0)
 assert.equal(deliveryFlow(null,2).index,0)
 assert.equal(deliveryFlow(null,3).index,3)
})
