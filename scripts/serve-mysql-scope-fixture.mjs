// Isolated synthetic acceptance fixture: never connects to any database.
import {createServer} from '../web/node_modules/vite/dist/node/index.js'
import path from 'node:path'
const calls=[],writes=[],environments=[{name:'test',display_name:'测试'},{name:'prod',display_name:'生产'}]
const connections=[{name:'shared-instance',kind:'mysql',endpoint:'database.example.invalid:3306',config:{tls:'false'},secret_configured:true},{name:'other-instance',kind:'mysql',endpoint:'other.example.invalid:3306',config:{},secret_configured:true},{name:'legacy',kind:'mysql',endpoint:'old.example.invalid:3306',config:{database:'old_db'},secret_configured:true}]
let vite
vite=await createServer({root:path.resolve('web'),configFile:path.resolve('web/vite.config.js'),plugins:[{name:'mysql-scope-fixture',configureServer(server){server.middlewares.use(async(req,res,next)=>{
 const u=new URL(req.url,'http://fixture'),original=u.pathname,p=original.replace(/^\/api\/resources\//,'/api/').replace(/^\/api\/workbench\/dbms\//,'/api/')
 const json=(v,status=200)=>{res.statusCode=status;res.setHeader('Content-Type','application/json');res.end(JSON.stringify(v))}
 if(original==='/__fixture/status')return json({calls,writes,connections})
 if(original.startsWith('/api/')){
  calls.push({method:req.method,path:original,query:Object.fromEntries(u.searchParams)})
  if(req.method!=='GET'){
   let raw='';for await(const chunk of req)raw+=chunk
   const body=JSON.parse(raw||'{}');writes.push({path:p,body})
   if(p==='/api/connections'&&req.method==='POST'){connections.push({...body,password:undefined,secret_configured:true});return json({ok:true})}
   if(p==='/api/query'&&req.method==='POST')return json({columns:['id','database'],rows:[{id:1,database:body.database||'old_db'}],elapsed_ms:1,target:{connection:body.connection,database:body.database||'old_db'}})
   return json({error:'Synthetic fixture rejects this operation'},403)
  }
  if(p==='/api/connections')return json(connections)
  const match=p.match(/^\/api\/connections\/([^/]+)\/databases$/)
  if(match){if(match[1]==='other-instance')return json({error:'模拟目录读取失败'},502);return json({databases:['orders_test','billing_test','orders_prod'],truncated:false})}
  if(p==='/api/me')return json({username:'fixture',role:'admin',auth_enabled:true})
  if(p==='/api/environments')return json(environments)
  if(p==='/api/policy')return json({query_approval:false,change_approval:true})
  if(p==='/api/query-target')return json({connection:u.searchParams.get('connection'),database:u.searchParams.get('database')||'old_db',environment:u.searchParams.get('environment')})
  if(p==='/api/services')return json([{name:'orders'}])
  if(['/api/registry/applications','/api/changes','/api/delivery/runs','/api/query-requests','/api/plugins','/api/audit'].includes(p))return json([])
  return json({error:'Unknown fixture API'},404)
 }
 if(original==='/__mysql-scope'){
  res.setHeader('Content-Type','text/html');return res.end(await vite.transformIndexHtml(req.url,'<html lang="zh-CN"><head><meta charset="UTF-8"><title>数据库实例与选库验收</title></head><body><div id="app"></div><script type="module" src="/src/qa/mysqlScopeFixture.js"></script></body></html>'))
 }
 next()
})}}],server:{host:'127.0.0.1',port:19987,strictPort:true}})
await vite.listen();console.log('Synthetic MySQL scope fixture http://127.0.0.1:19987/__mysql-scope')
