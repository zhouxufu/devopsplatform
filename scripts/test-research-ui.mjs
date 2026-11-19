// Fixture-only: every business API is intercepted; no source/deployment writes.
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import path from 'node:path'
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright')
const base='http://127.0.0.1:9800',dist=process.env.CONSOLE_UI_DIST||path.resolve('web/dist')
const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH})
const page=await browser.newPage({viewport:{width:1440,height:980}}),errors=[],writes=[],unexpected=[]
page.on('pageerror',e=>errors.push(e.message));page.on('console',m=>{if(m.type()==='error')errors.push(m.text())});page.on('dialog',d=>d.accept())
const work={id:7,source:'zentao:issues',external_id:'26087',kind:'bug',title:'菜单修复',status:'open',external_status:'active',assigned_to:'alice',resolved_by:'alice',owner:'sync',description:'<p>源描述</p>',release_required:null,metadata_revision:0,updated_at:1791856800,url:'http://source.invalid/zentao/index.php?m=bug&f=view&bugID=26087'}
let pending=[]
const other={...work,id:8,title:'权限修复',external_id:'26088'}
const bug={id:26087,title:work.title,status:'active',steps:work.description,assignedTo:'alice',resolvedBy:''}
const run={id:11,owner:'owner',digest:'frozen',status:'waiting_confirmation',plan:{template:{id:1,name:'版本准备',workbench:'research'},environment:{name:'dev',display_name:'开发'},scope:{work_items:[work,other],changes:[]},nodes:[{id:'confirm',kind:'manual',name:'解决人确认',approvers:['alice'],approval_mode:'all',assigned_work_items:{alice:[7,8]}}]},steps:[{node_id:'confirm',name:'解决人确认',kind:'manual',status:'pending',reviews:[]}]}
const task={id:'11:confirm',run_id:11,node_id:'confirm',kind:'manual',title:'解决人确认',version:'v1.2',owner:'owner',at:1791856800,digest:'frozen',status:'pending'}
await page.route('**/*',async route=>{
 const r=route.request(),u=new URL(r.url()),p=u.pathname.replace('/api/plugins/research',''),method=r.method(),reply=json=>route.fulfill({json})
 if(u.origin!==base)return route.abort()
 let file;if(u.pathname==='/api/plugins/research/ui')file='research.html';else if(/\/assets\/[^/]+\.(js|css)$/.test(u.pathname))file='assets/'+path.basename(u.pathname)
 if(file)return route.fulfill({body:await readFile(path.join(dist,file)),contentType:file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html'})
 if(!['fetch','xhr'].includes(r.resourceType()))return route.abort()
 if(method==='GET'){
  if(p.endsWith('/tracking'))return reply({deliveries:[],versions:[]})
  if(p.endsWith('/delivery-records'))return reply({items:[],has_more:false})
  if(p==='/api/me')return reply({username:'alice',display_name:'Alice',role:'admin',auth_enabled:true})
  if(p==='/api/users')return reply([{username:'alice',display_name:'Alice',enabled:true},{username:'qa',display_name:'测试',enabled:true}])
  if(p==='/api/plugins')return reply([{name:'research'},{name:'deploy'}])
  if(p==='/api/research/overview')return reply({actor:'alice',aliases:['alice'],connections:[{name:'issues',account:'alice',authorized:true}]})
  if(p==='/api/research/connections/issues/account-hint')return reply({account:'alice',linked:true})
  if(p==='/api/research/tasks')return reply({items:run.status==='success'?[]:[task],total:run.status==='success'?0:1,page:1})
  if(p==='/api/research/tasks/11/detail'||p==='/api/research/flows/runs/11')return reply(run)
  if(p==='/api/research/deliveries/delivery/11')return reply({run,summary:{...task,status:run.status,stage:run.status==='success'?'前置流程完成（尚未发布）':'解决人确认',assignees:['alice'],next_action:run.status==='success'?'发起人继续发布交付':'确认剩余工作项'},releases:[]})
  if(p==='/api/research/work-items/sources')return reply([{name:'issues',products:[1]}])
  if(p==='/api/research/work-items/page')return reply({items:[work,other],total:2,page:1,total_exact:true,has_more:false,people:{alice:'Alice',qa:'测试'}})
  if(p==='/api/work-items/people')return reply(['alice','qa'])
  if(p==='/api/research/work-items/7')return reply({item:work,remote:{bug,actions:{},digest:'remote-baseline'},activity:[],pending_operations:pending})
  if(p==='/api/research/work-items/7/action-options')return reply({bug,actions:{},digest:'remote-baseline',assignedTo:'qa',users:{alice:'Alice',qa:'测试'},builds:{trunk:'主干'}})
  if(p==='/api/services')return reply([{name:'orders',artifact_type:'jar'}])
  if(p==='/api/environments')return reply([{name:'dev',display_name:'开发'}])
  if(p==='/api/release-versions')return reply([])
  if(p==='/api/research/flows')return reply([{id:1,name:'准备',nodes:run.plan.nodes,workbench:'research',mode:'version',owner:'alice'}])
  if(p==='/api/research/flows/runs')return reply([run])
  if(p==='/api/work-items/7/delivery-content')return reply({digest:'content',changes:[]})
  if(p==='/api/services/orders/resources')return reply({requirements:[],bindings:[]})
 }
 if(method==='POST'||method==='PUT'){
  if(p==='/api/research/work-items/attach')return reply(work)
  const body=r.postDataJSON();writes.push({p,body})
  if(p==='/api/research/tasks/11/confirm'){assert.ok(body.work_item_ids.length);run.steps[0].reviews.push({actor:'alice',approved:true,work_item_ids:body.work_item_ids,at:1791856810});const ids=new Set(run.steps[0].reviews.flatMap(x=>x.work_item_ids));if(ids.size===2){run.status='success';run.steps[0].status='success'}return reply(run)}
  if(p==='/api/research/work-items/7/actions'){assert.equal(body.action,'resolve');assert.equal(body.expected_digest,'remote-baseline');assert.equal(body.metadata.release_required,false);assert.equal(body.metadata.no_release_reason,'重复问题');bug.status='resolved';work.external_status='resolved';work.release_required=false;work.no_release_reason='重复问题';return reply({id:body.operation_id,state:'success',message:'已验证'})}
  if(p==='/api/research/work-items/7/release-intent'){assert.equal(body.release_required,true);work.release_required=true;work.metadata_revision=1;return reply(work)}
  if(p==='/api/research/operations/pending/reconcile'){pending=[];return reply({id:'pending',state:'success',message:'已核对'})}
  if(p==='/api/research/flows'){assert.equal(body.workbench,'research');return reply(body)}
 }
 unexpected.push(method+' '+p);return route.fulfill({status:404,json:{error:'unexpected fixture route'}})
})
try{
 await page.goto(base+'/api/plugins/research/ui')
 await page.getByRole('button',{name:'解决人确认',exact:true}).click()
 await page.getByRole('checkbox',{name:'确认 权限修复'}).uncheck()
 await page.getByRole('button',{name:'确认本次交付内容'}).click()
 assert.deepEqual(writes.at(-1).body.work_item_ids,[7])
 await page.getByRole('button',{name:'← 返回原列表',exact:true}).click()
 await page.getByRole('button',{name:'解决人确认',exact:true}).click()
 assert.equal(await page.getByRole('checkbox',{name:'确认 菜单修复'}).isDisabled(),true)
 await page.getByRole('button',{name:'确认本次交付内容'}).click()
 assert.deepEqual(writes.at(-1).body.work_item_ids,[8])
 await page.getByRole('link',{name:'工作项',exact:true}).click()
 await page.getByPlaceholder('搜索标题或编号').fill('菜单')
 await page.getByRole('button',{name:'搜索',exact:true}).click()
 await page.getByRole('button',{name:'菜单修复',exact:true}).click()
 await page.locator('.research-detail-title').filter({hasText:'菜单修复'}).waitFor()
 await page.waitForFunction(()=>!document.querySelector('.research-work-table'))
 await page.getByRole('button',{name:'维护交付内容',exact:true}).click()
 await page.getByRole('button',{name:/添加代码与应用/}).click()
 await page.getByRole('dialog').getByRole('button',{name:'关闭',exact:true}).click()
 await page.getByRole('dialog').waitFor({state:'hidden'})
 assert.equal(await page.locator('.delivery-editor:visible').count(),0)
 await page.getByRole('button',{name:'维护交付内容',exact:true}).click()
 await page.locator('[data-row-key]').first().waitFor()
 assert.ok(await page.locator('[data-row-key]').count()>0,'closing the modal lost draft row')
 await page.getByRole('button',{name:'放弃修改并重载',exact:true}).click()
 await page.getByRole('dialog').getByRole('button',{name:'关闭',exact:true}).click()
 await page.getByRole('dialog').waitFor({state:'hidden'})
 await page.getByRole('button',{name:'解决',exact:true}).click()
 await page.getByRole('dialog').getByRole('button',{name:'人工调整',exact:true}).click()
 const intent=page.getByRole('dialog').locator('.r-grid label').filter({hasText:'生产交付判定'})
 await intent.locator('.el-select').click();await page.getByRole('option',{name:'不独立交付',exact:true}).click()
 await page.locator('.r-grid label').filter({hasText:'调整原因'}).locator('input').fill('重复问题')
 await page.getByRole('button',{name:'确认并同步来源平台',exact:true}).click()
 await page.getByRole('dialog').waitFor({state:'hidden'})
 await page.getByText('不独立交付',{exact:true}).first().waitFor()
 await page.getByRole('button',{name:'人工调整上线判定',exact:true}).click()
 await page.locator('.r-grid label').filter({hasText:'生产交付判定'}).locator('.el-select').click()
 await page.getByRole('option',{name:'独立生产交付候选',exact:true}).click()
 await page.getByRole('button',{name:'保存',exact:true}).click()
 await page.getByText('独立生产交付候选',{exact:true}).first().waitFor()
 pending=[{id:'pending',state:'uncertain',message:'结果待核对',request:{action:'comment',comment:'已处理'}}]
 await page.reload()
 await page.getByRole('button',{name:'读取结果，不重复提交',exact:true}).click()
 await page.getByRole('button',{name:'核对结果（不重放写操作）',exact:true}).click()
 await page.getByText('已验证回写结果',{exact:true}).waitFor()
 assert.equal(writes.filter(w=>w.p.endsWith('/actions')).length,1,'reconcile replayed source write')
 await page.getByRole('button',{name:'← 返回原列表'}).click()
 await page.getByPlaceholder('搜索标题或编号').waitFor()
 // Refresh intentionally resets cached filter; in-page tab continuity was checked above.
 assert.equal(await page.getByPlaceholder('搜索标题或编号').inputValue(),'')
 await page.getByTitle('工作台设置',{exact:true}).click()
 await page.getByRole('link',{name:'交付流程配置',exact:true}).click()
 await page.getByRole('button',{name:'配置流程',exact:true}).click()
 await page.getByRole('button',{name:'＋ 添加节点',exact:true}).click()
 await page.locator('.node-edit summary').click()
 await page.locator('.node-fields label').filter({hasText:'节点类型'}).locator('.el-select').click()
 await page.getByRole('option',{name:'人工审批',exact:true}).click()
 assert.ok(await page.locator('.node-fields label').filter({hasText:'审批人'}).count())
 await page.screenshot({path:'/tmp/research-flow-editor-qa.png',fullPage:true})
 assert.deepEqual(unexpected,[]);assert.deepEqual(errors,[])
 console.log('PASS research tasks, partial confirmations, source resolve, release intent, draft continuity, filters, configurable nodes; fixture-only writes',writes.length)
}finally{await browser.close()}
