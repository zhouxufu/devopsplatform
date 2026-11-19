// Mocked transport only: never pushes code, changes a resource or deploys.
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import path from 'node:path'
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright')
const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH})
const page=await browser.newPage({viewport:{width:1440,height:960}}),errors=[],writes=[]
page.on('pageerror',e=>errors.push(e.message));page.on('dialog',d=>d.accept())
await page.addInitScript(()=>sessionStorage.setItem('lp-deploy-environment','test'))
const base='http://127.0.0.1:9800',dist=process.env.CONSOLE_UI_DIST
const work={id:7,title:'混合交付测试',kind:'bug'}
const changes=[{id:1,title:'后端补丁一',kind:'code',service:'orders',reference:'feature/a',status:'ready'},{id:2,title:'前端已测试',kind:'code',service:'portal',reference:'feature/ui',status:'ready'},{id:3,title:'后端补丁二',kind:'code',service:'orders',reference:'feature/b',status:'ready'},{id:4,title:'本次不要执行',kind:'manual',status:'ready'}]
const defaults={strategy_kind:'standard',artifact_source:'build',applications:{orders:{code_policy:{mode:'integration',target_branch:'dev',base_branch:''}},portal:{artifact_source:'existing',code_policy:{}}}}
const template={id:0,name:'标准交付',mode:'version',nodes:[]},env={name:'test',display_name:'测试',purpose:'test'}
let job,lastDraft,previewBody,run
const nodes=ids=>[{id:'orders',name:'发布orders',kind:'deploy',service:'orders',change_ids:[1,3].filter(id=>ids.includes(id))},{id:'portal',name:'发布portal',kind:'deploy',service:'portal',change_ids:[2]},...(ids.includes(4)?[{id:'manual',name:'人工确认',kind:'manual',instructions:'不要执行',change_ids:[4]}]:[])]
await page.route('**/*',async route=>{
 const r=route.request(),u=new URL(r.url()),p=u.pathname.replace('/api/plugins/deploy',''),reply=json=>route.fulfill({json})
 if(u.origin!==base)return route.abort()
 let file;if(u.pathname==='/api/plugins/deploy/ui')file='deploy.html';else if(/\/assets\/[^/]+\.(js|css)$/.test(u.pathname))file='assets/'+path.basename(u.pathname)
 if(file)return route.fulfill({body:await readFile(path.join(dist,file)),contentType:file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html'})
 if(!['fetch','xhr'].includes(r.resourceType()))return route.continue()
 if(r.method()==='GET'){
  if(p==='/api/me')return reply({username:'alice',role:'admin'})
  if(p==='/api/services')return reply([{name:'orders'},{name:'portal'}])
  if(p==='/api/environments')return reply([env])
  if(p==='/api/environments/test/delivery-policy')return reply({revision:1,stages:[],defaults})
  if(p==='/api/changes')return reply(changes)
  if(p==='/api/work-items')return reply([work])
  if(p==='/api/delivery/artifacts')return reply({items:u.searchParams.get('service')==='portal'?[{artifact_id:300,version:'tested-ui',digest:'sha256:portal',service:'portal'}]:job?.state==='ready'?[{artifact_id:123,version:'built-orders',digest:'sha256:orders',service:'orders',build_id:9}]:[],total:1,page:1})
  if(p==='/api/delivery/code-preparations/mixed')return reply(job)
  if(p==='/api/delivery/runs/11')return reply(run)
  if(p==='/api/delivery/runs/11/control')return reply({state:'idle'})
  if(p==='/api/delivery/runs/page')return reply({items:[],total:0,page:1,page_size:20})
  return reply([])
 }
 const body=r.postDataJSON();writes.push(p)
 if(p==='/api/delivery/draft-plan'){lastDraft=body;const ids=body.selected_change_ids||changes.map(c=>c.id);return reply({template,defaults,scope:{type:'work_items',work_items:[work],changes:changes.filter(c=>ids.includes(c.id)),excluded_change_ids:changes.filter(c=>!ids.includes(c.id)).map(c=>c.id)},nodes:nodes(ids),blockers:[],warnings:[]})}
 if(p==='/api/delivery/code-preparations/preview'){
  assert.deepEqual(body.delivery.artifact_sources,{orders:'build',portal:'existing'});assert.equal(body.policies.orders.target_branch,'dev');assert.deepEqual(body.delivery.selected_change_ids,[1,2,3])
  job={id:'mixed',state:'preview',message:'已冻结',lease_until:0,repositories:[{services:['orders'],policy:body.policies.orders,base_branch:'dev',base_commit:'a'.repeat(40),target_branch:'dev',sources:[]}],builds:[{service:'orders',status:'pending'}]};return reply(job)
 }
 if(p==='/api/delivery/code-preparations/mixed/start'){job={...job,state:'ready',message:'构建完成',builds:[{service:'orders',status:'success',artifact_id:123,run_id:9}]};return reply(job)}
 if(p==='/api/delivery/preview'){previewBody=body;return reply({template,environment:env,scope:{type:'work_items',work_items:[work],changes:changes.slice(0,3)},services:[],nodes:body.nodes,blockers:[],warnings:[],preflight_token:'fixture',preflight_expires:Math.floor(Date.now()/1000)+600})}
 if(p==='/api/delivery/runs'){assert.deepEqual(body.artifacts,{orders:123,portal:300});assert.deepEqual(body.selected_change_ids,[1,2,3]);assert(body.code_confirmed);run={id:11,status:'approved',owner:'alice',digest:'fixture',plan:{template,environment:env,services:[],nodes:body.nodes,scope:{type:'work_items',work_items:[work],changes:changes.slice(0,3)}},steps:body.nodes.map(n=>({node_id:n.id,name:n.name,kind:n.kind,status:'pending'}))};return reply(run)}
 throw Error('Unexpected write '+p)
})
try{
 await page.goto(base+'/api/plugins/deploy/ui#/orchestration?work_item=7')
 await page.getByRole('checkbox',{name:'交付变更 #4',exact:true}).uncheck()
 await page.waitForFunction(()=>document.querySelectorAll('.scope-table tr').length===5)
 await page.getByRole('button',{name:'下一步：制品与步骤',exact:true}).click()
 await page.locator('.code-preparation').waitFor()
 assert.equal(await page.locator('.artifact-source').count(),2)
 assert((await page.locator('.artifact-source').nth(1).innerText()).includes('使用已有制品'))
 await page.locator('[data-artifact-app="portal"] .el-select').click();await page.getByRole('option',{name:/tested-ui/}).click()
 await page.reload()
 await page.locator('.code-preparation').waitFor()
 assert.equal(await page.locator('[aria-label="交付变更 #4"]').isChecked(),false);assert((await page.locator('[data-artifact-app="portal"]').innerText()).includes('tested-ui'))
 await page.getByRole('button',{name:'检查代码来源',exact:true}).click()
 await page.getByRole('button',{name:'合并并构建，不部署',exact:true}).click()
 await page.getByText('制品 #123',{exact:false}).waitFor()
 assert.equal(await page.locator('[data-artifact-app="portal"] [role=combobox]').isDisabled(),false)
 assert.equal(await page.getByRole('button',{name:'预检范围、制品与目标',exact:true}).isDisabled(),false)
 await page.getByRole('checkbox',{name:/我已确认制品覆盖所选工作项/}).check()
 await page.getByRole('button',{name:'预检范围、制品与目标',exact:true}).click()
 assert.deepEqual(previewBody.artifacts,{orders:123,portal:300});assert.deepEqual(previewBody.selected_change_ids,[1,2,3])
 await page.getByRole('button',{name:'提交执行单，不立即执行',exact:true}).click();await page.waitForURL(/run=11/)
 assert(!writes.some(p=>p.endsWith('/advance')||p.endsWith('/control/start')));assert.deepEqual(errors,[])
 await page.screenshot({path:'/tmp/devops-delivery-sources.png'})
 console.log(JSON.stringify({ok:true,checks:['environment and per-app defaults','mixed build and existing artifacts','same-app changes build once','excluded scope survives reload','existing artifact remains editable','no automatic deployment','fixture-only writes']}))
}catch(e){await page.screenshot({path:'/tmp/devops-delivery-sources-failure.png'});console.error({errors,lastDraft});throw e}finally{await browser.close()}
