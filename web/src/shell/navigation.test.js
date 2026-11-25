import test from 'node:test'
import assert from 'node:assert/strict'
import {safeWorkspaceHash} from './workspaceSession.js'
import {availableGroups,selectedNavigation,navigationLeaves,navigationPath,workspaceNavigation,homeWorkspaceTarget} from './navigation.js'
test('release planning belongs to research and is never offered in delivery navigation',()=>{
 const groups=availableGroups([{name:'research'},{name:'deploy'}])
 assert.equal(groups.find(g=>g.id==='research').items.find(i=>i.id==='versions').hash,'#/versions')
 assert.ok(navigationLeaves([groups.find(g=>g.id==='delivery')]).every(i=>!i.hash.includes('versions')))
 assert.equal(selectedNavigation('research','#/versions?item=28'),'versions')
 assert.equal(selectedNavigation('research','#/work/101?return=inbox'),'work')
 assert.equal(selectedNavigation('logs','#/live'),'live')
 assert.equal(selectedNavigation('deploy','#/changes?item=12&tab=changes'),'changes')
})
test('uninstalled plugin capabilities are absent from navigation',()=>{
 const groups=availableGroups([{name:'dbms'}])
 assert.equal(groups.length,1)
 assert.deepEqual(navigationLeaves(groups[0].items).map(i=>i.plugin),['dbms','dbms','dbms','dbms'])
})
test('research task lists never impersonate the fixed personal home tab',()=>{
 for(const [view,label] of [['todo','待我处理'],['owned','交付追踪'],['work','我的工作项'],['done','处理记录']]){
  const page=workspaceNavigation({plugin:{name:'research'},hash:'#/?view='+view+'&return=home'})
  assert.equal(page.id,'research-inbox');assert.equal(page.label,label)
 }
})
test('home tracking reuses its clean origin workspace without merging independent drafts',()=>{
 const make=(id,hash,extra={})=>({id,plugin:{name:'research'},hash,...extra})
 const tracking=make(1,'#/?view=owned&return=home')
 const standalone=make(2,'#/?view=owned')
 const hash='#/?view=owned&task=delivery:11&return=home'
 assert.equal(homeWorkspaceTarget([standalone,tracking],'research',hash),tracking)
 assert.equal(homeWorkspaceTarget([standalone],'research',hash),undefined)
 assert.equal(homeWorkspaceTarget([{...tracking,dirty:true}],'research',hash),undefined)
 assert.equal(homeWorkspaceTarget([{...tracking,busy:true}],'research',hash),undefined)
 assert.equal(homeWorkspaceTarget([tracking],'research','#/?view=todo&task=11:qa&return=home'),undefined)
 assert.equal(homeWorkspaceTarget([tracking],'research','#/work/11'),undefined)
 const exact=make(3,hash)
 assert.equal(homeWorkspaceTarget([tracking,exact],'research',hash),exact)
})
test('logging and monitoring are separate menu groups with bounded children',()=>{
 const groups=availableGroups([{name:'logs'},{name:'monitor'}])
 assert.deepEqual(groups.map(g=>g.label),['日志','监控'])
 assert.equal(navigationLeaves(groups[0].items).length,4)
 assert.ok(navigationLeaves(groups[0].items).every(i=>i.plugin==='logs'))
 assert.ok(navigationLeaves(groups[1].items).every(i=>i.plugin==='monitor'))
})

test('delivery subpages have distinct menu leaves, paths and workspace titles',()=>{
 const pages=[['#/orchestration?area=runs','orchestration','发布记录',2],['#/orchestration?area=templates','orchestration-templates','编排模板',3],['#/changes?tab=work&item=12','work','需求与 Bug',2],['#/changes?tab=changes&item=9','changes','变更记录',2]]
 for(const [hash,id,label,depth] of pages){assert.equal(selectedNavigation('deploy',hash),id);assert.equal(selectedNavigation('deploy',safeWorkspaceHash(hash)),id);assert.equal(workspaceNavigation({plugin:{name:'deploy'},hash}).label,label);assert.equal(navigationPath(id).length,depth)}
 assert.equal(selectedNavigation('deploy','#/orchestration?run=8'),'orchestration')
 assert.equal(selectedNavigation('deploy','#/changes'),'changes')
 assert.ok(navigationLeaves(availableGroups([{name:'research'}])).every(i=>i.plugin==='research'))
})

test('API documentation and testing remain independent menu leaves',()=>{
 const group=availableGroups([{name:'api-center'}]).find(g=>g.id==='api-center')
 assert.deepEqual(group.items.map(i=>i.hash),['#/documents','#/test-cases','#/test-execution'])
 assert.equal(selectedNavigation('api-center','#/test-execution?service=orders'),'api-testing')
})
test('business-owned plugins never duplicate in extension tools',async()=>{
 const {extensionPlugins}=await import('./navigation.js')
 const plugins=['research','deploy','dbms','redis','logs','monitor','api-center','devtools','delivery-core','platform','custom-tool'].map(name=>({name}))
 assert.deepEqual(extensionPlugins(plugins).map(p=>p.name),['custom-tool'])
 assert.equal(availableGroups(plugins).find(g=>g.id==='api-center').items.length,3)
})

test('shell breadcrumbs describe location independently of renamed workspace tabs',async()=>{
 const {workspaceBreadcrumb}=await import('./navigation.js')
 assert.deepEqual(workspaceBreadcrumb(null),['个人工作台'])
 assert.deepEqual(workspaceBreadcrumb({plugin:{name:'research'},hash:'#/versions',title:'我的版本窗口'}),['研发协作','发布版本'])
 assert.deepEqual(workspaceBreadcrumb({plugin:{name:'dbms'},hash:'#/query'}),['数据工具','数据库工作台'])
 assert.deepEqual(workspaceBreadcrumb({plugin:{name:'platform'},hash:'#/resources'}),['平台管理','资源中心'])
})
