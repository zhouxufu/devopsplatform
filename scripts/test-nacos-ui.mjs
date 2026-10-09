// Browser requests are intercepted: no resource credential, remote config or
// persistent application is changed. Backend protocol QA uses httptest only.
import assert from 'node:assert/strict';
const base=process.env.DELIVERY_UI_TEST_URL||'http://127.0.0.1:19823';
assert.equal(new URL(base).hostname,'127.0.0.1');
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright');
const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH});
const page=await browser.newPage({viewport:{width:1440,height:1000}}),errors=[],writes=[],previews=[];
page.on('pageerror',e=>errors.push(e.message));
const change={id:991001,title:'Nacos UI QA',kind:'nacos',status:'ready',owner:'local',service:'orders',work_items:[],nacos:{resource_alias:'config',data_id:'orders.yaml',group:'DEFAULT_GROUP',format:'yaml',entries:[{key:'feature.enabled',action:'set',value:'true'}]}};
const plan={node_id:'config',configs:[{change_id:change.id,service:'orders',alias:'config',connection:'qa-nacos',namespace:'qa',group:'DEFAULT_GROUP',data_id:'orders.yaml',format:'yaml',before_md5:'before',after_md5:'after',diff:[{key:'feature.enabled',before:'false',after:'true'}]}]};
const template={id:991004,name:'Independent Nacos',mode:'fixed',owner:'local',services:[],nodes:[{id:'config',name:'Nacos configuration',kind:'nacos',change_ids:[change.id]}]};
const run={id:991005,owner:'local',status:'success',digest:'qa',plan:{template,environment:{name:'test',display_name:'测试'},reason:'QA',services:[],nodes:template.nodes,warnings:[],blockers:[],nacos_plans:[plan]},steps:[{node_id:'config',name:'Nacos configuration',kind:'nacos',status:'success'}]};
let savedChange,savedConnection,restoreRequest,executionPreview,slow=false,releaseSlow;
await page.route('**/*',async route=>{
 const r=route.request(),url=new URL(r.url()),p=url.pathname.replace('/api/resources/','/api/'),method=r.method(),reply=json=>route.fulfill({json});
 if(!['127.0.0.1','localhost'].includes(url.hostname))return route.abort();
 if(!p.includes('/api/')||!['fetch','xhr'].includes(r.resourceType()))return route.continue();
 if(method==='POST'&&p.endsWith('/nacos-preview')){previews.push(r.postDataJSON());if(slow){slow=false;await new Promise(resolve=>{releaseSlow=resolve})}return reply(plan)}
 if(method==='POST'&&p.endsWith('/api/changes')){savedChange=r.postDataJSON();return reply({...savedChange,id:991002,status:'draft',owner:'local'})}
 if(method==='POST'&&p.endsWith('/api/connections')){savedConnection=r.postDataJSON();return reply({ok:true})}
 if(method==='POST'&&p.endsWith('/nacos-restore-draft')){restoreRequest=r.postDataJSON();return reply({...change,id:991006,status:'draft'})}
 if(method==='POST'&&p.endsWith('/api/delivery/preview')){executionPreview=r.postDataJSON();return reply(run.plan)}
 if(!['GET','HEAD','OPTIONS'].includes(method)){writes.push(p);return route.abort()}
 if(p.endsWith('/api/services'))return reply([{name:'orders'}]);
 if(p.endsWith('/api/me'))return reply({username:'local',role:'admin'});
 if(p.endsWith('/api/environments'))return reply([{name:'test',display_name:'测试'},{name:'prod',display_name:'生产'}]);
 if(p.endsWith('/api/changes'))return reply([change]);
 if(p.endsWith('/api/delivery/templates'))return reply([template]);
 if(p.endsWith('/api/delivery/runs'))return reply([run]);
 if(p.endsWith('/api/delivery/runs/991005'))return reply(run);
 if(p.endsWith('/api/nacos-executions'))return reply([{change_id:change.id,run_id:run.id,node_id:'config',status:'success',detail:'配置已发布并核对摘要'}]);
 if(p.endsWith('/api/services/orders/resources'))return reply({requirements:[{alias:'config',kind:'nacos'}],bindings:[]});
 if(p.endsWith('/api/connections'))return reply(savedConnection?[{...savedConnection,secret_configured:true}]:[]);
 return reply([]);
});
const field=(dialog,label)=>label==='类型'||label==='连接名称'||label==='服务地址'||label==='命名空间 ID'||label==='用户名'||label==='密码 / Token'?dialog.locator('.fld').filter({has:page.locator('label').filter({hasText:new RegExp('^'+label+'$')})}):dialog.locator('label').filter({hasText:new RegExp('^'+label)});
async function choose(dialog,label,option){const control=field(dialog,label).locator('.el-select');await control.click();const id=await control.getByRole('combobox').getAttribute('aria-controls');await page.locator('#'+id).getByRole('option',{name:option,exact:true}).click();await dialog.locator('.el-dialog__header').click()}
try{
 await page.goto(base+'/ui#/changes?tab=changes');
 await page.getByRole('button',{name:'Nacos 差异预览',exact:true}).click();
 const preview=page.getByRole('dialog',{name:'Nacos 配置差异（只读预览）',exact:true});
 await preview.getByRole('button',{name:'读取目标配置并对比'}).click();await preview.locator('.nacos-diff').waitFor();
 assert.equal(previews[0].environment,'test');assert((await preview.locator('.nacos-diff').innerText()).includes('feature.enabled'));
 await choose(preview,'目标环境','生产');await preview.locator('.nacos-diff').waitFor({state:'hidden'});
 await preview.getByRole('button',{name:'读取目标配置并对比'}).click();await preview.locator('.nacos-diff').waitFor();assert.equal(previews[1].environment,'prod');
 slow=true;const pending=page.waitForRequest(r=>r.url().endsWith('/nacos-preview'));await preview.getByRole('button',{name:'读取目标配置并对比'}).click();await pending;await preview.getByRole('button',{name:'Close this dialog'}).click();await page.getByRole('button',{name:'Nacos 差异预览',exact:true}).click();const completed=page.waitForResponse(r=>r.url().endsWith('/nacos-preview'));releaseSlow();await completed;await page.waitForTimeout(100);assert.equal(await preview.locator('.nacos-diff').count(),0,'closed-session response leaked into new preview');
 await preview.getByRole('button',{name:'Close this dialog'}).click();
 await page.getByRole('button',{name:/新建变更/}).click();
 const dialog=page.getByRole('dialog',{name:'变更',exact:true});
 await field(dialog,'标题').locator('input').fill('Independent configuration');await choose(dialog,'变更类型','Nacos 配置');await choose(dialog,'关联应用','orders');await choose(dialog,'配置资源','config');
 await field(dialog,'Data ID').locator('input').fill('orders.yaml');await dialog.getByPlaceholder('配置项，例如 feature.enabled').fill('feature.enabled');await dialog.getByPlaceholder('期望值').fill('true');
 await dialog.getByRole('button',{name:'保存',exact:true}).click();await dialog.waitFor({state:'hidden'});
 assert.equal(savedChange.nacos.entries[0].key,'feature.enabled');assert.equal(savedChange.nacos.resource_alias,'config');assert.equal(savedChange.sql,undefined);assert.equal(savedChange.nacos.format,'yaml');assert(!savedChange.environment,'change depends on environment');
 await page.locator('.platform-nav').getByRole('button',{name:/资源/}).click();
 await page.getByRole('button',{name:'＋ 新建连接',exact:true}).click();
 const connection=page.getByRole('dialog',{name:'资源连接',exact:true});
 await choose(connection,'类型','Nacos 配置中心（2.4 / 2.5）');
 await field(connection,'连接名称').locator('input').fill('qa-nacos');await field(connection,'服务地址').locator('input').fill('http://example.invalid:8848/nacos');await field(connection,'命名空间 ID').locator('input').fill('qa');
 await field(connection,'用户名').locator('input').fill('qa');await field(connection,'密码 / Token').locator('input').fill('synthetic-password');
 await connection.getByRole('button',{name:'加密保存',exact:true}).click();await connection.waitFor({state:'hidden'});
 assert.equal(savedConnection.kind,'nacos');assert.equal(savedConnection.config.nacos_version,'2.4');assert.equal(savedConnection.config.nacos_auth,'password');assert.equal(savedConnection.config.namespace,'qa');
 await page.getByRole('dialog',{name:'资源中心',exact:true}).getByRole('button',{name:'关闭并返回原页面',exact:true}).click();
 for(const width of [1440,900,700]){await page.setViewportSize({width,height:1000});await page.getByRole('button',{name:'Nacos 差异预览',exact:true}).click();await preview.getByRole('button',{name:'读取目标配置并对比'}).click();await preview.locator('.nacos-diff').waitFor();assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);await preview.getByRole('button',{name:'Close this dialog'}).click()}
 await page.setViewportSize({width:1440,height:1000});await page.goto(base+'/ui#/orchestration');await page.getByRole('button',{name:'查看 / 审核 / 执行',exact:true}).click();
 const executed=page.getByRole('dialog',{name:'执行单 #991005',exact:true});await executed.getByText(/发布成功并回读校验/).waitFor();await executed.getByRole('button',{name:'生成恢复草稿（不立即执行）',exact:true}).click();await page.getByText(/已创建恢复草稿 #991006/).waitFor();assert.equal(restoreRequest.change_id,change.id);assert.equal(await executed.isVisible(),true,'recovery interrupted original run');await executed.getByRole('button',{name:'Close this dialog'}).click();
 await page.getByRole('button',{name:'发起交付',exact:true}).click();const execution=page.getByRole('dialog',{name:'发起交付 · 范围与执行预览',exact:true});await field(execution,'本次配置变更').waitFor();await execution.getByRole('button',{name:'检查范围与配置',exact:true}).click();await execution.locator('.nacos-diff').waitFor();assert.deepEqual(executionPreview.artifacts,{});assert.equal(executionPreview.nodes[0].change_ids[0],change.id);await execution.getByRole('button',{name:'取消',exact:true}).click();
 assert.deepEqual(writes,[]);assert.deepEqual(errors,[]);console.log(JSON.stringify({ok:true,checks:['readonly preview','environment invalidates previous diff','independent structured change','change has no environment','shared fields','resource connection 2.4 defaults','responsive widths','execution ledger','recovery draft preserves run','independent Nacos node without artifacts','no external writes','no page errors']}));
}finally{await browser.close()}
