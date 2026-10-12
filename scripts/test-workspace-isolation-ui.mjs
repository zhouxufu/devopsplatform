// All business APIs are fixtures. This test never builds, deploys or queries
// real logs; it validates two kept-alive workspaces and their message boundary.
import assert from 'node:assert/strict'
import {readFile,mkdir} from 'node:fs/promises'
import path from 'node:path'
const base=process.env.CONSOLE_UI_TEST_URL||'http://127.0.0.1:9800',dist=process.env.CONSOLE_UI_DIST
assert.equal(new URL(base).hostname,'127.0.0.1')
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright')
const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH}),page=await browser.newPage({viewport:{width:1440,height:1000}})
const errors=[],writes=[];page.on('pageerror',e=>errors.push(e.message))
const plugins=[{name:'logs',display:'日志查询',url:'/api/plugins/logs/ui'},{name:'deploy',display:'部署平台',url:'/api/plugins/deploy/ui'}]
await page.route('**/*',async route=>{
 const r=route.request(),u=new URL(r.url()),p=u.pathname
 if(u.origin!==new URL(base).origin)return route.abort()
 if(!['GET','HEAD'].includes(r.method())){writes.push(r.method()+' '+p);return route.abort()}
 if(dist){let f;if(p==='/')f='shell.html';else if(/\/assets\/[^/]+\.(js|css)$/.test(p))f='assets/'+path.basename(p);else if(/\/api\/plugins\/(logs|deploy)\/ui\/?$/.test(p))f=p.split('/')[3]+'.html';if(f)return route.fulfill({body:await readFile(path.join(dist,f)),contentType:f.endsWith('.js')?'text/javascript':f.endsWith('.css')?'text/css':'text/html'})}
 if(!p.includes('/api/')||!['fetch','xhr'].includes(r.resourceType()))return route.continue()
 let json=[]
 if(p==='/api/plugins')json=plugins
 else if(p==='/api/me'||p.endsWith('/api/me'))json={username:'tester',role:'admin',auth_enabled:true}
 else if(p==='/api/resources/setup')json={completed:true,environments:[{name:'test',display_name:'测试'},{name:'prod',display_name:'生产'}]}
 else if(p==='/api/registry/services')json=[{name:'app-a'},{name:'app-b'}]
 else if(p==='/api/plugins/deploy/api/services')json=[{name:'app-a',artifact_type:'jar'},{name:'app-b',artifact_type:'jar'}]
 else if(p==='/api/plugins/logs/api/services')json={'app-a':{},'app-b':{}}
 else if(p==='/api/plugins/logs/api/search')json={hits:[{ts:Date.now(),service:u.searchParams.get('service')||'all',environment:u.searchParams.get('environment'),message:'fixture '+(u.searchParams.get('q')||'empty')}],has_more:false}
 return route.fulfill({json})
})
const logs=()=>page.frameLocator('iframe[src*="/api/plugins/logs/ui"]').nth(0)
async function openLogs(){await page.getByRole('button',{name:'打开插件工作区',exact:true}).click();await page.locator('.pm').getByRole('button',{name:'日志查询',exact:true}).click();await page.locator('iframe[src*="/api/plugins/logs/ui"]').last().waitFor()}
async function contextChoice(index,label){const c=page.locator('.contextbar .el-select').nth(index);await c.click();const id=await c.getByRole('combobox').getAttribute('aria-controls');await page.locator('#'+id).getByRole('option',{name:label,exact:true}).click()}
try{
 await page.goto(base);await openLogs();await contextChoice(0,'app-a');await contextChoice(1,'测试')
 const a=logs(),queryA=a.getByPlaceholder('搜索日志，例如 timeout OR refused');await queryA.fill('window-one');await queryA.press('Enter');await a.getByText('fixture window-one',{exact:true}).waitFor()
 const originA=await a.locator('html').evaluate(()=>performance.timeOrigin)
 await openLogs();const b=page.frameLocator('iframe[src*="/api/plugins/logs/ui"]').nth(1);await contextChoice(0,'app-b');await contextChoice(1,'生产')
 const queryB=b.getByPlaceholder('搜索日志，例如 timeout OR refused');await queryB.fill('window-two');await queryB.press('Enter');await b.getByText('fixture window-two',{exact:true}).waitFor()
 await page.locator('.wstab').filter({hasText:'日志查询 · app-a'}).click();assert.equal(await queryA.inputValue(),'window-one');await a.getByText('fixture window-one',{exact:true}).waitFor()
 assert.equal(await page.locator('.contextbar .el-select').nth(0).innerText(),'app-a');assert((await page.locator('.contextbar .el-select').nth(1).innerText()).includes('测试'))
 await page.getByRole('button',{name:'管理日志查询 · app-a工作区',exact:true}).click();await page.getByRole('menuitem',{name:'重命名',exact:true}).click();const modal=page.getByRole('dialog',{name:'重命名工作区',exact:true});await modal.getByRole('textbox',{name:'工作区名称'}).fill('A 错误排查');await modal.getByRole('button',{name:'保存名称',exact:true}).click();await modal.waitFor({state:'hidden'})
 await page.locator('.wstab').filter({hasText:'日志查询 · app-b'}).click();assert.equal(await queryB.inputValue(),'window-two');await b.getByText('fixture window-two',{exact:true}).waitFor()
 await page.locator('.wstab').filter({hasText:'A 错误排查'}).click();assert.equal(await a.locator('html').evaluate(()=>performance.timeOrigin),originA)
 assert.equal(await page.getByText('当前上下文',{exact:true}).count(),0);assert.equal(await page.getByText('切换后同步到所有已打开工作区',{exact:true}).count(),0)
 const desired=!(await page.locator('html').evaluate(e=>e.classList.contains('dark')));await page.getByRole('button',{name:desired?'切换深色主题':'切换浅色主题',exact:true}).click();await page.waitForTimeout(250);for(const f of [a,b])assert.equal(await f.locator('html').evaluate(e=>e.classList.contains('dark')),desired)
 await page.getByRole('button',{name:'关闭A 错误排查工作区',exact:true}).click();assert.equal(await page.locator('iframe[src*="/api/plugins/logs/ui"]').count(),1);assert.equal(await page.frameLocator('iframe[src*="/api/plugins/logs/ui"]').getByPlaceholder('搜索日志，例如 timeout OR refused').inputValue(),'window-two')
 assert((await page.locator('.wstab.on').innerText()).includes('app-b'))
 await mkdir('/tmp/devops-workspace-isolation-ui',{recursive:true});await page.screenshot({path:'/tmp/devops-workspace-isolation-ui/workspace.png'})
 assert.deepEqual(errors,[]);assert.deepEqual(writes,[]);console.log(JSON.stringify({ok:true,checks:['two log instances','independent application/environment/query','rename without remount','close does not affect survivor','theme remains shared','no real writes']}))
}finally{await browser.close()}
