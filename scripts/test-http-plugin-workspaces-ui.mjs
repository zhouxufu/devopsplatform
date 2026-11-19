// All requests are fixtures; this deliberately uses an insecure HTTP origin.
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import path from 'node:path'
const dist=process.env.CONSOLE_UI_DIST||path.resolve('web/dist'),base='http://devopsplatform.test'
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright')
const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH})
const plugins=[['research','研发工作台'],['deploy','部署平台'],['dbms','数据库工作台'],['redis','Redis 工作台'],['logs','日志查询'],['monitor','监控大盘']].map(([name,display])=>({name,display,url:`/api/plugins/${name}/ui`}))
try{
 const page=await browser.newPage({viewport:{width:1440,height:1000}}),errors=[],writes=[]
 page.on('pageerror',e=>errors.push(e.message));page.on('console',m=>{if(m.type()==='error')errors.push(m.text())})
 await page.route('**/*',async route=>{
  const r=route.request(),u=new URL(r.url()),p=u.pathname,reply=json=>route.fulfill({json})
  if(!['GET','HEAD'].includes(r.method())){writes.push(r.method()+' '+p);return route.abort()}
  if(p==='/uuid.js')return route.fulfill({body:await readFile('web/src/shared/uuid.js'),contentType:'text/javascript'})
  let file
  if(p==='/')file='shell.html'
  else if(/\/assets\/[^/]+\.(js|css)$/.test(p))file='assets/'+path.basename(p)
  else if(/^\/api\/plugins\/[^/]+\/ui$/.test(p)){const plugin=p.split('/')[3];file=['dbms','redis'].includes(plugin)?'workbench.html':plugin+'.html'}
  if(file)return route.fulfill({body:await readFile(path.join(dist,file)),contentType:file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html'})
  if(p==='/api/plugins')return reply(plugins)
  if(p.endsWith('/api/me')||p==='/api/me')return reply({username:'admin',role:'admin',auth_enabled:true})
  if(p==='/api/resources/setup')return reply({completed:true,environments:[{name:'test',display_name:'测试'}]})
  if(p.endsWith('/connections'))return reply([{name:'mysql-main',kind:'mysql',config:{database:'orders'}}])
  if(p.endsWith('/query-target'))return reply({connection:'mysql-main',database:'orders',db:'0'})
  if(p.endsWith('/environments'))return reply([{name:'test',display_name:'测试'}])
  if(p.includes('/policy'))return reply({query_approval:false,change_approval:true})
  if(p.endsWith('/research/overview'))return reply({actor:'admin',aliases:[],connections:[]})
  if(p.endsWith('/research/tasks')||p.endsWith('/research/deliveries'))return reply({items:[],total:0,page:1,page_size:20})
  if(p.endsWith('/page'))return reply({items:[],total:0,page:1,page_size:20})
  if(p.includes('/search'))return reply({hits:[],has_more:false})
  return reply([])
 })
 await page.addInitScript(()=>localStorage.setItem('lp-global-context',JSON.stringify({service:'estate-ai-seek',environment:'test'})))
 await page.goto(base)
 assert.equal(await page.evaluate(()=>isSecureContext),false)
 assert.equal(await page.evaluate(()=>typeof crypto.randomUUID),'undefined')
 await page.locator('.wstab').first().waitFor()
 for(const plugin of plugins){await page.getByRole('button',{name:'打开插件工作区',exact:true}).click();await page.locator('.pm').getByRole('button',{name:plugin.display,exact:true}).click();await page.frameLocator(`iframe[src*="/api/plugins/${plugin.name}/ui"]`).last().locator('#app').waitFor()}
 assert.equal(await page.locator('.wstab').count(),7)
 const research=page.frameLocator('iframe[src*="/api/plugins/research/ui"]').nth(1)
 const researchTabs=page.locator('.wstab').filter({hasText:'研发工作台'})
 assert.equal(await researchTabs.count(),2)
 assert(!(await researchTabs.allTextContents()).some(t=>t.includes('estate-ai-seek')),'research title inherited an unrelated application')
 await researchTabs.last().click()
 await research.getByRole('link',{name:'工作项',exact:true}).click()
 await page.locator('.wstab').filter({hasText:'研发工作台 · 工作项'}).waitFor()
 const researchFrame=page.frames().filter(f=>f.url().includes('/api/plugins/research/ui'))[1]
 await researchFrame.evaluate(()=>parent.postMessage({type:'workspace.label',label:'Bug #123 · 修复问题'},location.origin))
 await page.locator('.wstab').filter({hasText:'研发工作台 · Bug #123 · 修复问题'}).waitFor()
 // Messages from the host page cannot impersonate a plugin iframe.
 await page.evaluate(()=>postMessage({type:'workspace.label',label:'forged title'},location.origin))
 assert.equal(await page.locator('.wstab').filter({hasText:'forged title'}).count(),0)
 await page.getByRole('button',{name:'打开插件工作区',exact:true}).click();await page.locator('.pm').getByRole('button',{name:'数据库工作台',exact:true}).click()
 const database=page.frameLocator('iframe[src*="/api/plugins/dbms/ui"]').last()
 const field=database.locator('.query-context>label').filter({hasText:/^数据库连接/}).locator('.el-select')
 await field.click();await database.getByRole('option',{name:'mysql-main · orders',exact:true}).click()
 await page.locator('.wstab').filter({hasText:'数据库工作台 · mysql-main / orders'}).waitFor()
 const dbFrames=page.frames().filter(f=>f.url().includes('/api/plugins/dbms/ui'))
 for(let i=0;i<dbFrames.length;i++)await dbFrames[i].evaluate(label=>parent.postMessage({type:'workspace.label',label},location.origin),'mysql-'+i+' / estate')
 await page.locator('.wstab').filter({hasText:'数据库工作台 · mysql-1 / estate'}).waitFor()
 assert(!(await page.locator('.wstab').filter({hasText:'数据库工作台'}).allTextContents()).some(t=>t.includes('estate-ai-seek')))

 const id=await page.evaluate(async()=>{const {randomUUID}=await import('/uuid.js');return randomUUID()})
 assert.match(id,/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/)
 assert.deepEqual(errors,[]);assert.deepEqual(writes,[])
 console.log(JSON.stringify({ok:true,insecureHTTP:true,allSixPluginsOpen:true,uuidFallback:true,noRealWrites:true}))
}finally{await browser.close()}
