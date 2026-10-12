// Mock all business APIs and mutations. Static files may be loaded from the
// candidate dist; no deployment, resource connection or database write occurs.
import assert from 'node:assert/strict'
import {readFile,mkdir} from 'node:fs/promises'
import path from 'node:path'
const base=process.env.CONSOLE_UI_TEST_URL||'http://127.0.0.1:9800'
assert.equal(new URL(base).hostname,'127.0.0.1')
const dist=process.env.CONSOLE_UI_DIST
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright')
const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH})
const page=await browser.newPage({viewport:{width:1440,height:1000}}),errors=[],writes=[],unexpected=[],listQueries=[]
page.on('pageerror',e=>errors.push(e.message))
page.on('dialog',d=>d.accept())
const tpl={id:1,name:'多应用交付',mode:'version',owner:'alice',services:[],nodes:[{id:'deploy',kind:'deploy',name:'发布应用',service:''},{id:'observe',kind:'manual',name:'上线观察',instructions:'验证关键业务'}]}
const version={id:1,version:'v1',title:'一次发布多个 BUG',status:'ready',owner:'alice',changes:[{id:1,title:'BUG one',kind:'code',service:'orders',reference:'feature/one'},{id:2,title:'BUG two',kind:'code',service:'billing',reference:'feature/two'}]}
let mapping={version_id:1,bindings:[],locked:false},templateList=[tpl],currentRun,ctrl={state:'idle',message:'尚未启动自动推进'},slowResolve=false,releaseResolve
const candidates=name=>[{service:name,artifact_id:name==='orders'?501:502,version:'c123',digest:'sha256:fixture-'+name,branch:'release/v1',build_id:41}]
function makePlan(req){return {template:templateList.find(t=>t.id===req.template_id),environment:{name:req.environment||'test',display_name:'测试',approvers:'reviewer'},version:req.version_id?version:null,reason:req.reason,services:Object.entries(req.artifacts).map(([service,id])=>({service,artifact:{id,version:'c123',digest:'sha256:fixture-'+service},target:{host_group:'test-group',hosts:['qa-host'],batch_size:1,pause_between_batches:true}})),nodes:req.nodes,blockers:[],warnings:['需人工核对代码覆盖'],sql_plans:[],redis_plans:[],nacos_plans:[],preflight_token:'mock-preflight',preflight_expires:Math.floor(Date.now()/1000)+600}}
function makeRun(req){return {id:17,status:'approved',digest:'qa-digest',owner:'alice',created_at:1700000000,plan:makePlan(req),steps:[{node_id:'deploy',kind:'deploy',name:'发布应用 · orders',service:'orders',status:'waiting',release_id:81,started_at:1700000000},{node_id:'deploy',kind:'deploy',name:'发布应用 · billing',service:'billing',status:'pending'},{node_id:'observe',kind:'manual',name:'上线观察',status:'pending'}]}}
const child={id:81,status:'waiting_next_batch',succeeded:1,total:2}
await page.route('**/*',async route=>{
 const r=route.request(),u=new URL(r.url()),method=r.method(),p=u.pathname.replace(/^\/api\/plugins\/[^/]+(?=\/api\/)/,'')
 if(u.origin!==new URL(base).origin)return route.abort()
 const reply=json=>route.fulfill({json})
 if(dist){let file;if(u.pathname==='/')file='shell.html';else if(/\/assets\/[^/]+\.(js|css)$/.test(u.pathname))file='assets/'+path.basename(u.pathname);else if(/\/api\/plugins\/deploy\/ui\/?$/.test(u.pathname))file='deploy.html';if(file)return route.fulfill({body:await readFile(path.join(dist,file)),contentType:file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html'})}
 if(!p.includes('/api/')||!['fetch','xhr'].includes(r.resourceType()))return route.continue()
 if(!['GET','HEAD','OPTIONS'].includes(method)){
  const body=r.postDataJSON();writes.push({method,p,body})
  if(method==='PUT'&&p==='/api/release-versions/1/artifacts'){mapping={version_id:1,bindings:body.bindings.map(b=>({...candidates(b.service)[0],...b})),locked:false};return reply(mapping)}
  if(p==='/api/delivery/preview'){if(slowResolve){slowResolve=false;await new Promise(resolve=>{releaseResolve=resolve})}return reply(makePlan(body))}
  if(p==='/api/delivery/runs'){currentRun=makeRun(body);mapping.locked=true;return reply(currentRun)}
  if(p==='/api/delivery/templates'){const t={...body,id:2,owner:'alice'};templateList.push(t);return reply(t)}
  if(p==='/api/delivery/runs/17/start'){ctrl={state:'paused',message:'等待批次观察确认'};return reply(ctrl)}
  if(p==='/api/delivery/runs/17/approve'){currentRun.status='approved';return reply(currentRun)}
  if(p==='/api/delivery/runs/17/stop'){assert(body.reason);currentRun.status='failed';ctrl={state:'stopped',message:'操作人终止后续编排，保留台账'};return reply(currentRun)}
  if(p==='/api/delivery/runs/17/advance'){currentRun.steps[2].status='success';return reply(currentRun)}
  if(p==='/api/releases/81/continue'){child.status='running';return reply(child)}
  unexpected.push(p);return route.abort()
 }
 if(p==='/api/me')return reply({username:'alice',role:'member'})
 if(p==='/api/services')return reply([{name:'orders',artifact_type:'jar'},{name:'billing',artifact_type:'jar'}])
 if(p==='/api/environments')return reply([{name:'test',display_name:'测试'},{name:'prod',display_name:'生产'}])
 if(p==='/api/release-versions')return reply([version])
 if(p==='/api/release-versions/1/artifacts')return reply(mapping)
 if(p==='/api/changes')return reply(version.changes)
 if(p==='/api/delivery/templates')return reply(templateList)
 if(p==='/api/delivery/runs/owners')return reply(['alice','bob'])
 if(p==='/api/delivery/artifacts')return reply({items:candidates(u.searchParams.get('service')),total:120,page:Number(u.searchParams.get('page')||1),page_size:50})
 if(p==='/api/delivery/runs/page'){listQueries.push(Object.fromEntries(u.searchParams));return reply({items:currentRun?[{id:17,name:tpl.name,environment:'test',environment_name:'测试',version:'v1',owner:'alice',status:currentRun.status,applications:2,completed:0,total_steps:3,current_node:'发布应用',created_at:1700000000}]:[],total:621,page:Number(u.searchParams.get('page')||1),page_size:20})}
 if(p==='/api/delivery/runs/17/control')return reply(ctrl)
 if(p==='/api/delivery/runs/17')return reply(currentRun)
 if(p==='/api/releases/81')return reply(child)
 if(p==='/api/delivery/runs/17/logs')return reply({items:[{id:1,host:'qa-host',status:'running',log:'readiness checking\n<script>not executed</script>'}],total:21,page:Number(u.searchParams.get('page')||1)})
 if(p==='/api/delivery/runs/17/rollback-candidate')return reply({service:'orders',artifact_id:401,version:'old-version',digest:'sha256:old'})
 return reply([])
})
async function choose(label,option){const control=page.locator('.form label').filter({hasText:new RegExp('^'+label)}).first().locator('.el-select');await control.click();const id=await control.getByRole('combobox').getAttribute('aria-controls');await page.locator('#'+id).getByRole('option',{name:option,exact:true}).click()}
async function chooseArtifact(name){const row=page.locator('.artifact-row').filter({hasText:name});await row.locator('.el-select').click();const id=await row.getByRole('combobox').getAttribute('aria-controls');await page.locator('#'+id).getByRole('option').first().click()}
const button=name=>page.getByRole('button',{name,exact:true})
try{
 await page.goto(base+'/api/plugins/deploy/ui#/orchestration')
 await button('发起交付').click();await choose('发布版本','v1 · 一次发布多个 BUG');await choose('目标环境','测试');await page.getByPlaceholder('说明本次交付目的，生产按策略必填').fill('continuous draft')
 await button('下一步：核对制品与步骤').click();await chooseArtifact('orders');await chooseArtifact('billing')
 assert.equal(await page.getByRole('dialog').count(),0,'prepare uses long modal')
 await button('← 返回列表').click();await button('发起交付').click();assert.equal(await page.getByPlaceholder('说明本次交付目的，生产按策略必填').inputValue(),'continuous draft')
 await button('下一步：核对制品与步骤').click();await page.locator('.checkbox input').check();await button('关联版本制品').click()
 assert.equal(mapping.bindings.length,2);assert(mapping.bindings.every(b=>b.code_confirmed));assert.equal(writes.filter(w=>w.p.endsWith('/start')).length,0)
 slowResolve=true;const pending=page.waitForRequest(r=>r.url().endsWith('/delivery/preview'));await button('预检范围、制品与目标').click();await pending
 assert.equal(await page.getByPlaceholder('说明本次交付目的，生产按策略必填').isDisabled(),true)
 releaseResolve();await button('提交执行单，不立即部署').waitFor();await button('提交执行单，不立即部署').click()
 await page.getByRole('heading',{name:'多应用交付 待执行',exact:false}).waitFor()
 assert.equal(await page.getByRole('dialog').count(),0,'detail uses long modal')
 assert.equal(writes.filter(w=>w.p.endsWith('/start')).length,0,'submission starts deployment')
 assert.equal(writes.find(w=>w.p==='/api/delivery/runs').body.preflight_token,'mock-preflight')
 await button('查看发布日志').click();await page.getByRole('complementary',{name:'发布日志'}).waitFor();assert((await page.locator('.log-drawer pre').innerText()).includes('<script>not executed</script>'));await button('关闭').click()
 assert.equal(await page.locator('.node-detail').isVisible(),true,'closing log lost original step')
 await button('开始执行（自动推进）').click();assert.equal(writes.filter(w=>w.p.endsWith('/start')).length,1)
 await page.getByText('等待批次观察确认',{exact:false}).waitFor()
 await button('核对后继续下一批').click();assert.equal(writes.filter(w=>w.p.endsWith('/continue')).length,1)
 currentRun.status='running';ctrl={state:'interrupted',message:'协调器中断，不重放写操作'};currentRun.steps[0]={node_id:'sql',kind:'mysql',name:'SQL uncertain',status:'executing',started_at:1}
 await button('故障与恢复').click();await page.getByPlaceholder('核对结果后填写终止后续编排的原因').waitFor({timeout:6000});await page.getByPlaceholder('核对结果后填写终止后续编排的原因').fill('已核对结果，终止后续')
 await button('终止后续编排，保留台账').click();assert.equal(currentRun.steps[0].status,'executing','uncertain result falsified');assert.equal(writes.filter(w=>w.p.endsWith('/advance')).length,0,'uncertain SQL repeated')
 await button('← 返回列表').click();await button('下一页').click();await page.waitForTimeout(350);assert.equal(listQueries.at(-1).page,'2');assert.equal(listQueries.at(-1).page_size,'20')
 await button('只看我的').click();await page.waitForTimeout(350);assert.equal(listQueries.at(-1).owner,'alice');assert.equal(listQueries.at(-1).page,'1')
 await button('查看 / 处理').click();await button('故障与恢复').click()
 currentRun.status='failed';currentRun.steps[0].status='success';child.status='success';currentRun.steps[1].status='failed'
 // Add successful SQL and failed Redis to prove they cannot be replayed.
 currentRun.steps.push({node_id:'sql',kind:'mysql',name:'SQL completed',status:'success',started_at:1},{node_id:'redis',kind:'redis',name:'Redis failed',status:'failed',started_at:1})
 currentRun.plan.nodes.push({id:'sql',kind:'mysql',name:'SQL completed',change_ids:[99]},{id:'redis',kind:'redis',name:'Redis failed',change_ids:[100]})
 await button('保存剩余范围编排与接续草稿').waitFor({timeout:6000});await button('保存剩余范围编排与接续草稿').click()
 const saved=templateList.at(-1);assert.deepEqual(saved.services,['billing']);assert(!saved.nodes.some(n=>['mysql','redis'].includes(n.kind)),'recovery replays writes');assert(saved.nodes.some(n=>n.kind==='manual'))
 assert.equal(await page.getByRole('dialog').count(),0)
 // Dark and light, full-page controls on narrow screens.
 for(const theme of ['light','dark']){await page.evaluate(t=>{document.documentElement.dataset.theme=t;document.documentElement.classList.toggle('dark',t==='dark')},theme);for(const width of [1440,900,700,390]){await page.setViewportSize({width,height:1000});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false,'horizontal overflow '+theme+' '+width)}}
 await page.setViewportSize({width:1440,height:1000});await mkdir('/tmp/devops-orchestration-ui',{recursive:true});await page.screenshot({path:'/tmp/devops-orchestration-ui/continuous-prepare.png',fullPage:true})
 assert.deepEqual(errors,[]);assert.deepEqual(unexpected,[])
 console.log(JSON.stringify({ok:true,checks:['full-page prepare and detail','draft continuity','version artifact mapping','preflight loading and token','no start on submission','log drawer context','explicit auto start','batch observation','server pagination beyond 100','person filter','remaining-only recovery excludes successful SQL and failed Redis','responsive light/dark','no real writes'],mockedWrites:writes.length}))
}finally{await browser.close()}
