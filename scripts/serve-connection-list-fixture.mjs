// Synthetic list-only QA. Every mutation is rejected; no database connection tests.
import http from 'node:http'
import {readFile} from 'node:fs/promises'
import path from 'node:path'
const envs=[{name:'dev',display_name:'开发'},{name:'test',display_name:'测试'},{name:'prod',display_name:'生产',protected:true}]
const connections=Array.from({length:66},(_,i)=>({name:`business-${String(Math.floor(i/3)+1).padStart(2,'0')}-${envs[i%3].name}`,kind:'mysql',endpoint:`db-${envs[i%3].name}.example.test:3306`,config:{database:`business_${Math.floor(i/3)+1}`},secret_configured:true})).concat([{name:'cache-test',kind:'redis',endpoint:'cache.example.test:6379',config:{db:'0'},secret_configured:true}])
const group={name:'business-shared',revision:1,description:'多个应用复用三环境数据库绑定',requirements:[{alias:'业务数据库',kind:'mysql'}],bindings:envs.map(e=>({alias:'业务数据库',environment:e.name,connection:'business-01-'+e.name,config:{}}))}
http.createServer(async(req,res)=>{
 const u=new URL(req.url,'http://fixture'),p=u.pathname.replace(/^\/api\/resources\//,'/api/')
 const json=(v,status=200)=>{res.writeHead(status,{'Content-Type':'application/json','Cache-Control':'no-store'});res.end(JSON.stringify(v))}
 if(req.method!=='GET')return json({error:'Synthetic fixture rejects every mutation'},405)
 if(p==='/api/me')return json({username:'fixture-user',role:'admin',auth_enabled:true})
 if(p==='/api/plugins')return json([{name:'deploy',display:'部署平台',url:'/api/plugins/deploy/ui'}])
 if(p==='/api/resources/setup'||p==='/api/setup')return json({completed:true,environments:envs})
 if(p==='/api/connections')return json(connections)
 if(p==='/api/resource-groups')return json([group])
 if(p==='/api/environments')return json(envs)
 if(p==='/api/research/tasks'||p==='/api/research/deliveries')return json({items:[],total:0})
 if(p.startsWith('/api/'))return json([])
 const file=p==='/'?'shell.html':/\/assets\/[^/]+\.(css|js)$/.test(p)?'assets/'+path.basename(p):''
 if(file)try{const b=await readFile(path.join('web/dist',file));res.writeHead(200,{'Content-Type':file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html'});return res.end(b)}catch{}
 json({error:'not found'},404)
}).listen(19831,'127.0.0.1',()=>console.log('Connection list fixture: http://127.0.0.1:19831 (synthetic only)'))
