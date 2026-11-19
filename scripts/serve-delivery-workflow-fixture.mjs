// Browser QA only: synthetic in-memory data; never calls Git, SQL or cloud APIs.
import http from 'node:http'
import {readFile} from 'node:fs/promises'
import path from 'node:path'
const root=path.resolve('web/dist'),port=Number(process.env.WORKFLOW_FIXTURE_PORT||19826)
const calls=[],unknown=[],runs=new Map()
const envs=[{name:'dev',display_name:'开发',purpose:'development',protected:false,approvers:'',sort_order:1},{name:'prod',display_name:'生产',purpose:'production',protected:true,approvers:'qa',sort_order:2}]
const users=[{username:'alice',display_name:'研发同学',enabled:true,role:'admin'},{username:'qa',display_name:'测试同学',enabled:true,role:'member'}]
const work={id:7,kind:'bug',source:'manual',title:'本地夹具：菜单修复',owner:'alice',status:'open',release_required:true}
const change={id:9,kind:'manual',title:'核对菜单',reference:'检查菜单展示',owner:'alice',status:'ready',work_items:[work]}
const review={id:'scope',kind:'manual',name:'确认交付范围',instructions:'核对本人变更',assignee_source:'specified',approvers:['alice'],approval_mode:'all'}
const code={id:'code',kind:'code-prepare',name:'按范围合并构建',application_scope:'delivery',policies:{}}
const execute={id:'verify',kind:'manual',name:'发布后验收',instructions:'检查菜单展示',change_ids:[9]}
const templates=[{id:1,name:'标准开发交付（夹具）',mode:'version',owner:'alice',services:[],preparation:[review,code],nodes:[execute]},{id:2,name:'生产热修复（夹具）',mode:'version',owner:'alice',services:[],preparation:[review,code],nodes:[{id:'qa',kind:'approval',name:'测试审批',approvers:['qa'],approval_mode:'any'},execute]}]
const policies={dev:{revision:1,workflow_template_id:1,stages:[],defaults:{applications:{}}},prod:{revision:1,workflow_template_id:2,hotfix_template_id:2,stages:[],defaults:{applications:{}}}}
const scope={type:'work_items',work_items:[work],changes:[change],digest:'fixture-scope'}
const services=[{name:'orders',artifact_type:'jar',git_url:'https://example.invalid/fixture/orders.git'}]
const clone=x=>JSON.parse(JSON.stringify(x))
function preparation(req){const template=clone(templates.find(t=>t.id===req.template_id));template.workbench='research';template.nodes=template.preparation;return {template,scope:clone(scope),environment:envs.find(e=>e.name===req.environment),nodes:clone(template.nodes),preparation_request:clone(req),scene:req.scene||'',blockers:[],warnings:[]}}
const server=http.createServer(async(req,res)=>{
 const url=new URL(req.url,'http://fixture'),original=url.pathname,p=original.replace(/^\/api\/resources\//,'/api/').replace(/^\/api\/plugins\/[^/]+/,'')
 const json=(v,status=200)=>{res.writeHead(status,{'Content-Type':'application/json','Cache-Control':'no-store'});res.end(JSON.stringify(v))}
 if(original==='/__fixture/status')return json({calls,unknown,runs:[...runs.values()].map(r=>({id:r.id,status:r.status}))})
 if(p.startsWith('/api/'))calls.push(req.method+' '+p)
 let input={};if(req.method!=='GET'){try{let raw='';for await(const b of req)raw+=b;input=JSON.parse(raw||'{}')}catch{return json({error:'invalid fixture request'},400)}}

 if(p==='/api/hosts')return json([{name:'test-worker',environment:'dev',user:'worker'}])
 if(p==='/api/connections'||p==='/api/work-item-sync'||p==='/api/work-item-sources')return json([])
 if(p==='/api/delivery/runs'&&req.method==='GET')return json([])
 if(p==='/api/work-items/7/delivery-content'){
  if(req.method==='PUT'){for(const c of input.changes||[])calls.push('FIXTURE SAVED '+c.kind+' '+c.reference);return json({saved:true})}
  return json({digest:'fixture-editor-only',changes:[]})
 }
 if(p==='/api/services/orders/resources')return json({requirements:[]})
 if(p==='/api/research/development/preview')return json({id:'feature-fixture',revision:1,owner:'alice',work_item_id:7,state:'preview',branch:input.branch||'feature/work-7-fixture',repositories:[{services:input.services,url:services[0].git_url,base_branch:input.base_branch||'main',base_commit:'a'.repeat(40),status:'pending'}]})
 if(p==='/api/research/development/feature-fixture/create')return json({id:'feature-fixture',revision:3,state:'success',branch:'feature/work-7-fixture',repositories:[{services:['orders'],base_branch:'main',base_commit:'a'.repeat(40),status:'created'}],message:'本地模拟：分支已创建'})
 if(p==='/api/delivery/runs/301'){
  if(!runs.has(301)){const node={id:'test',name:'独立测试验证',kind:'verify',service:'orders',verification:{mode:'manual'},approvers:['alice'],approval_mode:'all'};runs.set(301,{id:301,owner:'alice',status:'running',digest:'fixture-verify',plan:{template:{name:'独立验证夹具'},environment:envs[0],nodes:[node],services:[{service:'orders',artifact:{id:41,digest:'sha256:fixture',version:'abc123'}}]},steps:[{node_id:'test',kind:'verify',name:node.name,status:'pending'}]})}
  return json(runs.get(301))
 }
 if(p==='/api/delivery/runs/301/verify'){const run=runs.get(301);run.steps[0].status=input.passed?'success':'failed';run.steps[0].reviews=[{actor:'alice',approved:input.passed,comment:input.comment}];run.steps[0].evidence={kind:'manual-verification',at:Math.floor(Date.now()/1000),artifacts:{orders:'sha256:fixture'}};return json(run)}
 if(/^\/api\/delivery\/runs\/(201|301)\/observations$/.test(p))return json([])
 if(p==='/api/delivery/runs/301/control')return json({state:'idle'})
 if(p==='/api/git/branches')return json({branches:[{name:'main',commit:'a'.repeat(40)}],default_branch:'main',synced_at:Math.floor(Date.now()/1000)})
 if(p==='/api/me')return json({...users[0],auth_enabled:true})
 if(p==='/api/users')return json(users)
 if(p==='/api/plugins')return json([])
 if(p==='/api/resources/setup'||p==='/api/setup')return json({completed:true,environments:envs})
 if(p==='/api/services')return json(services)
 if(p==='/api/environments'&&req.method==='GET')return json(envs)
 if(p==='/api/environments'&&req.method==='POST'){Object.assign(envs.find(e=>e.name===input.name),input);return json(input)}
 const policy=p.match(/^\/api\/environments\/(dev|prod)\/delivery-policy$/)
 if(policy){if(req.method==='PUT')policies[policy[1]]={...input,revision:input.revision+1};return json(policies[policy[1]])}
 if(p==='/api/delivery/templates'){if(req.method==='POST'){const saved={...input,id:templates.length+1,owner:'alice'};templates.push(saved);return json(saved)}return json(templates)}
 if(p==='/api/release-versions')return json([])
 if(p==='/api/work-items')return json([work])
 if(p==='/api/work-items/7')return json(work)
 if(p==='/api/changes')return json([change])
 if(p==='/api/delivery/runs/owners')return json(['alice'])
 if(p==='/api/delivery/runs/page')return json({items:[],total:0,page:1,page_size:20})
 if(p==='/api/delivery/draft-plan')return json({strategy_kind:'custom',template:templates.find(t=>t.id===input.template_id)||templates[0],scope,nodes:[execute],defaults:policies[input.environment]?.defaults||{},policy_snapshot:{stages:[]},blockers:[],warnings:[]})
 if(p==='/api/delivery/preparations/preview')return json({plan:preparation(input),digest:'fixture-preview-only'})
 if(p==='/api/delivery/preparations/runs'&&req.method==='POST'){const plan=preparation(input);const run={id:101,status:'waiting_confirmation',owner:'alice',digest:'fixture-confirm-only',plan,steps:plan.nodes.map(n=>({node_id:n.id,name:n.name,kind:n.kind,status:n.kind==='code-prepare'?'success':'pending',detail:n.kind==='code-prepare'?'本次无需构建代码，已跳过':'',reviews:[]}))};runs.set(run.id,run);return json(run)}
 if(p==='/api/delivery/preparations/runs/101')return json(runs.get(101)||{error:'fixture run missing'})
 if(p==='/api/research/tasks/101/scope'){const run=runs.get(101);run.status=input.approve?'success':'rejected';run.steps[0].status=input.approve?'success':'rejected';run.steps[0].reviews.push({actor:'alice',approved:input.approve,comment:input.comment});return json(run)}
 if(p==='/api/delivery/preview')return json({template:templates[0],scope,environment:envs[0],preparation_run_id:input.preparation_run_id,nodes:[execute],services:[],resource_targets:[],sql_plans:[],nacos_plans:[],redis_plans:[],blockers:[],warnings:[],preflight_token:'fixture-only-not-valid-on-real-server',preflight_expires:Math.floor(Date.now()/1000)+600})
 if(p==='/api/delivery/runs'&&req.method==='POST'){const run={id:201,status:'approved',owner:'alice',digest:'fixture-execution-only',plan:{template:templates[0],scope,environment:envs[0],preparation_run_id:input.preparation_run_id,nodes:[execute],services:[]},steps:[{node_id:'verify',kind:'manual',name:execute.name,status:'pending'}]};runs.set(201,run);return json(run)}
 if(p==='/api/delivery/runs/201')return json(runs.get(201))
 if(p==='/api/delivery/runs/201/control')return json({state:'idle'})
 if(p==='/api/nacos-executions'||p==='/api/redis-executions')return json([])
 if(p.startsWith('/api/release-notices/'))return json({revision:0,entries:[],record_kind:'delivery',record_id:201})
 let file=original==='/'?'deploy.html':['/deploy.html','/research.html'].includes(original)?original.slice(1):/\/assets\/[^/]+\.(js|css)$/.test(original)?'assets/'+path.basename(original):''
 if(file&&req.method==='GET'){try{const data=await readFile(path.join(root,file));res.writeHead(200,{'Content-Type':file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html','Cache-Control':'no-store'});res.end(data);return}catch{}}
 unknown.push(req.method+' '+p);return json({error:'Unimplemented fixture route '+p},req.method==='GET'?404:405)
})
server.listen(port,'127.0.0.1',()=>console.log('Synthetic delivery workflow fixture: http://127.0.0.1:'+port+' (no external operations)'))
