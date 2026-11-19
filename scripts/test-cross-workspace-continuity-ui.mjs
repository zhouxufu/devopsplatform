// Shell + real candidate iframes; intercept every business write.
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import path from 'node:path'
const base='http://127.0.0.1:9800',dist=process.env.CONSOLE_UI_DIST
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright')
const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH})
const page=await browser.newPage({viewport:{width:1440,height:960}}),errors=[],unexpected=[],saves=[]
page.on('pageerror',e=>errors.push(e.message));page.on('dialog',d=>d.accept())
const item={id:941001,title:'跨插件连续交付',kind:'requirement',status:'open',owner:'alice'}
const existing={id:941002,kind:'mysql',title:'已有 SQL',service:'api',owner:'alice',status:'draft',reference:'',summary:'',risk_level:'medium',rollback_plan:'',sql:{resource_alias:'db',content:'UPDATE menu SET name=\'original\' WHERE id=1;',max_affected_rows:10}}
const task={id:941003,name:'菜单比对',service:'api',resource_alias:'db',source_sql:'SELECT id,name FROM menu WHERE id>0',target_sql:'SELECT id,name FROM menu WHERE id>0',keys:['id'],revision:1,owner:'alice'}
let changes=[existing],digest='original-digest',freezeCount=0
await page.route('**/*',async route=>{
 const req=route.request(),url=new URL(req.url()),raw=url.pathname,p=raw.replace('/api/workbench/dbms/','/api/'),method=req.method(),reply=json=>route.fulfill({json})
 if(url.origin!==base)return route.abort()
 let file;if(raw==='/')file='shell.html';else if(/\/assets\/[^/]+\.(js|css)$/.test(raw))file='assets/'+path.basename(raw);else if(raw==='/api/plugins/deploy/ui')file='deploy.html';else if(raw==='/api/plugins/dbms/ui')file='workbench.html'
 if(file&&dist)return route.fulfill({body:await readFile(path.join(dist,file)),contentType:file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html'})
 if(!['fetch','xhr'].includes(req.resourceType()))return route.continue()
 if(method==='GET'){
  if(p==='/api/plugins')return reply([{name:'deploy',display:'部署平台',url:'/api/plugins/deploy/ui'},{name:'dbms',display:'数据库工作台',url:'/api/plugins/dbms/ui'}])
  if(p.endsWith('/api/me'))return reply({username:'alice',role:'admin',auth_enabled:true})
  if(p.endsWith('/api/services')||p.endsWith('/api/registry/applications'))return reply([{name:'api',artifact_type:'jar'}])
  if(p.endsWith('/api/environments'))return reply([{name:'dev',display_name:'开发'},{name:'test',display_name:'测试'}])
  if(p.endsWith('/api/setup'))return reply({initialized:true,environments:[{name:'dev'},{name:'test'}]})
  if(p.endsWith('/api/work-items/page'))return reply({items:[item],total:1,page:1,page_size:20})
  if(p.endsWith('/api/work-items'))return reply([item])
  if(p.endsWith('/api/work-items/'+item.id))return reply({...item,changes})
  if(p.endsWith('/api/work-items/'+item.id+'/delivery-content'))return reply({work_item:item,changes,digest})
  if(p.endsWith('/api/services/api/resources'))return reply({requirements:[{alias:'db',kind:'mysql'}],bindings:[]})
  if(p.endsWith('/api/data-sync/tasks'))return reply([task])
  if(p.endsWith('/api/policy'))return reply({query_approval:false,change_approval:true})
  if(p.endsWith('/api/changes'))return reply(changes)
  return reply([])
 }
 if(method==='POST'&&p.endsWith('/api/data-sync/tasks/'+task.id+'/compare'))return reply({task,...req.postDataJSON(),digest:'compare-digest',columns:['id','name'],differences:[{key:'["1"]',kind:'update',before:{id:'1',name:'old'},after:{id:'1',name:'new'}}],unchanged:0,target_only:0})
 if(method==='POST'&&p.endsWith('/api/data-sync/tasks/'+task.id+'/freeze')){
  const body=req.postDataJSON();assert.equal(body.work_item_id,item.id);assert.equal(body.work_digest,digest);freezeCount++
  const generated={...existing,id:941003+freezeCount,title:'同步草稿 '+freezeCount,sql:{resource_alias:'db',content:'UPDATE menu SET name=\'new\' WHERE id=1;',max_affected_rows:1,data_sync:{task_id:task.id,source_environment:'dev',target_environment:'test'}}}
  changes=[...changes,generated];digest='after-sync-digest-'+freezeCount;return reply(generated)
 }
 if(method==='PUT'&&p.endsWith('/api/work-items/'+item.id+'/delivery-content')){
  const body=req.postDataJSON();assert.equal(body.digest,digest,'stale digest sent after generating sync draft');saves.push(body);const retained=new Map(changes.map(c=>[c.id,c]));for(const c of body.changes)retained.set(c.id,{...c,owner:'alice',status:'draft'});changes=[...retained.values()];return reply({work_item:item,changes,digest})
 }
 unexpected.push(method+' '+p);return route.abort()
})
const choose=async(surface,label,option)=>{const select=surface.locator('label').filter({hasText:new RegExp('^'+label)}).locator('.el-select');await select.click();const popup=await select.getByRole('combobox').getAttribute('aria-controls');await surface.locator('#'+popup).getByRole('option',{name:option,exact:true}).click()}
try{
 await page.goto(base)
 const deploy=page.frameLocator('iframe[src*="/api/plugins/deploy/ui"]')
 await deploy.getByRole('link',{name:'变更',exact:true}).click();await deploy.getByRole('button',{name:item.title,exact:true}).click()
 await deploy.getByRole('button',{name:'维护交付内容',exact:true}).click()
 const editor=deploy.getByRole('dialog',{name:'交付内容 · '+item.title,exact:true})
 await editor.getByRole('button',{name:/^SQL 变更/}).click()
 const sql=editor.locator('label').filter({hasText:/^SQL 脚本/}).locator('textarea')
 const draft="UPDATE menu SET name='kept-draft' WHERE id=1;";await sql.fill(draft)
 const sourceURL=await deploy.locator('html').evaluate(()=>location.href)
 await editor.getByRole('button',{name:'引用同步任务',exact:true}).click()
 const db=page.frameLocator('iframe[src*="/api/plugins/dbms/ui"]'),sync=db.locator('.sync-page')
 await sync.getByRole('heading',{name:'数据对比同步',exact:true}).waitFor();assert.equal(await db.getByRole('dialog').count(),0)
 assert((await db.locator('.association-note').innerText()).includes(String(item.id)))
 const taskSelect=sync.locator('.toolbar').first().locator('.el-select');await taskSelect.click();const taskPopup=await taskSelect.getByRole('combobox').getAttribute('aria-controls');await db.locator('#'+taskPopup).getByRole('option',{name:task.name+' · api',exact:true}).click()
 await choose(db,'本次来源环境','开发');await choose(db,'本次目标环境','测试');await sync.getByRole('button',{name:'对比数据（只读）',exact:true}).click();await sync.locator('.diff-title input').check()
 await sync.getByRole('button',{name:/^生成同步草稿/}).click();await sync.locator('.result').waitFor();assert.equal(freezeCount,1)
 await page.locator('.wstab').filter({hasText:'部署平台'}).click();await editor.waitFor();assert.equal(await deploy.locator('html').evaluate(()=>location.href),sourceURL)
 assert.equal(await sql.first().inputValue(),draft,'switching plugins discarded SQL draft')
 await editor.getByRole('button',{name:'保存交付草稿',exact:true}).click()
 await editor.getByText('已保存 · 2 项',{exact:true}).waitFor();assert.equal(saves.length,1);assert.equal(saves[0].changes.find(c=>c.id===existing.id).sql.content,draft);assert(changes.some(c=>c.id===941004),'new sync draft lost on save');assert(!saves[0].changes.some(c=>c.id===941004),'frozen sync draft was rewritten')
 // Also cover an editor closed with a retained draft while another workspace changes its contents.
 const resumed="UPDATE menu SET name='closed-draft' WHERE id=1;";await sql.first().fill(resumed);await editor.getByRole('button',{name:'关闭',exact:true}).click();await editor.waitFor({state:'hidden'})
 await page.locator('.wstab').filter({hasText:'数据库工作台'}).click();await sync.getByRole('button',{name:'对比数据（只读）',exact:true}).click();await sync.locator('.diff-title input').check();await sync.getByRole('button',{name:/^生成同步草稿/}).click();await sync.getByText('已生成同步草稿 #941005 · dev → test',{exact:true}).waitFor()
 await page.locator('.wstab').filter({hasText:'部署平台'}).click();await deploy.getByRole('button',{name:'维护交付内容',exact:true}).click();await editor.waitFor();await editor.getByRole('button',{name:/^SQL 变更 · 3$/}).click();assert.equal(await sql.first().inputValue(),resumed)
 await editor.getByRole('button',{name:'保存交付草稿',exact:true}).click();await editor.getByText('已保存 · 3 项',{exact:true}).waitFor();assert.equal(saves.length,2);assert.equal(saves[1].changes.find(c=>c.id===existing.id).sql.content,resumed);assert(changes.some(c=>c.id===941005));assert.equal(freezeCount,2)
 assert.deepEqual(errors,[]);assert.deepEqual(unexpected,[])
 console.log(JSON.stringify({ok:true,checks:['canonical DBMS sync entry','source iframe/page retained','work item association preserved','generated draft not executed','new content merged without overwriting SQL input','closed draft merges later external additions','latest digest used','no actual writes']}))
}catch(e){await page.screenshot({path:'/tmp/devops-cross-workspace-failure.png'});throw e}finally{await browser.close()}
