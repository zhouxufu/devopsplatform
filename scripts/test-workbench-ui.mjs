// Mock every business request. No user metadata, Redis, or MySQL is modified.
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import path from 'node:path'
const base=process.env.WORKBENCH_UI_TEST_URL||'http://127.0.0.1:9800'
assert.equal(new URL(base).hostname,'127.0.0.1')
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright')
const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH})
const errors=[],unexpected=[],checks=[]
try {
 for(const kind of ['dbms','redis']) {
  const page=await browser.newPage({viewport:{width:1440,height:960}})
  page.on('pageerror',e=>errors.push(e.message));page.on('dialog',d=>d.accept())
  const env={name:'test',display_name:'测试',protected:false,approvers:'reviewer,second-reviewer'}
  const change={id:910001,kind:kind==='dbms'?'mysql':'redis',title:'Workbench QA',service:'resource-only',status:'ready',owner:'author',rollback_plan:'Manual verification',...(kind==='dbms'?{sql:{resource_alias:'db',content:"UPDATE sys_menu SET name='QA' WHERE id=1",max_affected_rows:10}}:{redis:{resource_alias:'cache',commands:'UNLINK menu:tree'}})}
  const tpl={id:910002,workbench:kind,name:'QA',mode:'fixed',services:[],nodes:[{id:'operation',kind:change.kind,name:'QA',change_ids:[change.id]}]}
  const policies={test:{query_approval:false,change_approval:true},prod:{query_approval:false,change_approval:true}}
  let plan={template:tpl,environment:env,reason:'QA',services:[],nodes:tpl.nodes,blockers:[],warnings:[]},run=null,ticket=null,saved,queries=0,executions=0
  await page.route('**/*',async route=>{
   const r=route.request(),u=new URL(r.url()),p=u.pathname.replace(`/api/workbench/${kind}/`,'/api/'),method=r.method(),reply=json=>route.fulfill({json})
   if(u.origin!==new URL(base).origin)return route.abort()
   if(process.env.WORKBENCH_UI_DIST){let file;if(/\/assets\/[^/]+\.(js|css)$/.test(p))file='assets/'+path.basename(p);else if(p===`/ui/${kind}`||p===`/api/plugins/${kind}/ui`)file='workbench.html';if(file)return route.fulfill({body:await readFile(path.join(process.env.WORKBENCH_UI_DIST,file)),contentType:file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html'})}
   if(!['fetch','xhr'].includes(r.resourceType()))return route.continue()
   if(method==='GET'){
    if(p.endsWith('/api/me'))return reply({username:'reviewer',role:'admin',auth_enabled:true})
    if(p.endsWith('/api/services')||p.endsWith('/api/registry/applications'))return reply([{name:'resource-only'}])
    if(p.endsWith('/api/environments'))return reply([env,{...env,name:'prod',display_name:'生产',protected:true}])
    if(p.endsWith('/api/policy'))return reply(policies[u.searchParams.get('environment')])
    if(p.endsWith('/api/changes'))return reply([change])
    if(p.endsWith('/api/delivery/runs'))return reply(run?[run]:[])
    if(p.endsWith('/api/query-requests'))return reply(ticket?[ticket]:[])
    if(p.endsWith('/api/data-sync/tasks'))return reply([])
    if(p.endsWith('/api/services/resource-only/resources'))return reply({requirements:[{alias:kind==='dbms'?'db':'cache',kind:change.kind}],bindings:[]})
    if(p.endsWith('/api/connections'))return reply([{name:'qa-'+kind,kind:change.kind}])
    return reply([])
   }
   if(method==='PUT'&&p.endsWith('/api/policy')){policies[u.searchParams.get('environment')]=r.postDataJSON();return reply(r.postDataJSON())}
   if(method==='POST'&&p.endsWith('/api/delivery/templates'))return reply(tpl)
   if(method==='POST'&&p.endsWith('/api/delivery/preview')){plan={...plan,environment:{...env,name:r.postDataJSON().environment},workbench_policy:policies[r.postDataJSON().environment]};return reply(plan)}
   if(method==='POST'&&p.endsWith('/api/delivery/runs')){run={id:910003,plan,status:'pending_approval',owner:'author',digest:'qa-digest',created_at:1,steps:[]};return reply(run)}
   if(method==='POST'&&p.endsWith('/approve')){run={...run,status:'approved',approver:'reviewer'};return reply(run)}
   if(method==='POST'&&p.endsWith('/advance')){executions++;return reply({...run,status:'completed'})}
   if(method==='POST'&&p.endsWith('/draft'))return reply({...change,status:'draft'})
   if(method==='PUT'&&p.endsWith('/api/changes/'+change.id)){saved=r.postDataJSON();return reply({...saved,status:'draft'})}
   if(method==='POST'&&p.endsWith('/api/query')){queries++;return reply({columns:['id','value'],rows:[{id:1,value:'QA <script>not executed</script>'}]})}
   if(method==='POST'&&p.endsWith('/api/query-requests')){ticket={id:910004,kind,query:r.postDataJSON(),status:'pending_approval',owner:'reviewer',approvers:{test:env.approvers},created_at:1};return reply(ticket)}
   if(method==='POST'&&p.endsWith('/query-requests/910004/execute')){queries++;ticket={...ticket,status:'completed'};return reply({ticket,result:{columns:['value'],rows:[{value:'approved snapshot'}]}})}
   unexpected.push(method+' '+p);return route.abort()
  })
  await page.goto(base+(process.env.WORKBENCH_GATEWAY==='1'?`/api/plugins/${kind}/ui`:`/ui/${kind}`))
  const nav=page.getByRole('navigation',{name:'工作台导航'})
  const choose=async(label,option)=>{const field=page.locator('label:visible').filter({hasText:new RegExp('^'+label)}).first(),control=field.locator('.el-select');await control.click();const id=await control.getByRole('combobox').getAttribute('aria-controls');await page.locator('#'+id).getByRole('option',{name:option,exact:true}).click();await page.locator('h2').click()}
  await page.getByRole('heading',{name:kind==='dbms'?'SQL 查询':'Redis 查询',exact:true}).waitFor()
  await choose('应用','resource-only');await choose(kind==='dbms'?'逻辑数据库资源':'逻辑 Redis 资源',kind==='dbms'?'db':'cache')
  await page.getByRole('button',{name:'执行只读查询',exact:true}).click();await page.getByText('QA <script>not executed</script>',{exact:true}).waitFor();assert.equal(await page.locator('tbody script').count(),0)
  assert.equal(await page.getByRole('dialog').count(),0)
  await nav.getByRole('button',{name:'变更提交',exact:true}).click()
  await page.getByRole('button',{name:'编辑 / 提交',exact:true}).click()
  assert.equal(await page.getByRole('heading',{name:'可复用变更',exact:true}).count(),0,'list stacked with editor')
  assert.equal(await page.getByRole('dialog').count(),0,'editor opened another window')
  await page.getByRole('button',{name:'预检变更',exact:true}).click();await page.getByRole('button',{name:'提交审核（不执行）',exact:true}).waitFor()
  await choose('目标环境','生产');assert.equal(await page.getByRole('button',{name:'提交审核（不执行）',exact:true}).count(),0,'stale preview')
  await choose('目标环境','测试');await page.getByRole('button',{name:'预检变更',exact:true}).click();await page.getByRole('button',{name:'提交审核（不执行）',exact:true}).click()
  await page.getByRole('heading',{name:'变更执行单 #910003',exact:true}).waitFor();assert.equal(await page.getByRole('dialog').count(),0);assert.equal(executions,0)
  await page.getByRole('button',{name:'批准此快照',exact:true}).click();await page.getByRole('button',{name:'确认执行 / 继续',exact:true}).waitFor();assert.equal(executions,0,'approval auto-executed')
  await page.getByRole('button',{name:'返回变更编辑',exact:true}).click();await page.getByRole('button',{name:'退回草稿修改',exact:true}).click()
  const draft=kind==='dbms'?"UPDATE sys_menu SET name='draft' WHERE id=1":'UNLINK menu:session'
  await page.locator('.editor textarea').first().fill(draft)
  await nav.getByRole('button',{name:'查询',exact:true}).click();await page.getByRole('button',{name:'执行只读查询',exact:true}).click();await page.getByText('QA <script>not executed</script>',{exact:true}).waitFor()
  if(kind==='dbms'){await nav.getByRole('button',{name:'数据对比同步',exact:true}).click();await page.locator('.sync-page h3').waitFor();assert.equal(await page.getByRole('dialog').count(),0);assert((await page.locator('.sync-page').innerText()).includes('不绑定环境'));await page.getByRole('button',{name:'＋ 新建任务',exact:true}).click();await page.locator('.sync-page label').filter({hasText:/^任务名称/}).locator('input').fill('continuous sync draft')}
  await nav.getByRole('button',{name:'变更提交',exact:true}).click();assert.equal(await page.locator('.editor textarea').first().inputValue(),draft)
  await page.locator('.platform-nav').getByRole('button',{name:/资源/}).click();const resourcePanel=page.getByRole('dialog',{name:'资源中心',exact:true});await resourcePanel.waitFor();await resourcePanel.getByRole('button',{name:'关闭并返回原页面',exact:true}).click();assert.equal(await page.locator('.editor textarea').first().inputValue(),draft)
  await Promise.all([page.waitForResponse(r=>r.request().method()==='GET'&&r.url().includes('/policy')),page.getByRole('button',{name:'保存草稿',exact:true}).click()]);assert.equal(saved.service,'resource-only');assert(!saved.environment)
  await nav.getByRole('button',{name:'审批设置',exact:true}).click();const switches=page.locator('.policy-row input[type=checkbox]');assert.equal(await switches.nth(0).isChecked(),false);assert.equal(await switches.nth(1).isChecked(),true)
  await switches.nth(0).check();await switches.nth(1).uncheck();await Promise.all([page.waitForResponse(r=>r.request().method()==='PUT'&&r.url().includes('/policy')),page.getByRole('button',{name:'保存审批设置',exact:true}).click()]);assert.deepEqual(policies.test,{query_approval:true,change_approval:false})
  await nav.getByRole('button',{name:'查询',exact:true}).click();const before=queries;await page.getByRole('button',{name:'提交查询申请',exact:true}).click();await page.getByRole('heading',{name:'查询申请 #910004',exact:true}).waitFor();assert.equal(queries,before);assert.equal(await page.getByRole('button',{name:'批准查询',exact:true}).count(),0,'self approval shown')
  ticket={...ticket,status:'approved',approver:'second-reviewer'};await page.getByRole('button',{name:'刷新',exact:true}).click();await page.getByRole('button',{name:'执行已批准查询',exact:true}).click();await page.getByText('approved snapshot',{exact:true}).waitFor()
  if(kind==='dbms'){await nav.getByRole('button',{name:'数据对比同步',exact:true}).click();assert.equal(await page.locator('.sync-page label').filter({hasText:/^任务名称/}).locator('input').inputValue(),'continuous sync draft')}
  for(const width of [1440,900,700,390]){await page.setViewportSize({width,height:960});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false,'page overflow')}
  if(process.env.WORKBENCH_SCREENSHOT){await page.setViewportSize({width:1440,height:960});await nav.getByRole('button',{name:'查询',exact:true}).click();await page.screenshot({path:process.env.WORKBENCH_SCREENSHOT+'-'+kind+'.png'})}
  checks.push(kind+': full-page query/change/review, independent policies, no auto-execution, draft continuity, responsive')
  await page.close()
 }
 assert.deepEqual(unexpected,[]);assert.deepEqual(errors,[]);console.log(JSON.stringify({ok:true,checks,noRealWrites:true,noBrowserErrors:true}))
}finally{await browser.close()}
