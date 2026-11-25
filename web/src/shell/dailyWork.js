// These links open existing workspaces; none starts a build, test or deployment.
export const dailyWorkGroups=[
 {id:'develop',label:'开发协作',items:[
  {plugin:'research',label:'需求与 Bug',hash:'#/work'},
  {plugin:'deploy',label:'变更记录',hash:'#/changes?tab=changes'},
  {plugin:'deploy',label:'应用与构建',hash:'#/'},
  {plugin:'research',label:'发布版本',hash:'#/versions'},
 ]},
 {id:'test',label:'接口测试',items:[
  {plugin:'api-center',label:'接口文档',hash:'#/documents'},
  {plugin:'api-center',label:'测试用例',hash:'#/test-cases'},
  {plugin:'api-center',label:'用例执行记录',hash:'#/test-cases?tab=records'},
  {plugin:'api-center',label:'接口调试',hash:'#/test-execution'},
 ]},
 {id:'operate',label:'发布与排障',items:[
  {plugin:'deploy',label:'发布记录',hash:'#/orchestration?area=runs'},
  {plugin:'logs',label:'查询日志',hash:'#/search'},
  {plugin:'monitor',label:'监控概览',hash:'#/'},
  {plugin:'dbms',label:'数据库查询',hash:'#/query'},
  {plugin:'dbms',label:'提交 SQL 变更',hash:'#/operations'},
  {plugin:'redis',label:'Redis 工作台',hash:'#/query'},
 ]},
]
export function availableDailyWork(plugins=[]){
 const available=new Set(plugins.map(p=>p.name))
 return dailyWorkGroups.map(group=>({...group,items:group.items.filter(item=>available.has(item.plugin))})).filter(group=>group.items.length)
}
export function taskActionLabel(task){
 if(task.kind==='approval')return '审核内容'
 if(task.kind==='code-conflict')return '处理冲突'
 if(task.kind==='exception')return '核对异常'
 if(task.kind==='manual')return '查看确认项'
 return '查看任务'
}
