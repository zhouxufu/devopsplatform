import {createServer} from '../web/node_modules/vite/dist/node/index.js'
import path from 'node:path'
let vite
vite=await createServer({root:path.resolve('web'),configFile:path.resolve('web/vite.config.js'),plugins:[{name:'java-auto-recheck-fixture',configureServer(server){server.middlewares.use(async(req,res,next)=>{
 const p=new URL(req.url,'http://fixture').pathname
 if(p.startsWith('/api/')){res.statusCode=403;res.setHeader('Content-Type','application/json');return res.end('{"error":"No business operations in fixture"}')}
 if(p==='/__auto-recheck'){res.setHeader('Content-Type','text/html');return res.end(await vite.transformIndexHtml(req.url,'<html lang="zh-CN"><head><meta charset="UTF-8"><title>自动核对隔离验证</title></head><body><div id="app"></div><script type="module" src="/src/qa/javaAutoRecheckFixture.js"></script></body></html>'))}
 next()
})}}],server:{host:'127.0.0.1',port:19988,strictPort:true}})
await vite.listen();console.log('http://127.0.0.1:19988/__auto-recheck')
