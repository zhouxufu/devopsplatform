// UI acceptance only: synthetic data, loopback binding, no remote execution.
// node scripts/serve-delivery-content-fixture.mjs
// Open /api/plugins/deploy/ui#/orchestration?work_items=201 (mixed), 202 (Nacos only), 203 (resources only).
import http from 'node:http'
import path from 'node:path'
import {readFile} from 'node:fs/promises'
const dist=path.resolve(process.env.DELIVERY_CONTENT_DIST||'web/dist')
const port=Number(process.env.DELIVERY_CONTENT_PORT||19841)
const calls=[],requests=[]
const draftCode=process.env.DELIVERY_CONTENT_DRAFT_CODE==='1'
let preparation=null
const names=['estate-ai-seek','estate-bus']
const resources=[
  {id:11,kind:'nacos',service:names[0],title:'调整找房推荐配置',summary:'更新开关与超时，移除旧配置项',nacos:{resource_alias:'config',data_id:'estate-ai.yaml',group:'DEFAULT_GROUP',format:'yaml',entries:[{key:'feature.enabled',action:'set',value:'true'},{key:'search.timeout',action:'set',value:'3000'},{key:'legacy.option',action:'delete',value:''}]}},
  {id:12,kind:'nacos',service:names[1],title:'调整总平台开关',nacos:{resource_alias:'config',data_id:'',group:'DEFAULT_GROUP',format:'properties',entries:[{key:'delivery.enabled',action:'set',value:'true'}]}},
  {id:21,kind:'mysql',service:names[0],title:'更新菜单权限',sql:{resource_alias:'business-db',content:'UPDATE menu SET enabled=1 WHERE id=100;',max_affected_rows:10,environment_overrides:[{environment:'test',content:'UPDATE menu SET enabled=1 WHERE id=101;',reason:'测试菜单编号不同'}]}},
  {id:22,kind:'mysql',service:names[1],title:'历史菜单修正',sql:{resource_alias:'business-db',content:'UPDATE menu SET enabled=1 WHERE id=200;',max_affected_rows:1,environment_overrides:[{environment:'test',skip:true,reason:'测试环境已完成，无需重复执行'}]}},
  {id:31,kind:'redis',service:names[1],title:'清理菜单缓存',redis:{resource_alias:'cache',commands:'TTL menu:tree\nUNLINK menu:tree\nEXPIRE menu:session 600'}}
].map(change=>({...change,status:'ready',risk_level:'medium',rollback_plan:'按变更记录人工核对恢复',...change}))
const code=names.map((service,index)=>({id:index+1,kind:'code',service,title:'应用代码优化',status:'ready',reference:'feature/content-ui'}))
if(draftCode)code[0].status='draft'
const workItems=[{id:204,title:'代码就绪原因与处理入口'},{id:201,title:'混合交付：应用、Nacos、MySQL、Redis'},{id:202,title:'纯 Nacos 配置交付'},{id:203,title:'纯资源交付：Nacos、MySQL、Redis'}].map(item=>({...item,kind:'requirement',source:'manual',status:'open',owner:'designer'}))
function selectedChanges(body){
  const id=body.work_item_ids?.[0]
  const scope=id===204?[code[0]]:id===202?resources.filter(change=>change.kind==='nacos'):id===203?resources:[...code,...resources]
  return scope.filter(change=>!body.selected_change_ids||body.selected_change_ids.includes(change.id))
}
const approval={id:'review',kind:'approval',name:'发布前审核',approvers:['reviewer'],approval_mode:'any'}
function nodesFor(changes){
  return [approval,...['mysql','nacos','redis'].flatMap(kind=>changes.filter(change=>change.kind===kind).map(change=>({id:kind+'-'+change.id,kind,name:change.title,service:change.service,change_ids:[change.id]}))),
    ...names.filter(name=>changes.some(change=>change.service===name&&change.kind==='code')).map(service=>({id:'deploy-'+service,kind:'deploy',name:'发布应用 · '+service,service}))]
}
function scopeFor(body){return {type:'work_items',work_items:workItems.filter(item=>body.work_item_ids?.includes(item.id)),changes:selectedChanges(body),excluded_change_ids:[],digest:'synthetic-only'}}
function previewFor(body){
  const scope=scopeFor(body),targetEnvironment={name:'test',display_name:'测试环境'}
  const sql=scope.changes.filter(change=>change.sql).map(change=>{
    const override=change.sql.environment_overrides.find(item=>item.environment===body.environment)
    return {node_id:'mysql-'+change.id,connection:'fixture-mysql',database:'test_menu',mode:'dml',warnings:[],script_selections:override?[{change_id:change.id,environment:body.environment,default_content:change.sql.content,...override}]:[],statements:override?.skip?[]:[{change_id:change.id,sql:override?.content||change.sql.content,action:'update',expected_rows:1,max_affected_rows:change.sql.max_affected_rows}]}
  })
  const nacos=scope.changes.filter(change=>change.nacos).map(change=>({node_id:'nacos-'+change.id,configs:[{change_id:change.id,service:change.service,alias:change.nacos.resource_alias,connection:'fixture-nacos',namespace:'test-namespace',data_id:change.service+'-test.yaml',group:'TEST_GROUP',format:change.nacos.format,before_md5:'synthetic-before',after_md5:'synthetic-after',diff:change.nacos.entries.map(entry=>({key:entry.key,before:'旧值',after:entry.action==='delete'?null:entry.value}))}]}))
  const redis=scope.changes.filter(change=>change.redis).map(change=>({node_id:'redis-'+change.id,changes:[{change_id:change.id,service:change.service,alias:change.redis.resource_alias,connection:'fixture-redis',endpoint:'fixture.invalid:6379',db:'2',prefix:'menu:',commands:change.redis.commands.split('\n').map(line=>line.split(' '))}]}))
  return {scope,nodes:body.nodes,environment:targetEnvironment,services:names.filter(service=>body.artifacts?.[service]).map(service=>({service,artifact:{id:body.artifacts[service],version:'合成验收版本',digest:'sha256:synthetic-only'},target:{target_type:'合成目标',hosts:[]}})),sql_plans:sql,nacos_plans:nacos,redis_plans:redis,resource_targets:[],warnings:[],blockers:body.reason==='模拟配置阻断'?['Nacos 变更 #12 的目标资源未配置']:[],preflight_token:'synthetic-no-execution',preflight_expires:Math.floor(Date.now()/1000)+600}
}
function json(res,data,status=200){res.writeHead(status,{'Content-Type':'application/json','Cache-Control':'no-store'});res.end(JSON.stringify(data))}
const server=http.createServer(async(req,res)=>{
  const url=new URL(req.url,'http://fixture'),original=url.pathname,p=original.replace(/^\/api\/plugins\/deploy/,'')
  calls.push(req.method+' '+original)
  if(original==='/__fixture/status')return json(res,{calls,requests})
  if(req.method==='POST'){
    let content='';for await(const chunk of req)content+=chunk
    const body=JSON.parse(content||'{}');requests.push({path:p,body})
    if(p==='/api/delivery/draft-plan')return json(res,{strategy_kind:'standard',strategy_version:1,template:{id:0,name:'标准交付',mode:'version',nodes:[]},scope:scopeFor(body),nodes:nodesFor(selectedChanges(body)),blockers:[],warnings:[]})
    if(draftCode&&p==='/api/changes/1/ready'){code[0].status='ready';return json(res,code[0])}
    if(draftCode&&p==='/api/delivery/code-preparations/preview'){
      if(code[0].status==='draft')return json(res,{error:'代码变更 #1 仍为草稿，尚未标记就绪。请先核对变更内容并标记就绪，再重新检查代码来源。'},400)
      preparation={id:'0aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',state:'preview',message:'来源检查完成（仅合成数据）',repositories:[],builds:[]};return json(res,preparation)
    }
    if(p==='/api/delivery/preview')return json(res,previewFor(body))
    return json(res,{error:'此夹具禁止执行、构建、部署或保存'},403)
  }
  if(req.method!=='GET')return json(res,{error:'此夹具只接受只读请求'},403)
  if(p==='/api/me')return json(res,{username:'designer',display_name:'合成验收',role:'admin',auth_enabled:true})
  if(p==='/api/plugins')return json(res,[{name:'deploy',display:'部署平台',url:'/api/plugins/deploy/ui'}])
  if(p==='/api/users')return json(res,[{username:'designer',display_name:'合成验收'},{username:'reviewer',display_name:'审核人员'}])
  if(p==='/api/environments')return json(res,[{name:'test',display_name:'测试环境'}])
  if(p==='/api/services')return json(res,names.map(name=>({name,application_id:name,artifact_type:'jar',branch:'test-release'})))
  if(p==='/api/work-items')return json(res,workItems)
  if(/^\/api\/work-items\/\d+$/.test(p))return json(res,workItems.find(item=>item.id===Number(p.split('/').at(-1))))
  if(draftCode&&p==='/api/delivery/code-preparations/0aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa')return preparation?json(res,preparation):json(res,{error:'代码准备不存在'},404)
  if(draftCode&&p==='/api/changes/1')return json(res,{...code[0],owner:'designer',risk_level:'medium',rollback_plan:'合成验收'})
  if(p==='/api/changes')return json(res,[...code,...resources])
  if(p==='/api/delivery/artifacts')return json(res,{items:[{artifact_id:names.indexOf(url.searchParams.get('service'))+100,version:'合成验收版本',digest:'sha256:synthetic-only',build_id:10,branch:'test-release'}],total:1,page:1})
  if(p==='/api/environments/test/delivery-policy')return json(res,{defaults:{strategy_kind:'standard'}})
  if(p==='/api/delivery/runs/page')return json(res,{items:[],total:0,page:1,page_size:20})
  if(p.startsWith('/api/'))return json(res,[])
  let file
  if(p==='/ui'||original==='/')file='deploy.html'
  const asset=original.match(/\/assets\/([^/]+)$/)?.[1]
  if(asset)file='assets/'+asset
  if(file)try{const body=await readFile(path.join(dist,file));res.writeHead(200,{'Content-Type':file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html','Cache-Control':'no-store'});return res.end(body)}catch{}
  res.writeHead(404);res.end('Not found')
})
server.listen(port,'127.0.0.1',()=>console.log(`Synthetic delivery content UI: http://127.0.0.1:${port}/api/plugins/deploy/ui#/orchestration?work_items=201`))
