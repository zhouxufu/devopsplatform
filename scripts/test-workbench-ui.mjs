// Intercept all business APIs. Never connect to user MySQL/Redis or write user metadata.
import assert from 'node:assert/strict'
const base=process.env.WORKBENCH_UI_TEST_URL||'http://127.0.0.1:19823'
assert.equal(new URL(base).hostname,'127.0.0.1')
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright')
const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH})
const env={name:'test',display_name:'测试',protected:false,approvers:'reviewer'}
const errors=[],writes=[],checks=[]
try{
for(const kind of ['dbms','redis']){
 const page=await browser.newPage({viewport:{width:1440,height:1000}})
 page.on('pageerror',e=>errors.push(e.message))
 const change={id:910001,kind:kind==='dbms'?'mysql':'redis',title:'Workbench QA',service:'resource-only',status:'ready',owner:'author',risk_level:'medium',rollback_plan:'Manual verification',...(kind==='dbms'?{sql:{resource_alias:'db',content:"UPDATE sys_menu SET name='QA' WHERE id=1",max_affected_rows:10}}:{redis:{resource_alias:'cache',commands:'UNLINK menu:tree'}})}
 const tpl={id:910002,workbench:kind,name:'QA',mode:'fixed',services:[],nodes:[{id:'operation',kind:change.kind,name:'QA',change_ids:[change.id]}]}
 const plan={template:tpl,environment:env,reason:'QA',services:[],nodes:tpl.nodes,blockers:[],warnings:[],...(kind==='dbms'?{sql_plans:[{node_id:'operation',connection:'qa-mysql',database:'qa',schema_digest:'qa',mode:'dml',statements:[{change_id:change.id,sql:change.sql.content,expected_rows:1,max_affected_rows:10}]}]}:{redis_plans:[{node_id:'operation',changes:[{change_id:change.id,service:change.service,alias:'cache',connection:'qa-redis',endpoint:'example.invalid:6379',db:'0',prefix:'menu:',commands:[['UNLINK','menu:tree']]}]}]})}
 let run={id:910003,plan,status:'pending_approval',owner:'author',digest:'qa-digest',created_at:1,steps:[{node_id:'operation',name:'QA',kind:tpl.nodes[0].kind,status:'pending'}]},submission,saved
 await page.route('**/*',async route=>{
  const r=route.request(),u=new URL(r.url()),p=u.pathname.replace(`/api/workbench/${kind}/`,'/api/').replace('/api/resources/','/api/'),method=r.method(),reply=value=>route.fulfill({json:value})
  if(!['127.0.0.1','localhost'].includes(u.hostname))return route.abort()
  if(!['fetch','xhr'].includes(r.resourceType()))return route.continue()
  if(method==='GET'){
   if(p.endsWith('/api/me'))return reply({username:'reviewer',role:'admin',auth_enabled:true})
   if(p.endsWith('/api/services')||p.endsWith('/api/registry/applications'))return reply([{name:'resource-only'}])
   if(p.endsWith('/api/environments'))return reply([env,{...env,name:'prod',display_name:'生产'}])
   if(p.endsWith('/api/changes'))return reply([change])
   if(p.endsWith('/api/delivery/runs'))return reply([run])
   if(p.endsWith('/api/services/resource-only/resources'))return reply({requirements:[{alias:kind==='dbms'?'db':'cache',kind:change.kind}],bindings:[]})
   if(p.endsWith('/api/connections'))return reply([{name:'other-git',kind:'git'},{name:'qa-mysql',kind:'mysql'},{name:'qa-redis',kind:'redis'}])
   return reply([])
  }
  if(method==='POST'&&p.endsWith('/api/delivery/templates')){assert.equal(r.postDataJSON().workbench,kind);assert.deepEqual(r.postDataJSON().services,[]);return reply(tpl)}
  if(method==='POST'&&p.endsWith('/api/delivery/preview'))return reply(plan)
  if(method==='POST'&&p.endsWith('/api/delivery/runs')){submission=r.postDataJSON();return reply(run)}
  if(method==='POST'&&p.endsWith('/approve')){assert.equal(r.postDataJSON().digest,run.digest);run={...run,status:'approved',approver:'reviewer'};return reply(run)}
  if(method==='POST'&&p.endsWith('/draft'))return reply({...change,status:'draft'})
  if(method==='PUT'&&p.endsWith('/api/changes/'+change.id)){saved=r.postDataJSON();return reply({...saved,status:'draft'})}
  if(method==='POST'&&p.endsWith('/api/query')){assert.equal(r.postDataJSON().environment,'test');return reply({columns:['id','name'],rows:[{id:'1',name:'QA <script>not executed</script>'}]})}
  writes.push(method+' '+p);return route.abort()
 })
 const url=base+(process.env.WORKBENCH_GATEWAY==='1'?`/api/plugins/${kind}/ui`:`/ui/${kind}`)
 await page.goto(url)
 await page.getByRole('button',{name:'继续操作',exact:true}).click()
 const choose=async(label,option)=>{const field=page.locator('label').filter({hasText:new RegExp('^'+label)}).first(),control=field.locator('.el-select');await control.click();const id=await control.getByRole('combobox').getAttribute('aria-controls');await page.locator('#'+id).getByRole('option',{name:option,exact:true}).click();await page.locator('h2').click()}
 await choose('本次目标环境','测试')
 await page.getByRole('button',{name:'预检（保存并就绪）',exact:true}).click()
 await page.getByRole('button',{name:'提交审核（不执行）',exact:true}).waitFor()
 await choose('本次目标环境','生产')
 assert.equal(await page.getByRole('button',{name:'提交审核（不执行）',exact:true}).count(),0,'stale target preview retained')
 await choose('本次目标环境','测试')
 await page.getByRole('button',{name:'预检（保存并就绪）',exact:true}).click()
 await page.getByRole('button',{name:'提交审核（不执行）',exact:true}).click()
 const review=page.getByRole('dialog',{name:'审核执行单 #910003',exact:true})
 await review.waitFor();assert.equal(submission.environment,'test');assert(!submission.artifacts&&!submission.version_id)
 assert.equal(await review.getByRole('button',{name:'执行下一步 / 确认结束',exact:true}).count(),0)
 await review.getByRole('button',{name:'批准此快照',exact:true}).click()
 await review.getByRole('button',{name:'执行下一步 / 确认结束',exact:true}).waitFor()
 await review.getByRole('button',{name:'关闭并保留页面',exact:true}).click()
 await page.getByRole('button',{name:'退回草稿修改',exact:true}).click()
 const text=page.locator('.editor textarea').first();await text.fill(kind==='dbms'?"UPDATE sys_menu SET name='draft' WHERE id=1":'UNLINK menu:session')
 await page.locator('.platform-nav').getByRole('button',{name:/资源/}).click()
 const resourcePanel=page.getByRole('dialog',{name:'资源中心',exact:true});await resourcePanel.waitFor()
 await resourcePanel.getByText(kind==='dbms'?'qa-mysql':'qa-redis',{exact:true}).waitFor()
 assert.equal(await resourcePanel.getByText('other-git',{exact:true}).count(),0)
 assert.equal(await resourcePanel.getByRole('button',{name:/构建 Runner/}).count(),0)
 await resourcePanel.getByRole('button',{name:'关闭并返回原页面',exact:true}).click()
 assert.equal(await text.inputValue(),kind==='dbms'?"UPDATE sys_menu SET name='draft' WHERE id=1":'UNLINK menu:session')
 await Promise.all([page.waitForResponse(r=>r.request().method()==='GET'&&r.url().includes('/changes')),page.getByRole('button',{name:'保存草稿',exact:true}).click()])
 assert(!saved.environment);assert.equal(saved.service,'resource-only')
 if(kind==='dbms'){
  await page.getByRole('button',{name:'只读查询',exact:true}).click();await choose('应用','resource-only');await choose('逻辑数据库资源','db')
  await page.getByRole('button',{name:'执行只读查询',exact:true}).click();await page.locator('tbody td').last().waitFor();assert.equal(await page.locator('tbody script').count(),0)
  await page.getByRole('button',{name:'数据对比同步',exact:true}).click();const sync=page.getByRole('dialog',{name:'数据同步任务',exact:true});await sync.waitFor();assert((await sync.innerText()).includes('不绑定环境'));await sync.getByRole('button',{name:'关闭并返回原页面',exact:true}).click();await sync.waitFor({state:'hidden'})
 }
 for(const width of [1440,900,700]){await page.setViewportSize({width,height:1000});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false)}
 checks.push(kind+': scoped resources, preview invalidation, mandatory review, continuous editor, no artifact dependency, responsive')
 if(process.env.WORKBENCH_SCREENSHOT)await page.screenshot({path:process.env.WORKBENCH_SCREENSHOT+'-'+kind+'.png'})
 await page.close()
}
assert.deepEqual(writes,[]);assert.deepEqual(errors,[]);console.log(JSON.stringify({ok:true,checks,noRealWrites:true,noPageErrors:true}))
if(process.env.WORKBENCH_GATEWAY==='1'){
 const page=await browser.newPage({viewport:{width:1440,height:1000}}),shellErrors=[]
 page.on('pageerror',e=>shellErrors.push(e.message))
 await page.route('**/*',async route=>{
  const r=route.request(),u=new URL(r.url()),p=u.pathname,reply=json=>route.fulfill({json})
  if(!['127.0.0.1','localhost'].includes(u.hostname))return route.abort()
  if(!['fetch','xhr'].includes(r.resourceType()))return route.continue()
  if(r.method()!=='GET')return route.abort()
  if(p==='/api/plugins')return route.continue()
  if(p==='/api/me')return reply({username:'reviewer',role:'admin',auth_enabled:true})
  if(p.endsWith('/setup'))return reply({completed:true,environments:[env]})
  if(p.endsWith('/environments'))return reply([env])
  return reply([])
 })
 await page.goto(base+'/')
 for(const name of ['数据库工作台','Redis 工作台']){
  await page.getByRole('button',{name:'打开插件工作区',exact:true}).click()
  await page.locator('.pm').getByRole('button',{name:new RegExp(name)}).click()
  const frame=page.frameLocator(`iframe[src*="/api/plugins/${name==='数据库工作台'?'dbms':'redis'}/ui"]`)
  await frame.getByRole('heading',{name:name==='数据库工作台'?'数据库工作台':'Redis 工作台',exact:true}).waitFor()
  assert.equal(await frame.locator('.layout').evaluate(el=>el.classList.contains('embedded')),true)
  await frame.getByRole('button',{name:'＋ 新建操作',exact:true}).click()
  await frame.locator('.editor label').filter({hasText:/^标题/}).locator('input').fill('Continuous shell draft')
  await frame.locator('.platform-nav').getByRole('button',{name:/资源/}).click()
  const panel=page.getByRole('dialog',{name:'资源中心',exact:true});await panel.waitFor()
  assert.equal(await panel.getByRole('button',{name:/构建 Runner/}).count(),0)
  await panel.getByRole('button',{name:'关闭并返回原页面',exact:true}).click();await panel.waitFor({state:'hidden'})
  assert.equal(await frame.locator('.editor label').filter({hasText:/^标题/}).locator('input').inputValue(),'Continuous shell draft')
 }
 assert.deepEqual(shellErrors,[]);await page.close();console.log(JSON.stringify({shell:true,checks:['plus launcher entries','embedded sidebars','global resources overlay','draft continuity across overlay close']}))
}
}finally{await browser.close()}
