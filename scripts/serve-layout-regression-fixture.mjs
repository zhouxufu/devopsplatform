// Disposable synthetic data for browser checks. No executor or external proxy.
import http from 'node:http'
import {readFile} from 'node:fs/promises'
import path from 'node:path'
const now=Math.floor(Date.now()/1000),calls=[],writes=[],blocked=[]
const service={name:'orders',application_id:'fixture-orders',display_name:'订单服务',artifact_type:'jar',git_url:'https://example.invalid/orders.git',branch:'main',build_tool:'maven',maven_executable:'auto',maven_goals:'clean package',artifact_glob:'target/*.jar',tags:['住房宝','测试'],steps:[]}
const apps=[service,{...service,name:'web',application_id:'fixture-web',display_name:'前端页面',artifact_type:'static',tags:['住房宝']},{...service,name:'core',application_id:'fixture-core',display_name:'核心服务',tags:['核心']}]
const envs=[{name:'test',display_name:'测试环境'},{name:'prod',display_name:'生产环境',protected:true,approvers:'reviewer'}]
const targets=envs.map(e=>({...e,service:'orders',environment:e.name,target_type:'script',deploy_config:{script:'printf fixture',script_runner:'local',script_timeout_seconds:'900'},runtime_env_keys:[],strategy:'rolling',hosts:[]}))
let cases=[{id:1,service:'orders',name:'查询订单信息',module:'订单',priority:'P1',visibility:'team',owner:'fixture',can_edit:true,revision:1,steps:[{name:'查询订单',action:'读取订单',expected:'返回订单信息',request:{method:'GET',path:'/orders/1',headers:{},body:'',assertions:[]},extractions:[]}]}]
const run={id:11,service:'orders',environment:'test',case_id:1,case_name:'查询订单信息',name:'查询订单信息',status:'success',mode:'case',started_at:now-120,ended_at:now-118,actor:'fixture',result:{status:200,passed:true,body:'{"id":1}',trace_id:'fixture-trace'},steps:[{name:'查询订单',state:'success',result:{status:200,passed:true,body:'{"id":1}'}}]}
let delay=800,empty=false,failCases=false
const port=Number(process.env.PORT||20175)
http.createServer(async(req,res)=>{
 const u=new URL(req.url,'http://fixture');const raw=u.pathname,p=raw.replace(/^\/api\/plugins\/(deploy|delivery-core|api-center)/,'').replace(/^\/api\/resources\//,'/api/')
 const json=(value,status=200)=>{res.writeHead(status,{'Content-Type':'application/json','Cache-Control':'no-store'});res.end(JSON.stringify(value))}
 if(raw==='/__fixture/state')return json({calls,writes,blocked,delay,empty})
 if(raw==='/__fixture/control'){if(u.searchParams.has('delay'))delay=Number(u.searchParams.get('delay'));if(u.searchParams.has('empty'))empty=u.searchParams.get('empty')==='1';if(u.searchParams.has('failCases'))failCases=u.searchParams.get('failCases')==='1';return json({delay,empty,failCases})}
 if(raw.startsWith('/api/')){
  calls.push({method:req.method,path:raw})
  if(req.method!=='GET'){
   let body='';for await(const c of req)body+=c
   if(p==='/api/api-testing/cases'&&req.method==='POST'){
    if(failCases)return json({error:'模拟保存失败，草稿保留'},503)
    const value={...JSON.parse(body),id:cases.length+1,revision:1,service:'orders',owner:'fixture',can_edit:true};cases.push(value);writes.push({path:p,method:req.method,name:value.name});return json(value)
   }
   if(p==='/api/git/branches')return json({branches:[{name:'main',commit:'a'.repeat(40)}],default_branch:'main'})
   blocked.push({path:raw,method:req.method});return json({error:'Fixture blocks builds, releases and executions'},403)
  }
  if(raw==='/api/me')return json({username:'fixture',display_name:'隔离验收',role:'admin',auth_enabled:false})
  if(raw==='/api/plugins')return json([{name:'deploy',display:'应用交付',status:'running',ui_path:'/ui'},{name:'api-center',display:'接口中心',status:'running',ui_path:'/ui'}])
  if(p==='/api/setup')return json({completed:true,environments:envs})
  if(p==='/api/registry/applications')return json(apps)
  if(p==='/api/application-references')return json({complete:true,applications:apps})
  if(p==='/api/environments')return json(envs)
  if(p==='/api/services'){await new Promise(r=>setTimeout(r,delay));return json(empty?[]:apps)}
  if(p==='/api/services/summary'){await new Promise(r=>setTimeout(r,delay));return json(empty?[]:apps.map(a=>({name:a.name,environments:targets,delivery:{runner:'local'},latest_run:{id:1,number:1,service:a.name,status:'success',started_at:now},latest_artifact:{id:1,service:a.name,version:'fixture',kind:'jar',digest:'sha256:fixture'}})))}
  if(p==='/api/services/orders')return json(service)
  if(p==='/api/services/orders/delivery')return json({runner:'local',variable_groups:[],build_environment:{mode:'local'}})
  if(p==='/api/services/orders/resources')return json({requirements:[],bindings:[]})
  if(p==='/api/services/orders/environments')return json(targets)
  if(p==='/api/services/orders/build-environments')return json({environments:envs,environment_required:false})
  if(p.endsWith('/delivery-default'))return json({revision:1,build_tool:'maven',artifact_source:'existing',value:null})
  if(p.endsWith('/delivery-policy'))return json({revision:1,defaults:{},stages:[]})
  if(p==='/api/runners')return json([{name:'local',kind:'local',enabled:true,max_parallel:1}])
  if(p==='/api/artifacts')return json([{id:1,service:'orders',kind:'jar',version:'fixture-v1',digest:'sha256:fixture',size:120,metadata:{branch:'main'}}])
  if(p==='/api/artifacts/1/build-compatibility')return json({compatible:true})
  if(['/api/pipeline/runs/page','/api/artifacts/page','/api/release-orders/page'].includes(p))return json({items:[],total:0,page:1,page_size:20})
  if(p==='/api/api-testing/cases')return json(cases)
  if(p==='/api/api-testing/cases/1')return json(cases[0])
  if(p==='/api/api-testing/context')return json({environments:envs,targets:[{key:'test-target',name:'测试入口'}],variables:[],enabled:true})
  if(p==='/api/api-testing/runs')return json({items:[run],total:1,page:1,page_size:15})
  if(p==='/api/api-testing/runs/11')return json(run)
  return json([])
 }
 if(raw==='/favicon.ico'){res.writeHead(204);return res.end()}
 const name=raw==='/'||raw==='/shell.html'?'shell.html':raw.endsWith('/ui')||raw.endsWith('/ui/')?(raw.includes('/api-center/')?'api-center.html':'deploy.html'):raw.endsWith('/api-center.html')?'api-center.html':raw.endsWith('/deploy.html')?'deploy.html':raw.includes('/assets/')?'assets/'+path.basename(raw):''
 if(name){try{const body=await readFile(path.resolve('web/dist',name));res.writeHead(200,{'Content-Type':name.endsWith('.js')?'text/javascript':name.endsWith('.css')?'text/css':'text/html','Cache-Control':'no-store'});return res.end(body)}catch{}}
 res.writeHead(404);res.end('Unknown fixture URL')
}).listen(port,'127.0.0.1',()=>console.log(`Layout QA http://127.0.0.1:${port}/`))
