// Fixture-only preview: never contact a database or execute a business mutation.
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import path from 'node:path'
const base='http://127.0.0.1:9800',dist=process.env.CONSOLE_UI_DIST
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright')
const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH})
const page=await browser.newPage({viewport:{width:1440,height:960}}),errors=[],unexpected=[]
page.on('pageerror',e=>errors.push(e.message))
const change={id:1,kind:'mysql',title:'QA SQL',status:'draft',owner:'alice',service:'orders',sql:{resource_alias:'db',content:'UPDATE menu SET name=\'qa\' WHERE id=1',max_affected_rows:100},work_items:[]}
let previews=0
await page.route('**/*',async route=>{
 const r=route.request(),u=new URL(r.url()),p=u.pathname,api=p.replace('/api/plugins/deploy',''),reply=json=>route.fulfill({json})
 if(u.origin!==base)return route.abort()
 let f;if(p==='/api/plugins/deploy/ui')f='deploy.html';else if(/\/assets\/[^/]+\.(js|css)$/.test(p))f='assets/'+path.basename(p)
 if(f&&dist)return route.fulfill({body:await readFile(path.join(dist,f)),contentType:f.endsWith('.js')?'text/javascript':f.endsWith('.css')?'text/css':'text/html'})
 if(!['xhr','fetch'].includes(r.resourceType()))return route.continue()
 if(r.method()==='GET'){
  if(api==='/api/me')return reply({username:'alice',role:'admin'})
  if(api==='/api/services')return reply([{name:'orders'}])
  if(api==='/api/environments')return reply([{name:'dev',display_name:'开发'}])
  if(api==='/api/changes/page')return reply({items:[change],total:1,page:1,page_size:20})
  if(api==='/api/changes/1')return reply(change)
  return reply([])
 }
 if(r.method()==='POST'&&api==='/api/changes/1/sql-preview'){
  previews++;assert.equal(r.postDataJSON().environment,'dev');await new Promise(resolve=>setTimeout(resolve,200))
  return reply({connection:'QA DB',database:'qa',mode:'dml',warnings:[],statements:[{action:'update',sql:change.sql.content,expected_rows:1}]})
 }
 unexpected.push(r.method()+' '+api);return route.abort()
})
try{
 await page.goto(base+'/api/plugins/deploy/ui#/changes?tab=changes');await page.getByRole('button',{name:'QA SQL的更多操作',exact:true}).click();await page.getByRole('menuitem',{name:'SQL 预检',exact:true}).click()
 const dialog=page.getByRole('dialog',{name:'SQL 结构与影响范围预检（不执行）',exact:true});await dialog.waitFor();const toolbar=dialog.locator('.sql-preview-toolbar'),button=toolbar.getByRole('button',{name:'读取目标库并预检',exact:true})
 for(const theme of ['light','dark']){
  await page.evaluate(t=>{localStorage.setItem('lp-theme',t);window.dispatchEvent(new StorageEvent('storage',{key:'lp-theme',newValue:t,storageArea:localStorage}))},theme)
  for(const width of [1440,900,700,390]){
   await page.setViewportSize({width,height:960});await page.waitForTimeout(150)
   const select=await toolbar.locator('.el-select__wrapper').boundingBox(),b=await button.boundingBox()
   assert(Math.abs(select.y+select.height-b.y-b.height)<=1,'bottom edges are misaligned '+theme+' '+width)
   assert(b.x>=select.x+select.width,'button must be beside the selector '+width)
   assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth+1),false)
   await page.screenshot({path:'/tmp/devops-sql-preview-toolbar-'+theme+'-'+width+'.png'})
  }
 }
 await button.click();assert(await toolbar.locator('input[role=combobox]').isDisabled());await dialog.getByText('计划执行 · update · 1 行',{exact:true}).waitFor();assert.equal(previews,1);assert.deepEqual(errors,[]);assert.deepEqual(unexpected,[])
 console.log(JSON.stringify({ok:true,checks:['same row and bottom alignment','light/dark 1440/900/700/390','loading and disabled environment','read-only preview preserved','no real database writes']}))
}catch(e){await page.screenshot({path:'/tmp/devops-sql-preview-toolbar-failure.png'});throw e}finally{await browser.close()}
