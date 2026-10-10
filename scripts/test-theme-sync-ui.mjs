// Exercise the real theme switch. No manual CSS classes or synthetic storage events.
import assert from 'node:assert/strict';
import {readFile,mkdir} from 'node:fs/promises';
import path from 'node:path';
const base=process.env.CONSOLE_UI_TEST_URL||'http://127.0.0.1:9800',dist=process.env.CONSOLE_UI_DIST;
assert.equal(new URL(base).hostname,'127.0.0.1');
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright');
const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH}),context=await browser.newContext({viewport:{width:1440,height:960}}),page=await context.newPage(),errors=[],writes=[];
context.on('page',p=>p.on('pageerror',e=>errors.push(e.message)));
page.on('pageerror',e=>errors.push(e.message));
await context.route('**/*',async route=>{
 const r=route.request(),u=new URL(r.url()),p=u.pathname;
 if(u.origin!==new URL(base).origin)return route.abort();
 if(!['GET','HEAD','OPTIONS'].includes(r.method())){writes.push(r.method()+' '+p);return route.abort()}
 if(dist){let file;if(p==='/')file='shell.html';else if(/\/assets\/[^/]+\.(js|css)$/.test(p))file='assets/'+path.basename(p);else if(/^\/api\/plugins\/(deploy|dbms|redis|logs|monitor)\/ui\/?$/.test(p)){const plugin=p.split('/')[3];file=(['dbms','redis'].includes(plugin)?'workbench':plugin)+'.html'}
  if(file)return route.fulfill({body:await readFile(path.join(dist,file)),contentType:file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html'});
  if(p==='/api/plugins/devtools/ui')return route.fulfill({body:await readFile(new URL('../internal/devtools/assets/ui.html',import.meta.url)),contentType:'text/html'});
 }
 return route.continue();
});
async function waitTheme(surface,dark){await surface.locator('html').waitFor({state:'attached'});for(let i=0;i<100;i++){const v=await surface.locator('html').evaluate(e=>({dark:e.classList.contains('dark'),scheme:getComputedStyle(e).colorScheme}));if(v.dark===dark&&v.scheme===(dark?'dark':'light'))return;await page.waitForTimeout(25)}assert.deepEqual(await surface.locator('html').evaluate(e=>({dark:e.classList.contains('dark'),scheme:getComputedStyle(e).colorScheme})),{dark,scheme:dark?'dark':'light'},'document did not follow theme')}
const plugin=id=>page.frameLocator(`iframe[src*="/api/plugins/${id}/ui"]`);
async function openPlugin(label,id){await page.getByRole('button',{name:'打开插件工作区',exact:true}).click();await page.locator('.pm').getByRole('button',{name:label,exact:true}).click();await plugin(id).locator('.layout').waitFor()}
async function toggle(dark){await page.getByRole('button',{name:dark?'切换深色主题':'切换浅色主题',exact:true}).click();await waitTheme(page,dark)}
const checked=[];
try{
 await page.goto(base);await plugin('deploy').getByRole('heading',{name:'基础构建',exact:true}).waitFor();
 if(await page.locator('html').evaluate(e=>e.classList.contains('dark')))await toggle(false);
 await waitTheme(plugin('deploy'),false);
 for(const [label,id] of [['数据库工作台','dbms'],['Redis 工作台','redis'],['日志查询','logs'],['监控','monitor']]){const options=await page.request.get(base+'/api/plugins').then(r=>r.json());const p=options.find(p=>p.name===id);assert(p,'test requires activated '+id);await openPlugin(p.display||label,id);await waitTheme(plugin(id),false)}
 const standalone=await context.newPage(),secondShell=await context.newPage();await standalone.goto(base+'/api/plugins/dbms/ui');await standalone.locator('.layout').waitFor();await secondShell.goto(base);await secondShell.frameLocator('iframe[src*="/api/plugins/deploy/ui"]').locator('.layout').waitFor();
 const records=Object.fromEntries(await Promise.all(['deploy','dbms','redis','logs','monitor'].map(async id=>[id,await plugin(id).locator('html').evaluate(()=>({href:location.href,started:performance.timeOrigin}))])));
 for(const dark of [true,false,true]){
  await toggle(dark);for(const id of ['deploy','dbms','redis','logs','monitor'])await waitTheme(plugin(id),dark);await waitTheme(standalone,dark);await waitTheme(secondShell,dark);
  await secondShell.getByRole('button',{name:dark?'切换浅色主题':'切换深色主题',exact:true}).waitFor();
  const colors=await plugin('dbms').locator('html').evaluate(()=>({body:getComputedStyle(document.body).backgroundColor,panel:getComputedStyle(document.documentElement).getPropertyValue('--panel').trim(),ep:getComputedStyle(document.documentElement).getPropertyValue('--el-bg-color').trim()}));assert.equal(colors.body,dark?'rgb(16, 23, 34)':'rgb(243, 245, 249)');assert.equal(colors.panel,dark?'#182230':'#ffffff');assert.equal(colors.ep,colors.panel);checked.push(dark?'dark':'light');
 }
 // Switching themes must not reload or navigate any kept-alive workspace.
 for(const id of Object.keys(records))assert.deepEqual(await plugin(id).locator('html').evaluate(()=>({href:location.href,started:performance.timeOrigin})),records[id]);
 await page.locator('.wstab').filter({hasText:'部署平台'}).first().click();await plugin('deploy').locator('.platform-nav button').filter({hasText:'MCP'}).click();const panel=page.getByRole('dialog',{name:'MCP',exact:true});await panel.waitFor();const mcp=page.frameLocator('iframe[src*="/api/plugins/devtools/ui"]');await mcp.getByRole('heading',{name:/MCP/}).first().waitFor();await waitTheme(mcp,true);
 await toggle(false);await waitTheme(mcp,false);await panel.getByRole('button',{name:'关闭并返回原页面',exact:true}).click();await toggle(true);await waitTheme(mcp,true);
 // A new/reloaded document should start with the same saved theme.
 await standalone.reload();await waitTheme(standalone,true);await page.reload();await plugin('deploy').getByRole('heading',{name:'基础构建',exact:true}).waitFor();await waitTheme(plugin('deploy'),true);
 await mkdir('/tmp/devops-theme-sync-ui',{recursive:true});await page.waitForTimeout(200);await page.screenshot({path:'/tmp/devops-theme-sync-ui/shell-dark.png'});await toggle(false);await waitTheme(plugin('deploy'),false);await page.waitForTimeout(200);assert.equal(await plugin('deploy').locator('body').evaluate(e=>getComputedStyle(e).backgroundColor),'rgb(243, 245, 249)');await page.screenshot({path:'/tmp/devops-theme-sync-ui/shell-light.png'});
 assert.deepEqual(errors,[]);assert.deepEqual(writes,[]);console.log(JSON.stringify({ok:true,checked,plugins:['deploy','dbms','redis','logs','monitor','devtools'],checks:['real theme button','visible and hidden workspaces','new and reloaded iframe','standalone and second shell storage sync','reactive toggle label','Element Plus palette','MCP open/hidden','no reload or navigation on toggle','no business writes']}));
}finally{await browser.close()}
