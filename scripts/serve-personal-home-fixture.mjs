// Supported-browser QA server. Synthetic data only; writes are rejected except the opt-in local personal-disposition proxy.
import http from 'node:http'
import {readFile} from 'node:fs/promises'
import path from 'node:path'
const dist=path.resolve('web/dist'),port=Number(process.env.HOME_FIXTURE_PORT||19825)
const calls=[],writes=[],fixture={taskError:false,deliveryError:false,sourceDelay:0}
const plugins=['research','deploy','dbms','redis','logs','monitor'].map(name=>({name,display:{research:'研发工作台',deploy:'部署平台',dbms:'数据库工作台',redis:'Redis 工作台',logs:'日志查询',monitor:'监控大盘'}[name],url:'/api/plugins/'+name+'/ui'}))
const task={id:'11:qa',source_kind:'delivery',run_id:11,node_id:'qa',kind:'approval',title:'菜单修复 · 测试审批',owner:'dev',environment:'生产',services:['menus'],version:'v1.2',at:1792022400,digest:'fixture-only',status:'pending_approval',assignees:['alice'],stage:'测试审批',next_action:'核对 SQL 与配置变更后审批',progress:'1 / 3'}
const conflict={...task,id:'12:conflict',run_id:12,kind:'exception',title:'订单服务 · 异常待核对',stage:'部署失败',status:'failed',blocked_reason:'健康检查超时，需要核对实例状态',next_action:'查看执行记录核对异常'}
const delivery={...task,id:'delivery:11',read_only:true,title:'菜单修复交付',owner:'alice',status:'running',stage:'等待测试审批',assignees:['qa']}
const server=http.createServer(async(req,res)=>{
 const url=new URL(req.url,'http://fixture'),original=url.pathname,p=original.replace(/^\/api\/plugins\/[^/]+/,'')
 const json=(body,status=200)=>{res.writeHead(status,{'Content-Type':'application/json','Cache-Control':'no-store'});res.end(JSON.stringify(body))}
 if(process.env.HOME_ATTENTION_BACKEND&&(/^\/api\/research\/(tasks|deliveries|attention)(\/|$)/.test(p)||original==='/__attention/status')){
  const body=req.method==='GET'?undefined:await new Promise(resolve=>{let body='';req.on('data',d=>body+=d);req.on('end',()=>resolve(body))})
  const target=original==='/__attention/status'?'/__fixture/status':p+url.search
  try { const response=await fetch(process.env.HOME_ATTENTION_BACKEND+target,{method:req.method,headers:{'Content-Type':'application/json'},body})
  res.writeHead(response.status,{'Content-Type':'application/json','Cache-Control':'no-store'});res.end(await response.text());return } catch {return json({error:'Local attention fixture is restarting'},503)}
 }
 if(req.method!=='GET'){writes.push(original);return json({error:'Fixture rejects every mutation'},405)}
 if(original==='/__fixture/control'){for(const k of ['taskError','deliveryError'])if(url.searchParams.has(k))fixture[k]=url.searchParams.get(k)==='1';return json(fixture)}
 if(original==='/__fixture/status')return json({calls,writes,fixture})
 if(original.startsWith('/api/'))calls.push(original+url.search)
 if(original==='/api/plugins')return json(plugins)
 if(p==='/api/me')return json({username:'alice',display_name:'研发同学',role:'member',auth_enabled:true})
 if(p==='/api/users')return json([])
 if(p==='/api/health')return json({ok:true})
 if(p==='/api/resources/setup')return json({completed:true,environments:[{name:'test',display_name:'测试'},{name:'prod',display_name:'生产'}]})
 if(['/api/registry/services','/api/registry/deployments','/api/services','/api/environments'].includes(p))return json([])
 if(p==='/api/research/overview')return json({actor:'alice',aliases:['alice'],connections:[]})
 if(p==='/api/research/work-items/sources')return json([])
 if(p==='/api/research/work-items/page')return json({items:process.env.HOME_ATTENTION_BACKEND?[{id:101,kind:'bug',external_id:'26820',title:'这个按他们的走',source:'platform',status_label:'处理中'},{id:102,kind:'bug',external_id:'26696',title:'用户需求 AI 总结与测试环境交付追踪',source:'platform',status_label:'处理中'}]:[],total:process.env.HOME_ATTENTION_BACKEND?2:0,page:1,people:{},products:{}})
 if(p==='/api/research/tasks'){
  if(fixture.taskError)return json({error:'模拟待办服务暂不可用'},503)
  let items=[conflict,task];for(const key of ['id','kind'])if(url.searchParams.get(key))items=items.filter(t=>t[key]===url.searchParams.get(key))
  if(url.searchParams.get('q'))items=items.filter(t=>t.title.includes(url.searchParams.get('q')))
  return json({items,total:items.length,page:1})
 }
 if(p==='/api/research/deliveries')return fixture.deliveryError?json({error:'模拟交付进度不可用'},503):json({items:url.searchParams.has('id')&&url.searchParams.get('id')!==delivery.id?[]:[delivery],total:1,page:1})
 if(p==='/api/research/deliveries/delivery/11')return json({summary:task,run:{id:11,status:'pending_approval',digest:task.digest,plan:{template:{name:'标准交付'},environment:{name:'prod',display_name:'生产'},scope:{work_items:[],changes:[]},nodes:[{id:'qa',kind:'approval',name:'测试审批',approvers:['alice']}]},steps:[{node_id:'qa',kind:'approval',name:'测试审批',status:'pending',reviews:[]}]},releases:[]})
 if(p==='/api/research/deliveries/delivery/12')return json({summary:conflict,run:{id:12,status:'failed',steps:[]},releases:[]})
 if(p==='/ui'&&/^\/api\/plugins\/(logs|monitor|dbms|redis)\//.test(original)){
  res.writeHead(200,{'Content-Type':'text/html'});return res.end('<!doctype html><title>工作区保活测试夹具</title><label>查询条件<input aria-label="查询条件"></label><p>仅用于窗口保活验证，不连接任何真实资源</p><script>parent.postMessage({type:"context.visibility",service:false,environment:false},location.origin)</script>')
 }
 let file=original==='/'?'shell.html':p==='/ui'?'research.html':/\/assets\/[^/]+\.(css|js)$/.test(original)?'assets/'+path.basename(original):''
 if(file){try{const body=await readFile(path.join(dist,file));res.writeHead(200,{'Content-Type':file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html','Cache-Control':'no-store'});res.end(body);return}catch{}}
 json({error:'Unimplemented fixture route '+original},404)
})
server.listen(port,'127.0.0.1',()=>console.log('Personal home fixture: http://127.0.0.1:'+port+(process.env.HOME_ATTENTION_BACKEND?' (local personal-disposition writes only)':' (all mutations rejected)')))
