// Only disposable synthetic artifacts and publication records; no executor.
import {createServer} from '../web/node_modules/vite/dist/node/index.js'
import path from 'node:path'
const calls=[],writes=[],now=Math.floor(Date.now()/1000),digest='sha256:'+('a'.repeat(64))
const artifacts=[{id:70,service:'orders',kind:'jar',version:'a12bc34',digest,size:18,source:'pipeline-run',source_id:15,metadata:{artifact_role:'compilation'}},{id:71,service:'orders',kind:'oci-image',version:'a12bc34',digest:'sha256:'+('b'.repeat(64)),uri:'registry.example.invalid/orders@sha256:'+('b'.repeat(64)),source:'pipeline-run',source_id:15,metadata:{compiled_artifact_id:'70'}}]
const environments=[{name:'test',display_name:'测试环境',purpose:'test'},{name:'prod',display_name:'生产环境',purpose:'production',protected:true,approvers:'reviewer'}]
const target={service:'orders',environment:'prod',display_name:'生产环境',target_type:'docker-host',host_group:'生产主机组',hosts:['qa-a','qa-b'],protected:true,deploy_config:{container_name:'orders-prod'}}
const instances=[{host:'qa-a',address:'10.0.0.11:22',kind:'docker-host',state:'running',owned:true,controllable:true,container:'orders-prod',container_id:'a'.repeat(64),image:'registry.invalid/orders@sha256:'+'b'.repeat(64),health:'healthy',version:'a12bc34',fingerprint:'fixture-a-1',checked_at:now},{host:'qa-b',address:'10.0.0.12:22',kind:'docker-host',state:'stopped',owned:true,controllable:true,container:'orders-prod',container_id:'c'.repeat(64),image:'registry.invalid/orders@sha256:'+'b'.repeat(64),version:'a12bc34',fingerprint:'fixture-b-1',checked_at:now}]
const used=new Set()
const service={name:'orders',display_name:'订单服务',artifact_type:'image',build_tool:'gradle',git_url:'https://example.invalid/orders.git',gradle_tasks:'clean bootJar',branch:'main',image_name:'orders',steps:[]}
let order={id:8,service:'orders',artifact_id:70,version:'a12bc34',environment:'prod',status:'waiting_manual',target_snapshot:target,actor:'developer',approver:'reviewer',total:1,succeeded:0,failed:0,started_at:now-600,manual_publication:{artifact_id:70,digest,assignee:'operator',instructions:target.deploy_config.manual_instructions}}
let run={id:9,status:'running',owner:'developer',approver:'reviewer',digest:'fixture-frozen',revision:2,created_at:now-650,plan:{template:{id:1,name:'生产发布流程',mode:'fixed'},environment:environments[1],reason:'手动发布交互验收',services:[{service:'orders',artifact:artifacts[0],target}],nodes:[{id:'publish',kind:'deploy',name:'发布应用',service:'orders'},{id:'verify',kind:'verify',name:'发布后接口验证',verification:{mode:'http',url:'https://example.invalid/health'}}]},steps:[{node_id:'publish',name:'发布订单服务',service:'orders',kind:'deploy',status:'waiting',release_id:8},{node_id:'verify',name:'发布后接口验证',kind:'verify',status:'pending'}]}
const page=items=>({items,total:items.length,page:1,page_size:20,total_exact:true})
let vite
vite=await createServer({root:path.resolve('web'),configFile:path.resolve('web/vite.config.js'),plugins:[{name:'manual-publication-fixture',configureServer(server){server.middlewares.use(async(req,res,next)=>{
 const u=new URL(req.url,'http://fixture'),p=u.pathname
 const json=(v,status=200)=>{res.statusCode=status;res.setHeader('Content-Type','application/json');res.end(JSON.stringify(v))}
 if(p==='/__fixture/state')return json({calls,writes,instances})
 if(p.startsWith('/api/')){
  calls.push({method:req.method,path:p})
  if(p==='/api/services/orders/environments/prod/instances'){
   const ref=req.headers.referer||''
   if(ref.includes('inspect_failure=1'))return json(instances.map(x=>({...x,state:'inspection-failed',owned:false,controllable:false,health:'',message:'主机已连接，但当前账号没有 Docker 访问权限',diagnostic_code:'docker-permission'})))
   return json(ref.includes('no_health=1')?instances.map(x=>({...x,health:''})):instances)
  }
  if(p.endsWith('/logs')&&p.includes('/instances/'))return json({text:'2026-10-28T10:00:00Z INFO Application started\n2026-10-28T10:00:01Z INFO request complete\nAPI_KEY=[REDACTED]',checked_at:Math.floor(Date.now()/1000)})
  if(p==='/api/artifacts/70/download'){res.setHeader('Content-Type','application/octet-stream');res.setHeader('Content-Disposition','attachment; filename=fixture.jar');return res.end(req.method==='HEAD'?'':'synthetic-only-jar')}
  if(req.method!=='GET'&&req.method!=='HEAD'){
   let raw='';for await(const part of req)raw+=part;const body=JSON.parse(raw||'{}');writes.push({path:p,body})
   const match=p.match(/^\/api\/services\/orders\/environments\/prod\/instances\/([^/]+)\/(restart|start|stop|ready)$/)
   if(match){const row=instances.find(x=>x.host===match[1]);if(!row||row.fingerprint!==body.fingerprint||!body.confirm_protected||!body.reason||used.has(body.request_id))return json({error:'合成实例已变化或缺少确认'},409);used.add(body.request_id);await new Promise(r=>setTimeout(r,800));row.state=match[2]==='stop'?'stopped':'running';row.fingerprint+='-next';row.checked_at=Math.floor(Date.now()/1000);return json(row)}
   if(p==='/api/releases/8/manual-result'){if(order.status!=='waiting_manual'||body.artifact_id!==70||body.digest!==digest||!body.comment)return json({error:'合成数据校验失败'},409);order={...order,status:body.result,succeeded:body.result==='success'?1:0,failed:body.result==='failed'?1:0,manual_publication:{...order.manual_publication,...body,actor:'operator',recorded_at:Math.floor(Date.now()/1000)}};return json(order)}
   if(p==='/api/delivery/runs/9/advance'){if(order.status!=='success')return json({error:'等待手动发布结果'},409);run={...run,steps:run.steps.map((s,i)=>i===0?{...s,status:'success'}:s)};return json(run)}
   if(p==='/api/services/orders/environments/prod'&&req.method==='PUT'){Object.assign(target,body);return json(target)}
   return json({error:'Synthetic fixture rejects this operation'},403)
  }
  if(p==='/api/me')return json({username:'operator',display_name:'发布负责人',role:(req.headers.referer||'').includes('member=1')?'member':'admin',auth_enabled:true})
  if(p==='/api/services/orders')return json(service)
  if(p==='/api/services')return json([service])
  if(p==='/api/services/orders/environments')return json([target])
  if(p==='/api/services/orders/delivery')return json({runner:'local',variable_groups:[],build_environment:{}})
  if(p==='/api/services/orders/build-environments')return json({environments,environment_required:false})
  if(p.endsWith('/delivery-policy'))return json({revision:1,defaults:{applications:{}},workflow_template_id:0})
  if(p.endsWith('/delivery-default'))return json({revision:1,build_tool:'gradle',value:null})
  if(p==='/api/environments'||p==='/api/resources/environments')return json(environments)
  if(p==='/api/artifacts'||p==='/api/pipeline/runs/15/artifacts')return json(artifacts)
  if(/^\/api\/artifacts\/\d+$/.test(p))return json(artifacts.find(a=>a.id===Number(p.split('/').at(-1))))
  if(p.endsWith('/build-compatibility'))return json({compatible:true})
  if(p==='/api/artifacts/page')return json(page(artifacts))
  if(p==='/api/pipeline/runs/15')return json({id:15,service:'orders',status:'success',actor:'developer',version:'a12bc34',artifact_id:71,artifacts,stages:[]})
  if(p==='/api/releases/8')return json(order)
  if(p==='/api/releases')return json([order])
  if(p==='/api/release-orders/page')return json(page([order]))
  if(p==='/api/delivery/runs/9')return json(run)
  if(p.endsWith('/control'))return json({state:'paused',message:'等待手动发布'})
  if(p==='/api/delivery/runs/page')return json(page([run]))
  if(p==='/api/delivery/templates')return json([{...run.plan.template,nodes:run.plan.nodes}])
  if(p==='/api/registry/applications')return json([{name:'orders',display_name:'订单服务',sources:['deploy']}])
  if(p==='/api/users')return json([{username:'operator',display_name:'发布负责人'},{username:'reviewer',display_name:'审核人'}])
  if(p.endsWith('/page'))return json(page([]))
  if(p.endsWith('/notice'))return json({text:'隔离验收，不发送通知',warnings:[]})
  if(p.endsWith('/log'))return json({log:'Synthetic fixture only'})
  return json([])
 }
 if(p==='/__instances'){
  res.setHeader('Content-Type','text/html');return res.end(await vite.transformIndexHtml(req.url,'<html lang="zh-CN"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>实例状态兼容性验收</title></head><body><div id="app"></div><script type="module" src="/src/qa/manualPublicationFixture.js"></script></body></html>'))
 }
 next()
})}}],server:{host:'127.0.0.1',port:20053,strictPort:true}})
await vite.listen();console.log('Synthetic preview http://127.0.0.1:20053/__instances#/svc/orders/environment')
