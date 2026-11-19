// Synthetic gateway plus real shell/API-center UI, paired with opt-in Go fixture.
import {createServer} from '../web/node_modules/vite/dist/node/index.js'
import path from 'node:path'
import {readFile} from 'node:fs/promises'
const environments=[{name:'test',display_name:'测试环境'},{name:'prod',display_name:'生产环境',protected:true}]
let vite
const handler=async(req,res,next)=>{
 const p=new URL(req.url,'http://fixture').pathname
 const json=v=>{res.setHeader('Content-Type','application/json');res.end(JSON.stringify(v))}
 if(p==='/api/plugins')return json(['api-center','logs','monitor','deploy'].map(name=>({name,display:({'api-center':'接口中心',logs:'日志查询',monitor:'监控',deploy:'应用与构建'})[name],url:'/api/plugins/'+name+'/ui'})))
 if(p==='/api/users'||p==='/api/registry/deployments'||p==='/api/plugins/monitor/api/otlp/services')return json([])
 if(p==='/api/registry/services')return json([{name:'orders'},{name:'other'}])
 if(p==='/api/resources/setup')return json({completed:true,environments})
 if(p==='/api/resources/environments')return json(environments)
 if(p==='/api/plugins/logs/api/services')return json({orders:{}})
 if(p==='/api/plugins/monitor/api/services')return json(['orders'])
 if(p==='/api/plugins/api-center/ui'||p==='/api/plugins/deploy/ui'){
  const source=(await readFile(p.includes('/deploy/')?'web/deploy.html':'web/api-center.html','utf8')).replace('<head>','<head><base href="/api/plugins/'+(p.includes('/deploy/')?'deploy':'api-center')+'/">')
  res.setHeader('Content-Type','text/html');return res.end(await vite.transformIndexHtml(req.url,source))
 }
 if(p==='/api/plugins/logs/ui'||p==='/api/plugins/monitor/ui'){
  res.setHeader('Content-Type','text/html');return res.end('<html lang="zh-CN"><meta charset="UTF-8"><h2>日志 / 监控上下文验收夹具</h2><pre id="context"></pre><script>window.addEventListener("message",e=>{if(e.origin===location.origin&&e.data?.type==="context.sync")document.getElementById("context").textContent=JSON.stringify({hash:location.hash,...e.data},null,2)})</script></html>')
 }
 for(const name of ['api-center','deploy'])if(req.url.startsWith('/api/plugins/'+name+'/api/'))req.url=req.url.replace('/api/plugins/'+name,'')
 next()
}
vite=await createServer({root:path.resolve('web'),configFile:path.resolve('web/vite.config.js'),plugins:[{name:'isolated-api-testing-gateway',configureServer(server){server.middlewares.use(handler)}}],server:{host:'127.0.0.1',port:19978,strictPort:true,proxy:{'/api/':{target:'http://127.0.0.1:19977'},'/fixture/':{target:'http://127.0.0.1:19977'}}}})
await vite.listen();console.log('API testing fixture http://127.0.0.1:19978/shell.html')
