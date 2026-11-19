// Fixture-only auth flow: no real login, source actions, deployments, or database writes.
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import path from 'node:path'
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright')
const base='http://127.0.0.1:19823',dist=process.env.CONSOLE_UI_DIST||path.resolve('web/dist')
const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH}),context=await browser.newContext(),page=await context.newPage({viewport:{width:1440,height:960}})
const errors=[],unexpected=[],writes=[];let authorized=true,marker=true,rootLoads=0,frameLoads=0,protectedRequests=0
const work={id:7,source:'manual',kind:'bug',title:'登录恢复测试',owner:'admin',status:'open'}
await context.route('**/*',async route=>{
 const req=route.request(),url=new URL(req.url()),p=url.pathname.replace('/api/plugins/research',''),reply=json=>route.fulfill({json})
 if(url.origin!==base)return route.abort()
 let file;if((url.pathname==='/'||url.pathname==='/login')){file='shell.html';rootLoads++}else if(url.pathname==='/api/plugins/research/ui'){file='research.html';frameLoads++}else if(/\/assets\/[^/]+\.(js|css)$/.test(p))file='assets/'+path.basename(p)
 if(file)return route.fulfill({body:await readFile(path.join(dist,file)),contentType:file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html'})
 if(p==='/api/login'){
  if(req.postDataJSON().password!=='fixture-correct')return route.fulfill({status:401,json:{error:'用户名或密码错误'}})
  authorized=true;return reply({ok:true,username:'admin',role:'admin'})
 }
 if(p.startsWith('/api/')){
  protectedRequests++
  if(!['GET','HEAD'].includes(req.method()))writes.push({p,body:req.postDataJSON()})
  if(!authorized)return route.fulfill({status:401,headers:marker?{'X-Platform-Auth-Required':'1'}:{},json:{error:'unauthorized'}})
 }
 if(p==='/api/source-401')return route.fulfill({status:401,json:{error:'来源账号已失效'}})
 if(p==='/api/forbidden')return route.fulfill({status:403,json:{error:'没有权限'}})
 if(req.method()==='GET'){
  if(p==='/api/plugins')return reply([{name:'research',display:'研发工作台',url:'/api/plugins/research/ui'}])
  if(p==='/api/me')return reply({username:'admin',display_name:'管理员',role:'admin',auth_enabled:true})
  if(p==='/api/users'||p==='/api/registry/deployments'||p==='/api/registry/services'||p==='/api/services')return reply([])
  if(p==='/api/research/overview')return reply({actor:'admin',aliases:['admin'],connections:[]})
  if(p==='/api/research/tasks')return reply({items:[],total:0,page:1})
  if(p==='/api/research/work-items/7')return reply({item:work,activity:[],pending_operations:[]})
  if(p.endsWith('/delivery-content'))return reply({changes:[],digest:'baseline'})
  if(p.endsWith('/delivery-records'))return reply({items:[],has_more:false})
  if(p.endsWith('/tracking'))return reply({deliveries:[],versions:[]})
 }
 unexpected.push(req.method()+' '+p);return route.fulfill({status:404,json:{error:'unexpected fixture route'}})
})
page.on('pageerror',e=>errors.push(e.message))
async function login(target=page){const dialog=target.locator('.login-page');await dialog.waitFor();await dialog.getByPlaceholder('请输入密码',{exact:true}).fill('fixture-correct');await dialog.getByRole('button',{name:'登录',exact:true}).click();await dialog.waitFor({state:'hidden'})}
try{
 await page.goto(base+'/');const frame=page.frameLocator('iframe');await frame.getByRole('button',{name:'代码待处理',exact:true}).waitFor()
 await frame.getByPlaceholder('搜索任务、应用、版本或发起人').fill('保留的搜索条件')
 const child=page.frames().find(f=>f.url().includes('/api/plugins/research/ui'));await child.evaluate(()=>{window.__restored=0;addEventListener('platform-auth-restored',()=>window.__restored++)})
 // A source's own 401 and ordinary permission denial must not log out the platform.
 assert.equal(await child.evaluate(async()=> (await fetch('/api/source-401')).status),401)
 assert.equal(await child.evaluate(async()=> (await fetch('/api/forbidden')).status),403)
 assert.equal(await page.locator('.login-page').count(),0)
 // Non-workspace messages cannot force a login overlay.
 await page.evaluate(()=>window.postMessage({type:'platform.auth.required'},location.origin));assert.equal(await page.locator('.login-page').count(),0)
 authorized=false;await frame.getByRole('button',{name:'代码待处理',exact:true}).click()
 const dialog=page.locator('.login-page');await dialog.waitFor();await page.getByText('登录已过期，请重新登录。当前工作区和草稿保留，提交操作不会自动重试。',{exact:true}).waitFor()
 const before=protectedRequests;await child.evaluate(()=>Promise.all([fetch('/api/test-a'),fetch('/api/test-b')]));assert.equal(protectedRequests,before,'requests continued while logged out')
 await dialog.getByPlaceholder('请输入密码',{exact:true}).fill('incorrect');await dialog.getByRole('button',{name:'登录',exact:true}).click();await dialog.getByRole('alert').filter({hasText:'用户名或密码错误'}).waitFor()
 await login();await child.waitForFunction(()=>window.__restored===1)
 assert.equal(rootLoads,1);assert.equal(frameLoads,1);assert.equal(await frame.getByPlaceholder('搜索任务、应用、版本或发起人').inputValue(),'保留的搜索条件')
 // Missing marker from an older gateway is verified against /api/me.
 marker=false;authorized=false
 await child.evaluate(()=>fetch('api/research/work-items/7/comment',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({comment:'一次提交'})}))
 await login();await child.waitForFunction(()=>window.__restored===2);assert.equal(writes.length,1,'a submitted mutation was replayed')
 await page.screenshot({path:'/tmp/platform-login-resumed.png'})
 // Standalone plugin pages go to the platform login and restore their deep link.
 const standalone=await context.newPage();standalone.on('pageerror',e=>errors.push(e.message));await standalone.goto(base+'/api/plugins/research/ui#/work/7');await standalone.getByRole('heading',{name:'登录恢复测试',exact:true}).waitFor()
 authorized=false;marker=true
 await standalone.evaluate(()=>fetch('api/research/work-items/7'))
 await standalone.locator('.login-page').waitFor();assert.match(standalone.url(),/return_to=/)
 await login(standalone);await standalone.getByRole('heading',{name:'登录恢复测试',exact:true}).waitFor();assert.match(standalone.url(),/\/api\/plugins\/research\/ui#\/work\/7$/)
 await standalone.close();assert.deepEqual(errors,[]);assert.deepEqual(unexpected,[])
 console.log(JSON.stringify({ok:true,checks:['iframe expiry shows platform login','same-user login retains window and input','failed login stays in dialog','403 and source 401 do not log out','only registered iframe messages trusted','background calls paused','legacy 401 verified against platform identity','no mutation replay','standalone deep link restored'],noRealWrites:true}))
}finally{await browser.close()}
