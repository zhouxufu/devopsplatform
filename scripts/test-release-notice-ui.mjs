// Fixture-only, no real notice sends, Bug edits or publication operations.
import assert from 'node:assert/strict'
import {readFile}from'node:fs/promises'
import path from'node:path'
const{chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright')
const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH})
const page=await browser.newPage({viewport:{width:1440,height:960}}),errors=[],unexpected=[],writes=[]
const base='http://127.0.0.1:9800',dist=path.resolve('web/dist')
page.on('pageerror',e=>errors.push(e.message));page.on('dialog',d=>d.accept())
await page.addInitScript(()=>Object.defineProperty(navigator,'clipboard',{value:{writeText:async text=>{window.__copied=text}}}))
let saved=null,conflict=true
const text='【发布说明】v1\n状态：待执行\n\n1. #7 菜单修复 @张三\n2. #8 用户修复（未填写反馈者）\n<script>window.__executed=true</script>'
const run={id:11,status:'approved',owner:'alice',digest:'approved-digest',steps:[{node_id:'manual',kind:'manual',name:'业务核对',status:'pending'}],plan:{template:{name:'发布菜单'},environment:{name:'test',display_name:'测试'},nodes:[{id:'manual',kind:'manual',name:'业务核对',instructions:'检查菜单'}],services:[],scope:{work_items:[],changes:[]}}}
await page.route('**/*',async route=>{
 const req=route.request(),u=new URL(req.url()),p=u.pathname.replace('/api/plugins/deploy',''),method=req.method(),reply=json=>route.fulfill({json})
 if(u.origin!==base)return route.abort()
 if(u.pathname==='/api/plugins/deploy/ui')return route.fulfill({body:await readFile(path.join(dist,'deploy.html')),contentType:'text/html'})
 if(/\/assets\/[^/]+\.(js|css)$/.test(u.pathname))return route.fulfill({body:await readFile(path.join(dist,'assets',path.basename(u.pathname))),contentType:u.pathname.endsWith('.js')?'text/javascript':'text/css'})
 if(!['fetch','xhr'].includes(req.resourceType()))return route.abort()
 if(method==='GET'){
  if(p==='/api/me')return reply({username:'alice',role:'admin',auth_enabled:true})
  if(p==='/api/delivery/runs/11')return reply(run)
  if(p==='/api/delivery/runs/11/control')return reply({state:'idle'})
  if(p==='/api/delivery/runs/page')return reply({items:[],total:0,page:1,page_size:20})
  if(p==='/api/release-notices/delivery/11')return reply(saved||{text:'',revision:0,scope_digest:'scope',warnings:[]})
  return reply([])
 }
 writes.push({p,method,body:req.postDataJSON()})
 if(p==='/api/release-notices/delivery/11'&&method==='POST')return reply({text,revision:saved?.revision||0,scope_digest:'scope',warnings:['Bug #8 尚无可用反馈者']})
 if(p==='/api/release-notices/delivery/11'&&method==='PUT'){
  if(conflict)return route.fulfill({status:409,json:{error:'发布说明已被其他人修改，请重新读取后合并'}})
  saved={...req.postDataJSON(),revision:(saved?.revision||0)+1};return reply(saved)
 }
 unexpected.push(method+' '+p);return route.fulfill({status:404,json:{error:'unexpected fixture route'}})
})
try{
 await page.goto(base+'/api/plugins/deploy/ui#/orchestration?run=11')
 const panel=page.locator('.release-notice');await panel.locator('summary').first().click()
 await panel.locator('.notice-text').filter({hasText:'@张三'}).waitFor()
 assert.equal(writes.filter(w=>w.method==='PUT').length,0)
 assert.equal(await page.evaluate(()=>window.__executed===true),false)
 await panel.getByRole('button',{name:'编辑说明',exact:true}).click()
 await panel.locator('textarea').fill('菜单修复完成，请 @张三 验证')
 await panel.getByRole('button',{name:'保存说明',exact:true}).click()
 await panel.getByText('发布说明已被其他人修改，请重新读取后合并',{exact:true}).waitFor()
 assert.equal(await panel.locator('textarea').inputValue(),'菜单修复完成，请 @张三 验证')
 conflict=false;await panel.getByRole('button',{name:'保存说明',exact:true}).click()
 await panel.locator('.notice-text').filter({hasText:'菜单修复完成，请 @张三 验证'}).waitFor()
 await panel.getByRole('button',{name:'复制通知',exact:true}).click()
 assert.equal(await page.evaluate(()=>window.__copied),saved.text)
 for(const width of [1440,800,390]){await page.setViewportSize({width,height:900});assert.equal(await panel.evaluate(e=>e.getBoundingClientRect().right<=innerWidth+1),true)}
 await page.setViewportSize({width:1440,height:960});await page.screenshot({path:'/tmp/release-notice-qa.png'})
 assert.equal(run.digest,'approved-digest');assert.deepEqual(unexpected,[]);assert.deepEqual(errors,[])
 assert.ok(writes.every(w=>w.p.startsWith('/api/release-notices/')),'unintended mutation/send')
 console.log('PASS release notice generation, missing feedback warning, plain text safety, editable draft, conflict retains input, saved revision, clipboard and responsive layout; fixtures only')
}finally{await browser.close()}
