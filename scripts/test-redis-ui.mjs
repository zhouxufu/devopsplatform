// Synthetic interception only. No user Redis, credential or application writes.
import assert from 'node:assert/strict';
const base=process.env.DELIVERY_UI_TEST_URL||'http://127.0.0.1:19823';
assert.equal(new URL(base).hostname,'127.0.0.1');
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright');
const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH});
const page=await browser.newPage({viewport:{width:1440,height:1000}}),errors=[],writes=[],previews=[];
page.on('pageerror',e=>errors.push(e.message));
const change={id:991001,title:'Redis UI QA',kind:'redis',status:'ready',owner:'local',service:'orders',work_items:[],redis:{resource_alias:'cache',commands:'TYPE menu:tree\nUNLINK menu:tree'}};
const plan={node_id:'cache',changes:[{change_id:change.id,service:'orders',alias:'cache',connection:'qa-redis',endpoint:'example.invalid:6379',db:'0',prefix:'menu:',commands:[['TYPE','menu:tree'],['UNLINK','menu:tree']]}]};
const template={id:991004,name:'Independent Redis',mode:'fixed',owner:'local',services:[],nodes:[{id:'cache',name:'Redis cleanup',kind:'redis',change_ids:[change.id]}]};
const environment={name:'test',display_name:'测试',protected:false,approvers:'local,reviewer'};
const run={id:991005,owner:'alice',status:'pending_approval',digest:'qa',plan:{template,environment,reason:'QA',services:[],nodes:template.nodes,warnings:[],blockers:[],redis_plans:[plan]},steps:[{node_id:'cache',name:'Redis cleanup',kind:'redis',status:'pending'}]};
let savedChange,savedConnection,executionPreview,workSave,slow=false,releaseSlow;
await page.route('**/*',async route=>{
 const r=route.request(),url=new URL(r.url()),p=url.pathname.replace('/api/resources/','/api/'),method=r.method(),reply=json=>route.fulfill({json});
 if(!['127.0.0.1','localhost'].includes(url.hostname))return route.abort();
 if(!p.includes('/api/')||!['fetch','xhr'].includes(r.resourceType()))return route.continue();
 if(method==='POST'&&p.endsWith('/redis-preview')){previews.push(r.postDataJSON());if(slow){slow=false;await new Promise(resolve=>{releaseSlow=resolve})}return reply(plan)}
 if(method==='POST'&&p.endsWith('/api/changes')){savedChange=r.postDataJSON();return reply({...savedChange,id:991002,status:'draft',owner:'local'})}
 if(method==='POST'&&p.endsWith('/api/connections')){savedConnection=r.postDataJSON();return reply({ok:true})}
 if(method==='POST'&&p.endsWith('/api/delivery/preview')){executionPreview=r.postDataJSON();return reply(run.plan)}
 if(method==='PUT'&&p.endsWith('/delivery-content')){workSave=r.postDataJSON();return reply({ok:true})}
 if(!['GET','HEAD','OPTIONS'].includes(method)){writes.push(p);return route.abort()}
 if(p.endsWith('/api/services'))return reply([{name:'orders'}]);
 if(p.endsWith('/api/me'))return reply({username:'local',role:'admin'});
 if(p.endsWith('/api/environments'))return reply([environment,{name:'prod',display_name:'生产',protected:true,approvers:'reviewer'}]);
 if(p.endsWith('/api/changes'))return reply([change]);
 if(p.endsWith('/api/work-items'))return reply([{id:991007,title:'Menu requirement',kind:'requirement',owner:'local',status:'open',description:''}]);
 if(p.endsWith('/api/work-items/page'))return reply({items:[{id:991007,title:'Menu requirement',kind:'requirement',owner:'local',status:'open',description:''}],total:1,page:1,page_size:20});
 if(p.endsWith('/api/work-items/people'))return reply([]);
 if(p.endsWith('/delivery-content'))return reply({digest:'work-qa',changes:[]});
 if(p.endsWith('/api/delivery/templates'))return reply([template]);
 if(p.endsWith('/api/delivery/runs'))return reply([run]);
 if(p.endsWith('/api/delivery/runs/991005'))return reply(run);
 if(p.endsWith('/api/redis-executions'))return reply([{change_id:change.id,command_index:1,run_id:run.id,node_id:'cache',status:'success',result:'1'}]);
 if(p.endsWith('/api/services/orders/resources'))return reply({requirements:[{alias:'cache',kind:'redis'}],bindings:[]});
 if(p.endsWith('/api/connections'))return reply(savedConnection?[{...savedConnection,secret_configured:true}]:[]);
 return reply([]);
});
const field=(dialog,label)=>['类型','连接名称','服务地址','Redis DB','允许操作的 Key 前缀','用户名','密码 / Token'].includes(label)?dialog.locator('.fld').filter({has:page.locator('label').filter({hasText:new RegExp('^'+label+'$')})}):dialog.locator('label').filter({hasText:new RegExp('^'+label)});
async function choose(dialog,label,option){const control=field(dialog,label).locator('.el-select');await control.click();const id=await control.getByRole('combobox').getAttribute('aria-controls');await page.locator('#'+id).getByRole('option',{name:option,exact:true}).click();await dialog.locator('.el-dialog__header').click()}
try{
 await page.goto(base+'/ui#/changes?tab=changes');await page.getByRole('button',{name:'Redis 命令预检',exact:true}).click();
 const preview=page.getByRole('dialog',{name:'Redis 命令预检（只读）',exact:true});await preview.getByRole('button',{name:'检查连接与命令范围'}).click();await preview.locator('.redis-command').first().waitFor();assert.equal(previews[0].environment,'test');
 await choose(preview,'目标环境','生产');await preview.locator('.redis-command').first().waitFor({state:'hidden'});await preview.getByRole('button',{name:'检查连接与命令范围'}).click();await preview.locator('.redis-command').first().waitFor();assert.equal(previews[1].environment,'prod');
 slow=true;const pending=page.waitForRequest(r=>r.url().endsWith('/redis-preview'));await preview.getByRole('button',{name:'检查连接与命令范围'}).click();await pending;await preview.getByRole('button',{name:'Close this dialog'}).click();await page.getByRole('button',{name:'Redis 命令预检',exact:true}).click();const completed=page.waitForResponse(r=>r.url().endsWith('/redis-preview'));releaseSlow();await completed;await page.waitForTimeout(100);assert.equal(await preview.locator('.redis-command').count(),0,'old session leaked');await preview.getByRole('button',{name:'Close this dialog'}).click();
 await page.getByRole('button',{name:'＋ 新建变更',exact:true}).click();const editor=page.getByRole('dialog',{name:'变更',exact:true});await choose(editor,'变更类型','Redis 命令');await field(editor,'标题').locator('input').fill('Redis new');await choose(editor,'关联应用','orders');await choose(editor,'Redis 资源','cache');await field(editor,'Redis 命令（每行一条）').locator('textarea').fill('UNLINK menu:tree');await editor.getByRole('button',{name:'保存',exact:true}).click();await editor.waitFor({state:'hidden'});assert.equal(savedChange.redis.commands,'UNLINK menu:tree');assert.equal(savedChange.redis.resource_alias,'cache');assert.equal(savedChange.sql,undefined);assert.equal(savedChange.nacos,undefined);assert(!savedChange.environment);
 await page.goto(base+'/ui#/changes?tab=work');await page.getByRole('button',{name:'交付内容',exact:true}).click();const work=page.getByRole('dialog',{name:'交付内容 · Menu requirement',exact:true});await work.getByRole('button',{name:'Redis 命令 · 0',exact:true}).click();await work.getByRole('button',{name:'＋ 添加Redis 命令',exact:true}).click();await choose(work,'应用','orders');await choose(work,'资源','cache · redis');await field(work,'Redis 命令（每行一条）').locator('textarea').fill('UNLINK menu:tree');await work.getByRole('button',{name:'保存全部内容'}).click();await page.getByText('全部交付内容已保存，仍停留在当前需求',{exact:true}).waitFor();assert.equal(workSave.changes[0].redis.resource_alias,'cache');assert.equal(workSave.changes[0].redis.commands,'UNLINK menu:tree');assert(await work.isVisible());await work.getByRole('button',{name:'关闭',exact:true}).click();
 await page.locator('.platform-nav').getByRole('button',{name:/资源/}).click();await page.getByRole('button',{name:'＋ 新建连接',exact:true}).click();const connection=page.getByRole('dialog',{name:'资源连接',exact:true});await choose(connection,'类型','Redis 缓存维护');await field(connection,'连接名称').locator('input').fill('qa-redis');await field(connection,'服务地址').locator('input').fill('example.invalid:6379');await field(connection,'允许操作的 Key 前缀').locator('input').fill('menu:');await field(connection,'密码 / Token').locator('input').fill('synthetic-password');await connection.getByRole('button',{name:'加密保存',exact:true}).click();await connection.waitFor({state:'hidden'});assert.equal(savedConnection.kind,'redis');assert.equal(savedConnection.config.db,'0');assert.equal(savedConnection.config.key_prefix,'menu:');assert.equal(savedConnection.config.redis_auth,'password');
 await page.getByRole('dialog',{name:'资源中心',exact:true}).getByRole('button',{name:'关闭并返回原页面',exact:true}).click();
 await page.goto(base+'/ui#/orchestration');await page.getByRole('button',{name:'查看 / 审核 / 执行',exact:true}).click();const executed=page.getByRole('dialog',{name:'执行单 #991005',exact:true});await executed.locator('.redis-command').first().waitFor();assert(await executed.getByRole('button',{name:'审核通过',exact:true}).isVisible());assert.equal(await executed.getByRole('button',{name:'执行下一个节点',exact:true}).count(),0);assert((await executed.locator('.redis-preview').innerText()).includes('已执行 · 1'));await executed.getByRole('button',{name:'Close this dialog'}).click();
 await page.getByRole('button',{name:'发起交付',exact:true}).click();const execution=page.getByRole('dialog',{name:'发起交付 · 范围与执行预览',exact:true});await execution.getByRole('button',{name:'检查范围与配置',exact:true}).click();await execution.locator('.redis-command').first().waitFor();assert((await execution.locator('.preview h4').first().innerText()).includes('需要审核'));assert.deepEqual(executionPreview.artifacts,{});assert.equal(executionPreview.nodes[0].change_ids[0],change.id);await execution.getByRole('button',{name:'取消',exact:true}).click();
 await page.goto(base+'/ui#/changes?tab=changes');for(const width of [1440,900,700]){await page.setViewportSize({width,height:1000});await page.getByRole('button',{name:'Redis 命令预检',exact:true}).click();await preview.getByRole('button',{name:'检查连接与命令范围'}).click();await preview.locator('.redis-command').first().waitFor();assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);await preview.getByRole('button',{name:'Close this dialog'}).click()}
 assert.deepEqual(writes,[]);assert.deepEqual(errors,[]);console.log(JSON.stringify({ok:true,checks:['readonly preview','environment invalidation','stale response isolation','independent structured change','continuous requirement editor','standard Redis connection','mandatory review visibility','command ledger','no artifact dependency','responsive widths','no real writes','no page errors']}));
}finally{await browser.close()}
