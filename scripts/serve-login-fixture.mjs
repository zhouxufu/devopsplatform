// Disposable authentication regression fixture. No executor or external service.
import http from 'node:http'
import {readFile} from 'node:fs/promises'
import path from 'node:path'

const port=Number(process.env.LOGIN_FIXTURE_PORT||19849)
const calls=[],state={authenticated:true,username:'alice',loginCalls:0,businessWrites:0,source401:false,failLogin:false}
const json=(res,value,status=200,headers={})=>{res.writeHead(status,{'Content-Type':'application/json','Cache-Control':'no-store',...headers});res.end(JSON.stringify(value))}
const pluginHTML=`<!doctype html><html lang="zh-CN"><meta charset="utf-8"><title>隔离工作区</title><body>
<h1>隔离工作区</h1><label for="draft">未提交草稿</label><textarea id="draft"></textarea>
<button id="read">模拟读取</button><button id="write">模拟提交</button><button id="reload">重新加载页面</button><button id="source">来源鉴权</button><p id="result"></p>
<script type="module">
import {installPlatformAuthGuard} from '/__fixture/platformAuth.js';installPlatformAuthGuard();
const result=document.querySelector('#result');
for(const [id,url,method] of [['read','/api/fixture/read','GET'],['write','/api/fixture/write','POST'],['source','/api/fixture/source','GET']])document.getElementById(id).onclick=async()=>{const r=await fetch(url,{method});result.textContent=id+':'+r.status};
document.querySelector('#reload').onclick=()=>location.reload();
document.querySelector('#draft').oninput=()=>parent.postMessage({type:'workspace.dirty',dirty:true},location.origin);
window.addEventListener('platform-auth-restored',()=>result.textContent='认证恢复，草稿保留');
parent.postMessage({type:'workspace.route'},location.origin);
</script></body></html>`
const server=http.createServer(async(req,res)=>{
 try{
  const url=new URL(req.url,'http://fixture'),p=url.pathname
  if(p==='/__fixture/state')return json(res,{...state,calls})
  if(p==='/__fixture/control'){
   for(const key of ['authenticated','source401','failLogin'])if(url.searchParams.has(key))state[key]=url.searchParams.get(key)==='1'
   return json(res,state)
  }
  if(p==='/__fixture/stop'){json(res,{stopped:true});server.close(()=>process.exit(0));server.closeIdleConnections();return}
  if(p==='/__fixture/platformAuth.js'||p==='/__fixture/loginNavigation.js'){
   const source=await readFile(path.resolve('web/src/shared',path.basename(p)),'utf8')
   res.writeHead(200,{'Content-Type':'text/javascript'});res.end(source);return
  }
  if(p.startsWith('/api/'))calls.push({path:p,method:req.method})
  if(p==='/api/login'&&req.method==='POST'){
   state.loginCalls++
   let body='';for await(const chunk of req)body+=chunk
   const value=JSON.parse(body)
   await new Promise(resolve=>setTimeout(resolve,300))
   if(state.failLogin||!['alice','bob'].includes(value.username)||value.password!=='fixture-password')return json(res,{error:'用户名或密码错误'},401)
   state.authenticated=true;state.username=value.username
   return json(res,{username:state.username,role:'member'})
  }
  if(p==='/api/logout'){state.authenticated=false;return json(res,{ok:true})}
  if(p==='/api/plugins/research/ui'){
   if(!state.authenticated){res.writeHead(303,{Location:'/login?return_to='+encodeURIComponent(req.url)});res.end();return}
   res.writeHead(200,{'Content-Type':'text/html','Cache-Control':'no-store'});res.end(pluginHTML);return
  }
  if(p.startsWith('/api/')){
   if(!state.authenticated)return json(res,{error:'登录已过期，请重新登录',code:'platform_login_required'},401,{'X-Platform-Auth-Required':'1'})
   if(req.method!=='GET'){state.businessWrites++;return json(res,{error:'夹具拒绝业务写入'},403)}
   if(p==='/api/fixture/source')return json(res,{error:'来源鉴权失败'},state.source401?401:403)
   if(p==='/api/me')return json(res,{username:state.username,display_name:state.username,role:'member',auth_enabled:true})
   if(p==='/api/plugins')return json(res,[{name:'research',display:'研发协作',url:'/api/plugins/research/ui'}])
   if(p.endsWith('/research/tasks')||p.endsWith('/research/deliveries'))return json(res,{items:[],total:0})
   if(p.endsWith('/page'))return json(res,{items:[],total:0,page:1,page_size:20})
   return json(res,[])
  }
  const file=p==='/'||p==='/login'?'shell.html':p.slice(1)
  if(file!=='shell.html'&&!file.startsWith('assets/'))return json(res,{error:'not found'},404)
  const root=path.resolve('web/dist'),target=path.resolve(root,file)
  if(!target.startsWith(root+path.sep))return json(res,{error:'forbidden'},403)
  const content=await readFile(target)
  const type={'.html':'text/html','.js':'text/javascript','.css':'text/css','.svg':'image/svg+xml'}[path.extname(target)]||'application/octet-stream'
  res.writeHead(200,{'Content-Type':type,'Cache-Control':'no-store'});res.end(content)
 }catch(error){json(res,{error:error.message},500)}
})
server.listen(port,'127.0.0.1',()=>console.log('Isolated login fixture http://127.0.0.1:'+port))
