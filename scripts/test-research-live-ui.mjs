// Fully intercepted fixture; no real Bug edits or registration.
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import path from 'node:path'
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright')
const base='http://127.0.0.1:9800',dist=path.resolve('web/dist')
const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH})
const page=await browser.newPage({viewport:{width:1440,height:960}}),requests=[],writes=[],unexpected=[],errors=[],canceled=[]
let failing=false,identity='public',registered=false
page.on('pageerror',e=>errors.push(e.message))
page.on('requestfailed',r=>{if(r.url().includes('q=slow-canceled'))canceled.push(r.url())})
const sourceRow=n=>({id:registered?700:0,source:'zentao:issues',external_id:String(n),kind:'bug',title:'实时来源 Bug '+n,status:'open',external_status:'active',severity:'1',priority:'2',resolution:'backend_custom',assigned_to:'alice',resolved_by:'alice',registered,source_updated_at:'2026-10-14 10:00:00'})
const item={...sourceRow(1),id:700,description:'<p>实时来源说明</p>'}
await page.route('**/*',async route=>{
 const req=route.request(),url=new URL(req.url()),p=url.pathname.replace('/api/plugins/research',''),method=req.method(),reply=json=>route.fulfill({json})
 if(url.origin!==base)return route.abort()
 if(url.pathname==='/api/plugins/research/ui')return route.fulfill({body:await readFile(path.join(dist,'research.html')),contentType:'text/html'})
 if(/\/assets\/[^/]+\.(js|css)$/.test(url.pathname))return route.fulfill({body:await readFile(path.join(dist,'assets',path.basename(url.pathname))),contentType:url.pathname.endsWith('.js')?'text/javascript':'text/css'})
 if(!['fetch','xhr'].includes(req.resourceType()))return route.abort()
 requests.push({p,method,query:url.searchParams.toString()})
 if(method==='GET'){
  if(p==='/api/me')return reply({username:'platform-alice',role:'member',auth_enabled:true})
  if(p==='/api/plugins')return reply([{name:'research'}])
  if(p==='/api/research/overview')return reply({actor:'platform-alice',aliases:['alice'],connections:[{name:'issues',account:identity==='personal'?'alice':'',authorized:identity==='personal',field_labels:{resolution:{backend_custom:'后端代码修复'}}}]})
  if(p==='/api/research/work-items/sources')return reply([{name:'issues',products:[1,2]}])
  if(p==='/api/environments')return reply([{name:'prod',display_name:'生产',protected:true}])
  if(p==='/api/services')return reply([])
  if(p==='/api/research/work-items/page'){
   if(url.searchParams.get('q')==='slow-canceled'){
    await new Promise(resolve=>setTimeout(resolve,400))
    await reply({items:[{...sourceRow(1),title:'旧请求不可覆盖新筛选'}],total:1,page:1,has_more:false}).catch(()=>{})
    return
   }
   if(failing)return route.fulfill({status:409,json:{error:'来源连接暂时不可用，未回退旧数据'}})
   const n=Number(url.searchParams.get('page')||1),local=url.searchParams.get('connection')==='manual',filtered=!!url.searchParams.get('release_required')
   const rows=local?[{id:900,source:'manual',kind:'requirement',title:'平台自定义需求',status:'open'}]:Array.from({length:20},(_,i)=>sourceRow((n-1)*20+i+1))
   const pending=url.searchParams.get('delivery')==='pending_release'
   return reply({items:rows,total:local?1:pending?40:12037,page:n,page_size:20,total_exact:pending||!filtered,has_more:!local,next_cursor:filtered?'fixture-next':'',read_identity:local?'platform':identity,checked_at:1791943200,query_mode:pending?'candidate_batch':'',products:pending?{1:'产品 #1',2:'产品 #2'}:{1:'产品 A',2:'产品 B'},people:pending?{}:{alice:'Alice',bob:'Bob'}})
  }
  if(p==='/api/research/work-items/700')return reply({item,remote:{bug:{id:'1',title:item.title,status:'active',steps:item.description,assignedTo:'alice'},digest:'fresh'},source_identity:identity,activity:[],pending_operations:[]})
 }
 if(method==='POST'&&p==='/api/research/work-items/attach'){assert.deepEqual(req.postDataJSON(),{connection:'issues',external_id:'1'});registered=true;writes.push(p);return reply(item)}
 unexpected.push(method+' '+p);return route.fulfill({status:404,json:{error:'unexpected fixture route'}})
})
try{
 await page.goto(base+'/api/plugins/research/ui#/work')
 await page.getByRole('button',{name:'实时来源 Bug 1',exact:true}).waitFor()
 await page.getByText('后端代码修复',{exact:true}).first().waitFor()
 assert.match(requests.filter(r=>r.p==='/api/research/work-items/page').at(-1).query,/product=0/)
 await page.getByText('全部产品',{exact:true}).first().waitFor()
 assert.equal(writes.length,0,'list automatically registered whole page')
 await page.getByPlaceholder('搜索标题或编号').fill('slow-canceled')
 await Promise.all([page.waitForRequest(r=>r.url().includes('q=slow-canceled')),page.getByRole('button',{name:'搜索',exact:true}).click()])
 await page.getByPlaceholder('搜索标题或编号').fill('latest')
 await Promise.all([page.waitForResponse(r=>r.url().includes('q=latest')),page.getByRole('button',{name:'由我解决',exact:true}).click()])
 await page.waitForFunction(()=>document.querySelector('.r-table')?.getAttribute('aria-busy')==='false')
 assert.ok(canceled.length,'obsolete request was not aborted')
 assert.equal(await page.getByRole('button',{name:'旧请求不可覆盖新筛选',exact:true}).count(),0)
 await page.getByPlaceholder('搜索标题或编号').fill('')
 await page.getByRole('button',{name:'全部',exact:true}).click()
 await page.getByRole('button',{name:'下一页',exact:true}).click();await page.getByRole('button',{name:'实时来源 Bug 21',exact:true}).waitFor();assert.equal(await page.getByRole('button',{name:'实时来源 Bug 1',exact:true}).count(),0)
 await page.getByRole('button',{name:'上一页',exact:true}).click();await page.getByRole('button',{name:'实时来源 Bug 1',exact:true}).waitFor()
 await Promise.all([page.waitForResponse(r=>r.url().includes('mine=1')),page.getByRole('button',{name:'指派给我',exact:true}).click()])
 assert.match(requests.filter(r=>r.p==='/api/research/work-items/page').at(-1).query,/person_role=assigned_to/)
 await page.getByRole('button',{name:'我的待上线',exact:true}).click();await page.getByText('共 40 项',{exact:true}).waitFor()
 assert.ok(await page.getByText('Alice',{exact:true}).count(),'batch query cleared personnel names')
 const pendingQuery=new URLSearchParams(requests.filter(r=>r.p==='/api/research/work-items/page').at(-1).query)
 assert.equal(pendingQuery.get('delivery'),'pending_release');assert.equal(pendingQuery.get('release_required'),'yes');assert.equal(pendingQuery.has('environment'),false)
 assert.equal(await page.getByText('核对上线环境',{exact:true}).count(),0)
 await Promise.all([page.waitForResponse(r=>r.url().includes('cursor=fixture-next')),page.getByRole('button',{name:'下一页',exact:true}).click()])
 await Promise.all([page.waitForResponse(r=>r.url().includes('release_required=unknown')),page.getByRole('button',{name:'上线待判定',exact:true}).click()])
 const unknownQuery=new URLSearchParams(requests.filter(r=>r.p==='/api/research/work-items/page').at(-1).query)
 assert.equal(unknownQuery.get('release_required'),'unknown');assert.equal(unknownQuery.has('delivery'),false)
 await page.getByRole('button',{name:'全部',exact:true}).click();await page.getByRole('button',{name:'实时来源 Bug 1',exact:true}).waitFor()
 failing=true;await page.getByRole('button',{name:'搜索',exact:true}).click();await page.getByRole('button',{name:'重试查询',exact:true}).waitFor();assert.equal(await page.locator('tbody tr').count(),0,'silently returned cached rows')
 failing=false;await page.getByRole('button',{name:'重试查询',exact:true}).click();await page.getByRole('button',{name:'实时来源 Bug 1',exact:true}).waitFor()
 await page.getByRole('button',{name:'实时来源 Bug 1',exact:true}).click();await page.getByRole('heading',{name:'实时来源 Bug 1',exact:true}).waitFor()
 assert.equal(await page.getByRole('button',{name:'解决',exact:true}).isDisabled(),true,'public query identity enabled personal writes')
 await page.getByRole('link',{name:'验证本人来源账号并返回',exact:true}).waitFor()
 await page.getByRole('button',{name:'← 返回原列表',exact:true}).click();await page.getByRole('button',{name:'实时来源 Bug 1',exact:true}).waitFor()
 for(const width of [1440,900,700]){await page.setViewportSize({width,height:960});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false)}
 await page.setViewportSize({width:1440,height:960});await page.screenshot({path:'/tmp/research-live-qa.png',fullPage:true})
 assert.deepEqual(writes,['/api/research/work-items/attach']);assert.deepEqual(errors,[]);assert.deepEqual(unexpected,[])
 assert.equal(requests.some(r=>r.p==='/api/work-items/page'||r.p==='/api/work-items/people'||r.p==='/api/research/refresh-fields'),false)
 console.log('PASS live-only data route, unregistered rows, names/fields, pagination/cursor, explicit single attach, public-read write guard, no cached fallback, three widths; fixture only')
}finally{await browser.close()}
