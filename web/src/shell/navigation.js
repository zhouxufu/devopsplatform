import {platformPageFromHash} from './platformPages.js'
// Menu ownership follows business scope, not the backend process serving it.
export const navigationGroups=[
 {id:'research',label:'研发协作',icon:'research',items:[{id:'work',label:'需求与 Bug',plugin:'research',hash:'#/work'},{id:'versions',label:'发布版本',plugin:'research',hash:'#/versions'},{id:'research-settings',label:'协作设置',plugin:'research',hash:'#/settings'}]},
 {id:'delivery',label:'应用交付',icon:'deploy',items:[
  {id:'build',label:'应用与构建',plugin:'deploy',hash:'#/'},
  {id:'changes',label:'变更记录',plugin:'deploy',hash:'#/changes?tab=changes'},
  {id:'orchestration',label:'发布记录',plugin:'deploy',hash:'#/orchestration?area=runs'},
  {id:'delivery-settings',label:'交付设置',items:[{id:'orchestration-templates',label:'编排模板',plugin:'deploy',hash:'#/orchestration?area=templates'}]},
 ]},
 {id:'api-center',label:'接口中心',icon:'api-center',items:[{id:'api-documents',label:'接口文档',plugin:'api-center',hash:'#/documents'},{id:'api-reports',label:'上报记录',plugin:'api-center',hash:'#/reports'},{id:'api-test-cases',label:'测试用例',plugin:'api-center',hash:'#/test-cases'},{id:'api-testing',label:'执行测试',plugin:'api-center',hash:'#/test-execution'}]},
 {id:'data',label:'数据工具',icon:'dbms',items:[
  {id:'database-group',label:'数据库',items:[{id:'dbms',label:'数据库工作台',plugin:'dbms',hash:'#/query'},{id:'sql-change',label:'SQL 变更提交',plugin:'dbms',hash:'#/operations'},{id:'sql-history',label:'数据库执行记录',plugin:'dbms',hash:'#/history'},{id:'sync',label:'数据迁移',plugin:'dbms',hash:'#/sync'}]},
  {id:'redis-group',label:'Redis',items:[{id:'redis',label:'Redis 工作台',plugin:'redis',hash:'#/query'},{id:'redis-change',label:'Redis 变更提交',plugin:'redis',hash:'#/operations'},{id:'redis-history',label:'Redis 执行记录',plugin:'redis',hash:'#/history'}]},
 ]},
 {id:'logging',label:'日志',icon:'logs',items:[
  {id:'log-query-group',label:'日志查看',items:[{id:'logs',label:'日志查询',plugin:'logs',hash:'#/search'},{id:'live',label:'实时日志',plugin:'logs',hash:'#/live'}]},
  {id:'log-config-group',label:'采集与订阅',items:[{id:'agents',label:'日志采集',plugin:'logs',hash:'#/status'},{id:'subs',label:'日志订阅',plugin:'logs',hash:'#/subs'}]},
 ]},
 {id:'monitoring',label:'监控',icon:'monitor',items:[
  {id:'monitor-view-group',label:'运行监测',items:[{id:'monitor',label:'监控概览',plugin:'monitor',hash:'#/'},{id:'machines',label:'主机监控',plugin:'monitor',hash:'#/machines'},{id:'api',label:'接口监控',plugin:'monitor',hash:'#/api'},{id:'jvm',label:'JVM 监控',plugin:'monitor',hash:'#/jvm'},{id:'trace',label:'链路追踪',plugin:'monitor',hash:'#/trace'}]},
  {id:'monitor-diagnosis-group',label:'运行诊断',items:[{id:'arthas',label:'Arthas 诊断',icon:'arthas',plugin:'monitor',hash:'#/arthas'}]},
  {id:'monitor-alert-group',label:'告警管理',items:[{id:'alerts',label:'告警',plugin:'monitor',hash:'#/alerts'},{id:'rules',label:'监控规则',plugin:'monitor',hash:'#/rules'}]},
 ]},
]
export function navigationLeaves(nodes=navigationGroups){return nodes.flatMap(node=>node.items?navigationLeaves(node.items):[node])}
export function navigationPath(key,nodes=navigationGroups){for(const node of nodes){if(node.id===key)return [key];const child=node.items&&navigationPath(key,node.items);if(child?.length)return [node.id,...child]}return []}
// Section titles are MenuItemGroup labels, not expandable third-level menus.
export function navigationOpenKeys(key,groups=navigationGroups){if(['apps','resources','mcp','plugins'].includes(key))return ['management'];const root=navigationPath(key,groups)[0];return root&&groups.some(g=>g.id===root&&g.items)?[root]:[]}
export function availableGroups(plugins){const names=new Set(plugins.map(p=>p.name));const filter=nodes=>nodes.flatMap(node=>{if(!node.items)return names.has(node.plugin)?[node]:[];const items=filter(node.items);return items.length?[{...node,items}]:[]});const groups=filter(navigationGroups)
 if(names.has('deploy')&&!names.has('research')){
  const delivery=groups.find(group=>group.id==='delivery')
  delivery.items.unshift({id:'work',label:'需求与 Bug',plugin:'deploy',hash:'#/changes?tab=work'})
 }
 return groups}
