// Isolated browser QA. All data is synthetic; no target host or real API calls.
import http from 'node:http'
import {readFile} from 'node:fs/promises'
import path from 'node:path'
let status='running',reads=0,saves=0
const service={name:'estate-ai-seek',artifact_type:'image',build_tool:'command',build_cmd:'true',branch:'test-release',steps:[]}
let target={service:service.name,environment:'test',display_name:'测试',target_type:'docker-host',host_group:'test-group',hosts:['fixture-host'],strategy:'rolling',batch_size:1,max_parallel:1,health_url:'http://{host}:8889/ready',deploy_config:{ports:'8889:8889',network:'bridge',mounts:JSON.stringify([{source:'/data/models/bge-v1',target:'/models/bge',read_only:true}])},runtime_env_keys:['SPRING_PROFILES_ACTIVE','DASHSCOPE_API_KEY']}
const terminal=()=>status!=='running'
const order=()=>({id:1,service:service.name,environment:'test',actor:'admin',artifact_id:1,version:'7fb50c5',status,started_at:1792395544,ended_at:terminal()?1792395560:0,total:1,succeeded:status==='success'?1:0,failed:terminal()&&status!=='success'?1:0,deploy_ids:[1],target_snapshot:target})
http.createServer(async(req,res)=>{
  const u=new URL(req.url,'http://fixture'),original=u.pathname,p=original.replace(/^\/api\/plugins\/[^/]+/,'')
  const json=(value,code=200)=>{res.writeHead(code,{'Content-Type':'application/json','Cache-Control':'no-store'});res.end(JSON.stringify(value))}
  if(p==='/__fixture/control'){if(u.searchParams.has('status'))status=u.searchParams.get('status');return json({status,reads,saves})}
  if(req.method==='PUT'&&p===`/api/services/${service.name}/environments/test`){
    let raw='';for await(const part of req)raw+=part
    const next=JSON.parse(raw),keys=new Set(next.runtime_env?Object.keys(next.runtime_env):target.runtime_env_keys)
    for(const [key,value] of Object.entries(next.runtime_env_patch||{})){if(value===null)keys.delete(key);else keys.add(key)}
    target={...next,runtime_env_keys:[...keys].sort()};delete target.runtime_env;delete target.runtime_env_patch;saves++;return json(target)
  }
  if(req.method!=='GET')return json({error:'Fixture rejects real actions'},405)
  if(p==='/api/me')return json({username:'admin',role:'admin',auth_enabled:true})
  if(p==='/api/services')return json([service])
  if(p==='/api/environments')return json([{name:'test',display_name:'测试',purpose:'test'}])
  if(p==='/api/environment-setup')return json({completed:true,environments:[{name:'test',display_name:'测试'}]})
  if(p==='/api/host-groups')return json([{name:'test-group',environment:'test',hosts:['fixture-host']}])
  if(p.endsWith('/delivery-default'))return json({revision:0,value:null,artifact_source:'existing'})
  if(p.endsWith('/environments'))return json([target])
  if(p.endsWith('/delivery'))return json({runner:'local',variable_groups:[]})
  if(p==='/api/runners')return json([{name:'local',kind:'local',enabled:true}])
  if(p==='/api/artifacts')return json([{id:1,service:service.name,version:'7fb50c5',kind:'docker-image-archive',digest:'sha256:fixture',metadata:{image_id:'sha256:fixture'}}])
  if(p==='/api/release-orders/page')return json({items:[order()],total:1,page:1,page_size:20})
  if(p==='/api/releases/1')return json(order())
  if(p==='/api/releases/1/notice')return json({description:'模拟发布说明',revision:0})
  if(p==='/api/releases'){reads++;return json([{id:1,service:service.name,environment:'test',artifact_id:1,version:'7fb50c5',status,started_at:1792395544,ended_at:terminal()?1792395560:0,total:1,succeeded:status==='success'?1:0,failed:terminal()&&status!=='success'?1:0,deploy_ids:[1],target_snapshot:target}])}
  if(p==='/api/deploys')return json([{id:1,service:service.name,environment:'test',host:'fixture-host',version:'7fb50c5',status,started_at:1792395544,ended_at:terminal()?1792395560:0}])
  if(p==='/api/deploys/1/log')return json({log:'✗ 容器启动失败：restarting，退出码 1，重启 1 次\nDashScope embedding is enabled but DASHSCOPE_API_KEY is not configured\nDASHSCOPE_API_KEY=[REDACTED]'})
  if(p.startsWith('/api/'))return json([])
  const f=p==='/ui'?'deploy.html':/\/assets\/[^/]+\.(css|js)$/.test(p)?'assets/'+path.basename(p):''
  if(f){try{const content=await readFile(path.resolve('web/dist',f));res.writeHead(200,{'Content-Type':f.endsWith('.js')?'text/javascript':f.endsWith('.css')?'text/css':'text/html','Cache-Control':'no-store'});return res.end(content)}catch{}}
  json({error:'not found'},404)
}).listen(19827,'127.0.0.1',()=>console.log('Docker release fixture: http://127.0.0.1:19827 (synthetic, no real releases)'))
