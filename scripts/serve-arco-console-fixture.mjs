// Isolated UI acceptance fixture. Only synthetic local records can be changed.
import http from 'node:http'
import {readFile} from 'node:fs/promises'
import path from 'node:path'
import {createRequire} from 'node:module'
const {WebSocketServer}=createRequire(path.resolve('web/package.json'))('ws')
const port=Number(process.env.ARCO_FIXTURE_PORT||19836)
const seed=process.env.ARCO_FIXTURE_SEED?JSON.parse(await readFile(process.env.ARCO_FIXTURE_SEED,'utf8')):{}
const dist=path.resolve('web/dist'),calls=[],writes=[]
// Resource-center preview is opt-in and only mutates this process's synthetic rows.
const resourcePreview=process.env.ARCO_FIXTURE_RESOURCE_PREVIEW==='1'
let runnerMode='',runnerSaveFail=false
let previewRunners=[{name:'demo-build',kind:'local',labels:'demo,java21,docker',enabled:true,max_parallel:2},{name:'local',kind:'local',labels:'darwin,local',enabled:true,max_parallel:1},{name:'legacy-build',kind:'local',labels:'java8,maven',enabled:false,max_parallel:1}]
const plugins=[['research','研发工作台'],['deploy','部署平台'],['dbms','数据库工作台'],['redis','Redis 工作台'],['logs','日志查询'],['monitor','监控'],['devtools','MCP'],['api-center','接口中心']].map(([name,display])=>({name,display,url:'/api/plugins/'+name+'/ui'}))
let expired=process.env.ARCO_FIXTURE_AUTH_REQUIRED==='1', failReads=false
const homeReadErrors={tasks:false,content:false}
let telemetryMode='normal',telemetryDelay=0
let queryFault=''
const alertRules=seed.alertRules||{logs:[],monitor:[{id:1,name:'接口错误率（示例）',service:'示例应用',environment:'test',metric:'http.server.duration',type:'error_rate',threshold:1,window_min:5,min_sample:10,enabled:true,webhook:'https://example.invalid/notify',silence_sec:300}]}
const me={username:'designer',display_name:'体验验收',role:process.env.ARCO_FIXTURE_ROLE||'admin',auth_enabled:true,enabled:true}
const environments=[{name:'test',display_name:'测试环境'},{name:'prod',display_name:'生产环境',protected:true}]
let resourceFault='',resourceSaveFail=false,resourceEmpty=''
const previewCatalog={
 connections:[{name:'demo-db',kind:'mysql',endpoint:'db-test.example.invalid:3306',config:{database:'demo'},secret_configured:true,updated_at:1792483200},{name:'demo-cache',kind:'redis',endpoint:'cache-test.example.invalid:6379',config:{db:'0',key_prefix:'demo:'},secret_configured:true,updated_at:1792483200},{name:'demo-git',kind:'git',endpoint:'https://code.example.invalid',config:{auth_mode:'https'},secret_configured:true,updated_at:1792483200},{name:'demo-nacos',kind:'nacos',endpoint:'https://nacos.example.invalid/nacos',config:{namespace:'test',nacos_version:'2.4'},secret_configured:true,updated_at:1792483200}],
 hosts:[{name:'test-app-01',environment:'test',addr:'192.0.2.10:22',user:'deployer',credential_configured:true}],
 'host-groups':[{name:'test-services',environment:'test',description:'测试应用组（模拟）',hosts:['test-app-01']}],
 'variable-groups':[{name:'build-common',description:'公共构建参数（模拟）',keys:['JAVA_HOME','MAVEN_OPTS'],updated_at:1792483200}],
 'resource-groups':[{name:'shared-business',description:'应用复用资源（模拟）',revision:2,requirements:[{alias:'business-db',kind:'mysql'}],bindings:[{alias:'business-db',environment:'test',connection:'demo-db',config:{}}]}],
 environments:environments.map((e,i)=>({...e,purpose:i?'production':'test',sort_order:i*10,approvers:i?'reviewer':''})),
 audit:[{id:1,created_at:1792483200,actor:'designer',action:'save',resource_type:'connection',resource_id:'demo-db'},{id:2,created_at:1792483201,actor:'reviewer',action:'approve',resource_type:'release',resource_id:'9'}]
}
const previewPolicy={test:{revision:3,workflow_template_id:1,hotfix_template_id:0,defaults:{applications:{'fixture-app':{artifact_source:'existing',target_branch:'test'}}},stages:[]},prod:{revision:4,workflow_template_id:1,hotfix_template_id:1,defaults:{applications:{}},stages:[]}}
const previewAgent={server_url:'http://logs.example.invalid:9801',monitor_url:'http://monitor.example.invalid:9802',run_user:'logagent',run_group:'logagent',spool_max_mb:1024,token_available:true,configured:true,synchronized:false,collect:[{application_id:'fixture',service:'示例应用',paths:['/var/log/demo/*.log'],multiline:''}]}
const work=seed.work||{id:101,title:'菜单体验优化（示例）',kind:'requirement',source:'manual',status:'open',owner:'designer',assigned_to:'designer',updated_at:1792483200,description:'界面验收示例工作项\n\n![示例截图](attachment:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa)',description_format:'markdown'}
let versions=seed.versions||[{id:1,version:'v1.2.0',title:'产品迭代（示例）',status:'draft',owner:'designer',updated_at:1792483200,application_count:2,work_item_count:1,change_count:0,work_items:[work],changes:[],manual_change_ids:[],description:'跨应用发布范围示例'}]
const demoService={name:'示例应用',application_id:'fixture',artifact_type:'jar',build_tool:'maven',git_url:'https://example.invalid/team/demo.git',branch:'main',maven_executable:'auto',maven_goals:'clean package',artifact_glob:'target/*.jar',tags:[],steps:[]}
const buildRows=seed.buildRows||[{id:3,service:'示例应用',version:'8c392ad',branch:'main',actor:'designer',status:'success',started_at:1792483200,ended_at:1792483242,created_at:1792483200,stages:[{name:'编译',status:'success'}],artifact_id:11}]
const releaseRows=seed.releaseRows||[{id:9,service:'示例应用',version:'8c392ad',environment:'test',actor:'designer',status:'success',started_at:1792483300,created_at:1792483300,succeeded:1,failed:0,total:1}]
let tasks=seed.tasks||[{id:1,name:'菜单迁移（示例）',service:'示例应用',resource_alias:'mysql',mode:'table',keys:['code'],source_sql:'SELECT id, code, name FROM menu',target_sql:'SELECT id, code, name FROM menu'}]
let deliveryChanges=structuredClone(seed.deliveryChanges||[]),deliveryRevision=1,deliverySaveFail=false
const json=(res,data,status=200)=>{res.writeHead(status,{'Content-Type':'application/json','Cache-Control':'no-store'});res.end(JSON.stringify(data))}
const server=http.createServer(async(req,res)=>{
 const u=new URL(req.url,'http://fixture'),original=u.pathname,p=original.replace(/^\/api\/plugins\/[^/]+/,'').replace(/^\/api\/workbench\/(dbms|redis)/,'/api')
 if(resourcePreview&&original==='/__fixture/runners'){runnerMode=u.searchParams.get('mode')||'';runnerSaveFail=u.searchParams.get('save-fail')==='1';return json(res,{runnerMode,runnerSaveFail})}
 if(resourcePreview&&original==='/__fixture/resources'){resourceFault=u.searchParams.get('fail')||'';resourceSaveFail=u.searchParams.get('save-fail')==='1';resourceEmpty=u.searchParams.get('empty')||'';return json(res,{resourceFault,resourceSaveFail,resourceEmpty})}
 if(original==='/__fixture/fail-reads'){failReads=u.searchParams.get('enabled')==='1';return json(res,{failReads})}
 if(failReads&&/^\/api\/(otlp|instances|machines|machine-series|alerts|alert-notifications|avail-rules)/.test(p))return json(res,{error:'模拟网络读取失败'},503)
 if(original==='/__fixture/status')return json(res,{calls,writes})
 if(original==='/__fixture/logs-workspace'){
  res.writeHead(200,{'Content-Type':'text/html; charset=utf-8','Cache-Control':'no-store'})
  return res.end('<!doctype html><html><body style="margin:0;display:flex;justify-content:center"><iframe title="日志工作区" style="width:1040px;height:704px;border:0" src="/api/plugins/logs/ui#/search"></iframe><script>addEventListener("message",e=>{if(e.origin===location.origin&&e.data?.type==="context.request")e.source.postMessage({type:"context.sync",service:"示例应用",environment:"test",rangeStart:Date.now()-900000,rangeEnd:Date.now()},location.origin)})</script></body></html>')
 }
 if(original==='/__fixture/query'){queryFault=u.searchParams.get('fail')||'';return json(res,{queryFault})}
 if(original==='/__fixture/telemetry'){if(['normal','partial','empty','error'].includes(u.searchParams.get('mode')))telemetryMode=u.searchParams.get('mode');if(u.searchParams.has('delay'))telemetryDelay=Math.max(0,Math.min(15000,Number(u.searchParams.get('delay'))||0));return json(res,{telemetryMode,telemetryDelay})}
 if(original==='/__fixture/home'){for(const key of ['tasks','content'])if(u.searchParams.has(key))homeReadErrors[key]=u.searchParams.get(key)==='1';return json(res,homeReadErrors)}
 if(seed.deliveryEditor&&original==='/__fixture/delivery'){
  deliverySaveFail=u.searchParams.get('save-fail')==='1'
  if(u.searchParams.get('add-sync')==='1'){deliveryChanges.push({id:900,kind:'mysql',title:'同步任务新增内容（模拟）',service:'estate-ai-seek',status:'draft',owner:'designer',sql:{resource_alias:'mysql',content:'SELECT 1',max_affected_rows:100,data_sync:{task_id:1}}});deliveryRevision++}
  return json(res,{revision:deliveryRevision,saveFail:deliverySaveFail})
 }
 if(original==='/__fixture/expire'){expired=true;return json(res,{ok:true})}
 if(expired&&original.startsWith('/api/')&&p!=='/api/login'){res.setHeader('X-Platform-Auth-Required','1');return json(res,{error:'登录已过期'},401)}
 calls.push(req.method+' '+original+u.search)
 const page=items=>{const current=Math.max(1,Number(u.searchParams.get('page'))||1),size=Number(u.searchParams.get('page_size'))||20;return {items:items.slice((current-1)*size,current*size),total:items.length,page:current,page_size:size,total_exact:true,has_more:current*size<items.length}}
 const plugin=original.match(/^\/api\/plugins\/([^/]+)/)?.[1]
 if(req.method!=='GET'){
  let text='';for await(const chunk of req)text+=chunk
  const body=JSON.parse(text||'{}');if(p==='/api/login'){if(process.env.ARCO_FIXTURE_PASSWORD&&body.password!==process.env.ARCO_FIXTURE_PASSWORD)return json(res,{error:'用户名或密码错误'},401);expired=false;return json(res,{...me,username:body.username||me.username})};writes.push({method:req.method,path:original,body})
  if(p==='/api/query'&&queryFault&&(queryFault==='all'||(body.commands||body.sql||'').toUpperCase().startsWith(queryFault.toUpperCase())))return json(res,{error:'模拟查询读取失败，请重试'},503)
  if(seed.deliveryEditor&&(p==='/api/work-items/101/delivery-content'||p==='/api/changes/batch')){
   await new Promise(resolve=>setTimeout(resolve,600))
   if(deliverySaveFail)return json(res,{error:'模拟保存失败；草稿应保留'},503)
   if(req.method==='PUT'&&body.digest!=='fixture-editor-'+deliveryRevision)return json(res,{error:'交付内容已被其他操作修改，请重新核对'},409)
   for(const change of body.changes||[]){const old=deliveryChanges.find(c=>c.id===change.id);if(old?.status==='ready'||old&&old.owner!=='designer')return json(res,{error:'Fixture rejects modifying readonly content'},403);const row={...change,id:change.id||Math.max(500,...deliveryChanges.map(c=>c.id))+1,owner:'designer',status:'draft'};if(old)deliveryChanges=deliveryChanges.map(c=>c.id===row.id?row:c);else deliveryChanges.push(row)}
   deliveryRevision++;return json(res,{ok:true})
  }
  if(seed.deliveryEditor&&p==='/api/git/branches')return json(res,{default_branch:'main',branches:[{name:'feature/summary'},{name:'main'}]})
  if(resourcePreview&&original.startsWith('/api/resources/')&&!original.endsWith('/runners')){
   await new Promise(resolve=>setTimeout(resolve,700))
   if(resourceSaveFail)return json(res,{error:'模拟保存失败，修改应保留'},503)
   const key=original.slice('/api/resources/'.length),collection=previewCatalog[key]
   if(collection&&key!=='audit'&&req.method==='POST'){
    const index=collection.findIndex(v=>v.name===body.name),previous=collection[index],row={...body,updated_at:Math.floor(Date.now()/1000)}
    if(key==='variable-groups'){row.keys=Object.keys(body.values||{}).length?Object.keys(body.values):previous?.keys||[];delete row.values}
    if(key==='resource-groups')row.revision=(previous?.revision||0)+1
    if(index<0)collection.push(row);else collection[index]=row
    return json(res,{ok:true})
   }
   if(key.endsWith('/delivery-policy')&&req.method==='PUT'){const name=key.split('/')[1];previewPolicy[name]={...body,revision:(previewPolicy[name]?.revision||0)+1};return json(res,previewPolicy[name])}
   if(key.endsWith('/nacos')&&req.method==='PUT')return json(res,{connection:body.connection})
   if(key.endsWith('/agent/config')&&req.method==='PUT'){Object.assign(previewAgent,body);return json(res,previewAgent)}
   if(key.endsWith('/test')&&req.method==='POST')return json(res,{ok:true})
   if(req.method==='DELETE'){const [type,name]=key.split('/');if(previewCatalog[type]){const at=previewCatalog[type].findIndex(v=>v.name===decodeURIComponent(name));if(at>=0)previewCatalog[type].splice(at,1);return json(res,{ok:true})}}
   return json(res,{error:'Resource fixture rejects this operation'},405)
  }
  if(resourcePreview&&original==='/api/resources/runners'&&req.method==='POST'){
   await new Promise(resolve=>setTimeout(resolve,800))
   if(runnerSaveFail)return json(res,{error:'模拟保存失败，草稿应保留'},503)
   const row={...body,updated_at:Math.floor(Date.now()/1000)},index=previewRunners.findIndex(item=>item.name===row.name)
   if(index<0)previewRunners.push(row);else previewRunners[index]=row
   return json(res,{ok:true})
  }
  if(p==='/api/alerts'&&['logs','monitor'].includes(plugin)&&req.method==='POST'){const rule={...body,id:Math.max(0,...alertRules[plugin].map(r=>r.id))+1,enabled:true};alertRules[plugin].push(rule);return json(res,rule)}
  if(p==='/api/data-sync/tasks'&&req.method==='POST'){const task={...body,id:Math.max(0,...tasks.map(t=>t.id))+1};tasks.push(task);return json(res,task)}
  if(/^\/api\/data-sync\/tasks\/\d+$/.test(p)&&req.method==='PUT'){const id=Number(p.split('/').at(-1)),task={...body,id};tasks=tasks.map(t=>t.id===id?task:t);return json(res,task)}
  if(/^\/api\/data-sync\/tasks\/\d+\/compare$/.test(p)){const task=tasks.find(t=>t.id===Number(p.split('/').at(-2)));return json(res,seed.syncComparison?{...seed.syncComparison,task,...body}:{task,...body,columns:['id','code','name'],differences:[{key:'["menu-a"]',kind:'insert',before:null,after:{id:1,code:'menu-a',name:'示例菜单'}}],digest:'fixture-only',unchanged:0,target_only:0})}
  if(p==='/api/delivery/draft-plan'&&req.method==='POST'){
   const changes=body.selected_change_ids==null?deliveryChanges:deliveryChanges.filter(c=>body.selected_change_ids.includes(c.id))
   const nodes=changes.map(c=>({id:'change-'+c.id,kind:c.kind==='manual'?'manual':c.kind,name:c.title,service:c.service,change_ids:[c.id],depends_on:[]}))
   return json(res,{template:{id:body.template_id||0,name:'界面验收编排（模拟）',mode:'version',services:[],nodes},scope:{work_items:[work],changes,services:[...new Set(changes.map(c=>c.service))]},nodes,defaults:{}})
  }
  if(p==='/api/release-versions/scope-preview')return json(res,[])
  if(p==='/api/release-versions'&&req.method==='POST'){const v={...body,id:versions.length+1,status:'draft',owner:me.username,work_items:body.work_item_ids?.includes(work.id)?[work]:[],changes:[],manual_change_ids:[],application_count:0,work_item_count:body.work_item_ids?.length||0,change_count:0,updated_at:1792483200};versions.push(v);return json(res,v)}
  if(p==='/api/query'&&body.sql?.startsWith('SHOW TABLES'))return json(res,{metadata:'tables',columns:['table_name'],rows:[{table_name:'menus'}]})
  if(p==='/api/query'&&body.sql?.startsWith('DESCRIBE'))return json(res,{metadata:'schema',columns:['field','type','key_type'],rows:(seed.queryColumns||['id','name']).map(field=>({field,type:field==='id'?'bigint':'varchar(255)',key_type:field==='id'?'PRI':''}))})
  if(p==='/api/query'&&body.commands)return json(res,{columns:['command','result'],rows:body.commands.startsWith('SCAN')?[{argv:['SCAN','0','MATCH','demo:*','COUNT','50'],result:['0',['demo:menu']]}]:body.commands.startsWith('TYPE')?[{argv:['TYPE','demo:menu'],result:'string'},{argv:['TTL','demo:menu'],result:3600}]:[{argv:['GET','demo:menu'],result:'示例菜单'}],elapsed_ms:4})
  if(p==='/api/query')return json(res,{columns:seed.queryColumns||['id','name'],rows:seed.queryRows||[{id:1,name:'示例菜单A'},{id:2,name:'示例菜单B'}],elapsed_ms:4,target:{connection:'demo-db',database:'demo'},truncated:false})
  return json(res,{error:'Fixture rejects this operation'},405)
 }
 if(p==='/api/work-item-attachments/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'){res.writeHead(200,{'Content-Type':'image/png','Cache-Control':'no-store'});return res.end(Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Wl6FnoAAAAASUVORK5CYII=','base64'))}
 if(p==='/api/work-items/101/delivery-content'&&homeReadErrors.content)return json(res,{error:'合成交付内容读取失败'},503)
 if(p==='/api/work-items/101/delivery-content')return json(res,seed.deliveryEditor?{changes:deliveryChanges,digest:'fixture-editor-'+deliveryRevision}:{changes:[],digest:'fixture-only'})
 if(p==='/api/research/work-items/101/tracking')return json(res,{versions:[],deliveries:[]})
 if(p==='/api/research/work-items/101/delivery-records')return json(res,{items:[{id:1,environment:'test',status:'success',owner:'designer',created_at:1792483200}],has_more:false})
 if(p==='/api/research/work-items/101/delivery-records/1')return json(res,{id:1,loaded:true,changes:[{id:1,kind:'mysql',title:'菜单结构调整（示例）',sql:{content:'SELECT 1',environment_overrides:[{environment:'prod',skip:true,reason:'示例'}]}}],artifacts:[],steps:[]})
 if(p==='/api/research/work-items/101')return json(res,seed.workDetail||{item:work,activity:[],pending_operations:[]})
 if(seed.actionOptions&&p==='/api/research/work-items/101/action-options')return json(res,seed.actionOptions)
 if(p==='/api/me')return json(res,me)
 if(p==='/api/tokens'&&seed.tokens)return json(res,seed.tokens)
 if(p==='/api/tools')return json(res,seed.tools||[])
 if(p==='/api/registry/services')return json(res,[{name:'示例应用'}])
 if(p==='/api/steps'&&seed.steps)return json(res,seed.steps)
 if(p==='/api/deploy/drivers'&&seed.drivers)return json(res,seed.drivers)
 if(p==='/api/arthas/targets'&&seed.arthasTargets)return json(res,seed.arthasTargets)
 if(resourcePreview&&original==='/api/registry/applications')return json(res,[{application_id:'fixture',name:'示例应用',sources:['logs']}])
 if(resourcePreview&&original.startsWith('/api/resources/')){
  const key=original.slice('/api/resources/'.length)
  if(resourceFault&&(key===resourceFault||(resourceFault==='policy'&&key.endsWith('/delivery-policy'))))return json(res,{error:'模拟资源读取失败'},503)
  if(previewCatalog[key])return json(res,resourceEmpty===key?[]:previewCatalog[key])
  if(key.endsWith('/delivery-policy'))return json(res,previewPolicy[key.split('/')[1]]||{revision:0,stages:[]})
  if(key==='delivery/templates')return json(res,[{id:1,name:'标准交付（模拟）',mode:'version',nodes:[]}])
  if(key.endsWith('/nacos'))return json(res,{connection:'demo-nacos'})
  if(key.endsWith('/agent/config'))return json(res,previewAgent)
  if(key.endsWith('/agent/status'))return json(res,{installed:true,active:true,status:'active',version:'fixture'})
 }
 if(resourcePreview&&original==='/api/resources/runners'){
  await new Promise(resolve=>setTimeout(resolve,300))
  if(runnerMode==='error')return json(res,{error:'模拟读取失败，请重试'},503)
  return json(res,runnerMode==='empty'?[]:runnerMode==='many'?[...previewRunners,...Array.from({length:23},(_,i)=>({name:`fixture-build-${i+1}`,kind:'local',labels:'fixture,java21',enabled:true,max_parallel:1}))]:previewRunners)
 }
 if(resourcePreview&&original==='/api/resources/hosts')return json(res,[{name:'test-app-01',environment:'test',addr:'192.0.2.10:22',user:'deployer',credential_configured:true}])
 if(resourcePreview&&original==='/api/resources/connections')return json(res,[{name:'demo-db',kind:'mysql',endpoint:'db-test.example.invalid:3306',config:{database:'demo'},secret_configured:true},{name:'demo-cache',kind:'redis',endpoint:'cache-test.example.invalid:6379',config:{db:'0'},secret_configured:true},{name:'demo-git',kind:'git',endpoint:'https://code.example.invalid',config:{auth_mode:'https'},secret_configured:true}])
 if(resourcePreview&&original==='/api/resources/host-groups')return json(res,[{name:'test-services',environment:'test',description:'测试应用组（模拟）',hosts:['test-app-01']}])
 if(resourcePreview&&original==='/api/resources/variable-groups')return json(res,[{name:'build-common',description:'公共构建参数（模拟）',keys:['JAVA_HOME','MAVEN_OPTS'],updated_at:1792483200}])
 if(original==='/api/plugins')return json(res,plugins)
 if(original==='/api/resources/setup')return json(res,{completed:true,environments})
 if(p==='/api/environments'||original==='/api/resources/environments')return json(res,original==='/api/resources/environments'||!['logs','monitor'].includes(plugin)?environments:environments.map(e=>e.name))
 if(original==='/api/registry/services')return json(res,[{name:'示例应用',application_id:'fixture'}])
 if(original==='/api/registry/deployments')return json(res,[])
 if(p==='/api/services')return json(res,plugin==='logs'?{'示例应用':10}:plugin==='monitor'?['示例应用']:seed.services||[demoService])
 if(p==='/api/users')return json(res,seed.users||[{username:'designer',display_name:'体验验收',enabled:true},{username:'reviewer',display_name:'审核人员',enabled:true}])
 if(p==='/api/alerts')return json(res,(alertRules[plugin]||[]).filter(r=>!u.searchParams.get('environment')||r.environment===u.searchParams.get('environment')))
 if(p==='/api/alerts/status')return json(res,{})
 if(p==='/api/avail-rules')return json(res,seed.availRules||[])
 if(p==='/api/alert-notifications')return json(res,seed.alertNotifications||[])
 if(p==='/api/otlp/trace')return json(res,seed.traceSpans||[])
 if(p==='/api/otlp/traces/recent')return json(res,seed.recentTraces||[])
 if(p==='/api/otlp/services')return json(res,['示例应用'])
 if(p==='/api/instances')return json(res,u.searchParams.get('environment')==='prod'?[]:['fixture-1'])
 if(p==='/api/otlp/names')return json(res,u.searchParams.get('environment')==='prod'?[]:['http.server.duration','jvm.memory.used'])
 if(p==='/api/otlp/interfaces')return json(res,[{iface:'/demo',cnt:10}])
 if(p==='/api/otlp/iface-attrs')return json(res,[{meth:'GET',status:'200'}])
 if(p==='/api/otlp/series')return json(res,[{minute:Date.now()-60000,val:10,cnt:0},{minute:Date.now(),val:12,cnt:0}])
 if(p==='/api/otlp/quantiles')return json(res,[{minute:Date.now(),cnt:10,avg:0.01,p50:0.01,p95:0.02,p99:0.03,max:0.04}])
 if(p==='/api/otlp/availability')return json(res,[{minute:Date.now(),total:10,ok:10}])
 if(p==='/api/machines'&&telemetryMode==='partial'&&seed.partialMachines)return json(res,seed.partialMachines)
 if(p==='/api/machines')return json(res,u.searchParams.get('environment')==='prod'?[]:[{agent_id:'fixture-1',environment:'test',cpu:10,mem_used:2,mem_total:8,disk_used:10,disk_total:100,net_rx_kbs:1,net_tx_kbs:2}])
 if(p==='/api/machine-series')return json(res,[{minute:Date.now()-60000,cpu:10,mem_used:2},{minute:Date.now(),cpu:12,mem_used:2}])
 if(p==='/api/delivery/templates')return json(res,[{id:1,name:'示例交付编排',mode:'version',owner:'designer',preparation:[],nodes:[{id:'review',kind:'approval',name:'发布审核',approvers:['reviewer'],approval_mode:'any'}]}])
 if(p==='/api/delivery/runs/page')return json(res,page(seed.deliveries||[]))
 if(p==='/api/delivery/runs')return json(res,seed.deliveries||[])
 if(/^\/api\/delivery\/runs\/\d+$/.test(p))return json(res,(seed.deliveries||[]).find(v=>v.id===Number(p.split('/').at(-1)))||{})
 if(/^\/api\/delivery\/runs\/\d+\/control$/.test(p))return json(res,seed.deliveryControl||{state:'stopped',message:'示例执行已结束'})
 if(p==='/api/data-sync/tasks')return json(res,tasks)
 if(p==='/api/services/summary'&&seed.serviceSummary)return json(res,seed.serviceSummary)
 if(p==='/api/pipeline/runs')return json(res,buildRows.filter(r=>!u.searchParams.get('service')||r.service===u.searchParams.get('service')))
 if(p==='/api/release-orders/page')return json(res,page(releaseRows))
 if(p==='/api/release-orders')return json(res,releaseRows)
 if(p.startsWith('/api/services/')&&p.endsWith('/delivery'))return json(res,{runner:'local',variable_groups:[],build_environment:{mode:'local'}})
 if(original.startsWith('/api/resources/services/')&&original.endsWith('/resources'))return json(res,{requirements:seed.deliveryEditor?[{kind:'mysql',alias:'mysql'},{kind:'nacos',alias:'nacos'},{kind:'redis',alias:'redis'}]:[{kind:'mysql',alias:'mysql'}]})
 if(p.startsWith('/api/services/')&&p.endsWith('/resources'))return json(res,{requirements:seed.deliveryEditor?[{kind:'mysql',alias:'mysql'},{kind:'nacos',alias:'nacos'},{kind:'redis',alias:'redis'}]:[{kind:'mysql',alias:'mysql'}]})
 if(p==='/api/connections'||original==='/api/resources/connections')return json(res,[{name:'demo-db',kind:plugin==='redis'?'redis':'mysql',config:{database:'demo',host:'example.invalid',port:'3306'}}])
 if(p==='/api/research/overview')return json(res,{actor:me.username,aliases:[me.username],connections:seed.researchConnections||[]})
 if(seed.researchConnections&&/^\/api\/research\/connections\/[^/]+\/account-hint$/.test(p))return json(res,{account:me.username})
 if(p==='/api/research/tasks'){
  if(seed.homeTaskError||homeReadErrors.tasks)return json(res,{error:'合成待办读取失败'},503)
  let items=seed.homeTasks||[]
  if(u.searchParams.get('kind'))items=items.filter(t=>t.kind===u.searchParams.get('kind'))
  if(u.searchParams.get('q'))items=items.filter(t=>[t.title,...(t.services||[]),t.version].filter(Boolean).join(' ').includes(u.searchParams.get('q')))
  return json(res,page(items))
 }
 if(p.startsWith('/api/research/deliveries'))return json(res,page(seed.homeDeliveries||[]))
 if(p.startsWith('/api/research/activities'))return json(res,page([]))
 if(p==='/api/research/work-items')return json(res,{...page(seed.workItems||[work]),dictionaries:{}})
 if(p==='/api/work-items/candidates'||p==='/api/research/work-items/page')return json(res,{...page((seed.workItems||[work]).map(w=>({...w,delivery_ready:true,release_required:true}))),page_size:20})
 if(p==='/api/work-items/candidates/selected')return json(res,{items:[work]})
 if(p==='/api/work-items/delivery-page')return json(res,{...page((seed.workItems||[work]).map(w=>({...w,linked_items:[],ownership:'independent',preparation:'ready',change_count:1,ready_count:1,delivery_status:'not_started',release_required:true}))),source_errors:[],checked_at:1793145600})
 if(p==='/api/work-items/page')return json(res,page(seed.workItems||[work]))
 if(p==='/api/work-items')return json(res,[work])
 if(p==='/api/work-items/101')return json(res,work)
 if(p==='/api/work-items/owners')return json(res,['designer'])
 if(p==='/api/work-items/101/comments'||p==='/api/work-items/101/deliveries'||p==='/api/work-items/101/activities')return json(res,[])
 if(p==='/api/release-versions/owners')return json(res,['designer'])
 if(p==='/api/release-versions/page')return json(res,page(versions))
 if(p==='/api/release-versions')return json(res,versions)
 if(/^\/api\/release-versions\/\d+$/.test(p))return json(res,versions.find(v=>v.id===Number(p.split('/').at(-1)))||{},200)
 if(p.endsWith('/readiness'))return json(res,seed.readiness||{advisory:true,checked_at:1792483200,code:[],checks:[{key:'scope',title:'版本范围待补充',detail:'示例记录，无实际交付内容',status:'pending',action:'scope'}],counts:{pass:0,pending:1,blocked:0,manual:0}})
 if(p.startsWith('/api/release-notices/'))return json(res,seed.notice||{text:'',stale:false})
 if(p==='/api/changes/page')return json(res,page(seed.changes||[]))
 if(p==='/api/changes')return json(res,seed.changes||[])
 if(/^\/api\/changes\/\d+$/.test(p))return json(res,(seed.changes||[]).find(v=>v.id===Number(p.split('/').at(-1)))||{})
 if(/^\/api\/pipeline\/runs\/\d+$/.test(p))return json(res,buildRows.find(v=>v.id===Number(p.split('/').at(-1)))||{})
 if(/^\/api\/pipeline\/runs\/\d+\/log$/.test(p))return json(res,{log:'示例构建日志\n━━ 编译（成功）\n[INFO] BUILD SUCCESS\n'})
 if(/^\/api\/pipeline\/runs\/\d+\/capabilities$/.test(p))return json(res,{commit:'8c392ad',can_retry:false,can_stop:false})
 if(p==='/api/pipeline/runs/page')return json(res,page(buildRows.filter(r=>!u.searchParams.get('service')||r.service===u.searchParams.get('service'))))
 if(p==='/api/artifacts/page')return json(res,page(seed.artifacts||[{id:11,service:'estate-ai-seek',version:'8c392ad',kind:'jar',digest:'sha256:fixture',pipeline_run_id:3,branch:'main',created_at:1792483242}]))
 // API-center fixtures are read-only synthetic documents/reports, never remote requests.
 if(p==='/api/api-documents')return json(res,[{id:1,service:'estate-ai-seek',environment:'test',provider:'manual',created_at:1792483200}])
 if(p==='/api/api-documents/1')return json(res,{id:1,info:{title:'验收接口',version:'1.0'},operations:[{id:'demo',path:'/demo',method:'GET',summary:'示例接口',tags:['验收']}],document:{openapi:'3.0.3',paths:{'/demo':{get:{responses:{'200':{description:'OK'}}}}}}})
 if(p==='/api/api-testing/context')return json(res,{environments,targets:[{key:'demo',name:'模拟目标',base_url:'https://example.invalid',fingerprint:'fixture'}],variables:[],enabled:true})
 if(p==='/api/api-testing/cases')return json(res,seed.apiTestingCases||[])
 if(p==='/api/api-testing/scenario-cases')return json(res,{cases:seed.apiTestingScenarios||[],truncated:false})
 if(p==='/api/api-testing/suites')return json(res,[])
 if(/^\/api\/api-testing\/cases\/\d+\/layout$/.test(p))return json(res,{revision:0,positions:{},zoom:1,pan:{x:20,y:28}})
 if(/^\/api\/api-testing\/cases\/\d+$/.test(p)){const item=(seed.apiTestingCases||[]).find(c=>c.id===Number(p.split('/').at(-1)));return item?json(res,item):json(res,{error:'合成用例不存在'},404)}
 if(p==='/api/api-testing/runs')return json(res,{items:[],total:0})
 if(p==='/api/api-testing/runs/1'&&seed.apiTestingReport)return json(res,seed.apiTestingReport)
 if(p==='/api/api-testing/runs/1')return json(res,{id:1,name:'只读验收报告',target:{label:'模拟目标',artifact_id:1},service:'estate-ai-seek',environment:'test',status:'success',actor:'designer',source:'manual',started_at:1792483200,ended_at:1792483201,steps:[],result:{status_code:200,body:'{"data":{"id":123,"items":[{"name":"示例"}]}}',headers:{'Content-Type':'application/json'},checks:[],duration_ms:20}})
 if(p==='/api/query-history')return json(res,[])
 if(/^\/api\/(?:resources\/)?connections\/[^/]+\/databases$/.test(p))return json(res,{databases:['demo'],truncated:false})
 if(p==='/api/policy')return json(res,{query_approval:false,change_approval:true})
 if((p==='/api/target'||p==='/api/query-target'))return json(res,{connection:'demo-db',database:'demo',engine:'mysql',db:0,key_prefix:'demo:'})
 if(p==='/api/query/tables')return json(res,[{name:'menus',table_name:'menus'}])
 if(p==='/api/health')return json(res,{ok:true})
 if(p==='/api/search')return json(res,{hits:seed.logHits||[{ts:new Date().toISOString(),service:'示例应用',environment:'test',host:'fixture-host',level:'INFO',message:'菜单权限核对完成 · 界面布局验收日志（模拟数据）'}],has_more:false})
 if(p==='/api/stats'&&telemetryDelay)await new Promise(resolve=>setTimeout(resolve,telemetryDelay))
 if(p==='/api/stats'&&telemetryMode==='error')return json(res,{error:'合成采集状态读取失败'},503)
 if(p==='/api/stats'&&telemetryMode==='partial'&&seed.partialLogStats)return json(res,seed.partialLogStats)
 if(p==='/api/stats'&&telemetryMode==='empty')return json(res,{agents:[],services:{},disk_used:0})
 if(p==='/api/stats'&&seed.logStats)return json(res,seed.logStats)
 if(p==='/api/stats')return json(res,{agents:[{id:'fixture-agent',name:'示例采集机',environment:'test',last_seen:Math.floor(Date.now()/1000),online:true}],services:{'示例应用':10},today_count:10,disk_used:1024})
 if(p.startsWith('/api/'))return json(res,[])
 const entry=plugin==='api-center'?'api-center':plugin==='research'?'research':plugin==='dbms'||plugin==='redis'?'workbench':plugin==='deploy'?'deploy':plugin==='logs'?'logs':plugin==='monitor'?'monitor':plugin==='devtools'?'devtools':'shell'
 const file=original==='/'||original==='/login'||p==='/ui'||p==='/ui/'?entry+'.html':/\/assets\/[^/]+\.(js|css|html)$/.test(original)?'assets/'+path.basename(original):null
 if(file)try{const body=await readFile(path.join(dist,file));res.writeHead(200,{'Content-Type':file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html','Cache-Control':'no-store'});return res.end(body)}catch{}
 json(res,{error:'Not found'},404)
})
const tail=new WebSocketServer({noServer:true})
server.on('upgrade',(req,socket,head)=>{if(!new URL(req.url,'http://fixture').pathname.endsWith('/api/tail'))return socket.destroy();tail.handleUpgrade(req,socket,head,ws=>{const send=()=>ws.readyState===1&&ws.send(JSON.stringify({type:'log',hit:seed.liveLogHit||{ts:new Date().toISOString(),service:'示例应用',environment:'test',level:'INFO',message:'实时日志验收 · 本地模拟事件'}}));send();const timer=setInterval(send,2000);ws.on('close',()=>clearInterval(timer))})})
server.listen(port,'127.0.0.1',()=>console.log('Arco fixture: http://127.0.0.1:'+port+'/ (synthetic data only)'))
