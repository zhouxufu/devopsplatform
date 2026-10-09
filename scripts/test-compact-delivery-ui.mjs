// All business writes are intercepted. No actual upload, change, or execution.
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import path from 'node:path'
const base='http://127.0.0.1:9800',dist=process.env.CONSOLE_UI_DIST
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright')
const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH})
const page=await browser.newPage({viewport:{width:1440,height:960}}),errors=[],unexpected=[]
let uploaded=0,batch=null,work=null,batches=0
page.on('pageerror',e=>errors.push(e.message));page.on('dialog',d=>d.accept())
const png=Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/l9sAAAAASUVORK5CYII=','base64')
await page.route('**/*',async route=>{
 const r=route.request(),u=new URL(r.url()),p=u.pathname,method=r.method(),reply=json=>route.fulfill({json})
 if(u.origin!==base)return route.abort()
 let file;if(/\/assets\/[^/]+\.(js|css)$/.test(p))file='assets/'+path.basename(p);else if(p==='/api/plugins/deploy/ui')file='deploy.html'
 if(file&&dist)return route.fulfill({body:await readFile(path.join(dist,file)),contentType:file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html'})
 if(!['xhr','fetch'].includes(r.resourceType())){
  if(/\/work-item-attachments\//.test(p))return route.fulfill({contentType:'image/png',body:png})
  return route.continue()
 }
 if(method==='GET'){
  if(p.endsWith('/api/me'))return reply({username:'alice',role:'admin',auth_enabled:true})
  if(p.endsWith('/api/services'))return reply([{name:'api'},{name:'web'}])
  if(p.endsWith('/api/environments'))return reply([{name:'test',display_name:'测试'}])
  if(p.endsWith('/api/work-items/page'))return reply({items:work?[work]:[],total:work?1:0,page:1,page_size:20})
  if(p.endsWith('/api/work-items'))return reply(work?[work]:[])
  if(/\/api\/work-items\/\d+$/.test(p))return reply(work)
  if(p.endsWith('/api/services/api/resources')||p.endsWith('/api/services/web/resources'))return reply({requirements:[{alias:'db',kind:'mysql'},{alias:'config',kind:'nacos'}],bindings:[]})
  return reply([])
 }
 if(method==='POST'&&p.endsWith('/api/work-item-attachments')){uploaded++;return reply({id:uploaded.toString(16).padStart(32,'0'),name:'image-'+uploaded+'.png',reference:'attachment:'+uploaded.toString(16).padStart(32,'0')})}
 if(method==='POST'&&p.endsWith('/api/work-items')){work={...r.postDataJSON(),id:910101,owner:'alice'};return reply(work)}
 if(method==='POST'&&p.endsWith('/api/changes/batch')){batch=r.postDataJSON();batches++;return reply(batch.changes.map((c,i)=>({...c,id:i+1,status:'draft'})))}
 unexpected.push(method+' '+p);return route.abort()
})
const choose=async(surface,label,option)=>{
 const control=surface.locator('label').filter({hasText:new RegExp('^'+label)}).first().locator('.el-select');await control.click()
 const id=await control.getByRole('combobox').getAttribute('aria-controls');await page.locator('#'+id).getByRole('option',{name:option,exact:true}).click();await surface.locator('strong').first().click().catch(()=>{})
}
try{
 await page.goto(base+'/api/plugins/deploy/ui#/changes?tab=work')
 await page.getByRole('button',{name:'＋ 新建需求',exact:true}).click()
 // The actual modal title is implementation-local; use the editor's parent dialog.
 const editor=page.locator('.markdown-editor'),dialog=page.getByRole('dialog').filter({has:editor})
 await editor.waitFor();await dialog.locator('label').filter({hasText:/^标题/}).locator('input').fill('Markdown 多图需求')
 await editor.getByRole('textbox',{name:'需求说明编辑器'}).fill('# 一级标题\n\n- 检查配置\n- 核对数据\n\n<script>window.__qaXSS=1</script>\n[危险链接](javascript:alert(1))')
 await editor.locator('input[type=file]').setInputFiles([{name:'one.png',mimeType:'image/png',buffer:png},{name:'two.png',mimeType:'image/png',buffer:png}])
 await page.waitForFunction(()=>document.querySelector('.markdown-editor textarea')?.value.includes('attachment:00000000000000000000000000000002'))
 await editor.getByRole('button',{name:'预览',exact:true}).click();assert.equal(await editor.locator('.description-body h1').innerText(),'一级标题');assert.equal(await editor.locator('.description-body li').count(),2);assert.equal(await editor.locator('.description-body script').count(),0)
 assert.equal(await editor.getByRole('button',{name:/^查看图片 /}).count(),2)
 assert.equal(await page.evaluate(()=>window.__qaXSS),undefined)
 await dialog.getByRole('button',{name:'关闭并保留',exact:true}).click();await dialog.waitFor({state:'hidden'});await page.getByRole('button',{name:'＋ 新建需求',exact:true}).click();assert((await editor.getByRole('textbox',{name:'需求说明编辑器'}).inputValue()).includes('attachment:00000000000000000000000000000002'));assert.equal(await dialog.locator('label').filter({hasText:/^标题/}).locator('input').inputValue(),'Markdown 多图需求')
 await dialog.getByRole('button',{name:'保存',exact:true}).click();await dialog.waitFor({state:'hidden'});assert.equal(work.description_format,'markdown');assert.equal(uploaded,2)
 await page.getByRole('button',{name:'查看图片 2 · image-2.png',exact:true}).click();const gallery=page.getByRole('dialog',{name:'图片预览',exact:true});await gallery.waitFor();await gallery.getByRole('button',{name:'上一张',exact:true}).click();assert((await gallery.locator('img').getAttribute('src')).endsWith('00000000000000000000000000000001'));assert(await gallery.getByRole('button',{name:'上一张',exact:true}).isDisabled());await gallery.getByRole('button',{name:'下一张',exact:true}).click();assert((await gallery.locator('img').getAttribute('src')).endsWith('00000000000000000000000000000002'));assert(await gallery.getByRole('button',{name:'下一张',exact:true}).isDisabled());await page.getByRole('button',{name:'关闭预览',exact:true}).click()
 await page.getByRole('button',{name:'← 返回列表',exact:true}).click();await page.locator('.tabs').getByRole('button',{name:'变更列表',exact:true}).click();await page.getByRole('button',{name:'＋ 新建变更',exact:true}).click()
 const bulk=page.getByRole('dialog',{name:'批量新建变更',exact:true});await bulk.getByRole('button',{name:'移除',exact:true}).click()
 await bulk.getByRole('button',{name:/^其他操作/}).click();await bulk.getByRole('button',{name:'＋ 添加其他操作',exact:true}).click();await choose(bulk,'应用','api');await bulk.locator('label').filter({hasText:/^操作 \/ 引用/}).locator('textarea').fill('上线后核验')
 await bulk.getByRole('button',{name:/^SQL 变更/}).click();await bulk.getByRole('button',{name:'＋ 添加SQL 变更',exact:true}).click();await choose(bulk,'应用','web');await choose(bulk,'资源','db · mysql');await bulk.locator('label').filter({hasText:/^SQL 脚本/}).locator('textarea').fill("UPDATE menu SET name='新菜单' WHERE id=1;")
 await bulk.getByRole('button',{name:/^Nacos 配置/}).click();await bulk.getByRole('button',{name:'＋ 添加Nacos 配置',exact:true}).click();await choose(bulk,'应用','api');await choose(bulk,'资源','config · nacos');await bulk.locator('label').filter({hasText:/^Data ID/}).locator('input').fill('app.yaml');await bulk.getByPlaceholder('配置项，例如 feature.enabled').fill('feature.menu');await bulk.getByPlaceholder('期望值').fill('true')
 await bulk.getByRole('button',{name:'关闭',exact:true}).click();await bulk.waitFor({state:'hidden'});await page.getByRole('button',{name:'＋ 新建变更',exact:true}).click();await bulk.waitFor();await bulk.getByRole('button',{name:/^SQL 变更/}).click();assert((await bulk.locator('textarea').first().inputValue()).includes('新菜单'))
 await bulk.getByRole('button',{name:'保存全部变更',exact:true}).click();await bulk.waitFor({state:'hidden'});assert.equal(batches,1);assert.equal(batch.changes.length,3);assert.deepEqual(new Set(batch.changes.map(c=>c.kind)),new Set(['manual','mysql','nacos']));assert.equal(batch.changes.find(c=>c.kind==='mysql').service,'web')
 assert.deepEqual(errors,[]);assert.deepEqual(unexpected,[])
 console.log(JSON.stringify({ok:true,checks:['Markdown safe preview','two image uploads and gallery navigation','requirement draft closes and resumes','persisted format','mixed multi-app batch','closed batch draft resumes','one atomic save','no real writes']}))
}catch(e){console.log(await page.getByRole('dialog').allTextContents());await page.screenshot({path:'/tmp/devops-compact-test-failure.png'});throw e}finally{await browser.close()}
