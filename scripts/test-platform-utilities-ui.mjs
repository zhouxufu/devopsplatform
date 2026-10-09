// Local, read-only UI regression: no synchronization or token writes.
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import path from 'node:path'
const base=process.env.CONSOLE_UI_TEST_URL||'http://127.0.0.1:9800'
assert.equal(new URL(base).hostname,'127.0.0.1')
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright')
const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH})
const page=await browser.newPage({viewport:{width:1440,height:960}})
const errors=[],writes=[]
page.on('pageerror',e=>errors.push(e.message))
await page.route('**/*',async route=>{
 const req=route.request(),url=new URL(req.url())
 if(url.origin!==new URL(base).origin)return route.abort()
 if(!['GET','HEAD','OPTIONS'].includes(req.method())){writes.push(url.pathname);return route.abort()}
 if(url.pathname.endsWith('/api/work-item-sources'))return route.fulfill({json:[{connection_kind:'tracker-demo',kinds:['requirement','bug'],default_status:'done',status_config_key:'state',statuses:[{value:'done',label:'已完成'}]}]})
 if(url.pathname.endsWith('/api/work-item-sync'))return route.fulfill({json:[]})
 if(url.pathname.endsWith('/api/connections'))return route.fulfill({json:[{name:'通用工作项来源',kind:'tracker-demo',config:{state:'done'}}]})
 if(process.env.CONSOLE_UI_DIST){
  let file
  if(url.pathname==='/')file='shell.html'
  else if(/\/assets\/[^/]+\.(?:js|css)$/.test(url.pathname))file=`assets/${path.basename(url.pathname)}`
  else if(url.pathname==='/api/plugins/deploy/ui')file='deploy.html'
  if(file)return route.fulfill({body:await readFile(path.join(process.env.CONSOLE_UI_DIST,file)),contentType:file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html'})
  if(url.pathname==='/api/plugins/devtools/ui')return route.fulfill({body:await readFile(new URL('../internal/devtools/assets/ui.html',import.meta.url)),contentType:'text/html'})
 }
 return route.continue()
})
try{
 await page.goto(base)
 const deploy=page.frameLocator('iframe[src*="/api/plugins/deploy/ui"]')
 const sidebar=deploy.locator('.workspace-sidebar')
 await deploy.getByRole('link',{name:'变更',exact:true}).click()
 await deploy.getByRole('heading',{name:'变更管控',exact:true}).waitFor()
 await page.mouse.move(1200,900);await deploy.getByRole('heading',{name:'变更管控',exact:true}).click();await page.waitForTimeout(200)
 assert.equal(await sidebar.evaluate(e=>e.getBoundingClientRect().width),64)
 const left=await deploy.locator('.layout>main').evaluate(e=>e.getBoundingClientRect().left)
 await sidebar.hover();await page.waitForTimeout(250)
 assert.equal(await sidebar.evaluate(e=>e.getBoundingClientRect().width),176)
 assert.equal(await deploy.locator('.layout>main').evaluate(e=>e.getBoundingClientRect().left),left)
 await deploy.getByRole('button',{name:'同步工作项',exact:true}).click()
 const sync=deploy.getByRole('dialog',{name:'同步工作项',exact:true})
 await sync.getByText('沿用连接默认：已完成',{exact:true}).waitFor()
 await sync.getByText('通用工作项来源',{exact:true}).waitFor()
 assert((await sync.textContent()).includes('需求、BUG'))
 assert(!(await sync.textContent()).includes('禅道'))
 await sync.locator('.el-dialog__headerbtn').click()
 await deploy.locator('.platform-nav button').filter({hasText:'MCP'}).click()
 const panel=page.getByRole('dialog',{name:'MCP',exact:true})
 await panel.waitFor()
 const mcp=page.frameLocator('iframe[title="MCP 访问管理"]')
 await mcp.getByRole('heading',{name:'MCP',exact:true}).waitFor()
 assert.equal(await mcp.locator('html').evaluate(el=>el.classList.contains('dark')),await page.locator('html').evaluate(el=>el.classList.contains('dark')))
 await page.screenshot({path:'/tmp/devops-mcp-panel.png'})
 await mcp.locator('button:visible:not(:disabled)').last().press('Tab')
 await page.waitForFunction(()=>document.activeElement?.textContent?.trim()==='应用中心')
 assert.equal(await panel.getByRole('button',{name:'应用中心',exact:true}).evaluate(el=>el===document.activeElement),true)
 await mcp.getByRole('button',{name:'＋ 创建访问令牌',exact:true}).click()
 await mcp.locator('#name').fill('UI-only-unsaved-draft')
 await mcp.getByRole('button',{name:'取消',exact:true}).click()
 await mcp.getByRole('button',{name:'客户端接入',exact:true}).click()
 await panel.getByRole('button',{name:'关闭并返回原页面',exact:true}).click()
 await deploy.getByRole('heading',{name:'变更管控',exact:true}).waitFor()
 await deploy.locator('.platform-nav button').filter({hasText:'MCP'}).click()
 await mcp.locator('#setup').waitFor({state:'visible'})
 assert.equal(await mcp.locator('#name').inputValue(),'UI-only-unsaved-draft')
 await mcp.getByRole('button',{name:'客户端接入',exact:true}).press('Escape')
 await panel.waitFor({state:'hidden'})
 await page.setViewportSize({width:390,height:960})
 await deploy.locator('.platform-nav button').filter({hasText:'MCP'}).click()
 await panel.waitFor()
 assert.equal(await page.locator('body').evaluate(()=>document.documentElement.scrollWidth>innerWidth),false)
 await panel.getByRole('button',{name:'关闭并返回原页面',exact:true}).click()
 // Utilities only: closing MCP still leaves an entry to reopen it.
 await page.route('**/api/plugins',route=>route.fulfill({json:[{name:'devtools',display:'MCP',url:'/api/plugins/devtools/ui'}]}))
 await page.goto(base)
 await panel.waitFor()
 await panel.getByRole('button',{name:'关闭并返回原页面',exact:true}).click()
 await page.getByRole('button',{name:'打开 MCP',exact:true}).click()
 await panel.waitFor()
 await page.route('**/api/plugins',route=>route.fulfill({json:[]}))
 await page.goto(base+'/api/plugins/deploy/ui#/changes')
 await page.getByRole('heading',{name:'变更管控',exact:true}).waitFor()
 assert.equal(await page.locator('.platform-nav button').filter({hasText:'MCP'}).count(),0)
 assert.deepEqual(errors,[]);assert.deepEqual(writes,[])
 console.log(JSON.stringify({ok:true,checks:['generic adapter-driven sync','MCP sibling entry','hover expansion without layout shift','iframe retained on close','native token cancel','iframe Escape and Tab boundary','theme sync','mobile fit','utilities-only shell','activated plugin visibility','no business writes']}))
}finally{await browser.close()}
