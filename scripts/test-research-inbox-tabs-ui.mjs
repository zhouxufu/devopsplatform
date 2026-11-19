// Fully intercepted fixture: no real source, approval, or deployment writes.
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import path from 'node:path'
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright')
const base='http://127.0.0.1:9800',dist=path.resolve('web/dist')
const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH})
const page=await browser.newPage({viewport:{width:1440,height:960}}),unexpected=[],errors=[],warnings=[],pending=[]
let hold=false
page.on('pageerror',e=>errors.push(e.message))
page.on('console',m=>{if(m.type()==='warning')warnings.push(m.text())})
await page.route('**/*',async route=>{
 const req=route.request(),url=new URL(req.url()),p=url.pathname.replace('/api/plugins/research',''),reply=json=>route.fulfill({json})
 if(url.origin!==base)return route.abort()
 if(url.pathname==='/api/plugins/research/ui')return route.fulfill({body:await readFile(path.join(dist,'research.html')),contentType:'text/html'})
 if(/\/assets\/[^/]+\.(js|css)$/.test(url.pathname))return route.fulfill({body:await readFile(path.join(dist,'assets',path.basename(url.pathname))),contentType:url.pathname.endsWith('.js')?'text/javascript':'text/css'})
 if(!['fetch','xhr'].includes(req.resourceType()))return route.abort()
 if(req.method()!=='GET'){unexpected.push(req.method()+' '+p);return route.abort()}
 if(p==='/api/me')return reply({username:'alice',role:'member',auth_enabled:true})
 if(p==='/api/plugins')return reply([{name:'research'}])
 if(p==='/api/research/overview')return reply({actor:'alice',aliases:['alice'],connections:[]})
 if(p==='/api/research/work-items/sources')return reply([])
 if(p==='/api/environments')return reply([])
 if(p==='/api/research/work-items/page'){const current=Number(url.searchParams.get('page')||1);return reply({items:[{id:900,source:'manual',kind:'requirement',title:'本人工作项',status:'open'}],total:45,page:current,total_exact:true,has_more:current<3})}
 if(p==='/api/research/tasks'||p==='/api/research/deliveries'){
  const view=url.searchParams.get('view')||'todo'
  const payload={items:[{id:view+'-fixture',run_id:1,title:view==='owned'?'本人发起交付':view==='done'?'本人处理记录':'本人待审批',kind:'approval',status:'pending_approval',owner:'alice',at:1791943200}],total:1,page:1}
  if(hold&&view==='owned')return new Promise(resolve=>pending.push(async()=>{await reply(payload);resolve()}))
  return reply(payload)
 }
 unexpected.push(req.method()+' '+p);return route.fulfill({status:404,json:{error:'unexpected fixture route'}})
})
const tab=name=>page.locator('.page-header-navigation').getByRole('button',{name,exact:true})
const assertTab=async name=>assert.equal(await tab(name).getAttribute('aria-pressed'),'true')
try{
 await page.goto(base+'/api/plugins/research/ui')
 await page.getByRole('button',{name:'本人待审批',exact:true}).waitFor()
 await tab('我的工作项').click()
 await page.getByRole('button',{name:'本人工作项',exact:true}).waitFor()
 await page.getByPlaceholder('搜索标题或编号').fill('保留筛选')
 await page.locator('.inbox-work-panel').getByRole('button',{name:'下一页',exact:true}).click()
 await page.waitForFunction(()=>document.querySelector('.inbox-work-panel .r-pagination')?.textContent.includes('2 / 3'))
 for(const [name,title] of [['待我处理','本人待审批'],['我发起的','本人发起交付'],['处理记录','本人处理记录']]){
  await tab(name).click();await assertTab(name)
  await page.getByRole('button',{name:title,exact:true}).waitFor()
  assert.equal(await page.locator('.inbox-work-panel').isVisible(),false,'hidden work panel still visible')
  assert.equal(await page.locator('.research-work-items').isVisible(),false,'hidden work list still visible')
  assert.equal(await page.locator('.r-table:visible').count(),1,'two visible tables after tab switch')
  assert.equal(await page.getByRole('button',{name:'本人工作项',exact:true}).isVisible(),false)
 }
 await tab('我的工作项').click();await assertTab('我的工作项')
 assert.equal(await page.getByPlaceholder('搜索标题或编号').inputValue(),'保留筛选','switching lost work filters')
 assert.equal(await page.locator('.inbox-table').count(),0)
 assert.match(await page.locator('.inbox-work-panel .r-pagination').textContent(),/2 \/ 3/,'switching lost work page')
 // Navigation remains responsive during an in-flight request; late results cannot replace the current view.
 hold=true;await tab('我发起的').click();await assertTab('我发起的')
 await page.waitForFunction(()=>document.querySelector('.inbox-table')?.getAttribute('aria-busy')==='true')
 await tab('处理记录').click();await assertTab('处理记录')
 await page.getByRole('button',{name:'本人处理记录',exact:true}).waitFor()
 while(pending.length)await pending.shift()()
 await page.waitForFunction(()=>document.querySelector('.inbox-table')?.getAttribute('aria-busy')==='false')
 assert.equal(await page.getByRole('button',{name:'本人发起交付',exact:true}).count(),0,'stale response overwrote selected view')
 await tab('待我处理').click();await page.getByRole('button',{name:'本人待审批',exact:true}).waitFor()
 await tab('我的工作项').click();await page.getByRole('button',{name:'本人工作项',exact:true}).waitFor()
 await tab('待我处理').click();await page.getByRole('button',{name:'本人待审批',exact:true}).waitFor()
 await page.screenshot({path:'/tmp/research-inbox-tabs-qa.png',fullPage:true})
 assert.deepEqual(errors,[]);assert.deepEqual(unexpected,[])
 assert.equal(warnings.some(w=>w.includes('non-element root node')||w.includes('Runtime directive')),false)
 console.log('PASS four inbox tabs, single visible panel, retained work filters, responsive pending-request navigation, late-result isolation; fixture only')
}finally{while(pending.length)await pending.shift()();await browser.close()}
