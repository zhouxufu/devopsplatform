// Fixture-only QA: all business mutations are intercepted; never execute external changes.
import assert from 'node:assert/strict';
import {readFile,mkdir} from 'node:fs/promises';
import path from 'node:path';
const base=process.env.CONSOLE_UI_TEST_URL||'http://127.0.0.1:9800',dist=process.env.CONSOLE_UI_DIST;
assert.equal(new URL(base).hostname,'127.0.0.1');
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright');
const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH});
const page=await browser.newPage({viewport:{width:1440,height:960}}),errors=[],writes=[],requests=[];
page.on('pageerror',e=>errors.push(e.message));
const changes=Array.from({length:621},(_,i)=>({id:621-i,kind:i%3===0?'mysql':i%3===1?'code':'nacos',title:`变更 ${621-i}${i===0?'长标题'.repeat(30):''}`,service:i%2?'app-b':'app-a',status:i%2?'ready':'draft',owner:i%2?'bob':'alice',work_items:i%2?[{id:1,kind:'bug',external_id:'30001',title:'关联 BUG'}]:[],updated_at:1791511200,frozen:i===1,data_sync:i===3}));
const versions=Array.from({length:621},(_,i)=>({id:621-i,version:`v${621-i}`,title:`发布版本 ${621-i}`,status:i%2?'ready':'draft',owner:i%2?'bob':'alice',updated_at:1791511200,application_count:2,change_count:3,work_item_count:1,work_items:[]}));
function fullChange(v){return {...v,summary:'DETAIL BODY ONLY',reference:v.kind==='code'?'feature/menu':'',sql:v.kind==='mysql'?{resource_alias:'main',content:'UPDATE sys_menu SET name=\'TEST ONLY\' WHERE id=1;',max_affected_rows:1}:undefined,nacos:v.kind==='nacos'?{resource_alias:'config',data_id:'app.yaml',entries:[{key:'feature.menu',value:'true',action:'set'}]}:undefined}}
function fullVersion(v){return {...v,description:'VERSION DETAIL ONLY',work_items:[{id:1,title:'关联 BUG'}],manual_change_ids:[17],changes:[fullChange(changes[0])],promotions:[]}}
await page.route('**/*',async route=>{
 const r=route.request(),u=new URL(r.url()),p=u.pathname;
 if(u.origin!==new URL(base).origin)return route.abort();
 if(!['GET','HEAD','OPTIONS'].includes(r.method())){writes.push({method:r.method(),path:p,body:r.postDataJSON()});if(r.method()==='PUT'&&p.endsWith('/api/release-versions/621'))return route.fulfill({json:fullVersion(versions[0])});return route.abort()}
 if(/\/api\/(changes|release-versions)\/page$/.test(p)){
  const type=p.includes('/changes/')?'changes':'versions';requests.push({type,q:u.searchParams});
  const q=u.searchParams.get('q')||'',owner=u.searchParams.get('owner'),status=u.searchParams.get('status'),kind=u.searchParams.get('kind'),service=u.searchParams.get('service'),association=u.searchParams.get('association');
  const matched=(type==='changes'?changes:versions).filter(v=>(!q||`${v.title} ${v.version||''}`.includes(q))&&(!owner||v.owner===owner)&&(!status||v.status===status)&&(!kind||v.kind===kind)&&(!service||v.service===service||type==='versions')&&(!association||(association==='linked'?v.work_items?.length:!v.work_items?.length)));
  const size=Number(u.searchParams.get('page_size'))||20,num=Math.min(Number(u.searchParams.get('page'))||1,Math.max(1,Math.ceil(matched.length/size)));
  return route.fulfill({json:{items:matched.slice((num-1)*size,num*size),total:matched.length,page:num,page_size:size}});
 }
 if(/\/api\/(changes|release-versions)\/owners$/.test(p))return route.fulfill({json:['alice','bob']});
 if(/\/api\/changes\/\d+$/.test(p))return route.fulfill({json:fullChange(changes.find(v=>v.id===Number(p.split('/').at(-1))))});
 if(/\/api\/release-versions\/\d+$/.test(p))return route.fulfill({json:fullVersion(versions.find(v=>v.id===Number(p.split('/').at(-1))))});
 if(p.endsWith('/api/changes'))return route.fulfill({json:changes.slice(0,500).map(fullChange)});
 if(p.endsWith('/api/release-versions'))return route.fulfill({json:versions.slice(0,500).map(fullVersion)});
 if(p.endsWith('/api/services'))return route.fulfill({json:[{name:'app-a'},{name:'app-b'}]});
 if(p.endsWith('/api/me'))return route.fulfill({json:{username:'alice',role:'member',auth_enabled:true}});
 if(p.endsWith('/api/work-items/page'))return route.fulfill({json:{items:[],page:1,page_size:20,total:0}});
 if(p.endsWith('/api/work-items/1'))return route.fulfill({json:{id:1,title:'关联 BUG',kind:'bug',status:'closed',owner:'bob'}});
 if(p.startsWith('/api/plugins/deploy/api/'))return route.fulfill({json:[]});
 if(dist){let file;if(p==='/')file='shell.html';else if(/\/assets\/[^/]+\.(?:js|css)$/.test(p))file=`assets/${path.basename(p)}`;else if(p==='/api/plugins/deploy/ui')file='deploy.html';if(file)return route.fulfill({body:await readFile(path.join(dist,file)),contentType:file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html'})}
 return route.continue();
});
const ui=page.frameLocator('iframe[src*="/api/plugins/deploy/ui"]'),list=ui.locator('.delivery-list:visible'),rows=list.locator('tbody tr:has(.title-link)');
async function select(label,text){const box=list.getByRole('combobox',{name:label,exact:true});await box.locator('xpath=ancestor::div[contains(@class,"el-select__wrapper")]').locator('.el-select__suffix').click();const id=await box.getAttribute('aria-controls');await ui.locator('#'+id).getByRole('option',{name:text,exact:true}).click()}
async function count(total){await list.getByText(`共 ${total} 条`,{exact:true}).waitFor();await rows.first().waitFor()}
async function rowCount(expected){for(let i=0;i<100;i++){if(await rows.count()===expected)return;await page.waitForTimeout(25)}assert.equal(await rows.count(),expected)}
try{
 await page.goto(base);await ui.getByRole('link',{name:'变更',exact:true}).click();await ui.locator('.tabs').getByRole('button',{name:'变更列表',exact:true}).click();await count(621);assert.equal(await rows.count(),20);
 assert.equal(await list.getByText('DETAIL BODY ONLY',{exact:true}).count(),0);assert.equal(await list.locator('pre').count(),0);
 await list.getByRole('button',{name:'末页',exact:true}).click();await list.getByText('32 / 32 页',{exact:true}).waitFor();await rowCount(1);
 await rows.first().locator('.title-link').click();await ui.getByRole('heading',{name:'变更 1',exact:true}).waitFor();await ui.getByText('DETAIL BODY ONLY',{exact:true}).waitFor();assert.equal(await ui.locator('.workspace pre').count(),0);
 await ui.getByRole('button',{name:'← 返回列表',exact:true}).click();await list.getByText('32 / 32 页',{exact:true}).waitFor();
 await list.getByRole('button',{name:'只看我的',exact:true}).click();await count(311);assert.equal(requests.at(-1).q.get('owner'),'alice');
 await select('筛选应用','app-a');await count(311);await select('变更类型','SQL / 数据同步');await count(104);await select('记录状态','草稿');await count(104);await select('关联关系','独立变更');await count(104);
 await list.getByLabel('搜索变更',{exact:true}).fill('变更 621');await count(1);await rows.first().locator('.title-link').click();await ui.locator('.workspace pre').getByText(/UPDATE sys_menu/).waitFor();
 await ui.getByRole('button',{name:'← 返回列表',exact:true}).click();await count(1);assert.equal(await list.getByLabel('搜索变更',{exact:true}).inputValue(),'变更 621');
 await ui.locator('.tabs').getByRole('button',{name:'发布版本',exact:true}).click();await count(621);assert.equal(await list.getByText('VERSION DETAIL ONLY',{exact:true}).count(),0);assert.equal(await list.getByText('2 应用 · 1 工作项 · 3 变更',{exact:true}).count(),20);assert(await list.getByText('已冻结',{exact:true}).count()>0);
 await list.getByRole('button',{name:'末页',exact:true}).click();await list.getByText('32 / 32 页',{exact:true}).waitFor();await rows.first().locator('.title-link').click();await ui.getByRole('heading',{name:'v1 · 发布版本 1',exact:true}).waitFor();await ui.getByRole('button',{name:'← 返回列表',exact:true}).click();await list.getByText('32 / 32 页',{exact:true}).waitFor();
 await ui.locator('.tabs').getByRole('button',{name:'变更列表',exact:true}).click();await count(1);assert.equal(await list.getByLabel('搜索变更',{exact:true}).inputValue(),'变更 621');await list.getByRole('button',{name:'清空筛选',exact:true}).click();await count(621);
 await rows.nth(0).getByRole('checkbox').check();await rows.nth(2).getByRole('checkbox').check();await list.getByRole('button',{name:'批量纳入版本',exact:true}).click();const modal=ui.getByRole('dialog',{name:'纳入发布版本'});await modal.waitFor();
 await modal.getByRole('button',{name:'加载更多草稿版本（已加载 100 / 311）',exact:true}).click();await modal.getByRole('button',{name:'加载更多草稿版本（已加载 200 / 311）',exact:true}).waitFor();
 const box=modal.getByRole('combobox');
 async function chooseDraft(label){await box.locator('xpath=ancestor::div[contains(@class,"el-select__wrapper")]').locator('.el-select__suffix').click();const id=await box.getAttribute('aria-controls');await ui.locator('#'+id).getByRole('option',{name:label,exact:true}).click()}
 await modal.getByRole('textbox').fill('v1');await chooseDraft('v1 · 发布版本 1');assert((await box.locator('xpath=ancestor::div[contains(@class,"el-select__wrapper")]').innerText()).includes('v1 · 发布版本 1'));
 await modal.getByRole('textbox').fill('v621');await chooseDraft('v621 · 发布版本 621');await modal.getByRole('button',{name:'确认纳入',exact:true}).click();await modal.waitFor({state:'hidden'});await count(621);
 assert.equal(writes.length,1);assert.equal(writes[0].method,'PUT');assert.deepEqual(writes[0].body.manual_change_ids,[17,621,619]);assert.deepEqual(writes[0].body.work_item_ids,[1]);
 await select('每页条数','50 条 / 页');await list.getByText('1 / 13 页',{exact:true}).waitFor();await rowCount(50);
 await list.locator('.table-scroll').evaluate(e=>{e.scrollTop=350;e.scrollLeft=80});const before=await list.locator('.table-scroll').evaluate(e=>({top:e.scrollTop,left:e.scrollLeft}));await rows.nth(12).locator('.title-link').click();await ui.locator('.workspace').waitFor();await ui.getByRole('button',{name:'← 返回列表',exact:true}).click();await count(621);await page.waitForTimeout(100);assert.deepEqual(await list.locator('.table-scroll').evaluate(e=>({top:e.scrollTop,left:e.scrollLeft})),before);
 await mkdir('/tmp/devops-delivery-record-tables',{recursive:true});
 for(const tab of ['变更列表','发布版本']){await ui.locator('.tabs').getByRole('button',{name:tab,exact:true}).click();await list.locator('.records-table').waitFor();for(const theme of ['light','dark']){await page.evaluate(t=>{localStorage.setItem('lp-theme',t);document.documentElement.classList.toggle('dark',t==='dark')},theme);await ui.locator('html').evaluate((el,t)=>{el.classList.toggle('dark',t==='dark');el.setAttribute('data-theme',t)},theme);for(const width of [1440,900,700,390]){await page.setViewportSize({width,height:960});await page.waitForTimeout(100);assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);assert.equal(await ui.locator('body').evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);await page.screenshot({path:`/tmp/devops-delivery-record-tables/${tab}-${theme}-${width}.png`})}}}
 assert.deepEqual(errors,[]);console.log('PASS: changes/versions tables, 621 records, server filters, pagination, detail/tab/scroll continuity, atomic batch scope, responsive light/dark; one fixture-only PUT, no execution');
}finally{await browser.close()}
