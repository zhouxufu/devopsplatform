// Local-only acceptance: actual monitor backend + disposable SQLite + simulated Arthas.
import {createServer as createHTTP,request as httpRequest} from 'node:http'
import {execFileSync,spawn} from 'node:child_process'
import {mkdtemp,readFile} from 'node:fs/promises'
import {tmpdir} from 'node:os'
import path from 'node:path'
import {createServer} from '../web/node_modules/vite/dist/node/index.js'
const port=Number(process.env.ARTHAS_FIXTURE_PORT||19992),backendPort=Number(process.env.ARTHAS_FIXTURE_BACKEND_PORT||19994),agentPort=Number(process.env.ARTHAS_FIXTURE_AGENT_PORT||19993),deploymentPort=Number(process.env.ARTHAS_FIXTURE_DEPLOYMENT_PORT||19996),dataPort=Number(process.env.ARTHAS_FIXTURE_DATA_PORT||19995)
let failNextAttach=process.env.ARTHAS_FIXTURE_ATTACH_FAILURE==='1'
const temp=await mkdtemp(path.join(tmpdir(),'devops-arthas-qa-')),calls=[],remoteCalls=[]
const mock=createHTTP(async(req,res)=>{
 let body='';for await(const part of req)body+=part
 let input;try{input=JSON.parse(body)}catch{res.writeHead(400);return res.end()}
 remoteCalls.push({action:input.action,command:input.command||'',authenticated:!!req.headers.authorization})
 const json=value=>{res.setHeader('Content-Type','application/json');res.end(JSON.stringify(value))}
 if(input.action==='init_session')return json({state:'SUCCEEDED',sessionId:'fixture-'+remoteCalls.length,consumerId:'fixture-consumer'})
 if(input.action!=='exec')return json({state:'SUCCEEDED'})
 if(input.command==='fixture-unknown')return req.socket.destroy()
 const failed=input.command==='fixture-failure'
 const models={
  jvm:{type:'jvm',jvmInfo:{RUNTIME:[{name:'JVM-VERSION',value:'17.0.12'},{name:'JVM-NAME',value:'OpenJDK 64-Bit Server VM'},{name:'UPTIME',value:'21 分钟'},{name:'INPUT-ARGUMENTS',value:'-Dpassword=fixture-sensitive'}],THREAD:[{name:'THREAD-COUNT',value:52},{name:'DAEMON-THREAD-COUNT',value:31}]}},
  memory:{type:'memory',memoryInfo:{HEAP:[{name:'heap',used:134217728,max:536870912,usage:'25%'}],NON_HEAP:[{name:'metaspace',used:67108864,max:-1,usage:'未设上限'}]}},
  'thread -n 5':{type:'thread',busyThreads:[{id:42,name:'worker-1',state:'RUNNABLE',cpu:12.8},{id:12,name:'worker-2',state:'WAITING',cpu:0.1}]},
  'thread -b':{type:'thread',blockingLockInfo:{blockingThreadInfo:{threadId:12,threadName:'worker-2',threadState:'BLOCKED',lockOwnerName:'worker-1',lockName:'java.util.concurrent.Lock'}}},
 }
 if(input.command==='jvm')await new Promise(resolve=>setTimeout(resolve,500))
 json({state:failed?'FAILED':'SUCCEEDED',body:{jobStatus:'TERMINATED',timeExpired:false,results:[models[input.command]||{type:'message',message:failed?'模拟失败，无真实 JVM 操作':'模拟 Arthas 响应：'+input.command+'；<script>不执行 HTML</script>'},{type:'status',statusCode:failed?1:0}]}})
})
await new Promise(resolve=>mock.listen(agentPort,'127.0.0.1',resolve))
const deployedCalls=[]
const deployedInstances=[{id:'a'.repeat(40),name:'订单 · 模拟 Docker',service:'orders',environment:'test',host:'fixture-docker',kind:'docker-host'},{id:'b'.repeat(40),name:'订单 · 模拟 Java 主机',service:'orders',environment:'test',host:'fixture-java',kind:'ssh-jar'},{id:'c'.repeat(40),name:'订单 · 模拟权限受限容器',service:'orders',environment:'test',host:'fixture-denied',kind:'docker-host'}]
const deployment=createHTTP(async(req,res)=>{
 const u=new URL(req.url,'http://fixture'),parts=u.pathname.split('/'),id=parts[4],instance=deployedInstances.find(i=>i.id===id)
 let raw='';for await(const chunk of req)raw+=chunk
 deployedCalls.push({method:req.method,path:u.pathname})
 const json=(value,status=200)=>{res.statusCode=status;res.setHeader('Content-Type','application/json');res.end(JSON.stringify(value))}
 if(req.headers['x-role']!=='admin')return json({error:'admin only'},403)
 if(!id)return json(deployedInstances.filter(i=>(!u.searchParams.get('service')||i.service===u.searchParams.get('service'))&&(!u.searchParams.get('environment')||i.environment===u.searchParams.get('environment'))))
 if(!instance)return json({error:'实例不存在'},404)
 if(id.startsWith('c'))return json({error:'目标实例的进程或目录读取权限不足，请检查已有 SSH 账号和容器权限；平台不会自动提权'},409)
 const fingerprint=(id.startsWith('a')?'d':'e').repeat(40)
 if(req.method==='GET')return json({instance,processes:(id.startsWith('b')?[42,84]:[1]).map(pid=>({pid,fingerprint:pid===84?'f'.repeat(40):fingerprint,attach_mode:id.startsWith('a')?'native-arm64':'yes'}))})
 if(parts[5]==='attach'){await new Promise(resolve=>setTimeout(resolve,400));if(failNextAttach){failNextAttach=false;return json({error:'容器在检测期间已重启、退出或被替换（累计重启 8 次）；本次操作已停止。请先查看应用启动日志，待容器稳定后重新检测'},409)}return json({...instance,fingerprint:JSON.parse(raw).fingerprint})}
 if(parts[5]==='api'){const upstream=await fetch('http://127.0.0.1:'+agentPort+'/api',{method:'POST',headers:{'Content-Type':'application/json'},body:raw});return json(await upstream.json())}
 return json({error:'unknown fixture route'},404)
})
await new Promise(resolve=>deployment.listen(deploymentPort,'127.0.0.1',resolve))
execFileSync(process.env.GO_BIN||'/Users/zhouzheng/sdk/go/bin/go',['build','-ldflags=-linkmode=external','-o',temp+'/monitor','./cmd/monitor'],{stdio:'inherit'})
if(process.platform==='darwin')execFileSync('/usr/bin/codesign',['--force','--sign','-',temp+'/monitor'],{stdio:'inherit'})
const child=spawn(temp+'/monitor',['-addr','127.0.0.1:'+dataPort,'-admin-addr','127.0.0.1:'+backendPort,'-data',temp+'/data','-db-driver','sqlite','-db-dsn','file:'+temp+'/data/metrics.db?_pragma=journal_mode(WAL)','-token','fixture-data-only','-bus','http://127.0.0.1:1','-deploy','http://127.0.0.1:'+deploymentPort],{stdio:['ignore','ignore','inherit']})
for(let attempt=0;attempt<100;attempt++){try{if((await fetch('http://127.0.0.1:'+backendPort+'/api/health')).ok)break}catch{}await new Promise(resolve=>setTimeout(resolve,100));if(attempt===99)throw Error('fixture monitor not ready')}
// Seed a disposable target, not a runtime business connection.
await fetch('http://127.0.0.1:'+backendPort+'/api/arthas/targets',{method:'POST',headers:{'X-User':'fixture-admin','X-Role':'admin','Content-Type':'application/json'},body:JSON.stringify({name:'模拟测试 JVM',service:'orders',environment:'test',endpoint:'http://127.0.0.1:'+agentPort})})
let vite
const handle=async(req,res,next)=>{
 const u=new URL(req.url,'http://fixture'),original=u.pathname,p=original.replace(/^\/api\/plugins\/monitor/,'')
 const json=(value,status=200)=>{res.statusCode=status;res.setHeader('Content-Type','application/json');res.end(JSON.stringify(value))}
 if(original==='/__fixture/status')return json({calls,remoteCalls,deployedCalls,temp})
 if(original==='/api/plugins/monitor/ui'){const html=(await readFile('web/monitor.html','utf8')).replace('<head>','<head><base href="/api/plugins/monitor/">');res.setHeader('Content-Type','text/html');return res.end(await vite.transformIndexHtml(req.url,html))}
 if(!original.startsWith('/api/'))return next()
 calls.push(req.method+' '+original)
 if(p.startsWith('/api/arthas/')){
  const proxy=httpRequest({host:'127.0.0.1',port:backendPort,path:p+u.search,method:req.method,headers:{...req.headers,host:req.headers.host,'x-user':'fixture-admin','x-role':process.env.ARTHAS_FIXTURE_ROLE||'admin'}},upstream=>{res.writeHead(upstream.statusCode,upstream.headers);upstream.pipe(res)})
  proxy.on('error',()=>{res.statusCode=502;res.end('{"error":"fixture backend unavailable"}')});req.pipe(proxy);return
 }
 if(req.method!=='GET')return json({error:'Only disposable Arthas fixture writes allowed'},403)
 if(original==='/api/me')return json({username:'fixture-admin',display_name:'Arthas 验收',role:process.env.ARTHAS_FIXTURE_ROLE||'admin',auth_enabled:false})
 if(original==='/api/plugins')return json([{name:'monitor',display:'监控',url:'/api/plugins/monitor/ui'}])
 if(p==='/api/services'||p==='/api/otlp/services')return json(['orders','billing'])
 if(p==='/api/environments')return json(['test','prod'])
 if(original==='/api/registry/services'||original==='/api/registry/applications')return json([{name:'orders',display_name:'订单服务',application_id:'fixture-orders'},{name:'billing',display_name:'账单服务',application_id:'fixture-billing'}])
 if(p.startsWith('/api/research/inbox'))return json({items:[],total:0})
 return json([])
}
vite=await createServer({root:path.resolve('web'),configFile:path.resolve('web/vite.config.js'),plugins:[{name:'arthas-qa',configureServer(server){server.middlewares.use(handle)}}],server:{host:'127.0.0.1',port,strictPort:true}})
await vite.listen();console.log('Isolated Arthas console http://127.0.0.1:'+port+'/shell.html')
for(const signal of ['SIGTERM','SIGINT'])process.on(signal,async()=>{child.kill('SIGTERM');mock.close();deployment.close();await vite.close();process.exit(0)})