// Plugins already represented by business menus must not reappear as generic extensions.
export function extensionPlugins(plugins){
 const owned=new Set(['devtools','delivery-core','platform',...navigationLeaves().map(item=>item.plugin)])
 return plugins.filter(plugin=>!owned.has(plugin.name))
}
export function selectedNavigation(plugin,hash){
 if(plugin==='platform')return platformPageFromHash(hash)?.id||'management'
 const url=new URL((hash||'#/').slice(1),'http://navigation'),path=url.pathname
 if(plugin==='deploy'&&path==='/changes'&&url.searchParams.get('tab')==='work')return 'work'
 for(const item of navigationLeaves()){if(item.plugin!==plugin)continue;const target=new URL(item.hash.slice(1),'http://navigation');if(target.pathname===path&&[...target.searchParams].every(([key,value])=>url.searchParams.get(key)===value))return item.id}
 if(plugin==='api-center'&&path==='/testing')return 'api-testing'
 if(plugin==='deploy'&&path==='/orchestration')return 'orchestration'
 if(plugin==='deploy'&&path==='/changes')return 'changes'
 return plugin==='research'?(path.startsWith('/work/')?'work':'home'):plugin==='deploy'?'build':plugin==='dbms'?'dbms':plugin==='logs'?'logs':plugin==='monitor'?'monitor':plugin
}
export function workspaceNavigation(workspace){
 if(workspace.plugin.name==='platform'){const page=platformPageFromHash(workspace.hash);return page?{...page,id:'platform-'+page.id}:{id:'management',label:'平台管理',icon:'settings'}}
 if(workspace.plugin.name==='research'){
  const url=new URL((workspace.hash||'#/').slice(1),'http://navigation')
  if(url.pathname==='/')return {id:'research-inbox',label:researchInboxTitle(url.searchParams.get('view')),icon:'research'}
 }
 const id=selectedNavigation(workspace.plugin.name,workspace.hash),item=navigationLeaves().find(i=>i.id===id)
 if(item){const group=navigationGroups.find(g=>navigationPath(id,[g]).length);return {...item,icon:item.icon||group.icon}}
 return {id:workspace.plugin.name,label:workspace.plugin.display||workspace.plugin.name,icon:workspace.plugin.name}
}
export const researchInboxTitle=view=>({todo:'待我处理',owned:'交付追踪',work:'我的工作项',done:'处理记录'})[view]||'待我处理'

export function homeWorkspaceTarget(workspaces,plugin,hash){
 const matches=w=>w.plugin.name===plugin&&!w.dirty&&!w.busy
 const exact=workspaces.find(w=>matches(w)&&w.hash===hash)
 if(exact)return exact
 if(plugin!=='research')return
 const destination=new URL(hash.slice(1),'http://navigation')
 if(destination.pathname!=='/'||destination.searchParams.get('return')!=='home')return
 return workspaces.find(w=>{
  if(!matches(w))return false
  const source=new URL((w.hash||'#/').slice(1),'http://navigation')
  return source.pathname==='/'&&source.searchParams.get('return')==='home'&&source.searchParams.get('view')===destination.searchParams.get('view')
 })
}

// The shell describes location; workspace names may be renamed independently.
export function workspaceBreadcrumb(workspace){
 if(!workspace)return ['个人工作台']
 const item=workspaceNavigation(workspace)
 if(workspace.plugin.name==='platform')return ['平台管理',item.label]
 const group=navigationGroups.find(g=>navigationPath(item.id,[g]).length)
 if(item.id==='orchestration-templates')return [group.label,'交付设置',item.label]
 return group?[group.label,item.label]:[item.label]
}
