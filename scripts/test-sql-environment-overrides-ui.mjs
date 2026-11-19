// All business requests are intercepted; never connects to a database or publishes changes.
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import path from 'node:path'
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright')
const base='http://127.0.0.1:19823',dist=process.env.CONSOLE_UI_DIST||path.resolve('web/dist')
const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH}),page=await browser.newPage({viewport:{width:1440,height:960}})
const errors=[],unexpected=[],writes=[];let envReads=0
page.on('pageerror',e=>errors.push(e.message));page.on('dialog',d=>d.accept())
const work={id:7,source:'manual',title:'环境例外测试',kind:'bug',status:'open',owner:'alice',release_required:true}
let change={id:9,kind:'mysql',title:'修正门店配置',service:'orders',owner:'alice',status:'draft',sql:{resource_alias:'db',content:'UPDATE menu SET enabled=1 WHERE id=100',max_affected_rows:100}}
const statement=()=>({change_id:9,sql:change.sql.content,action:'update',expected_rows:1,max_affected_rows:100})
const selection=()=>({change_id:9,environment:'prod',...change.sql.environment_overrides[0],default_content:change.sql.content})
const sqlPlan=()=>({node_id:'sql',connection:'production-db',database:'app',mode:'dml',schema_digest:'frozen-schema',warnings:[],script_selections:[selection()],statements:[{...statement(),sql:selection().content}]})
await page.route('**/*',async route=>{
 const req=route.request(),url=new URL(req.url()),p=url.pathname.replace(/\/api\/plugins\/(research|deploy)/,''),reply=json=>route.fulfill({json})
 if(url.origin!==base)return route.abort()
 let file;if(/\/api\/plugins\/(research|deploy)\/ui$/.test(url.pathname))file=url.pathname.includes('/research/')?'research.html':'deploy.html';else if(/\/assets\/[^/]+\.(js|css)$/.test(p))file='assets/'+path.basename(p)
 if(file)return route.fulfill({body:await readFile(path.join(dist,file)),contentType:file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html'})
 if(req.method()==='GET'){
  if(p==='/api/me')return reply({username:'alice',role:'admin',auth_enabled:true})
  if(p==='/api/services')return reply([{name:'orders'}])
  if(p==='/api/environments'){envReads++;return reply([{name:'test',display_name:'测试'},{name:'prod',display_name:'生产'}])}
  if(p.endsWith('/services/orders/resources'))return reply({requirements:[{kind:'mysql',alias:'db'}],bindings:[]})
  if(p==='/api/research/overview')return reply({connections:[]})
  if(p==='/api/research/work-items/7')return reply({item:work,activity:[],pending_operations:[]})
  if(p==='/api/research/work-items/7/tracking')return reply({deliveries:[],versions:[]})
  if(p==='/api/work-items/7/delivery-content')return reply({changes:[change],digest:'baseline'})
  if(p==='/api/research/work-items/7/delivery-records')return reply({items:change.sql.environment_overrides?.length?[{id:22,status:'success',environment:'生产',owner:'alice',created_at:1792032000,loaded:false}]:[],has_more:false})
  if(p==='/api/research/work-items/7/delivery-records/22')return reply({id:22,loaded:true,changes:[change],sql_plans:[sqlPlan()],steps:[]})
  if(p==='/api/changes/page')return reply({items:[change],total:1,page:1,page_size:20})
  if(p==='/api/changes/9')return reply(change)
  return reply([])
 }
 if(req.method()==='PUT'&&p==='/api/work-items/7/delivery-content'){
  const body=req.postDataJSON();writes.push(body);assert.equal(body.changes.length,1);assert.equal(body.changes[0].id,9);change={...change,...body.changes[0]};return reply({ok:true})
 }
 if(req.method()==='POST'&&p==='/api/changes/9/sql-preview'){
  assert.equal(req.postDataJSON().environment,'prod');return reply(sqlPlan())
 }
 unexpected.push(req.method()+' '+p);return route.abort()
})
try{
 await page.goto(base+'/api/plugins/research/ui#/work/7');await page.getByRole('button',{name:'维护交付内容',exact:true}).click()
 const modal=page.getByRole('dialog');await modal.getByRole('button',{name:'SQL 变更 · 1',exact:true}).click()
 const settings=modal.locator('.sql-environment-settings');assert.equal(await settings.evaluate(e=>e.open),false);assert.equal(envReads,0,'default editing loads advanced environments')
 await settings.locator('summary').click();await settings.getByRole('button',{name:'＋ 添加环境例外',exact:true}).click()
 let entry=settings.locator('.sql-environment-entry').first()
 await entry.locator('label').filter({hasText:'目标环境'}).locator('.el-select').click();await page.getByRole('option',{name:'生产',exact:true}).click()
 await entry.locator('textarea').fill('UPDATE menu SET enabled=1 WHERE id=856')
 await modal.getByRole('button',{name:'保存交付草稿',exact:true}).click();await page.getByText('第 1 个 SQL 环境例外：请填写原因',{exact:true}).waitFor();assert.equal(writes.length,0)
 await entry.getByPlaceholder('请说明原因，随本次交付一同审核和留档').fill('生产门店编号不同')
 await modal.getByRole('button',{name:'保存交付草稿',exact:true}).click();await page.getByText('全部交付内容已保存，仍停留在当前需求',{exact:true}).waitFor()
 assert.equal(writes.length,1);assert.equal(change.sql.content,'UPDATE menu SET enabled=1 WHERE id=100');assert.equal(change.sql.environment_overrides[0].content,'UPDATE menu SET enabled=1 WHERE id=856')
 // SQL editor remains compact by default after reload, but saved exceptions are discoverable.
 await modal.getByRole('button',{name:'SQL 变更 · 1',exact:true}).click();if(!await settings.evaluate(e=>e.open))await settings.locator('summary').click();entry=settings.locator('.sql-environment-entry').first()
 await entry.locator('label').filter({hasText:'执行方式'}).locator('.el-select').click();await page.getByRole('option',{name:'该环境无需执行',exact:true}).click();assert.equal(await entry.locator('textarea').count(),0)
 await entry.locator('label').filter({hasText:'执行方式'}).locator('.el-select').click();await page.getByRole('option',{name:'使用专用 SQL',exact:true}).click();assert.equal(await entry.locator('textarea').inputValue(),'UPDATE menu SET enabled=1 WHERE id=856')
 await settings.getByRole('button',{name:'＋ 添加环境例外',exact:true}).click()
 const skipped=settings.locator('.sql-environment-entry').nth(1)
 await skipped.locator('label').filter({hasText:'目标环境'}).locator('.el-select').click();await page.getByRole('option',{name:'测试',exact:true}).click()
 await skipped.locator('label').filter({hasText:'执行方式'}).locator('.el-select').click();await page.getByRole('option',{name:'该环境无需执行',exact:true}).click()
 await skipped.getByPlaceholder('请说明原因，随本次交付一同审核和留档').fill('测试数据已由基础数据覆盖')
 await modal.getByRole('button',{name:'保存交付草稿',exact:true}).click()
 await modal.locator('.footer>span').filter({hasText:/^已保存/}).waitFor()
 assert.equal(writes.length,2);assert.equal(change.sql.environment_overrides[1].skip,true);assert.equal(change.sql.environment_overrides[1].content,undefined)
 if(!await settings.evaluate(e=>e.open))await settings.locator('summary').click();await settings.locator('.sql-environment-entry').nth(1).getByText('该环境无需执行',{exact:true}).waitFor()
 for(const width of [1440,900,390]){
  await page.setViewportSize({width,height:960});await page.screenshot({path:'/tmp/sql-environment-width-'+width+'.png'})
  const outside=await page.locator('.el-dialog').evaluate(e=>[...e.querySelectorAll('*')].filter(x=>x.getBoundingClientRect().right>innerWidth+1).slice(0,12).map(x=>({tag:x.tagName,cls:String(x.className),right:x.getBoundingClientRect().right})))
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth+1),false,JSON.stringify(outside))
 }

 await page.screenshot({path:'/tmp/sql-environment-editor-390.png',fullPage:true});await page.setViewportSize({width:1440,height:960})
 await modal.getByRole('button',{name:'关闭',exact:true}).click();await modal.waitFor({state:'hidden'})
 // Frozen history must show the selected production script and default side by side.
 await page.reload();await page.getByRole('button',{name:'交付记录（1）',exact:true}).click()
 await page.getByText('本环境实际执行 SQL',{exact:true}).waitFor();assert.equal(await page.locator('.sql-script-diff pre').nth(0).textContent(),change.sql.content);assert.equal(await page.locator('.sql-script-diff pre').nth(1).textContent(),change.sql.environment_overrides[0].content)
 await page.goto(base+'/api/plugins/deploy/ui#/changes?tab=changes');await page.getByRole('button',{name:'修正门店配置的更多操作',exact:true}).click();await page.getByRole('menuitem',{name:'SQL 预检',exact:true}).click()
 const preview=page.getByRole('dialog',{name:'SQL 结构与影响范围预检（不执行）',exact:true});await preview.locator('.sql-preview-toolbar .el-select').click();await page.getByRole('option',{name:'生产',exact:true}).click();await preview.getByRole('button',{name:'读取目标库并预检',exact:true}).click();await preview.getByText('本环境实际执行 SQL',{exact:true}).waitFor();await page.screenshot({path:'/tmp/sql-environment-preview.png',fullPage:true})
 assert.deepEqual(errors,[]);assert.deepEqual(unexpected,[])
 console.log(JSON.stringify({ok:true,checks:['advanced collapsed by default','lazy environment loading','default SQL unchanged','specific script saved and retained','reason validation','skip toggle restores input','three responsive widths','frozen history shows actual script','target preview shows default and environment SQL'],noRealWrites:true}))
}finally{await browser.close()}
