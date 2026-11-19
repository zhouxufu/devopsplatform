// Fixture-only business APIs: no Git push, build, SQL, Nacos publish or deployment.
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import path from 'node:path'
const base='http://127.0.0.1:9800',dist=process.env.CONSOLE_UI_DIST
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright')
const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH})
const page=await browser.newPage({viewport:{width:1440,height:960}}),errors=[],unexpected=[],writes=[]
page.on('pageerror',e=>errors.push(e.message));page.on('dialog',d=>d.accept())
await page.addInitScript(()=>sessionStorage.setItem('lp-deploy-environment','test'))
const work={id:7,title:'紧急修复',kind:'bug'},changes=[{id:1,title:'代码补丁',kind:'code',service:'orders',reference:'hotfix/7',status:'ready'},{id:2,title:'菜单修复',kind:'mysql',service:'orders',status:'ready'},{id:3,title:'配置修复',kind:'nacos',service:'orders',status:'ready'}]
const tpl={id:1,name:'工作项交付',mode:'version',owner:'alice',services:[],nodes:[{id:'sql',kind:'mysql',name:'SQL 变更'},{id:'config',kind:'nacos',name:'配置变更'},{id:'deploy',kind:'deploy',name:'发布应用'}]}
const scope={type:'work_items',work_items:[work],changes,digest:'scope-fixture'}
let run
await page.route('**/*',async route=>{
 const r=route.request(),u=new URL(r.url()),p=u.pathname,method=r.method(),api=p.replace('/api/plugins/deploy',''),reply=json=>route.fulfill({json})
 if(u.origin!==base)return route.abort()
 let file;if(p==='/api/plugins/deploy/ui')file='deploy.html';else if(/\/assets\/[^/]+\.(js|css)$/.test(p))file='assets/'+path.basename(p)
 if(file&&dist)return route.fulfill({body:await readFile(path.join(dist,file)),contentType:file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html'})
 if(!['xhr','fetch'].includes(r.resourceType()))return route.continue()
 if(method==='GET'){
  if(api==='/api/me')return reply({username:'alice',role:'admin',auth_enabled:true})
  if(api==='/api/delivery/templates')return reply([tpl])
  if(api==='/api/services')return reply([{name:'orders'}])
  if(api==='/api/environments')return reply([{name:'test',display_name:'测试',approvers:'reviewer'}])
  if(api==='/api/work-items')return reply([work])
  if(api==='/api/changes')return reply(changes)
  if(api==='/api/delivery/artifacts')return reply({items:[{service:'orders',artifact_id:123,version:'patch123',digest:'sha256:qa',branch:'hotfix/7',build_id:9}],total:1,page:1})
  if(api==='/api/delivery/runs/page')return reply({items:run?[run]:[],total:run?1:0,page:1,page_size:20})
  if(api==='/api/delivery/runs/11')return reply(run)
  if(api==='/api/delivery/runs/11/control')return reply({state:'idle',message:'尚未启动'})
  return reply([])
 }
 const body=r.postDataJSON();writes.push({api,body})
 if(api==='/api/delivery/draft-plan')return reply({strategy_kind:'standard',strategy_version:1,template:{...tpl,id:0},scope,nodes:tpl.nodes,blockers:[],warnings:[]})
 if(api==='/api/delivery/work-item-scope'){assert.deepEqual(body.work_item_ids,[7]);return reply(scope)}
 if(api==='/api/delivery/preview')return reply({scope,template:tpl,environment:{name:'test',display_name:'测试',approvers:'reviewer'},services:[{service:'orders',artifact:{id:123,version:'patch123'},target:{host_group:'qa',hosts:['fixture'],batch_size:1}}],nodes:body.nodes,blockers:[],warnings:['代码覆盖需人工确认'],sql_plans:[{node_id:'sql',connection:'qa-db',database:'orders',mode:'applied',applied:[{change_id:2,run_id:8}],statements:[]}],nacos_plans:[{node_id:'config',configs:[{change_id:3,service:'orders',alias:'config',data_id:'orders.yaml',skip:true,prior_run_id:8,diff:[]}]}],preflight_token:'fixture-token',preflight_expires:Math.floor(Date.now()/1000)+600})
 if(api==='/api/delivery/runs'){
  assert.deepEqual(body.work_item_ids,[7]);assert.equal(body.source_type,'work_items');assert.equal(body.code_confirmed,true);assert.equal(body.artifacts.orders,123);assert.equal(body.preflight_token,'fixture-token')
  run={id:11,owner:'alice',status:'pending_approval',steps:tpl.nodes.map(n=>({node_id:n.id,kind:n.kind,name:n.name,status:'pending'})),plan:{scope,template:tpl,environment:{name:'test',display_name:'测试',approvers:'reviewer'},services:[],nodes:tpl.nodes},digest:'fixture'};return reply(run)
 }
 unexpected.push(method+' '+api);return route.abort()
})
try{
 await page.goto(base+'/api/plugins/deploy/ui#/orchestration?work_item=7')
 await page.getByRole('button',{name:'下一步：制品与步骤',exact:true}).waitFor()
 await page.waitForFunction(()=>document.querySelectorAll('.artifact-row').length===1)
 await page.getByRole('button',{name:'下一步：制品与步骤',exact:true}).click()
 const select=page.locator('.artifact-row .el-select').first();await select.click();await page.getByRole('option',{name:/patch123/}).click()
 await page.getByRole('checkbox',{name:/我已确认制品覆盖所选工作项/}).check()
 await page.getByRole('checkbox',{name:/我已核对跨类型步骤/}).check()
 await page.getByRole('button',{name:'预检范围、制品与目标',exact:true}).click()
 await page.getByText('#2 · 已成功执行，自动跳过',{exact:false}).waitFor();await page.getByText('已成功执行且当前配置项一致，自动跳过',{exact:false}).waitFor()
 assert.equal(await page.getByRole('dialog').count(),0)
 await page.getByRole('button',{name:'提交执行单，不立即执行',exact:true}).click();await page.getByText('工作项交付',{exact:false}).last().waitFor()
 assert(page.url().includes('run=11'));assert.equal(writes.filter(w=>w.api.endsWith('/start')||w.api.endsWith('/advance')).length,0)
 assert.deepEqual(errors,[]);assert.deepEqual(unexpected,[])
 await page.screenshot({path:'/tmp/devops-work-item-delivery.png'})
 console.log(JSON.stringify({ok:true,checks:['Bug direct entry','one artifact per application','manual code coverage confirmation','SQL/Nacos skip provenance','immutable submit','no automatic execution','no real writes']}))
}catch(e){await page.screenshot({path:'/tmp/devops-work-item-delivery-failure.png'});throw e}finally{await browser.close()}
