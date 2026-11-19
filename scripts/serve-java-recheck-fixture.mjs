import {createServer} from '../web/node_modules/vite/dist/node/index.js'
import path from 'node:path'
const target={service:'fixture',environment:'dev',display_name:'开发环境',target_type:'ssh-jar',remote_dir:'/fixture/app',start_cmd:'fixture',stop_cmd:'fixture',hosts:['测试主机'],deploy_config:{java_mode:'managed'}}
let order={id:26,number:6,service:'fixture',environment:'dev',artifact_id:21,version:'original-commit',status:'needs_review',target_snapshot:target,deploy_ids:[26]},deploy={id:26,service:'fixture',environment:'dev',host:'测试主机',artifact_id:21,version:'original-commit',status:'needs_review'},calls=0
let vite
vite=await createServer({root:path.resolve('web'),configFile:path.resolve('web/vite.config.js'),plugins:[{name:'java-recheck-fixture',configureServer(server){server.middlewares.use(async(req,res,next)=>{
 const p=new URL(req.url,'http://fixture').pathname
 const json=(v,status=200)=>{res.statusCode=status;res.setHeader('Content-Type','application/json');res.end(JSON.stringify(v))}
 if(p.startsWith('/api/')){
  if(p==='/api/deploys/26/recheck'&&req.method==='POST') {calls++;if(calls===1)return setTimeout(()=>json({error:'实例尚未就绪，原发布仍待核对；请查看原部署日志'},409),500);deploy.status='success';order={...order,status:'success',succeeded:1,total:1};return setTimeout(()=>json(deploy),500)}
  if(req.method!=='GET')return json({error:'fixture prohibits business operations'},403)
  if(p==='/api/state')return json({order,deploy,calls,target})
  if(p==='/api/releases/26')return json(order)
  if(p.endsWith('/instances'))return json([{host:'测试主机',address:'127.0.0.1',state:'running',version:'original-commit',kind:'ssh-jar',owned:true,controllable:true,pid:123,fingerprint:'fixture',checked_at:1794990000}])
  if(p.endsWith('/build-environments'))return json({environments:[{environment:'dev',display_name:'开发环境',build_variant:'dev'}],environment_required:true})
  if(p.endsWith('/delivery-default'))return json({value:{branch:'public-dev',code_policy:{mode:'integration',target_branch:'public-dev'},build_variant:{name:'dev',maven_profiles:'dev'}}})
  return json([])
 }
 if(p==='/__recheck'){res.setHeader('Content-Type','text/html');return res.end(await vite.transformIndexHtml(req.url,'<html lang="zh-CN"><head><meta charset="UTF-8"><title>原发布核对隔离验证</title></head><body><div id="app"></div><script type="module" src="/src/qa/javaRecheckFixture.js"></script></body></html>'))}
 next()
})}}],server:{host:'127.0.0.1',port:19987,strictPort:true}})
await vite.listen();console.log('http://127.0.0.1:19987/__recheck')
