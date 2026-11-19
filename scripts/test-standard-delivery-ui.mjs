// Fixture-only API interception: no Git, deployment or business resource writes.
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import path from 'node:path'
const base='http://127.0.0.1:9800',dist=process.env.CONSOLE_UI_DIST
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright')
const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH})
const page=await browser.newPage({viewport:{width:1440,height:960}}),errors=[],writes=[],unexpected=[]
page.on('pageerror',e=>errors.push(e.message));page.on('dialog',d=>d.accept())
await page.addInitScript(()=>sessionStorage.setItem('lp-deploy-environment','test'))
const work={id:7,title:'标准交付测试',kind:'bug',status:'closed'},changes=[{id:1,kind:'code',title:'补丁一',service:'orders',status:'ready',reference:'feature/a'},{id:2,kind:'code',title:'补丁二',service:'orders',status:'ready',reference:'feature/b'},{id:3,kind:'manual',title:'业务验证',status:'ready',reference:'验证菜单'}]
const nodes=[{id:'env-review-1',kind:'approval',name:'开发负责人审核',approvers:['reviewer'],approval_mode:'any'},{id:'std-deploy',kind:'deploy',name:'发布 orders',service:'orders',change_ids:[1,2]},{id:'std-manual',kind:'manual',name:'业务验证',instructions:'验证菜单',change_ids:[3]}]
let run,draftInput,environment={name:'test',display_name:'测试',approvers:''}
const secondWork={...work,id:8,title:'第二个 Bug'}
await page.route('**/*',async route=>{
 const r=route.request(),u=new URL(r.url()),p=u.pathname.replace('/api/plugins/deploy',''),method=r.method(),reply=json=>route.fulfill({json})
 if(u.origin!==base)return route.abort()
 let file;if(u.pathname==='/api/plugins/deploy/ui')file='deploy.html';else if(/\/assets\/[^/]+\.(js|css)$/.test(u.pathname))file='assets/'+path.basename(u.pathname)
 if(file&&dist)return route.fulfill({body:await readFile(path.join(dist,file)),contentType:file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html'})
 if(!['fetch','xhr'].includes(r.resourceType()))return route.continue()
 if(method==='GET'){
  if(p==='/api/me')return reply({username:'alice',role:'admin',auth_enabled:true})
  if(p==='/api/users')return reply([{username:'reviewer',display_name:'审核人',enabled:true}])
  if(p==='/api/delivery/templates')return reply([])
  if(p==='/api/services')return reply([{name:'orders'}])
  if(p==='/api/environments'||p==='/api/resources/environments')return reply([environment])
  if(p==='/api/resources/environments/test/delivery-policy')return reply({revision:0,stages:[]})
  if(p==='/api/work-items')return reply([work,secondWork])
  if(p==='/api/work-items/page')return reply({items:[work],total:1,page:1})
  if(p==='/api/work-items/people')return reply(['alice'])
  if(p==='/api/changes')return reply(changes)
  if(p==='/api/delivery/artifacts')return reply({items:[{artifact_id:123,service:'orders',version:'release123',branch:'release/fix',digest:'sha256:fixture',build_id:8}],total:1,page:1})
  if(p==='/api/delivery/runs/page')return reply({items:[],total:0,page:1,page_size:20})
  if(p==='/api/delivery/runs/11')return reply(run)
  if(p==='/api/delivery/runs/11/control')return reply({state:'idle'})
  return reply([])
 }
 const body=r.postDataJSON();writes.push({p,body})
 if(p==='/api/resources/environments'){environment=body;return reply(body)}
 if(p==='/api/delivery/draft-plan'){
  assert.equal(body.strategy_kind,'standard');assert.equal(body.template_id,0)
  draftInput=body;const included=body.selected_change_ids||changes.map(c=>c.id),scope={type:'work_items',work_items:[work,secondWork].filter(w=>body.work_item_ids.includes(w.id)),changes:changes.filter(c=>included.includes(c.id)),excluded_change_ids:changes.filter(c=>!included.includes(c.id)).map(c=>c.id),digest:'fixture-scope'}
  return reply({strategy_kind:'standard',strategy_version:1,template:{id:0,name:'标准交付',mode:'version',nodes:[]},scope,nodes:nodes.filter(n=>n.kind==='approval'||n.change_ids.some(id=>included.includes(id))).map(n=>({...n,...(n.kind==='approval'?{approvers:environment.approvers?['reviewer']:[]}:{}),...(n.change_ids?{change_ids:n.change_ids.filter(id=>included.includes(id))}:{})})),blockers:environment.approvers?[]:['目标环境未配置审核人员，请在环境审核设置中配置'],warnings:[]})
 }
 if(p==='/api/delivery/templates')return route.fulfill({status:500,json:{error:'模拟模板保存失败'}})
 if(p==='/api/delivery/preview')return reply({scope:{type:'work_items',work_items:[work],changes},template:{id:0,name:'标准交付'},environment:{name:'test',display_name:'测试'},services:[{service:'orders',artifact:{id:123,version:'release123'},target:{host_group:'fixture',batch_size:1,hosts:['qa']}}],nodes:body.nodes,blockers:[],warnings:[],preflight_token:'fixture-token',preflight_expires:Math.floor(Date.now()/1000)+600})
 if(p==='/api/delivery/runs'){
  assert.equal(body.strategy_kind,'standard');assert.equal(body.template_id,0);assert.equal(body.artifacts.orders,123);assert.equal(body.code_confirmed,true);assert.equal(body.order_confirmed,true)
  run={id:11,digest:'fixture-run',owner:'alice',status:'pending_approval',plan:{template:{name:'标准交付'},scope:{type:'work_items',work_items:[work],changes},environment:{name:'test',display_name:'测试'},nodes:body.nodes,services:[]},steps:body.nodes.map(n=>({node_id:n.id,kind:n.kind,name:n.name,status:'pending'}))};return reply(run)
 }
 unexpected.push(method+' '+p);return route.abort()
})
const button=name=>page.getByRole('button',{name,exact:true})
try{
 await page.goto(base+'/api/plugins/deploy/ui#/orchestration?work_items=7,8')
 await page.locator('.scope-table tbody tr').first().waitFor()
 assert.equal(await page.locator('.scope-table tbody tr').count(),3)
 assert(!await page.getByText('＋ 新建编排，保存后继续',{exact:true}).count())
 await button('搜索并调整范围').click();await page.getByRole('dialog').waitFor();await page.getByRole('textbox',{name:'搜索交付来源'}).fill('标准');await page.getByRole('dialog').getByRole('button',{name:'搜索',exact:true}).click();await page.getByRole('checkbox',{name:'选择 标准交付测试'}).waitFor();await button('取消，保留原范围').click()
 assert.deepEqual(draftInput.work_item_ids,[7,8])
 await button('下一步：制品与步骤').click();assert.equal(await page.locator('.artifact-row').count(),1)
 await button('环境审核设置').click();const settings=page.getByRole('dialog',{name:'编辑环境',exact:true});await settings.waitFor()
 const purpose=settings.locator('.fld').filter({hasText:'环境用途'}).locator('.el-select');await purpose.click();await page.getByRole('option',{name:'测试 / 验收',exact:true}).click()
 const approvers=settings.locator('.fld').filter({hasText:'发布 / 操作审核人员'}).locator('.el-select');await approvers.click();const selector=await approvers.getByRole('combobox').getAttribute('aria-controls');await page.locator('#'+selector).getByRole('option',{name:'审核人 · reviewer',exact:true}).click();await settings.getByRole('button',{name:'保存环境与策略',exact:true}).click();await settings.waitFor({state:'hidden'})
 await page.waitForFunction(()=>document.querySelector('.step-section')?.textContent.includes('环境审核设置')&&!document.querySelector('.step-section [role=alert]'))
 assert.deepEqual(draftInput.work_item_ids,[7,8]);assert.equal(environment.approvers,'reviewer')
 assert.equal(environment.purpose,'test')
 const approval=page.locator('.step-section .node-edit').first();await approval.locator('summary').click();assert((await approval.innerText()).includes('审核人'));assert.equal(await approval.locator('.node-fields').nth(1).locator('.el-select').first().getByRole('combobox').isDisabled(),true);await approval.locator('summary').click()

 await page.locator('.artifact-row .el-select').first().click();await page.getByRole('option',{name:/release123/}).click()
 await page.getByRole('checkbox',{name:/我已确认制品覆盖所选工作项/}).check()
 await page.getByRole('checkbox',{name:/我已核对跨类型步骤/}).check()
 const firstNode=page.locator('.step-section .node-edit').first();await firstNode.locator('summary').click();assert.equal(await firstNode.getByRole('button',{name:'删除',exact:true}).isDisabled(),true)
 await page.locator('.form details').filter({has:page.locator('summary').filter({hasText:'高级设置'})}).locator('summary').click();await button('保存本次步骤为模板').click()
 await page.locator('form .el-input input').first().fill('保留交付草稿');await button('保存并继续交付').click();await page.getByText('模拟模板保存失败').waitFor();await button('返回，不保存').click()
 assert.equal(await page.locator('.artifact-row .el-select').first().innerText().then(v=>v.includes('release123')),true)
 for(const width of [1440,900,700,390]){await page.setViewportSize({width,height:960});await page.screenshot({path:'/tmp/devops-standard-delivery-'+width+'.png'});const size=await page.evaluate(()=>({scroll:document.documentElement.scrollWidth,width:innerWidth}));assert(size.scroll<=size.width+1,'page overflows at '+width)}
 await page.setViewportSize({width:1440,height:960});await button('预检范围、制品与目标').click();await button('提交执行单，不立即执行').waitFor();await button('提交执行单，不立即执行').click();await page.waitForURL(/run=11/)
 assert.equal(writes.filter(v=>v.p.endsWith('/start')||v.p.endsWith('/advance')).length,0)
 assert.deepEqual(errors,[]);assert.deepEqual(unexpected,[])
 console.log(JSON.stringify({ok:true,checks:['missing environment reviewers shown','inline environment settings save','draft scope preserved','policy reviewers refreshed and locked','zero templates standard delivery','search picker cancellation','one app artifact for two Bugs','mandatory reviews locked','template failure preserves draft','four widths','submit without execution']}))
}catch(e){await page.screenshot({path:'/tmp/devops-standard-delivery-failure.png'});console.error({errors,unexpected});throw e}finally{await browser.close()}
