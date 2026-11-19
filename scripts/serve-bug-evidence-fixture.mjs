// Real workspaces with an isolated Go backend; no production data or operations.
import {createServer} from '../web/node_modules/vite/dist/node/index.js'
import path from 'node:path'
import {readFile} from 'node:fs/promises'
const envs=[{name:'test',display_name:'测试环境'},{name:'prod',display_name:'生产环境',protected:true}]
let vite
const handler=async(req,res,next)=>{
 const p=new URL(req.url,'http://fixture').pathname,json=v=>{res.setHeader('Content-Type','application/json');res.end(JSON.stringify(v))}
 const names=['research','api-center','logs','deploy']
 if(p==='/api/plugins')return json(names.map(name=>({name,display:({research:'研发工作台','api-center':'接口中心',logs:'日志查询',deploy:'应用与构建'})[name],url:'/api/plugins/'+name+'/ui'})))
 if(['/api/users','/api/registry/deployments'].includes(p))return json([])
 if(p==='/api/registry/services')return json([{name:'orders'},{name:'other'}])
 if(p==='/api/resources/setup')return json({completed:true,environments:envs})
 if(p==='/api/resources/environments')return json(envs)
 if(p==='/api/plugins/logs/api/services')return json({orders:{}})
 for(const name of names){
  if(p==='/api/plugins/'+name+'/ui'){
   const file=name==='api-center'?'api-center':name
   const source=(await readFile('web/'+file+'.html','utf8')).replace('<head>','<head><base href="/api/plugins/'+name+'/">')
   res.setHeader('Content-Type','text/html');return res.end(await vite.transformIndexHtml(req.url,source))
  }
  if(req.url.startsWith('/api/plugins/'+name+'/api/'))req.url=req.url.replace('/api/plugins/'+name,'')
 }
 next()
}
vite=await createServer({root:path.resolve('web'),configFile:path.resolve('web/vite.config.js'),plugins:[{name:'bug-evidence-fixture',configureServer(server){server.middlewares.use(handler)}}],server:{host:'127.0.0.1',port:19984,strictPort:true,proxy:{'/api/':{target:'http://127.0.0.1:19983'},'/fixture/':{target:'http://127.0.0.1:19983'}}}})
await vite.listen();console.log('Bug evidence fixture http://127.0.0.1:19984/shell.html')
