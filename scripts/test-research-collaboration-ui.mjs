// Fixture-only collaboration QA: intercept every API, never write to real systems.
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import path from 'node:path'
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright')
const base='http://127.0.0.1:9800',dist=path.resolve('web/dist')
const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH})
const page=await browser.newPage({viewport:{width:1440,height:960}}),errors=[],unexpected=[],writes=[]
page.on('pageerror',e=>errors.push(e.message));page.on('dialog',d=>d.accept())
await page.addInitScript(()=>{window.__opened=[];window.open=u=>{window.__opened.push(u);return null}})
const work={id:7,title:'菜单问题',owner:'alice',resolved_by:'alice',kind:'bug',source:'manual',status:'closed',release_required:true}
const change={id:9,title:'菜单字段修复',kind:'mysql',service:'menus',risk_level:'high',rollback_plan:'还原菜单字段',sql:{resource_alias:'db',content:'UPDATE menu SET enabled=1 WHERE id=7',max_affected_rows:1}}
const run={id:11,owner:'requester',digest:'frozen',status:'waiting_confirmation',plan:{template:{name:'测试交付',workbench:'research'},environment:{name:'test',display_name:'测试'},scope:{work_items:[work],changes:[change]},nodes:[{id:'confirm',name:'研发确认',kind:'manual',approvers:['alice'],approval_mode:'all',assigned_work_items:{alice:[7]}},{id:'qa',name:'测试审批',kind:'approval',approvers:['qa'],approval_mode:'any'}],services:[{service:'menus',artifact:{id:23,version:'abc123',digest:'sha256:frozen'},target:{environment:'test',target_type:'ssh-jar',hosts:['host-a'],batch_size:1,health_url:'http://localhost:8888/health'}}],sql_plans:[{node_id:'sql',connection:'test-mysql',database:'app',schema_digest:'schema-frozen',mode:'dml',statements:[{change_id:9,sql:change.sql.content,action:'update',expected_rows:1,max_affected_rows:1}]}],nacos_plans:[{node_id:'config',configs:[{change_id:10,service:'menus',alias:'config',data_id:'menus.yaml',connection:'test-config',namespace:'test',group:'DEFAULT_GROUP',format:'yaml',before_md5:'before',after_md5:'after',diff:[{key:'feature.menu',before:'false',after:'true'}]}]}]},steps:[{node_id:'confirm',name:'研发确认',kind:'manual',status:'pending',reviews:[]},{node_id:'qa',name:'测试审批',kind:'approval',status:'pending',reviews:[]}]}
const task={id:'11:confirm',run_id:11,node_id:'confirm',title:'研发确认',owner:'requester',kind:'manual',status:'pending',digest:'frozen',at:1791856800,stage:'研发确认',assignees:['alice'],services:['menus'],next_action:'确认当前交付内容',environment:'测试'}
const failed={...structuredClone(run),id:12,owner:'alice',status:'failed',plan:{...structuredClone(run.plan),template:{name:'生产菜单交付'},environment:{name:'prod',display_name:'生产'}},steps:[{node_id:'deploy',name:'发布菜单',kind:'deploy',status:'failed',detail:'Readiness check timed out'}]}
const failure={id:'delivery:12',run_id:12,source_kind:'delivery',kind:'exception',title:'处理异常 · 生产菜单交付',owner:'alice',status:'failed',read_only:true,stage:'发布菜单',assignees:['alice'],blocked_reason:'Readiness check timed out',next_action:'核对执行台账后恢复，不重放',resolution_digest:'exception-frozen',at:1791856800,environment:'生产'}
const prep={...structuredClone(run),id:13,owner:'alice',status:'success',plan:{...structuredClone(run.plan),template:{name:'前置确认',workbench:'research'}},steps:run.steps.map(s=>({...s,status:'success'}))}
const prepSummary={id:'delivery:13',source_kind:'delivery',run_id:13,title:'前置确认',status:'success',owner:'alice',at:1791856800,stage:'前置流程完成（尚未发布）',assignees:['alice'],next_action:'发起人继续发布交付',read_only:true}
let closed=false
const summary=()=>({...task,status:run.status,stage:run.status==='pending_approval'?'测试审批':'研发确认',assignees:run.status==='pending_approval'?['qa']:['alice'],next_action:run.status==='pending_approval'?'等待测试审批；通过后进入下一阶段':'确认本次内容'})
await page.route('**/*',async route=>{
 const r=route.request(),u=new URL(r.url()),p=u.pathname.replace('/api/plugins/research',''),method=r.method(),reply=json=>route.fulfill({json})
 if(u.origin!==base)return route.abort()
 let file;if(u.pathname==='/api/plugins/research/ui')file='research.html';else if(/\/assets\/[^/]+\.(js|css)$/.test(u.pathname))file='assets/'+path.basename(u.pathname)
 if(file)return route.fulfill({body:await readFile(path.join(dist,file)),contentType:file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html'})
 if(!['fetch','xhr'].includes(r.resourceType()))return route.abort()
 if(method==='GET'){
  if(p==='/api/me')return reply({username:'alice',role:'admin',auth_enabled:true})
  if(p==='/api/users')return reply([{username:'alice',enabled:true},{username:'qa',enabled:true}])
  if(p==='/api/plugins')return reply([{name:'research'},{name:'deploy'}])
  if(p==='/api/research/tasks'){const items=u.searchParams.get('view')==='done'?[{...task,read_only:true},...(closed?[{...failure,resolution_digest:'',next_action:'已核对发布日志，恢复单 #123'}]:[])]:[...(run.status==='waiting_confirmation'?[task]:[]),...(!closed?[failure]:[])];return reply({items:items.filter(t=>!u.searchParams.get('kind')||t.kind===u.searchParams.get('kind')),total:items.length,page:1})}
  if(p==='/api/research/deliveries')return reply({items:[prepSummary,failure],total:2,page:1})
  if(p==='/api/research/deliveries/delivery/11')return reply({run,summary:summary(),releases:[]})
  if(p==='/api/research/deliveries/delivery/12')return reply({run:failed,summary:failure,releases:[]})
  if(p==='/api/research/deliveries/delivery/13')return reply({run:prep,summary:prepSummary,releases:[]})
  if(p==='/api/research/flows/runs/13')return reply(prep)
  if(p==='/api/research/flows')return reply([])
  if(p==='/api/research/flows/runs')return reply([prep])
  if(p==='/api/services'||p==='/api/release-versions')return reply([])
  if(p==='/api/environments')return reply([{name:'test',display_name:'测试'}])
 }
 if(method==='POST'){
  const body=r.postDataJSON();writes.push({p,body})
  if(p==='/api/research/tasks/11/confirm'){assert.deepEqual(body.work_item_ids,[7]);assert.equal(body.comment,'验证完成，交给测试');run.steps[0].status='success';run.steps[0].reviews=[{actor:'alice',approved:true,work_item_ids:[7],comment:body.comment,at:1791857000}];run.status='pending_approval';return reply(run)}
  if(p==='/api/research/exceptions/resolve'){assert.equal(body.id,'delivery:12');assert.equal(body.digest,'exception-frozen');assert.match(body.comment,/#123/);closed=true;return reply({ok:true})}
 }
 unexpected.push(method+' '+p);return route.fulfill({status:404,json:{error:'unexpected fixture route'}})
})
try{
 await page.goto(base+'/api/plugins/research/ui')
 for(const name of ['待我处理','我发起的','我的工作项','处理记录'])await page.getByRole('button',{name,exact:true}).waitFor()
 await page.getByPlaceholder('搜索任务、应用、版本或发起人').fill('menus')
 await page.getByRole('button',{name:'搜索',exact:true}).click()
 await page.getByRole('button',{name:'研发确认',exact:true}).click()
 await page.getByPlaceholder('拒绝时须填写原因').fill('验证完成，交给测试')
 await page.getByRole('button',{name:'SQL / 数据同步',exact:true}).click()
 await page.getByText(change.sql.content,{exact:true}).waitFor()
 await page.getByRole('button',{name:'应用与制品',exact:true}).click()
 await page.locator('td').filter({hasText:'制品 #23 · abc123'}).first().waitFor()
 await page.getByRole('button',{name:'配置 / Redis',exact:true}).click()
 await page.getByText('feature.menu',{exact:true}).waitFor()
 await page.getByRole('button',{name:'进度与交接',exact:true}).click()
 await page.getByRole('button',{name:'交付内容',exact:true}).click()
 assert.equal(await page.getByPlaceholder('拒绝时须填写原因').inputValue(),'验证完成，交给测试')
 await page.getByRole('button',{name:'核对最新状态',exact:true}).click()
 assert.equal(await page.getByPlaceholder('拒绝时须填写原因').inputValue(),'验证完成，交给测试')
 await page.getByRole('button',{name:'确认本次交付内容',exact:true}).click()
 await page.locator('.r-receipt').filter({hasText:'本次处理后交给 qa'}).waitFor()
 assert.equal(await page.getByRole('button',{name:'确认本次交付内容',exact:true}).count(),0,'completed actor could sign again')
 assert.equal(await page.locator('.el-dialog:visible').count(),0,'long modal added')
 await page.getByRole('button',{name:'← 返回原列表',exact:true}).click()
 assert.equal(await page.getByPlaceholder('搜索任务、应用、版本或发起人').inputValue(),'menus')
 await page.getByRole('button',{name:'我发起的',exact:true}).click()
 await page.getByText('前置完成 · 未发布',{exact:false}).waitFor()
 await page.getByRole('button',{name:'前置确认',exact:true}).click()
 await page.getByRole('button',{name:'继续当前交付流程',exact:true}).click()
 await page.getByRole('button',{name:'← 返回个人工作台',exact:true}).click()
 await page.getByRole('button',{name:'← 返回原列表',exact:true}).click()
 await page.getByRole('button',{name:'待我处理',exact:true}).click()
 await page.getByRole('button',{name:'异常待处理',exact:true}).click()
 await page.getByRole('button',{name:'处理异常 · 生产菜单交付',exact:true}).click()
 await page.getByRole('button',{name:'进入原交付处理',exact:true}).click()
 assert.ok((await page.evaluate(()=>window.__opened)).some(u=>u.endsWith('#/orchestration?run=12')),'lost exact original run')
 await page.getByPlaceholder(/填写已核对的日志/).fill('已核对发布日志并创建恢复单 #123')
 await page.getByRole('button',{name:'记录异常处理结果',exact:true}).click()
 await page.locator('.r-receipt').filter({hasText:'原交付失败状态保留'}).waitFor()
 assert.equal(failed.status,'failed','review falsified success')
 await page.getByRole('button',{name:'← 返回原列表',exact:true}).click()
 await page.getByRole('button',{name:'处理记录',exact:true}).click()
 await page.locator('td').filter({hasText:'已核对发布日志，恢复单 #123'}).first().waitFor()
 for(const width of [1440,1024,800]){await page.setViewportSize({width,height:960});assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),'page-level horizontal overflow')}
 await page.setViewportSize({width:1440,height:960});await page.screenshot({path:'/tmp/research-collaboration-qa.png',fullPage:true})
 assert.equal(writes.length,2);assert.deepEqual(unexpected,[]);assert.deepEqual(errors,[])
 console.log('PASS personal tracking, handoff, frozen review content, exception closure, exact-run recovery, opinion/filter continuity, responsive layout; fixture-only writes',writes.length)
}finally{await browser.close()}
