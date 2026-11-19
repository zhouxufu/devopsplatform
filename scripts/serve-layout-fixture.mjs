// Local layout QA only. No real connections or business mutations.
import http from 'node:http'
import {readFile} from 'node:fs/promises'
import path from 'node:path'
const dist=path.resolve('web/dist'),calls=[],rejected=[]
let empty=false,branchError=false
const service={name:'estate-ai-seek',application_id:'layout-fixture',artifact_type:'image',image_name:'fixture/app',git_url:'https://example.invalid/app.git',branch:'test-release',build_tool:'command',steps:[{step:'git-checkout',params:{}},{step:'docker-build',params:{}}]}
const run={id:1,service:service.name,branch:'test-release',version:'7fb50c5',actor:'admin',status:'failed',started_at:1792377600,ended_at:1792377603,stages:[{name:'拉取源码',status:'success'},{name:'Docker 镜像构建',status:'failed'}]}
http.createServer(async(req,res)=>{
 const url=new URL(req.url,'http://fixture'),original=url.pathname,p=original.replace(/^\/api\/plugins\/[^/]+/,'')
 const json=(body,status=200)=>{res.writeHead(status,{'Content-Type':'application/json','Cache-Control':'no-store'});res.end(JSON.stringify(body))}
 if(original==='/__fixture/control'){empty=url.searchParams.get('empty')==='1';branchError=url.searchParams.get('branchError')==='1';return json({empty,branchError})}
 if(original==='/__fixture/status')return json({calls,rejected})
 calls.push(req.method+' '+original+url.search)
 if(req.method==='POST'&&p==='/api/git/branches')return branchError?json({error:'模拟分支读取失败'},503):json({branches:['main','test-release','dev','feature/one','feature/two','hotfix'].map(name=>({name,commit:'a'.repeat(40)})),default_branch:'test-release'})
 if(req.method!=='GET'){rejected.push(original);return json({error:'Fixture rejects mutations'},405)}
 if(p==='/api/me')return json({username:'admin',display_name:'测试研发',role:'admin',auth_enabled:true})
 if(p==='/api/plugins')return json([{name:'research'},{name:'deploy'}])
 if(p==='/api/services')return json([service])
 if(p==='/api/research/overview')return json({actor:'admin',aliases:['admin'],connections:empty?[]:[{name:'需求平台',account:'',credential_saved:false,auth_status:'unverified',public_read_configured:true}]})
 if(p.endsWith('/account-hint'))return json({account:'demo-developer',linked:true})
 if(p==='/api/pipeline/runs')return json([run])
 if(p==='/api/pipeline/runs/page')return json({items:[run],total:1,page:1,page_size:20})
 if(p.endsWith('/delivery'))return json({runner:'local',variable_groups:[],build_environment:{mode:'local'}})
 if(p==='/api/runners')return json([{name:'local',kind:'local',enabled:true}])
 if(p==='/api/environments')return json([{name:'test',display_name:'测试'}])
 if(p.endsWith('/environments'))return json([])
 if(p.startsWith('/api/'))return json([])
 const file=p==='/ui'?(original.includes('/research/')?'research.html':'deploy.html'):/\/assets\/[^/]+\.(css|js)$/.test(original)?'assets/'+path.basename(original):''
 if(file){try{const body=await readFile(path.join(dist,file));res.writeHead(200,{'Content-Type':file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html','Cache-Control':'no-store'});return res.end(body)}catch{}}
 json({error:'Not found'},404)
}).listen(19826,'127.0.0.1',()=>console.log('Layout fixture: http://127.0.0.1:19826 (no real business writes)'))
