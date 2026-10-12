// All business requests are mocked, including every Agent/SSH operation.
import assert from 'node:assert/strict'
import {readFile,mkdir} from 'node:fs/promises'
import path from 'node:path'
const base=process.env.CONSOLE_UI_TEST_URL||'http://127.0.0.1:9800',dist=process.env.CONSOLE_UI_DIST
assert.equal(new URL(base).hostname,'127.0.0.1')
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright')
const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH}),page=await browser.newPage({viewport:{width:1440,height:1000}})
const errors=[],unexpected=[],calls=new Map(),gates=new Map(),failures=new Set(),installed={node1:false,node2:true},configs={}
const hosts=['node1','node2'].map(name=>({name,environment:'test',addr:'192.0.2.'+(name==='node1'?1:2),user:'qa',credential_configured:true}))
const config=name=>configs[name]||{host_name:name,token_available:true,configured:true,synchronized:false,server_url:`http://${name}.invalid:9801`,run_user:'logagent',run_group:'logagent',spool_max_mb:1024,collect:[{application_id:'app1',service:'qa-app',paths:['/var/log/qa/*.log'],multiline:''}]}
function hold(key){let release;const promise=new Promise(r=>release=r);gates.set(key,{promise,release});return ()=>{gates.delete(key);release()}}
page.on('pageerror',e=>errors.push(e.message))
await page.route('**/*',async route=>{
 const request=route.request(),url=new URL(request.url()),p=url.pathname,method=request.method()
 if(url.origin!==new URL(base).origin)return route.abort()
 if(dist){let file
  if(/\/assets\/[^/]+\.(js|css)$/.test(p))file='assets/'+path.basename(p)
  else if(/^\/api\/plugins\/(logs|redis)\/ui\/?$/.test(p))file=(p.includes('/redis/')?'workbench':'logs')+'.html'
  if(file)return route.fulfill({body:await readFile(path.join(dist,file)),contentType:file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html'})
 }
 if(!['fetch','xhr'].includes(request.resourceType()))return route.continue()
 const key=method+' '+p;calls.set(key,(calls.get(key)||0)+1)
 if(gates.has(key))await gates.get(key).promise
 if(failures.has(key))return route.fulfill({status:502,json:{error:'QA 模拟失败，请重试'}})
 const reply=json=>route.fulfill({json})
 const agent=p.match(/^\/api\/resources\/hosts\/(node[12])\/agent\/(status|config|config\/apply|install)$/)
 if(agent){const [,name,action]=agent
  if(method==='GET'&&action==='status')return reply({installed:installed[name],active:installed[name],status:installed[name]?'active':'not-installed'})
  if(method==='GET'&&action==='config')return reply(config(name))
  if(method==='PUT'&&action==='config'){configs[name]={...request.postDataJSON(),host_name:name,token_available:true,configured:true,synchronized:false};return reply(configs[name])}
  if(method==='POST'&&action==='install'){installed[name]=true;return reply({version:'qa-only'})}
  if(method==='POST'&&action==='config/apply'){configs[name]={...config(name),synchronized:true};return reply(configs[name])}
 }
 if(method==='POST'&&/^\/api\/resources\/(hosts\/node[12]|connections\/qa-cache)\/test$/.test(p))return reply({ok:true})
 if(method==='GET'){
  if(p==='/api/plugins')return reply([{name:'logs',display:'日志查询'},{name:'redis',display:'Redis 工作台'}])
  if(p==='/api/me')return reply({username:'qa',role:'admin',auth_enabled:true})
  if(p.endsWith('/api/registry/applications')||p.endsWith('/api/services'))return reply([{name:'qa-app',application_id:'app1',sources:['logs']}])
  if(p==='/api/resources/hosts')return reply(hosts)
  if(p.endsWith('/api/environments')||p==='/api/resources/environments')return reply([{name:'test',display_name:'测试'}])
  if(p==='/api/resources/connections')return reply([{name:'qa-cache',kind:'redis',endpoint:'qa.invalid:6379',secret_configured:true}])
  if(p.endsWith('/api/stats'))return reply({agents:[]})
  if(p.endsWith('/api/policy'))return reply({query_approval:false,change_approval:true})
  if(p.endsWith('/api/search'))return reply({hits:[]})
  return reply([])
 }
 unexpected.push(key);return route.abort()
})
async function openResources(){await page.locator('.platform-nav').getByRole('button',{name:/资源/}).click();await page.getByRole('dialog',{name:'资源中心',exact:true}).waitFor()}
const row=name=>page.getByRole('dialog',{name:'资源中心',exact:true}).locator('tbody tr').filter({hasText:name})
const dialog=name=>page.getByRole('dialog',{name:'日志采集配置 · '+name,exact:true})
async function spin(button){await button.waitFor();assert.equal(await button.isDisabled(),true);assert.equal(await button.locator('.is-loading').count(),1)}
async function ready(name){await dialog(name).getByText('日志采集项',{exact:true}).waitFor();assert.equal(await dialog(name).getByRole('status').count(),0)}
async function notify(text){await page.locator('.el-message').filter({hasText:text}).last().waitFor()}
try{
 await page.goto(base+'/api/plugins/logs/ui');await openResources();await row('node1').waitFor()
 let release=hold('GET /api/resources/hosts/node1/agent/status')
 await row('node1').getByRole('button',{name:'检测 Agent',exact:true}).click()
 await spin(row('node1').getByRole('button',{name:'检测中…',exact:true}))
 assert.equal(await row('node1').getByRole('button',{name:'采集配置',exact:true}).isDisabled(),true)
 assert.equal(await row('node2').getByRole('button',{name:'检测 Agent',exact:true}).isDisabled(),false)
 await row('node1').getByRole('button',{name:'检测中…',exact:true}).evaluate(e=>e.click());assert.equal(calls.get('GET /api/resources/hosts/node1/agent/status'),1)
 await row('node2').getByRole('button',{name:'检测 Agent',exact:true}).click();await notify('Agent 检测完成')
 release();await row('node1').getByRole('button',{name:'检测 Agent',exact:true}).waitFor()
 release=hold('POST /api/resources/hosts/node1/test');await row('node1').getByRole('button',{name:'SSH 测试',exact:true}).click();await spin(row('node1').getByRole('button',{name:'SSH 测试中…',exact:true}));release();await notify('SSH 连接成功')
 // Close an in-flight read, then open another host. The old response must not win.
 const releaseOld=hold('GET /api/resources/hosts/node1/agent/config')
 await row('node1').getByRole('button',{name:'采集配置',exact:true}).click()
 await dialog('node1').getByRole('status').waitFor();await spin(row('node1').getByRole('button',{name:'读取配置中…',exact:true}))
 await dialog('node1').getByRole('button',{name:'关闭',exact:true}).click()
 const releaseNew=hold('GET /api/resources/hosts/node2/agent/config')
 await row('node2').getByRole('button',{name:'采集配置',exact:true}).click();releaseOld();await dialog('node2').getByRole('status').waitFor();releaseNew();await ready('node2')
 assert.equal(await dialog('node2').getByPlaceholder('http://平台地址:9801').inputValue(),'http://node2.invalid:9801')
 await dialog('node2').getByRole('button',{name:'关闭',exact:true}).click()
 await row('node1').getByRole('button',{name:'采集配置',exact:true}).click();await ready('node1')
 const d=dialog('node1'),paths=d.getByPlaceholder('/opt/apps/order/logs/*.log')
 await paths.fill('/var/log/qa/edited.log')
 release=hold('PUT /api/resources/hosts/node1/agent/config')
 await d.getByRole('button',{name:'仅保存期望配置',exact:true}).click();await spin(d.getByRole('button',{name:'保存中…',exact:true}))
 assert.equal(await paths.isDisabled(),true);assert.equal(await d.getByRole('button',{name:'关闭',exact:true}).isDisabled(),true)
 await page.keyboard.press('Escape');assert.equal(await d.isVisible(),true)
 await d.getByRole('button',{name:'保存中…',exact:true}).evaluate(e=>e.click());assert.equal(calls.get('PUT /api/resources/hosts/node1/agent/config'),1)
 release();await notify('期望配置已保存');await d.getByRole('button',{name:'仅保存期望配置',exact:true}).waitFor();assert.equal(await paths.isDisabled(),false)
 // Persisting and installing are distinct phases; a failed install remains retryable.
 const installKey='POST /api/resources/hosts/node1/agent/install'
 failures.add(installKey);const releaseSave=hold('PUT /api/resources/hosts/node1/agent/config'),releaseInstall=hold(installKey)
 await d.getByRole('button',{name:'保存并安装 Agent',exact:true}).click();await spin(d.getByRole('button',{name:'正在保存期望配置…',exact:true}))
 assert.equal(calls.get(installKey)||0,0);releaseSave();await spin(d.getByRole('button',{name:'正在安装 Agent 并应用配置…',exact:true}))
 const busyBox=await d.getByRole('button',{name:'正在安装 Agent 并应用配置…',exact:true}).boundingBox();assert(busyBox.y+busyBox.height<=1000,'busy action hidden below viewport')
 await mkdir('/tmp/devops-agent-loading-ui',{recursive:true});await page.screenshot({path:'/tmp/devops-agent-loading-ui/install.png'})
 releaseInstall();await notify('QA 模拟失败');await d.getByRole('button',{name:'保存并安装 Agent',exact:true}).waitFor();assert.equal(await paths.inputValue(),'/var/log/qa/edited.log')
 failures.delete(installKey);release=hold(installKey);await d.getByRole('button',{name:'保存并安装 Agent',exact:true}).click();await spin(d.getByRole('button',{name:'正在安装 Agent 并应用配置…',exact:true}));release();await notify('Agent 已安装');await d.getByRole('button',{name:'保存并下发',exact:true}).waitFor()
 const applyKey='POST /api/resources/hosts/node1/agent/config/apply'
 failures.add(applyKey);release=hold(applyKey);await d.getByRole('button',{name:'保存并下发',exact:true}).click();await spin(d.getByRole('button',{name:'正在下发采集配置…',exact:true}));release();await notify('QA 模拟失败');await d.getByRole('button',{name:'保存并下发',exact:true}).waitFor();assert.equal(await paths.inputValue(),'/var/log/qa/edited.log')
 failures.delete(applyKey);release=hold(applyKey);await d.getByRole('button',{name:'保存并下发',exact:true}).click();await spin(d.getByRole('button',{name:'正在下发采集配置…',exact:true}));await page.screenshot({path:'/tmp/devops-agent-loading-ui/apply.png'});release();await notify('采集配置已保存并下发');assert.equal(await paths.inputValue(),'/var/log/qa/edited.log')
 await d.getByRole('button',{name:'关闭',exact:true}).click()
 release=hold(installKey);await row('node1').getByRole('button',{name:'升级 Agent',exact:true}).click();await spin(row('node1').getByRole('button',{name:'安装处理中…',exact:true}));assert.equal(await row('node1').getByRole('button',{name:'检测 Agent',exact:true}).isDisabled(),true);release();await row('node1').getByRole('button',{name:'升级 Agent',exact:true}).waitFor()
 failures.add('GET /api/resources/hosts/node1/agent/config');await row('node1').getByRole('button',{name:'采集配置',exact:true}).click();await notify('QA 模拟失败');await d.waitFor({state:'hidden'});assert.equal(await row('node1').getByRole('button',{name:'采集配置',exact:true}).isDisabled(),false)
 // The same resource component supplies connection-test feedback to Redis/DBMS.
 await page.goto(base+'/api/plugins/redis/ui');await openResources()
 const card=page.locator('.resource-card').filter({hasText:'qa-cache'}),testKey='POST /api/resources/connections/qa-cache/test'
 release=hold(testKey);await card.getByRole('button',{name:'测试',exact:true}).click();await spin(card.getByRole('button',{name:'测试中…',exact:true}));await card.getByRole('button',{name:'测试中…',exact:true}).evaluate(e=>e.click());assert.equal(calls.get(testKey),1);release();await card.getByRole('button',{name:'✓ 可连接',exact:true}).waitFor()
 for(const width of [1440,900,700,390]){await page.setViewportSize({width,height:1000});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false)}
 assert.deepEqual(errors,[]);assert.deepEqual(unexpected,[])
 console.log(JSON.stringify({ok:true,mockOnly:true,checks:['Agent detection loading','SSH loading','independent hosts','no duplicate requests','immediate config read feedback','canceled reads do not overwrite another host','save/install/apply phases','busy editor protection','error recovery and draft retention','apply retains editable paths','row upgrade loading','connection loading','four widths']}))
}finally{for(const gate of gates.values())gate.release();await browser.close()}
