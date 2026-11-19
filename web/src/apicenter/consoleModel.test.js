import test from 'node:test'
import assert from 'node:assert/strict'
import {contextBlocker,initialConsoleTab,reportReturnTab,requestName} from './consoleModel.js'
test('direct debugging and separate histories have unambiguous entry and return destinations',()=>{
 assert.equal(initialConsoleTab({}),'request')
 assert.equal(initialConsoleTab({view:'history',source:'manual'}),'manualHistory')
 assert.equal(initialConsoleTab({view:'history',source:'case'}),'caseHistory')
 assert.equal(initialConsoleTab({},true),'caseHistory')
 assert.equal(reportReturnTab('manualHistory',{case_id:5}),'manualHistory')
 assert.equal(reportReturnTab('caseHistory',{source:'manual'}),'caseHistory')
 assert.equal(reportReturnTab('request',{suite_id:1}),'caseHistory')
 assert.equal(reportReturnTab('request',{source:'manual'}),'manualHistory')
 assert.equal(requestName({method:'POST',path:'/orders'}),'POST /orders')
 assert.equal(requestName({path:'/orders?token=secret#fragment'}),'GET /orders')
 assert.equal(requestName({path:'x'.repeat(300)}).length,180)
})
test('disabled execution distinguishes unavailable context, disabled testing and protected access',()=>{
 const input={service:'orders',context:{environments:[{name:'test'}],targets:[{key:'entry'}],enabled:true},environment:'test',target:'entry',admin:false}
 assert.equal(contextBlocker(input),null)
 assert.equal(contextBlocker({...input,loading:true}).action,'')
 assert.equal(contextBlocker({...input,error:'failure'}).action,'refresh')
 assert.equal(contextBlocker({...input,context:{environments:[]}}).action,'environment')
 assert.match(contextBlocker({...input,context:{...input.context,enabled:false}}).message,/未启用/)
 assert.match(contextBlocker({...input,context:{...input.context,targets:[]}}).message,/暂无可用访问地址/)
 assert.match(contextBlocker({...input,target:''}).message,/请选择测试目标/)
 const protectedInput={...input,context:{...input.context,environments:[{name:'test',protected:true}]}}
 assert.match(contextBlocker(protectedInput).message,/仅管理员/)
 assert.equal(contextBlocker({...protectedInput,admin:true}),null)
})
