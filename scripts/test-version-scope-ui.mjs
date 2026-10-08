// Planning-only browser regression against disposable QA data; never executes.
import assert from 'node:assert/strict';
const base=process.env.DELIVERY_UI_TEST_URL||'http://127.0.0.1:19823';
assert.equal(new URL(base).hostname,'127.0.0.1');
assert(!['9800','19803','19813'].includes(new URL(base).port));
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright');
const browser=await chromium.launch({headless:true,...(process.env.CHROME_PATH?{executablePath:process.env.CHROME_PATH}:{})});
const page=await browser.newPage({viewport:{width:1440,height:1000}}),errors=[];
page.on('pageerror',e=>errors.push(e.message));page.on('dialog',d=>d.accept());
await page.route('**/api/me',r=>r.fulfill({json:{username:'local',role:'admin'}}));
const read=async p=>{const r=await page.request.get(base+p);assert(r.ok(),await r.text());return r.json()};
const post=async(p,data)=>{const r=await page.request.post(base+p,{data});assert(r.ok(),await r.text());return r.json()};
const dialog=()=>page.getByRole('dialog').filter({visible:true});
const field=n=>dialog().locator('label').filter({hasText:new RegExp('^'+n)}).locator('input,textarea').first();
const select=async(n,o)=>{await dialog().locator('label').filter({hasText:new RegExp('^'+n)}).locator('.el-select').click();await page.getByRole('option',{name:o,exact:true}).click();await dialog().locator('.el-dialog__header').click()};
try{
 const env=(await read('/api/setup')).environments;assert(env.length===1&&env[0].name==='qa-ui');
 assert((await read('/api/work-items')).every(w=>w.title.startsWith('UI回归')));
 const suffix=Date.now().toString(36),prefix='UI回归范围-'+suffix;
 const a=await post('/api/work-items',{title:prefix+'A'}),b=await post('/api/work-items',{title:prefix+'B'});
 const own=await post('/api/changes',{kind:'manual',title:prefix+'独有',work_item_ids:[a.id]}),shared=await post('/api/changes',{kind:'manual',title:prefix+'共享',work_item_ids:[a.id,b.id]}),manual=await post('/api/changes',{kind:'manual',title:prefix+'手工',work_item_ids:[]});
 await page.goto(base+'/ui#/changes?tab=versions');await page.getByRole('button',{name:'＋ 创建发布版本'}).click();
 await field('标题').fill(prefix);await field('版本号').fill('scope-'+suffix);await dialog().getByRole('button',{name:'保存',exact:true}).click();
 await page.getByRole('button',{name:'编辑范围'}).waitFor();const v=(await read('/api/release-versions')).find(v=>v.title===prefix);assert.equal((v.changes||[]).length,0);
 const blocked=page.waitForResponse(r=>r.url().endsWith(`/api/release-versions/${v.id}/ready`));await page.getByRole('button',{name:'冻结并标记就绪'}).click();assert.equal((await blocked).status(),409);
 await page.getByRole('button',{name:'编辑范围'}).click();await select('选择需求 / BUG',a.title);await select('选择需求 / BUG',b.title);await select('手工补入变更','人工操作 · '+manual.title);
 assert.equal(await dialog().locator('.selection p').count(),3);await dialog().getByRole('button',{name:'保存',exact:true}).click();await dialog().waitFor({state:'hidden'});
 await page.reload();await page.getByRole('button',{name:'编辑范围'}).click();await select('选择需求 / BUG',a.title);
 assert.equal(await dialog().locator('.selection p').count(),2,'Deselecting demand kept an exclusive change');
 await dialog().getByRole('button',{name:'保存',exact:true}).click();await dialog().waitFor({state:'hidden'});await page.waitForLoadState('networkidle');
 const saved=(await read('/api/release-versions')).find(x=>x.id===v.id);assert.deepEqual(saved.work_items.map(w=>w.id),[b.id]);assert.deepEqual(saved.changes.map(c=>c.id),[shared.id,manual.id]);assert.deepEqual(saved.manual_change_ids,[manual.id]);
 await post(`/api/changes/${shared.id}/ready`,{});await post(`/api/changes/${manual.id}/ready`,{});await post(`/api/release-versions/${v.id}/ready`,{});
 for(let i=0;i<2;i++)await post('/api/delivery/templates',{name:prefix+'编排'+i,mode:'version',nodes:[{id:'verify',name:'人工验收',kind:'manual',instructions:'仅回归，不执行'}]});
 await page.goto(base+`/ui#/orchestration?version=${v.id}`);await page.getByRole('dialog',{name:'选择交付编排',exact:true}).waitFor();assert.equal(await page.getByRole('dialog',{name:'发起交付 · 范围与执行预览',exact:true}).count(),0,'First template was implicitly selected');
 await page.getByRole('dialog',{name:'选择交付编排',exact:true}).getByRole('button',{name:'取消',exact:true}).click();await page.getByRole('button',{name:'← 返回版本'}).click();await page.locator('.workspace h2').filter({hasText:prefix}).waitFor();
 assert.equal(await page.getByRole('button',{name:'退回草稿',exact:true}).count(),0,'Frozen change shows an unusable reopen action');
 await page.locator('.workspace').screenshot({path:'runtime/version-workspace-ui-qa.png',animations:'disabled'});
 for(const width of [1440,900,700]){await page.setViewportSize({width,height:1000});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false)}
 assert.deepEqual(errors,[]);
 console.log(JSON.stringify({ok:true,checks:['empty draft','freeze rejects empty scope','multi-demand union','deselect removes exclusive but preserves shared/manual','provenance survives refresh','explicit template choice','return to original version','three widths','no execution','no browser errors']}));
}finally{await browser.close()}
