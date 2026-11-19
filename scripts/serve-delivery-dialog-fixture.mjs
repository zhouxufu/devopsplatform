// Loopback-only browser acceptance. All writes below mutate synthetic memory;
// this server has no Git, database, Runner or remote-resource client.
import http from 'node:http'
import path from 'node:path'
import {readFile} from 'node:fs/promises'
const dist=path.resolve(process.env.DIALOG_FIXTURE_DIST||'/tmp/framework-dialog-build-20261022')
const port=Number(process.env.DIALOG_FIXTURE_PORT||19843),writes=[]
const environment={name:'test',display_name:'测试环境（合成验收）'}
const work={id:204,title:'弹窗验收',kind:'bug',owner:'designer',status:'open'}
const change={id:1,kind:'code',service:'estate-ai-seek',title:'合成代码变更',reference:'feature/dialog',status:'ready'}
const node={id:'manual',kind:'manual',name:'人工核对',instructions:'仅供界面验收，不执行真实资源'}
const run={id:401,owner:'designer',status:'approved',digest:'synthetic-no-execution',plan:{template:{name:'交付确认弹窗验收'},environment,scope:{type:'work_items',work_items:[work],changes:[],excluded_change_ids:[]},nodes:[node],services:[]},steps:[{node_id:node.id,kind:node.kind,name:node.name,status:'pending'}]}
const failedNode={id:'deploy',kind:'deploy',name:'发布应用 · estate-ai-seek',service:'estate-ai-seek'}
const artifact={id:100,version:'synthetic-v1',digest:'sha256:synthetic'}
const failedRun={id:402,owner:'designer',revision:1,status:'failed',digest:'synthetic-failed-run',plan:{template:{name:'应用失败恢复验收'},environment,scope:{type:'work_items',work_items:[work],changes:[change]},services:[{service:'estate-ai-seek',artifact,target:{hosts:['fixture-host']}}],nodes:[failedNode,node]},steps:[{node_id:'deploy',kind:'deploy',name:failedNode.name,service:failedNode.service,status:'failed',release_id:9},{node_id:node.id,kind:node.kind,name:node.name,status:'pending'}]}
const child={id:9,service:'estate-ai-seek',status:'failed',artifact_id:100,total:1,succeeded:0,failed:1}
const control={state:'idle'}
let preparation={id:'0aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',state:'preview',message:'来源已冻结（合成验收）',repositories:[],builds:[]}
const json=(res,value,status=200)=>{res.writeHead(status,{'Content-Type':'application/json','Cache-Control':'no-store'});res.end(JSON.stringify(value))}
http.createServer(async(req,res)=>{
 const u=new URL(req.url,'http://fixture'),original=u.pathname,p=original.replace(/^\/api\/plugins\/deploy/,'')
 if(original==='/__fixture/status')return json(res,{writes,run_status:run.status,control,preparation_state:preparation.state})
 if(req.method==='POST'){
  let raw='';for await(const chunk of req)raw+=chunk
  const body=JSON.parse(raw||'{}');writes.push({path:p,body})
  if(p==='/api/delivery/runs/402/retry-deployment'){failedRun.revision++;failedRun.status='running';failedRun.steps[0]={...failedRun.steps[0],status:'waiting',previous_release_ids:[9],release_id:10};return json(res,failedRun)}
  if(p==='/api/delivery/runs/402/resolve-deployment'){failedRun.revision++;failedRun.status='running';failedRun.steps[0].status='success';failedRun.steps[0].manual_recovery={actor:'designer',comment:body.comment,at:Math.floor(Date.now()/1000),artifact_id:100};return json(res,failedRun)}
  if(p==='/api/delivery/runs/401/start'){control.state='running';run.status='running';return json(res,control)}
  if(p==='/api/delivery/runs/401/pause'){control.state='paused';return json(res,control)}
  if(p==='/api/delivery/runs/401/advance'){run.steps[0].status='success';run.status='success';return json(res,run)}
  if(p==='/api/delivery/draft-plan')return json(res,{strategy_kind:'standard',template:{id:0,name:'标准交付',mode:'version',nodes:[]},scope:{type:'work_items',work_items:[work],changes:[change]},nodes:[],blockers:[],warnings:[]})
  if(p==='/api/delivery/code-preparations/preview')return json(res,preparation)
  if(p==='/api/delivery/code-preparations/'+preparation.id+'/start'){preparation={...preparation,state:'ready',message:'仅合成状态，未执行 Git 或构建'};return json(res,preparation)}
  if(p==='/api/delivery/code-preparations/'+preparation.id+'/cancel'){preparation={...preparation,state:'canceled'};return json(res,preparation)}
  return json(res,{error:'夹具禁止此操作'},403)
 }
 if(req.method!=='GET')return json(res,{error:'夹具禁止此操作'},403)
 if(p==='/api/me')return json(res,{username:'designer',role:'admin',auth_enabled:true})
 if(p==='/api/users')return json(res,[{username:'designer',display_name:'合成验收'}])
 if(p==='/api/services')return json(res,[{name:'estate-ai-seek',artifact_type:'jar'}])
 if(p==='/api/environments')return json(res,[environment])
 if(p==='/api/environments/test/delivery-policy')return json(res,{defaults:{strategy_kind:'standard',artifact_source:'build',applications:{'estate-ai-seek':{code_policy:{mode:'integration',target_branch:'test-release'}}}}})
 if(p==='/api/work-items')return json(res,[work])
 if(p==='/api/work-items/204')return json(res,work)
 if(p==='/api/changes')return json(res,[change])
 if(p==='/api/delivery/runs/402')return json(res,failedRun)
 if(p==='/api/delivery/runs/402/control')return json(res,{state:'paused',message:'应用发布失败，等待重试或人工处理'})
 if(p==='/api/releases/9')return json(res,child)
 if(p==='/api/releases/10')return json(res,{...child,id:10,status:'queued',failed:0})
 if(p==='/api/delivery/runs/402/retry-preview')return json(res,{...failedRun.steps[0],revision:failedRun.revision,digest:failedRun.digest,release_id:9,service:'estate-ai-seek',artifact_id:100,version:artifact.version,targets:['fixture-host'],skipped:0})
 if(p==='/api/delivery/runs/401')return json(res,run)
 if(p==='/api/delivery/runs/401/control')return json(res,control)
 if(p==='/api/delivery/code-preparations/'+preparation.id)return json(res,preparation)
 if(p==='/api/delivery/runs/page')return json(res,{items:[],total:0,page:1,page_size:20})
 if(p.startsWith('/api/release-notices/'))return json(res,{revision:0,entries:[],record_kind:'delivery',record_id:401})
 if(p.startsWith('/api/'))return json(res,[])
 const file=p==='/ui'?'deploy.html':/\/assets\/[^/]+\.(css|js)$/.test(p)?'assets/'+path.basename(p):''
 if(file)try{const bytes=await readFile(path.join(dist,file));res.writeHead(200,{'Content-Type':file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html','Cache-Control':'no-store'});return res.end(bytes)}catch{}
 return json(res,{error:'not found'},404)
}).listen(port,'127.0.0.1',()=>console.log('Synthetic dialog QA: http://127.0.0.1:'+port+'/api/plugins/deploy/ui#/orchestration?run=401'))
