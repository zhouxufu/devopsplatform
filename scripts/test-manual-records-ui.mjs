// Isolated fixtures only: memo content must never become executable code.
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import path from 'node:path'
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright')
const base='http://127.0.0.1:9800',dist=path.resolve('web/dist')
const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH})
const page=await browser.newPage({viewport:{width:1440,height:960}}),errors=[],unexpected=[],writes=[]
page.on('pageerror',e=>errors.push(e.message));page.on('dialog',d=>d.accept())
await page.addInitScript(()=>sessionStorage.setItem('lp-deploy-environment','test'))
const memo='1. 人工更新外部菜单\n2. 核对是否生效\n<script>window.__memoExecuted=true</script>'
const work={id:7,title:'菜单需求',kind:'requirement',owner:'alice',source:'manual'}
const changes=[{id:1,title:'更新外部菜单',kind:'manual',reference:memo,status:'ready',owner:'alice'},{id:2,title:'下次人工操作',kind:'manual',reference:'本次不处理',status:'ready',owner:'alice'}]
let run,handled=false
const task=()=>({id:'11:manual-1',run_id:11,node_id:'manual-1',source_kind:'delivery',title:'更新外部菜单',owner:'alice',kind:'manual',status:handled?'success':'pending',digest:'frozen',at:1791943200,stage:'更新外部菜单',environment:'测试',instructions:memo,assignees:handled?[]:['alice']})
await page.route('**/*',async route=>{
 const req=route.request(),url=new URL(req.url()),p=url.pathname.replace(/^\/api\/plugins\/(research|deploy)/,''),method=req.method(),reply=json=>route.fulfill({json})
 if(url.origin!==base)return route.abort()
 if(/^\/api\/plugins\/(research|deploy)\/ui$/.test(url.pathname))return route.fulfill({body:await readFile(path.join(dist,url.pathname.includes('research')?'research.html':'deploy.html')),contentType:'text/html'})
 if(/\/assets\/[^/]+\.(js|css)$/.test(url.pathname))return route.fulfill({body:await readFile(path.join(dist,'assets',path.basename(url.pathname))),contentType:url.pathname.endsWith('.js')?'text/javascript':'text/css'})
 if(!['fetch','xhr'].includes(req.resourceType()))return route.abort()
 if(method==='GET'){
  if(p==='/api/me')return reply({username:'alice',role:'admin',auth_enabled:true})
  if(p==='/api/plugins')return reply([{name:'research'},{name:'deploy'}])
  if(p==='/api/services'||p==='/api/delivery/templates')return reply([])
  if(p==='/api/environments')return reply([{name:'test',display_name:'测试',purpose:'test'}])
  if(p==='/api/work-items')return reply([work])
  if(p==='/api/changes')return reply(changes)
  if(p==='/api/research/overview')return reply({actor:'alice',aliases:['alice'],connections:[]})
  if(p==='/api/delivery/runs/page')return reply({items:[],total:0,page:1,page_size:20})
  if(p==='/api/delivery/runs/11')return reply(run)
  if(p==='/api/delivery/runs/11/control')return reply({state:'idle'})
  if(p==='/api/research/tasks')return reply({items:handled===(url.searchParams.get('view')==='done')?[task()]:[],total:handled===(url.searchParams.get('view')==='done')?1:0,page:1})
  if(p==='/api/research/deliveries/delivery/11')return reply({run,summary:{...task(),next_action:handled?'继续原交付':'处理当前记录'},linked_deliveries:[],releases:[]})
  return reply([])
 }
 const body=req.postDataJSON();writes.push({p,body})
 if(p==='/api/delivery/draft-plan'){
  const selected=body.selected_change_ids||[1,2],cs=changes.filter(c=>selected.includes(c.id))
  return reply({strategy_kind:'standard',strategy_version:1,template:{id:0,name:'标准交付',mode:'version'},scope:{type:'work_items',work_items:[work],changes:cs,excluded_change_ids:changes.filter(c=>!selected.includes(c.id)).map(c=>c.id)},nodes:cs.map(c=>({id:'manual-'+c.id,kind:'manual',name:c.title,instructions:c.reference,change_ids:[c.id]})),blockers:[],warnings:[]})
 }
 if(p==='/api/delivery/preview')return reply({template:{name:'标准交付'},environment:{name:'test',display_name:'测试'},scope:{type:'work_items',work_items:[work],changes:[changes[0]]},services:[],nodes:body.nodes,blockers:[],warnings:[],preflight_token:'token-fixture',preflight_expires:Math.floor(Date.now()/1000)+600})
 if(p==='/api/delivery/runs'){
  assert.deepEqual(body.selected_change_ids,[1]);assert.equal(body.nodes.length,1);assert.equal(body.nodes[0].instructions,memo)
  run={id:11,owner:'alice',digest:'frozen',status:'approved',plan:{template:{name:'标准交付'},environment:{name:'test',display_name:'测试'},scope:{work_items:[work],changes:[changes[0]]},nodes:body.nodes,services:[]},steps:body.nodes.map(n=>({node_id:n.id,name:n.name,kind:'manual',status:'pending',reviews:[]}))};return reply(run)
 }
 if(p==='/api/research/tasks/11/manual-1'){
  assert.equal(body.approve,true);assert.equal(body.comment,'已核对外部菜单')
  handled=true;run.status='running';run.steps[0].status='success';run.steps[0].reviews=[{actor:'alice',approved:true,comment:body.comment,at:1791944200}];return reply(run)
 }
 unexpected.push(method+' '+p);return route.fulfill({status:404,json:{error:'unexpected fixture route'}})
})
try{
 await page.goto(base+'/api/plugins/deploy/ui#/orchestration?work_item=7')
 await page.getByRole('checkbox',{name:'交付变更 #2',exact:true}).waitFor()
 await page.getByRole('checkbox',{name:'交付变更 #2',exact:true}).uncheck()
 await page.getByText('人工处理清单 · 1 项',{exact:true}).waitFor()
 await page.getByText('人工处理清单 · 1 项',{exact:true}).click()
 await page.locator('.manual-record-text').filter({hasText:'人工更新外部菜单'}).waitFor()
 assert.equal(await page.locator('.manual-record-text').count(),1)
 await page.getByRole('button',{name:'下一步：制品与步骤',exact:true}).click()
 const node=page.locator('.step-section .node-edit');await node.locator('summary').click()
 assert.equal(await node.locator('textarea').isDisabled(),true,'frozen note editable as workflow instructions')
 await page.getByRole('button',{name:'预检范围、制品与目标',exact:true}).click()
 await page.getByRole('button',{name:'提交执行单，不立即执行',exact:true}).click()
 await page.waitForURL(/run=11/)
 assert.equal(writes.some(w=>/\/(advance|start)$/.test(w.p)),false)
 await page.goto(base+'/api/plugins/research/ui')
 await page.getByRole('button',{name:'更新外部菜单',exact:true}).click()
 await page.getByRole('button',{name:'人工处理清单 · 1',exact:true}).click()
 await page.locator('.review-content pre').filter({hasText:'人工更新外部菜单'}).waitFor()
 assert.equal(await page.evaluate(()=>window.__memoExecuted===true),false)
 assert.equal(await page.getByRole('checkbox',{name:'确认 菜单需求'}).count(),0,'memo confirmation coupled to Bug code confirmation')
 await page.getByPlaceholder('记录处理结果（选填）').fill('已核对外部菜单')
 await page.getByRole('button',{name:'记录已处理，继续流程',exact:true}).click()
 await page.locator('.r-receipt').waitFor()
 assert.equal(await page.getByRole('button',{name:'记录已处理，继续流程',exact:true}).count(),0)
 await page.getByRole('button',{name:'进度与交接',exact:true}).click()
 await page.getByText(/alice：同意 · 已核对外部菜单/).waitFor()
 await page.getByRole('button',{name:'← 返回原列表',exact:true}).click()
 await page.getByRole('button',{name:'处理记录',exact:true}).click()
 await page.getByRole('button',{name:'更新外部菜单',exact:true}).waitFor()
 assert.deepEqual(unexpected,[]);assert.deepEqual(errors,[])
 console.log('PASS selected memo checklist, excluded scope, frozen instructions, no auto-execution, personal manual task, plain text safety, confirmation and handling history; fixtures only')
}finally{await browser.close()}
