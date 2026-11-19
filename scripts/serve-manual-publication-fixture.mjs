// Only disposable synthetic artifacts and publication records; no executor.
import {createServer} from '../web/node_modules/vite/dist/node/index.js'
import path from 'node:path'
const calls=[],writes=[],now=Math.floor(Date.now()/1000),digest='sha256:'+('a'.repeat(64))
const visualFixture=process.env.REMAINING_UI_FIXTURE==='1'
const artifacts=[{id:70,service:'orders',kind:'jar',version:'a12bc34',digest,size:18,source:'pipeline-run',source_id:15,metadata:{artifact_role:'compilation'}},{id:71,service:'orders',kind:'oci-image',version:'a12bc34',digest:'sha256:'+('b'.repeat(64)),uri:'registry.example.invalid/orders@sha256:'+('b'.repeat(64)),source:'pipeline-run',source_id:15,metadata:{compiled_artifact_id:'70'}}]
const environments=[{name:'test',display_name:'测试环境',purpose:'test'},{name:'prod',display_name:'生产环境',purpose:'production',protected:true,approvers:'reviewer'}]
const target={service:'orders',environment:'prod',display_name:'生产环境',target_type:'manual',hosts:[],protected:true,deploy_config:{manual_artifact_kind:'jar',manual_assignee:'operator',manual_instructions:'1. 下载本次审核的 JAR，核对 SHA-256。\n2. 通过现有运维流程完成发布。\n3. 检查接口及日志，再登记实际结果。'}}
const service={name:'orders',display_name:'订单服务',artifact_type:'image',build_tool:'gradle',git_url:'https://example.invalid/orders.git',gradle_tasks:'clean bootJar',branch:'main',image_name:'orders',steps:[]}
let order={id:8,service:'orders',artifact_id:70,version:'a12bc34',environment:'prod',status:'waiting_manual',target_snapshot:target,actor:'developer',approver:'reviewer',total:1,succeeded:0,failed:0,started_at:now-600,manual_publication:{artifact_id:70,digest,assignee:'operator',instructions:target.deploy_config.manual_instructions}}
let run={id:9,status:'running',owner:'developer',approver:'reviewer',digest:'fixture-frozen',revision:2,created_at:now-650,plan:{template:{id:1,name:'生产发布流程',mode:'fixed'},environment:environments[1],reason:'手动发布交互验收',services:[{service:'orders',artifact:artifacts[0],target}],nodes:[{id:'publish',kind:'deploy',name:'发布应用',service:'orders'},{id:'verify',kind:'verify',name:'发布后接口验证',verification:{mode:'http',url:'https://example.invalid/health'}}]},steps:[{node_id:'publish',name:'发布订单服务',service:'orders',kind:'deploy',status:'waiting',release_id:8},{node_id:'verify',name:'发布后接口验证',kind:'verify',status:'pending'}]}
const page=items=>({items,total:items.length,page:1,page_size:20,total_exact:true})
let vite
vite=await createServer({root:path.resolve('web'),configFile:path.resolve('web/vite.config.js'),plugins:[{name:'manual-publication-fixture',configureServer(server){server.middlewares.use(async(req,res,next)=>{
 const u=new URL(req.url,'http://fixture'),p=u.pathname
 const json=(v,status=200)=>{res.statusCode=status;res.setHeader('Content-Type','application/json');res.end(JSON.stringify(v))}
 if(p==='/__fixture/state')return json({calls,writes,order,run})
 if(p.startsWith('/api/')){
  calls.push({method:req.method,path:p})
  if(p==='/api/artifacts/70/download'){res.setHeader('Content-Type','application/octet-stream');res.setHeader('Content-Disposition','attachment; filename=fixture.jar');return res.end(req.method==='HEAD'?'':'synthetic-only-jar')}
  if(req.method!=='GET'&&req.method!=='HEAD'){
   let raw='';for await(const part of req)raw+=part;const body=JSON.parse(raw||'{}');writes.push({path:p,body})
   if(p==='/api/releases/8/manual-result'){if(order.status!=='waiting_manual'||body.artifact_id!==70||body.digest!==digest||!body.comment)return json({error:'合成数据校验失败'},409);order={...order,status:body.result,succeeded:body.result==='success'?1:0,failed:body.result==='failed'?1:0,manual_publication:{...order.manual_publication,...body,actor:'operator',recorded_at:Math.floor(Date.now()/1000)}};return json(order)}
   if(p==='/api/delivery/runs/9/advance'){if(order.status!=='success')return json({error:'等待手动发布结果'},409);run={...run,steps:run.steps.map((s,i)=>i===0?{...s,status:'success'}:s)};return json(run)}
   if(p==='/api/services/orders/environments/prod'&&req.method==='PUT'){Object.assign(target,body);return json(target)}
   return json({error:'Synthetic fixture rejects this operation'},403)
  }
  if(p==='/api/me')return json({username:'operator',display_name:'发布负责人',role:'admin',auth_enabled:true})
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
  if(p==='/api/pipeline/runs/15')return json({id:15,service:'orders',number:15,status:'success',actor:'developer',branch:'feature/order-payment-consistency',version:'a12bc34',started_at:now-120,ended_at:now-20,artifact_id:71,artifacts,stages:visualFixture?['读取源码','校验构建环境','编译应用','运行单元测试','打包 JAR','构建运行镜像','生成接口文档','记录制品'].map(name=>({name,status:'success'})):[]})
  if(visualFixture&&p==='/api/pipeline/runs/15/log')return json({log:Array.from({length:80},(_,i)=>'[INFO] 界面布局验证 '+i+' · Building orders / verifying payment consistency').join('\n')})
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
 if(p==='/__manual'){
  res.setHeader('Content-Type','text/html');return res.end(await vite.transformIndexHtml(req.url,'<html lang="zh-CN"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>多制品与手动发布验收</title></head><body><div id="app"></div><script type="module" src="/src/qa/manualPublicationFixture.js"></script></body></html>'))
 }
 next()
})}}],server:{host:'127.0.0.1',port:19988,strictPort:true}})
await vite.listen();console.log('Synthetic preview http://127.0.0.1:19988/__manual#/orchestration?run=9')
