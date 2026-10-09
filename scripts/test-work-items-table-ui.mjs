// Fixture-only, read-only UI QA: no issue synchronization or external writes.
import assert from 'node:assert/strict';
import {readFile,mkdir} from 'node:fs/promises';
import path from 'node:path';
const base=process.env.CONSOLE_UI_TEST_URL||'http://127.0.0.1:9800';
assert.equal(new URL(base).hostname,'127.0.0.1');
const dist=process.env.CONSOLE_UI_DIST;
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright');
const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH});
const page=await browser.newPage({viewport:{width:1440,height:960}}),errors=[],writes=[],requests=[];
page.on('pageerror',e=>errors.push(e.message));
const items=Array.from({length:621},(_,i)=>({id:621-i,source:'zentao:QA',external_id:String(30000+i),kind:'bug',title:`菜单 BUG ${621-i} ${i===0?'长标题'.repeat(50):''}`,status:i%2?'open':'closed',external_status:i%2?'active':'closed',owner:'sync-admin',assigned_to:i%2?'alice':'bob',resolved_by:i%2?'':'alice',opened_by:'carol',closed_by:i%2?'':'reviewer',description:'<p>详情说明</p>',updated_at:1791511200}));
await page.route('**/*',async route=>{
 const r=route.request(),u=new URL(r.url()),p=u.pathname;
 if(u.origin!==new URL(base).origin)return route.abort();
 if(!['GET','HEAD','OPTIONS'].includes(r.method())){writes.push(p);return route.abort()}
 if(p.endsWith('/api/work-items/page')){
  requests.push(u.searchParams);
  const person=u.searchParams.get('person'),role=u.searchParams.get('person_role'),q=u.searchParams.get('q'),status=u.searchParams.get('status');
  let matched=items.filter(v=>(!q||v.title.includes(q))&&(!status||v.status===status||v.external_status===status)&&(!person||(role==='related'?['assigned_to','resolved_by','opened_by','closed_by'].some(k=>v[k]===person):v[role]===person)));
  const size=Number(u.searchParams.get('page_size'))||20,pages=Math.max(1,Math.ceil(matched.length/size)),num=Math.min(Number(u.searchParams.get('page'))||1,pages);
  return route.fulfill({json:{items:matched.slice((num-1)*size,num*size),total:matched.length,page:num,page_size:size}});
 }
 if(p.endsWith('/api/work-items/people'))return route.fulfill({json:['alice','bob','carol','reviewer']});
 if(/\/api\/work-items\/\d+$/.test(p))return route.fulfill({json:items.find(v=>v.id===Number(p.split('/').at(-1)))});
 if(p.endsWith('/api/work-items'))return route.fulfill({json:items.slice(0,500)});
 if(p.endsWith('/api/me'))return route.fulfill({json:{username:'alice',role:'member',auth_enabled:true}});
 if(['/api/changes','/api/release-versions','/api/delivery/runs','/api/work-item-sync','/api/work-item-sources'].some(end=>p.endsWith(end)))return route.fulfill({json:[]});
 if(p.endsWith('/delivery-content'))return route.fulfill({json:{changes:[],digest:'fixture'}});
 if(dist){
  let file;if(p==='/')file='shell.html';else if(/\/assets\/[^/]+\.(?:js|css)$/.test(p))file=`assets/${path.basename(p)}`;else if(p==='/api/plugins/deploy/ui')file='deploy.html';
  if(file)return route.fulfill({body:await readFile(path.join(dist,file)),contentType:file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html'});
 }
 return route.continue();
});
const ui=page.frameLocator('iframe[src*="/api/plugins/deploy/ui"]');
async function select(label,text){const box=ui.getByRole('combobox',{name:label,exact:true});await box.locator('xpath=ancestor::div[contains(@class,"el-select__wrapper")]').locator('.el-select__suffix').click();const id=await box.getAttribute('aria-controls');await ui.locator('#'+id).getByRole('option',{name:text,exact:true}).click()}
const visibleRows=ui.locator('.work-table tbody tr:has(.work-title)');
try{
 await page.goto(base);await ui.getByRole('link',{name:'变更',exact:true}).click();await ui.getByText('共 621 条',{exact:true}).waitFor();assert.equal(await visibleRows.count(),20);
 assert.equal(await ui.locator('.work-list').getByText('详情说明',{exact:true}).count(),0);
 await ui.getByRole('button',{name:'末页',exact:true}).click();await ui.getByText('32 / 32 页',{exact:true}).waitFor();await visibleRows.first().waitFor();assert.equal(await visibleRows.count(),1);
 await ui.locator('.work-title').first().click();await ui.getByRole('heading',{name:items.at(-1).title,exact:true}).waitFor();await ui.getByRole('button',{name:'← 返回列表',exact:true}).click();await ui.getByText('32 / 32 页',{exact:true}).waitFor();
 await ui.getByRole('button',{name:'只看我的',exact:true}).click();await ui.getByText('共 621 条',{exact:true}).waitFor();assert.equal(requests.at(-1).get('person'),'alice');assert.equal(requests.at(-1).get('person_role'),'related');
 await select('人员关系','指派给');await ui.getByText('共 310 条',{exact:true}).waitFor();assert((await visibleRows.allTextContents()).every(v=>v.includes('alice')));
 await select('人员关系','由谁解决');await ui.getByText('共 311 条',{exact:true}).waitFor();
 await ui.getByRole('button',{name:'清空筛选',exact:true}).click();await ui.getByText('共 621 条',{exact:true}).waitFor();
 await ui.getByLabel('搜索需求与 BUG').fill('菜单 BUG 1 ');await ui.getByText('共 1 条',{exact:true}).waitFor();
 await ui.getByRole('button',{name:'交付内容',exact:true}).click();const editor=ui.getByRole('dialog',{name:/交付内容/});await editor.waitFor();await editor.getByRole('button',{name:'关闭',exact:true}).click();await editor.waitFor({state:'hidden'});await ui.getByText('共 1 条',{exact:true}).waitFor();
 await ui.getByRole('button',{name:'清空筛选',exact:true}).click();await ui.getByText('共 621 条',{exact:true}).waitFor();
 await select('每页条数','50 条 / 页');await ui.getByText('1 / 13 页',{exact:true}).waitFor();await visibleRows.nth(49).waitFor();assert.equal(await visibleRows.count(),50);
 await mkdir('/tmp/devops-work-table-ui',{recursive:true});
 for(const theme of ['light','dark']){
  await page.evaluate(t=>{localStorage.setItem('lp-theme',t);document.documentElement.classList.toggle('dark',t==='dark')},theme);
  await ui.locator('html').evaluate((el,t)=>{el.classList.toggle('dark',t==='dark');el.setAttribute('data-theme',t)},theme);
  for(const width of [1440,900,700,390]){
   await page.setViewportSize({width,height:960});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);assert.equal(await ui.locator('body').evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
   await page.waitForTimeout(250);
   await page.screenshot({path:`/tmp/devops-work-table-ui/table-${theme}-${width}.png`});
  }
 }
 assert.deepEqual(errors,[]);assert.deepEqual(writes,[]);console.log('PASS: 621 records, server pagination, personnel filters, detail/editor continuity, light/dark responsive table; no writes');
}finally{await browser.close()}
