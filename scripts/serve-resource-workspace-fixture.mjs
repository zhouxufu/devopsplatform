// Disposable synthetic API; no real database, credentials, executor or proxy.
import {createServer} from '../web/node_modules/vite/dist/node/index.js'
import path from 'node:path'
const enrollmentFixture=process.env.DEPLOYMENT_ENROLLMENT_FIXTURE==='1'
let enrollment=null,enrollmentTimer=null
let saveFailure=false,readFailure=''
const calls=[],writes=[],now=Math.floor(Date.now()/1000)
const environments=[{name:'test',display_name:'测试环境',purpose:'test'},{name:'prod',display_name:'生产环境',purpose:'production',protected:true,approvers:'reviewer'}]
const connections=[{name:'shared',kind:'mysql',endpoint:'db.example.invalid:3306',secret_configured:true},{name:'cache',kind:'redis',endpoint:'cache.example.invalid:6379',secret_configured:true}]
const hosts=[{name:'shared',environment:'test',addr:'10.0.0.2:22',user:'deploy',credential_configured:true}]
const audit=Array.from({length:137},(_,i)=>({id:137-i,actor:i===136?'old-operator':'admin',action:'save',resource_type:i%2?'host':'connection',resource_id:i===136?'historical-before-100':'resource-'+i,created_at:now-i*86400}))
let agentConfig={token_available:true,configured:true,synchronized:false,server_url:'http://logs.example.invalid:9801',monitor_url:'http://monitor.example.invalid:9802',run_user:'logagent',run_group:'logagent',spool_max_mb:1024,collect:[{application_id:'app-orders',service:'orders',paths:['/var/log/orders.log'],multiline:''}]}
const service={name:'orders',display_name:'订单服务',artifact_type:'image',build_tool:'gradle',git_url:'https://code.example.invalid/orders.git',branch:'main',steps:[],deploy_config:{}}
let appTargets=[]
const group={name:'shared-db',revision:3,requirements:[{alias:'db',kind:'mysql'}],bindings:[{alias:'db',environment:'test',connection:'shared',config:{database:'orders_test'}}]}
let resources={composition_version:2,resource_groups:[],group_revisions:{},own_requirements:[],own_bindings:[],nacos_configs:[]}
let vite
vite=await createServer({root:path.resolve('web'),configFile:path.resolve('web/vite.config.js'),plugins:[{name:'resource-workspace-fixture',configureServer(server){server.middlewares.use(async(req,res,next)=>{
 const u=new URL(req.url,'http://fixture'),p=u.pathname.replace(/^\/api\/resources\//,'/api/')
 const json=(value,status=200)=>{res.statusCode=status;res.setHeader('Content-Type','application/json');res.end(JSON.stringify(value))}
 if(p==='/__fixture/state')return json({calls,writes,resources,agentConfig,appTargets,enrollment})
 if(p==='/__fixture/fault'){saveFailure=u.searchParams.get('save')==='1';readFailure=u.searchParams.get('read')||'';return json({saveFailure,readFailure})}
 if(p==='/__fixture/stop'){json({ok:true});setTimeout(()=>vite.close(),30);return}
 if(p.startsWith('/api/')){
  calls.push({method:req.method,path:p,query:Object.fromEntries(u.searchParams)})
  if(req.method==='GET'&&readFailure&&p===readFailure)return json({error:'模拟资源读取失败'},503)
  if(req.method!=='GET'){
   let raw='';for await(const part of req)raw+=part;const body=JSON.parse(raw||'{}');writes.push({method:req.method,path:p,body})
   if(saveFailure&&['/api/connections','/api/runners'].includes(p))return json({error:'模拟保存失败，修改应保留'},503)
   if(enrollmentFixture&&p==='/api/hosts/shared/deployment/preview'){
    enrollment={operation_id:'11111111111111111111111111111111',phase:'preview',plan:{services:body.services,run_user:'logagent',run_group:'logagent',url:body.url,scope:{host:'shared',environment:'test',roots:['/srv/orders'],agent_id:'agent_fixture'},expires:now+600}}
    return json(enrollment)
   }
   if(enrollmentFixture&&p==='/api/hosts/shared/deployment/confirm'){
    if(enrollment?.phase!=='preview'||!body.confirm)return json({error:'状态已改变'},409)
    enrollment.phase=(req.headers.referer||'').includes('uncertain=1')?'needs_review':'awaiting_connection'
    if(enrollment.phase==='awaiting_connection')enrollmentTimer=setTimeout(()=>enrollment.phase='ready',3500)
    return json(enrollment)
   }
   if(enrollmentFixture&&p==='/api/hosts/shared/deployment/disable'){clearTimeout(enrollmentTimer);enrollment.phase='disabled';return json(enrollment)}
   if(p==='/api/services/orders/environments/test'&&req.method==='PUT'){appTargets=[{...body,service:'orders',display_name:'测试环境'}];return json(appTargets[0])}
   if(p==='/api/hosts/shared/agent/config'&&req.method==='PUT'){agentConfig={...body,token_available:true,configured:true,synchronized:false};return json(agentConfig)}
   if(p==='/api/hosts/shared/agent/config/apply'&&req.method==='POST')return json({...agentConfig,synchronized:true,applied_at:now})
   if(p==='/api/services/orders/resources'&&req.method==='PUT'){resources=body;return json({ok:true})}
   if(['/api/connections/shared/test','/api/hosts/shared/test'].includes(p)&&req.method==='POST')return json({ok:true})
   return json({error:'Synthetic fixture rejects this mutation'},403)
  }
  if(enrollmentFixture&&p==='/api/hosts/shared/deployment')return json({options:[{service:'orders',root:'/srv/orders'}],enrollment})
  if(enrollmentFixture&&p==='/api/artifacts')return json([{id:15,service:'orders',kind:'jar',version:'cd137349491',source:'pipeline-run',source_id:28,source_number:10,digest:'sha256:753872288de67f4eaa8cf17d242833b0ef3977cb461be484d320d9aac4f2b6d4',size:252206085,created_at:1794208998,metadata:{build_variant:'test'}}])
  if(enrollmentFixture&&p==='/api/artifacts/15/build-compatibility')return json({compatible:true,reason:''})
  if(p==='/api/hosts/shared/agent/status'){await new Promise(r=>setTimeout(r,180));return json({installed:true,active:true,status:'active',version:'fixture'})}
  if(p==='/api/hosts/shared/agent/config')return json(agentConfig)
  if(p==='/api/plugins/logs/api/stats')return json({agents:[{id:'test/shared',online:true}]})
  if(p==='/api/registry/applications')return json([{application_id:'app-orders',name:'orders',display_name:'订单服务'}])
  if(p==='/api/services/orders')return json(enrollmentFixture?{...service,artifact_type:'jar'}:service)
  if(p==='/api/services')return json([service])
  if(p==='/api/services/orders/environments')return json(enrollmentFixture?[{service:'orders',environment:'test',hosts:['shared'],target_type:'ssh-jar',remote_dir:'/srv/orders',deploy_config:{java_mode:'managed',java_version:'8',java_port:'4010'}}]:appTargets)
  if(p==='/api/services/orders/delivery')return json({runner:'local',variable_groups:[],build_environment:{}})
  if(p==='/api/services/orders/build-environments')return json({environments,environment_required:false})
  if(p.endsWith('/delivery-default'))return json({revision:1,build_tool:'gradle',value:null})
  if(p==='/api/me')return json({username:'fixture',role:'admin',auth_enabled:true})
  if(p==='/api/connections')return (req.headers.referer||'').includes('fail=1')?json({error:'模拟连接列表读取失败'},503):json(connections)
  if(p==='/api/environments')return json(environments)
  if(p.endsWith('/delivery-policy'))return json({revision:1,defaults:{},stages:[]})
  if(p==='/api/hosts')return json(hosts)
  if(p==='/api/host-groups')return json([])
  if(p==='/api/variable-groups')return json([{name:'shared-vars',keys:['JAVA_HOME']}])
  if(p==='/api/resource-groups')return json([group])
  if(p==='/api/services/orders/resources')return json(resources)
  if(p==='/api/audit/page'){
   let filtered=audit.filter(r=>(!u.searchParams.get('q')||[r.actor,r.action,r.resource_id].some(x=>x.includes(u.searchParams.get('q'))))&&(!u.searchParams.get('resource_type')||r.resource_type===u.searchParams.get('resource_type'))&&(!u.searchParams.get('resource_id')||r.resource_id===u.searchParams.get('resource_id')))
   const page=Number(u.searchParams.get('page')||1);return json({items:filtered.slice((page-1)*20,page*20),total:filtered.length,page,page_size:20,resource_types:['connection','host']})
  }
  if(p==='/api/connections/shared/databases')return json({databases:['orders_test','orders_prod'],truncated:false})
  if(p==='/api/runners')return json([{name:'local',kind:'local',enabled:true,max_parallel:1}])
  return json([])
 }
 if(p==='/__resources'||p==='/__direct-hosts'){
  res.setHeader('Content-Type','text/html');return res.end(await vite.transformIndexHtml(req.url,'<html lang="zh-CN"><head><meta charset="UTF-8"><title>资源中心交互验收（隔离数据）</title></head><body><div id="app"></div><script type="module" src="'+(p==='/__direct-hosts'?'/src/qa/directHostsFixture.js':'/src/qa/resourceWorkspaceFixture.js')+'"></script></body></html>'))
 }
 next()
})}}],server:{host:'127.0.0.1',port:Number(process.env.RESOURCE_FIXTURE_PORT||20055),strictPort:true}})
await vite.listen();console.log('Synthetic resource workspace http://127.0.0.1:20055/__resources')
