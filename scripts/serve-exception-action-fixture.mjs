// Isolated supported-browser QA. Never contacts real services or changes releases.
import http from 'node:http'
import {readFile} from 'node:fs/promises'
import path from 'node:path'
let closed=false,record=null
const bulk=process.env.QUICK_IGNORE_FIXTURE==='1',records=new Map(),requests=[]
const task={id:'release:3',run_id:3,source_kind:'release',kind:'exception',title:'处理异常 · estate-ai-seek · 应用发布',owner:'admin',environment:'test',service:'estate-ai-seek',status:'failed',stage:'发布异常',assignees:['admin'],resolution_digest:'fixture-digest',at:1792395544,blocked_reason:'失败 1 个实例；需核对日志和实际运行状态',next_action:'在原发布单核对结果后处理，不自动重试'}
const tasks=bulk?[task,{...task,id:'release:4',run_id:4,title:'异常 · 批量成功'}, {...task,id:'release:5',run_id:5,title:'异常 · 快照已变化'}, {...task,id:'manual:6',run_id:6,kind:'manual',title:'待确认 · 不允许忽略'}, {...task,id:'operation:7',run_id:7,source_kind:'operation',title:'来源回写待核对 · 不允许忽略'}]:[task]
http.createServer(async(req,res)=>{
 const u=new URL(req.url,'http://fixture'),p=u.pathname
 const json=(v,status=200)=>{res.writeHead(status,{'Content-Type':'application/json','Cache-Control':'no-store'});res.end(JSON.stringify(v))}
 if(p==='/__fixture/status')return json({closed,record,records:[...records.values()],requests,release_status:'failed'})
 if(p==='/api/research/exceptions/resolve'&&req.method==='POST'){
  let raw='';for await(const part of req)raw+=part
  const body=JSON.parse(raw)
  requests.push(body)
  const current=tasks.find(t=>t.id===body.id)
  if(!current||current.kind!=='exception'||current.source_kind==='operation')return json({error:'不能忽略'},409)
  await new Promise(resolve=>setTimeout(resolve,450))
  if((bulk?records.has(body.id)||body.id==='release:5':closed)||body.digest!==current.resolution_digest)return json({error:'异常已变化'},409)
  if([...body.comment.trim()].length<6)return json({error:'至少6字符'},400)
  closed=true;record={...current,resolution_action:body.action,next_action:'忽略原因：'+body.comment,stage:'异常待办已忽略（原状态保留）'};records.set(body.id,record);return json({ok:true})
 }
 if(req.method!=='GET')return json({error:'Fixture rejects real operations'},405)
 if(p==='/api/me')return json({username:'admin',role:'admin',auth_enabled:true})
 if(p==='/api/research/tasks'){
  let items=u.searchParams.get('view')==='done'?[...records.values()]:tasks.filter(t=>!records.has(t.id))
  if(u.searchParams.has('id'))items=items.filter(t=>t.id===u.searchParams.get('id'))
  if(u.searchParams.get('kind'))items=items.filter(t=>t.kind===u.searchParams.get('kind'))
  if(u.searchParams.get('q'))items=items.filter(t=>t.title.includes(u.searchParams.get('q'))||t.service.includes(u.searchParams.get('q')))
  return json({items,total:items.length})
 }
 if(p==='/api/research/deliveries/release/3')return json({summary:task,release:{id:3,status:'failed',service:'estate-ai-seek',environment:'test',artifact_id:1,version:'7fb50c5',succeeded:0,failed:1,total:1,hosts:['fixture-host'],strategy:'rolling',batch_size:1}})
 if(p.startsWith('/api/'))return json([])
 const file=p==='/ui'?'research.html':/\/assets\/[^/]+\.(css|js)$/.test(p)?'assets/'+path.basename(p):''
 if(file)try{const b=await readFile(path.join('web/dist',file));res.writeHead(200,{'Content-Type':file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html'});return res.end(b)}catch{}
 json({error:'not found'},404)
}).listen(19829,'127.0.0.1',()=>console.log('Exception fixture: http://127.0.0.1:19829/ui (synthetic only)'))
