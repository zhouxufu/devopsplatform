// Candidate selection and version saves are mocked. No source tracker, build,
// deployment or user database is changed.
import assert from 'node:assert/strict'
import {readFile,mkdir} from 'node:fs/promises'
import path from 'node:path'
const base=process.env.CONSOLE_UI_TEST_URL||'http://127.0.0.1:9800',dist=process.env.CONSOLE_UI_DIST
assert.equal(new URL(base).hostname,'127.0.0.1')
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright')
const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH})
const page=await browser.newPage({viewport:{width:1440,height:1000}}),errors=[],writes=[],unexpected=[],queries=[],previews=[]
page.on('pageerror',e=>errors.push(e.message));page.on('dialog',d=>d.accept())
function work(id){return {id,external_id:String(1000+id),source:'tracker:qa',kind:'bug',title:'Candidate '+id,status:'closed',external_status:'closed',resolved_by:'alice',updated_at:1700000000,change_count:id===2?0:1,ready_count:id===2?0:1,planned_count:id===601?1:0,successful_delivery:id===901}}
const change={id:99,title:'Shared implementation',kind:'code',status:'ready',service:'orders',work_items:[work(1),work(601),work(901)]}
const existing={id:12,version:'old-draft',title:'Existing draft',status:'draft',owner:'alice',work_items:[work(901)],manual_change_ids:[99],changes:[change]}
let saved,slow=false,releaseSlow,missing=false
await page.route('**/*',async route=>{
 const r=route.request(),u=new URL(r.url()),method=r.method(),p=u.pathname.replace(/^\/api\/plugins\/[^/]+(?=\/api\/)/,'')
 if(u.origin!==new URL(base).origin)return route.abort()
 const reply=json=>route.fulfill({json})
 if(dist){let file;if(u.pathname==='/')file='shell.html';else if(/\/assets\/[^/]+\.(js|css)$/.test(u.pathname))file='assets/'+path.basename(u.pathname);else if(/\/api\/plugins\/deploy\/ui\/?$/.test(u.pathname))file='deploy.html';if(file)return route.fulfill({body:await readFile(path.join(dist,file)),contentType:file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html'})}
 if(!p.includes('/api/')||!['fetch','xhr'].includes(r.resourceType()))return route.continue()
 if(!['GET','HEAD','OPTIONS'].includes(method)){
  const body=r.postDataJSON()
  if(p==='/api/release-versions/scope-preview'){previews.push(body);return reply(body.work_item_ids.length||body.manual_change_ids.length?[change]:[])}
  if(p==='/api/release-versions'){writes.push(body);saved={...body,id:13,status:'draft',owner:'alice',work_items:body.work_item_ids.map(work),manual_change_ids:body.manual_change_ids,changes:[change]};return reply(saved)}
  unexpected.push(p);return route.abort()
 }
 if(p==='/api/me')return reply({username:'alice',role:'member',auth_enabled:true})
 if(p==='/api/environments')return reply([{name:'test',display_name:'测试'},{name:'prod',display_name:'生产'}])
 if(p==='/api/services')return reply([{name:'orders'}])
 if(p==='/api/work-items')return reply([work(1),work(2)]) // Parent only knows two records; 601/901 must be fetched.
 if(p==='/api/changes')return reply([change])
 if(p==='/api/release-versions')return reply([existing,...(saved?[saved]:[])])
 if(p==='/api/release-versions/12')return reply(existing)
 if(p==='/api/release-versions/13')return reply(saved)
 if(p==='/api/release-versions/page')return reply({items:[],page:1,page_size:20,total:0})
 if(p==='/api/work-items/people')return reply(['alice','bob'])
 if(p==='/api/work-items/candidate-sources')return reply(['tracker:qa','tracker:other','manual'])
 if(p==='/api/work-items/candidates/selected'){const ids=(u.searchParams.get('ids')||'').split(',').filter(Boolean).map(Number);return reply({items:missing?[]:ids.map(work),total:ids.length,page:1,page_size:ids.length})}
 if(p==='/api/work-items/candidates'){
  const q=Object.fromEntries(u.searchParams);queries.push(q)
  if(slow){slow=false;await new Promise(resolve=>{releaseSlow=resolve});return reply({items:[work(777)],total:1,page:1,page_size:20})}
  const index=Number(q.page||1);return reply({items:q.q?[work(2)]:index===2?[work(601),work(602)]:[work(1),work(2)],total:621,page:index,page_size:20})
 }
 if(/^\/api\/work-items\/\d+$/.test(p))return reply({...work(Number(p.split('/').at(-1))),description:'<p>Safe preview</p><script>alert(1)</script>',description_format:'html'})
 return reply([])
})
const button=(surface,name)=>surface.getByRole('button',{name,exact:true})
const editor=()=>page.getByRole('dialog',{name:'发布版本',exact:true}),picker=()=>page.getByRole('dialog',{name:'选择需求与 BUG',exact:true})
async function choose(surface,label,option){assert.equal(label,'候选来源');const control=surface.locator('.picker-filters').first().locator('.el-select').nth(2);await control.click();const id=await control.getByRole('combobox').getAttribute('aria-controls');await page.locator('#'+id).getByRole('option',{name:option,exact:true}).click()}
try{
 await page.goto(base+'/api/plugins/deploy/ui#/changes?tab=versions')
 await page.getByRole('button',{name:/创建发布版本/}).click();await editor().locator('label').filter({hasText:/^标题/}).locator('input').fill('Release selection QA');await editor().getByPlaceholder('例如 2026.10.1').fill('qa-v1')
 await button(editor(),'选择需求与 BUG').click();await picker().waitFor();await page.waitForTimeout(350)
 assert.equal(await editor().isVisible(),false,'two stacked dialogs');assert.equal(await page.getByRole('dialog').count(),1)
 assert.equal(queries.at(-1).kind,'bug');assert.equal(queries.at(-1).status,'closed');assert.equal(queries.at(-1).delivery,'not_recorded');assert.equal(queries.at(-1).environment,'prod');assert.equal(queries.at(-1).start,undefined,'default date hides older candidates')
 await picker().getByRole('checkbox',{name:'选择 1001',exact:true}).check();await button(picker(),'下一页').click();await picker().getByRole('checkbox',{name:'选择 1601',exact:true}).check()
 await button(picker(),'只看我的').click();await page.waitForTimeout(350);assert.equal(queries.at(-1).person,'alice');assert.equal(queries.at(-1).person_role,'related')
 assert.equal(await picker().getByRole('complementary',{name:'跨页已选清单'}).locator('article').count(),2)
 await button(picker(),'确认选择 2 项').click();await editor().waitFor();await page.waitForTimeout(350)
 assert.equal(await editor().getByPlaceholder('例如 2026.10.1').inputValue(),'qa-v1');assert.equal(await editor().locator('label').filter({hasText:/^标题/}).locator('input').inputValue(),'Release selection QA')
 assert.deepEqual(previews.at(-1).work_item_ids,[1,601]);assert.equal(await editor().locator('.scope-preview-scroll p').count(),1,'shared change not deduplicated')
 await button(editor(),'选择需求与 BUG').click();await picker().waitFor();await picker().getByRole('complementary').locator('article').filter({hasText:'Candidate 601'}).waitFor()
 await button(picker(),'清空').click();await button(picker(),'取消，保留原选择').click();await editor().waitFor();assert((await editor().innerText()).includes('已选 2 项'),'cancel modified original IDs')
 await button(editor(),'选择需求与 BUG').click();await picker().waitFor();await choose(picker(),'候选来源','tracker:qa');await picker().getByText('日期 / 编号清单',{exact:true}).click();await picker().getByPlaceholder(/批量外部编号/).fill('1001,1601');await button(picker(),'按编号清单筛选').click();await page.waitForTimeout(350);assert.equal(queries.at(-1).numbers,'1001,1601');assert.equal(queries.at(-1).source,'tracker:qa')
 await button(picker(),'取消，保留原选择').click();await button(editor(),'保存').click();await page.getByRole('heading',{name:'qa-v1 · Release selection QA',exact:true}).waitFor()
 assert.deepEqual(writes[0].work_item_ids,[1,601]);assert.equal(writes.length,1);assert.deepEqual(unexpected,[])
 await page.goto(base+'/api/plugins/deploy/ui#/changes?tab=versions&item=12');await button(page,'编辑范围').click();await button(editor(),'选择需求与 BUG').click();await picker().getByRole('complementary').getByText(/Candidate 901/).waitFor()
 await page.waitForFunction(()=>[...document.querySelectorAll('button')].some(b=>b.textContent.trim()==='确认选择 1 项'&&!b.disabled))
 assert.equal(await button(picker(),'确认选择 1 项').isEnabled(),true,'selected item outside filters lost')
 slow=true;const pending=page.waitForRequest(r=>r.url().includes('/work-items/candidates?')&&r.url().includes('q=slow'));await picker().getByPlaceholder('搜索标题、外部编号或 #平台编号').fill('slow');await pending;await button(picker(),'取消，保留原选择').click();await button(editor(),'选择需求与 BUG').click();await picker().waitFor();await picker().getByPlaceholder('搜索标题、外部编号或 #平台编号').fill('fresh');releaseSlow();await picker().getByRole('checkbox',{name:'选择 1002',exact:true}).waitFor();assert.equal(await picker().getByRole('checkbox',{name:'选择 1777',exact:true}).count(),0,'old session overwrote candidates')
 for(const theme of ['light','dark']){await page.evaluate(t=>{document.documentElement.dataset.theme=t;document.documentElement.classList.toggle('dark',t==='dark')},theme);for(const width of [1440,900,700,390]){await page.setViewportSize({width,height:1000});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false,'overflow '+theme+' '+width)}}
 await page.setViewportSize({width:1440,height:1000});await mkdir('/tmp/devops-version-picker-ui',{recursive:true});await page.screenshot({path:'/tmp/devops-version-picker-ui/picker.png'})
 await button(picker(),'取消，保留原选择').click();missing=true;await button(editor(),'选择需求与 BUG').click();await picker().getByText('记录已失效，请移除',{exact:true}).waitFor();assert.equal(await button(picker(),'确认选择 1 项').isDisabled(),true,'missing item allowed');await picker().getByRole('button',{name:'移除 #901',exact:true}).click();assert(await button(picker(),'确认选择 0 项').isEnabled())
 assert.deepEqual(errors,[]);assert.deepEqual(unexpected,[]);console.log(JSON.stringify({ok:true,mockOnly:true,checks:['single modal','closed BUG and no production record defaults','no guessed date','621 rows server pagination','cross-page selection','person shortcut','source-scoped number list','parent input continuity','cancel rollback','metadata preview deduplication','selection beyond old 500 cache','old session isolation','missing ID guard','four widths and light/dark','no execution']}))
}finally{await browser.close()}
