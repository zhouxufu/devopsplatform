import test from 'node:test'
import assert from 'node:assert/strict'
import {chooseWorkspace,workspaceTarget} from './workspaceTarget.js'
import {runtimeWorkspaceHash,workspaceSnapshot} from './workspaceSession.js'
import {workspaceTitle} from './workspaceTitle.js'
const plugin={name:'api-center'}
const doc=(id=7,app='orders',env='test',operation='')=>'#/documents?'+new URLSearchParams({service:app,environment:env,snapshot:String(id),...(operation?{operation}:{})})
const ws=(id,hash,extra={})=>({id,plugin,hash,...extra})
test('same document identity reuses a tab regardless of query ordering or protocol default',()=>{
 const current=ws(1,doc())
 assert.equal(chooseWorkspace([current],plugin.name,'#/documents?snapshot=7&environment=test&service=orders&protocol=http').workspace.id,1)
 for(const other of [doc(8),doc(7,'billing'),doc(7,'orders','prod'),doc()+'&protocol=dubbo'])assert.equal(chooseWorkspace([current],plugin.name,other),null)
})
test('global search detail and application document share exact identity while preserving search state',()=>{
 const current=ws(1,'#/documents?environment=__all&q=media&snapshot=7&detail_service=orders&detail_environment=test&detail_protocol=http&operation=get%20/a')
 const match=chooseWorkspace([current],plugin.name,doc(7,'orders','test','get /b'))
 assert.equal(match.workspace.id,1);assert.equal(match.navigate,true)
 const q=new URL(match.hash.slice(1),'http://fixture').searchParams
 assert.equal(q.get('q'),'media');assert.equal(q.get('environment'),'__all');assert.equal(q.get('operation'),'get /b')
})
test('draft and busy tabs may be activated unchanged but never navigated to another operation',()=>{
 for(const flags of [{dirty:true},{busy:true}]){
  const current=ws(1,doc(7,'orders','test','get /a'),flags)
  assert.equal(chooseWorkspace([current],plugin.name,doc(7,'orders','test','get /a')).navigate,false)
  assert.equal(chooseWorkspace([current],plugin.name,doc(7,'orders','test','get /b')),null)
 }
})
test('menus resume the most recently used overview and do not consume a detail or draft tab',()=>{
 const list=[ws(1,'#/documents?service=orders',{lastActivated:8}),ws(2,doc(),{lastActivated:20}),ws(3,'#/documents?service=billing',{lastActivated:15}),ws(4,'#/documents',{dirty:true,lastActivated:30})]
 const chosen=chooseWorkspace(list,plugin.name,'#/documents',{menu:true})
 assert.equal(chosen.workspace.id,3);assert.equal(chosen.navigate,false)
 assert.equal(chooseWorkspace([list[1],list[3]],plugin.name,'#/documents',{menu:true}),null)
 assert.equal(chooseWorkspace(list,plugin.name,'#/documents',{menu:true,forceNew:true}),null)
})
test('record identity survives in runtime only and protocol cannot mix HTTP and Dubbo',()=>{
 const id='a'.repeat(32),hash=`#/reports?service=orders&environment=test&protocol=http&report=${id}`
 const current=ws(1,runtimeWorkspaceHash(hash,plugin.name))
 assert.equal(chooseWorkspace([current],plugin.name,hash).workspace.id,1)
 assert.equal(chooseWorkspace([current],plugin.name,hash.replace('orders','other')),null)
 assert.equal(chooseWorkspace([current],plugin.name,hash.replace(id,'b'.repeat(32))),null)
 const saved=workspaceSnapshot([current],1)
 assert.equal(saved.windows[0].hash.includes('report='),false)
 const rpc=runtimeWorkspaceHash(doc(7,'orders','test','example.HouseService#get(java.lang.Long)')+'&protocol=dubbo',plugin.name)
 assert.equal(new URL(rpc.slice(1),'http://fixture').searchParams.get('operation'),'example.HouseService#get(java.lang.Long)')
})
test('explicit new tab bypasses reuse and title avoids duplicate page labels',()=>{
 const w=ws(1,doc(),{label:'接口文档 · 房源服务 · 测试 · 文档 #7'})
 assert.equal(chooseWorkspace([w],plugin.name,doc(),{forceNew:true}),null)
 assert.equal(workspaceTitle(w,[w]),'接口文档 · 房源服务 · 测试 · 文档 #7')
 const duplicate=ws(2,doc(),{label:w.label})
 assert.match(workspaceTitle(duplicate,[w,duplicate]),/窗口 2$/)
 assert.equal(workspaceTitle({...w,title:'自定义名称'},[w]),'自定义名称')
})
test('activation chooses active duplicate first and distinguishes generic tool context',()=>{
 const list=[ws(1,doc(),{lastActivated:9}),ws(2,doc(),{lastActivated:5})]
 assert.equal(chooseWorkspace(list,plugin.name,doc(),{active:2}).workspace.id,2)
 const logs={id:3,plugin:{name:'logs'},hash:'#/search',context:{service:'orders',environment:'test'}}
 assert.equal(chooseWorkspace([logs],'logs','#/search',{context:{service:'orders',environment:'prod'}}),null)
})

test('explicit document navigation leaves a comparison view but never overwrites its draft',()=>{
 const w=ws(1,doc()+'&tab=history&base_id=3&q=media')
 const chosen=chooseWorkspace([w],plugin.name,doc())
 assert.equal(chosen.navigate,true)
 const q=new URL(chosen.hash.slice(1),'http://fixture').searchParams
 assert.equal(q.has('tab'),false);assert.equal(q.has('base_id'),false);assert.equal(q.get('q'),'media')
 assert.equal(chooseWorkspace([{...w,dirty:true}],plugin.name,doc()),null)
})

test('overview duplicates use a single page label and remain distinguishable',()=>{
 const a=ws(1,'#/documents?service=orders',{label:'接口文档 · orders'}),b=ws(2,a.hash,{label:a.label})
 assert.equal(workspaceTitle(a,[a,b]),'接口文档 · orders · 1')
 assert.equal(workspaceTitle(b,[a,b]),'接口文档 · orders · 2')
})

test('historical unassigned environment aliases refer to the same document',()=>{
 const current=ws(1,doc(7,'orders','__unassigned')+'&detail_environment=')
 assert.equal(chooseWorkspace([current],plugin.name,doc(7,'orders','__unassigned')).workspace.id,1)
})

test('opening or editing a test case keeps the workspace title renderable',()=>{
 const overview=ws(1,'#/test-cases')
 const editor=ws(2,'#/test-cases?service=&case=12',{label:'测试用例'})
 const execution=ws(3,'#/test-execution?scope=scenario&case=12')
 const report=ws(4,'#/test-execution?service=orders&run=18',{label:'测试报告 · orders'})
 const workspaces=[overview,editor,execution,report]
 assert.equal(workspaceTitle(editor,workspaces),'测试用例 · 2')
 assert.equal(workspaceTitle(execution,workspaces),'执行测试')
 assert.equal(workspaceTitle(report,workspaces),'执行测试 · 测试报告 · orders')
})
