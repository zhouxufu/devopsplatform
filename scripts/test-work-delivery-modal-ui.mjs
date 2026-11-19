// Intercept all business reads/writes. No real source action or deployment.
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import path from 'node:path'
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright')
const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH}),base='http://127.0.0.1:19823',dist=path.resolve('web/dist')
const page=await browser.newPage({viewport:{width:1440,height:960}}),errors=[],writes=[],unexpected=[],detailReads=[],dialogs=[]
page.on('pageerror',e=>errors.push(e.message));page.on('dialog',d=>{dialogs.push(d.message());d.accept()});page.on('console',m=>{if(m.type()==='warning'&&m.text().includes('Runtime directive'))errors.push(m.text())})
const work={id:7,source:'manual',kind:'bug',title:'交付弹窗测试',description:'Bug 正文',status:'open',owner:'alice',release_required:true}
let changes=[{id:10,kind:'manual',title:'当前处理记录',reference:'当前草稿',status:'draft',owner:'alice'}],recordFail=true,hasRecords=true,snapshotFail=true
const records=[22,21].map((id,i)=>({id,status:i?'failed':'pending_approval',owner:'alice',environment:i?'测试':'生产',created_at:1792032000-i,change_count:1,loaded:false}))
await page.route('**/*',async route=>{
 const req=route.request(),url=new URL(req.url()),p=url.pathname.replace('/api/plugins/research',''),method=req.method(),reply=json=>route.fulfill({json})
 if(url.origin!==base)return route.abort()
 let file;if(url.pathname==='/api/plugins/research/ui')file='research.html';else if(/\/assets\/[^/]+\.(js|css)$/.test(p))file='assets/'+path.basename(p)
 if(file)return route.fulfill({body:await readFile(path.join(dist,file)),contentType:file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html'})
 if(method==='GET'){
  if(p==='/api/me')return reply({username:'alice',role:'admin',auth_enabled:true})
  if(p==='/api/services')return reply([])
  if(p==='/api/research/overview')return reply({actor:'alice',aliases:['alice'],connections:[]})
  if(p==='/api/research/work-items/sources')return reply([])
  if(p==='/api/research/work-items/page')return reply({items:[work],page:1,total:1,total_exact:true,has_more:false})
  if(p==='/api/research/work-items/7')return reply({item:work,activity:[],pending_operations:[]})
  if(p==='/api/work-items/7/delivery-content')return reply({changes,digest:'baseline'})
  if(p==='/api/research/work-items/7/delivery-records')return recordFail?route.fulfill({status:500,json:{error:'fixture unavailable'}}):reply({items:hasRecords?records:[],has_more:false})
  if(/^\/api\/research\/work-items\/7\/delivery-records\/\d+$/.test(p)){
   const id=Number(p.split('/').at(-1));detailReads.push(id)
   if(snapshotFail)return route.fulfill({status:500,json:{error:'snapshot temporarily unavailable'}})
   return reply({...records.find(r=>r.id===id),loaded:true,changes:[{id:3,kind:'mysql',title:'冻结 SQL',sql:{content:'UPDATE frozen_table SET value = 1'}}],artifacts:[],steps:[{node_id:'sql',name:'执行 SQL',status:id===21?'failed':'pending',detail:id===21?'冻结的执行失败原因':''}]})
  }
  return reply([])
 }
 if(method==='PUT'&&p==='/api/work-items/7/delivery-content'){
  const body=req.postDataJSON();writes.push({p,body});assert.equal(body.changes[0].id,10);changes=body.changes.map(c=>({...c,status:'draft',owner:'alice'}));return reply({ok:true})
 }
 unexpected.push(method+' '+p);return route.abort()
})
try{
 await page.goto(base+'/api/plugins/research/ui#/work/7')
 await page.getByRole('button',{name:'重试读取交付记录',exact:true}).waitFor()
 assert.equal(await page.locator('nav button').filter({hasText:/交付记录/}).count(),0)
 await page.getByText('交付内容：自定义记录 1',{exact:true}).waitFor()
 assert.equal(await page.locator('.delivery-editor:visible').count(),0)
 await page.getByRole('button',{name:'维护交付内容',exact:true}).click()
 const modal=page.getByRole('dialog',{name:'维护交付内容 · 交付弹窗测试',exact:true})
 await modal.getByRole('button',{name:'自定义记录 · 1',exact:true}).click();await modal.locator('textarea').first().fill('尚未保存的输入')
 await modal.getByRole('button',{name:'关闭',exact:true}).click();await modal.waitFor({state:'hidden'})
 assert.equal(dialogs.length,0,'closing a maintenance dialog should retain drafts without a discard prompt')
 assert.equal(await page.locator('.delivery-editor:visible').count(),0,'editor leaked below Bug comments')
 await page.getByRole('button',{name:'维护交付内容',exact:true}).click()
 await modal.getByRole('button',{name:'自定义记录 · 1',exact:true}).click()
 assert.equal(await modal.locator('textarea').first().inputValue(),'尚未保存的输入')
 await modal.getByRole('button',{name:'保存交付草稿',exact:true}).click()
 await page.getByText('全部交付内容已保存，仍停留在当前需求',{exact:true}).waitFor()
 assert.match(page.url(),/#\/work\/7$/);assert.equal(writes.length,1)
 await modal.getByRole('button',{name:'关闭',exact:true}).click();await modal.waitFor({state:'hidden'})
 recordFail=false
 await page.getByRole('button',{name:'重试读取交付记录',exact:true}).click()
 await page.getByRole('button',{name:'交付记录（2）',exact:true}).click()
 await page.getByRole('button',{name:'重试读取快照',exact:true}).waitFor()
 assert.deepEqual(detailReads,[22],'collapsed older records fetched their contents')
 snapshotFail=false;await page.getByRole('button',{name:'重试读取快照',exact:true}).click()
 await page.getByText('UPDATE frozen_table SET value = 1',{exact:true}).waitFor()
 assert.equal(await page.getByText('尚未保存的输入',{exact:true}).count(),0,'record renders current draft instead of snapshot')
 await page.getByRole('button',{name:/交付单 #21/}).click()
 await page.getByText('冻结的执行失败原因',{exact:true}).waitFor();assert.deepEqual(detailReads,[22,22,21])
 assert.match(page.url(),/#\/work\/7$/,'viewing a record navigated to another page')
 await page.screenshot({path:'/tmp/work-delivery-inline-records.png',fullPage:true})
 hasRecords=false;await page.reload();await page.getByRole('button',{name:'维护交付内容',exact:true}).waitFor()
 await page.waitForFunction(()=>!document.querySelector('.delivery-overview [role=status]'))
 assert.equal(await page.locator('nav button').filter({hasText:/交付记录/}).count(),0,'empty records tab is visible')
 await page.goto(base+'/api/plugins/research/ui#/work')
 await page.getByRole('button',{name:'维护交付内容',exact:true}).click();await modal.waitFor()
 assert.match(page.url(),/#\/work$/,'list maintenance navigated away')
 await modal.getByRole('button',{name:'自定义记录 · 1',exact:true}).click()
 for(const width of [1440,900,700,390]){
  await page.setViewportSize({width,height:800})
  const footer=await modal.locator('.el-dialog__footer').boundingBox();assert(footer.y+footer.height<=802)
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth+2),false)
 }
 await page.screenshot({path:'/tmp/work-delivery-modal-390.png',fullPage:true})
 await modal.getByRole('button',{name:'关闭',exact:true}).click();await modal.waitFor({state:'hidden'})
 assert.deepEqual(errors,[]);assert.deepEqual(unexpected,[])
 console.log(JSON.stringify({ok:true,checks:['detail and list use one modal','no inline editor leak','close/reopen preserves draft','save stays in original page','record failure retry','hide confirmed-empty records','latest record expands','lazy snapshots and inline retry','older records expand in place','frozen content and execution results','fixed footer at four widths'],noRealWrites:true}))
}finally{await browser.close()}
