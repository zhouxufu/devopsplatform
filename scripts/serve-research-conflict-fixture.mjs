// Synthetic read/write ledger only; no upstream network or real Bug mutations.
import http from 'node:http'
import {readFile} from 'node:fs/promises'
import path from 'node:path'
let changed=false,posts=[]
const unknown=[]
const item=()=>({id:5,source:'zentao:fixture',external_id:'26565',kind:'bug',title:changed?'并发修改后的最新标题':'指派冲突隔离验证',description:'<p>隔离夹具</p>',status:'open',external_status:'active',assigned_to:'alice',metadata_revision:changed?1:0,owner:'alice',release_required:true})
const remote=()=>({bug:{id:'26565',title:item().title,steps:item().description,status:'active',assignedTo:'alice'},digest:changed?'new':'old',assignedTo:'alice',users:{alice:'原处理人',qa:'测试接收人'},builds:{trunk:'主干'},resolutions:{fixed:'代码修复'}})
http.createServer(async(req,res)=>{
 const u=new URL(req.url,'http://fixture'),p=u.pathname
 const json=(v,status=200)=>{res.writeHead(status,{'Content-Type':'application/json','Cache-Control':'no-store'});res.end(JSON.stringify(v))}
 if(p==='/__fixture/status')return json({changed,posts,unknown})
 if(req.method==='POST'&&p==='/api/research/work-items/5/actions'){
  let raw='';for await(const chunk of req)raw+=chunk
  const body=JSON.parse(raw);posts.push(body)
  if(!changed){changed=true;return json({code:'research_source_conflict',error:'Bug 已被他人修改，请重新读取后提交'},409)}
  if(body.expected_digest!=='new')return json({error:'旧摘要未更新'},400)
  return json({id:body.operation_id,state:'success',message:'隔离模拟成功'})
 }
 if(req.method!=='GET')return json({error:'Fixture refuses other writes'},405)
 if(p==='/api/me')return json({username:'alice',role:'admin',auth_enabled:true})
 if(p==='/api/services')return json([])
 if(p==='/api/plugins')return json([])
 if(p==='/api/research/overview')return json({connections:[{name:'fixture',authorized:true}]})
 if(p==='/api/research/work-items/5')return json({item:item(),remote:remote(),source_identity:'personal',activity:[],pending_operations:[]})
 if(p==='/api/research/work-items/5/action-options')return json(remote())
 if(p==='/api/work-items/5/delivery-content')return json({changes:[]})
 if(p==='/api/research/work-items/5/delivery-records')return json({items:[],has_more:false})
 if(p==='/api/research/work-items/5/tracking')return json({deliveries:[],versions:[]})
 if(p.startsWith('/api/')){unknown.push(p);return json([])}
 const file=p==='/ui'?'research.html':/^\/assets\/[^/]+\.(js|css)$/.test(p)?'assets/'+path.basename(p):''
 if(file)try{res.writeHead(200,{'Content-Type':file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html'});return res.end(await readFile(path.join('web/dist',file)))}catch{}
 json({error:'not found'},404)
}).listen(19834,'127.0.0.1',()=>console.log('Synthetic fixture: http://127.0.0.1:19834/ui#/work/5'))
