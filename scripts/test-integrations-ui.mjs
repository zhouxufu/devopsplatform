// Real Chrome + real disposable local MySQL; simulated ZenTao 16.0 JSON API.
// Run after test-delivery-ui.mjs against its isolated QA preview only.
import assert from 'node:assert/strict'
import http from 'node:http'
const base=process.env.DELIVERY_UI_TEST_URL||'http://127.0.0.1:19823'
const target=process.env.DEVOPS_SQL_TEST_ADDR
assert.equal(new URL(base).hostname,'127.0.0.1')
assert(!['9800','19803','19813'].includes(new URL(base).port),'Use a disposable regression preview')
assert(target?.startsWith('127.0.0.1:'),'Use a disposable loopback MySQL')
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright')
const browser=await chromium.launch({headless:true,...(process.env.CHROME_PATH?{executablePath:process.env.CHROME_PATH}:{})})
const page=await browser.newPage({viewport:{width:1440,height:1000}})
const errors=[];page.on('pageerror',e=>errors.push(e.message));page.on('dialog',d=>d.accept())
await page.route('**/api/me',r=>r.fulfill({json:{username:'local',role:'admin'}}))
const req=page.request
async function read(path){const r=await req.get(base+path);assert(r.ok(),await r.text());return r.json()}
async function post(path,body){const r=await req.post(base+path,{data:body});assert(r.ok(),await r.text());return r.json()}
const suffix=Date.now().toString(36),table='sql_qa_ui_'+suffix,service='ui-qa-'+suffix,connection='UI回归MySQL-'+suffix,sqlTitle='UI回归SQL-'+suffix
const mock=http.createServer((request,response)=>{
  const url=new URL(request.url,'http://localhost');let data
  if(url.searchParams.get('f')==='getSessionID')data={sessionName:'zentaosid',sessionID:'qa-cookie'}
  else if(url.searchParams.get('f')==='login')data={user:{account:'qa'}}
  else data={bugs:[{id:101,title:'UI回归禅道BUG-'+suffix,status:'closed',closedDate:'2026-10-01 12:00:00',steps:'QA'}]}
  response.setHeader('Content-Type','application/json');response.end(JSON.stringify({status:'success',data:JSON.stringify(data)}))
})
await new Promise(resolve=>mock.listen(0,'127.0.0.1',resolve))
const dlg=name=>page.getByRole('dialog',{name,exact:true})
async function select(container,label,option){await container.locator('label').filter({hasText:new RegExp('^'+label)}).locator('.el-select').click();await page.getByRole('option',{name:option,exact:true}).click()}
const field=(container,label)=>container.locator('label').filter({hasText:new RegExp('^'+label)}).locator('input,textarea').first()
async function resourceField(label,value){await dlg('资源连接').locator('label').filter({hasText:new RegExp('^'+label+'$')}).locator('..').locator('input').first().fill(value)}
try{
  const setup=await read('/api/setup');assert(setup.environments.length===1&&setup.environments[0].name==='qa-ui','Non-QA environment')
  assert((await read('/api/services')).every(s=>s.name.startsWith('ui-qa-')),'Non-QA app')
  await post('/api/services',{name:service,artifact_type:'jar',build_tool:'command',build_cmd:'true',artifact_glob:'*.jar'})
  await page.goto(base+'/ui#/changes?tab=changes')
  await page.locator('.platform-nav button').filter({hasText:'资源'}).click()
  await dlg('资源中心').getByRole('button',{name:'＋ 新建连接'}).click()
  await resourceField('连接名称',connection)
  await dlg('资源连接').locator('.fld').filter({has:page.getByText('类型',{exact:true})}).locator('.el-select').click()
  await page.getByRole('option',{name:'MySQL 变更数据库',exact:true}).click()
  await resourceField('服务地址',target);await resourceField('数据库','delivery_qa');await resourceField('用户名','root');await resourceField('密码 / Token',process.env.DEVOPS_SQL_TEST_PASSWORD||'')
  await dlg('资源连接').getByRole('button',{name:'加密保存'}).click()
  await dlg('资源中心').getByText(connection,{exact:true}).waitFor()
  await dlg('资源中心').getByRole('button',{name:'关闭并返回原页面'}).click()
  await page.getByRole('button',{name:'＋ 新建变更'}).click()
  await field(dlg('变更'),'标题').fill(sqlTitle);await select(dlg('变更'),'变更类型','SQL 变更');await select(dlg('变更'),'关联应用',service)
  await field(dlg('变更'),'执行 SQL').fill(`CREATE TABLE ${table} (id int NOT NULL, label varchar(64) NOT NULL DEFAULT '', PRIMARY KEY (id));`)
  await dlg('变更').getByRole('button',{name:'配置应用资源'}).click()
  await dlg('应用环境资源绑定').getByRole('button',{name:'＋ 声明资源'}).click()
  await dlg('应用环境资源绑定').getByPlaceholder('资源别名，如 business-db').fill('db')
  await dlg('应用环境资源绑定').getByRole('button',{name:'＋ 环境绑定'}).click()
  await dlg('应用环境资源绑定').locator('.binding-row').nth(1).locator('.el-select').nth(0).click();await page.getByRole('option',{name:'UI 回归环境',exact:true}).click()
  await dlg('应用环境资源绑定').locator('.binding-row').nth(1).locator('.el-select').nth(1).click();await page.getByRole('option',{name:connection+' · delivery_qa',exact:true}).click()
  await dlg('应用环境资源绑定').getByRole('button',{name:'保存绑定'}).click();await dlg('应用环境资源绑定').waitFor({state:'hidden'})
  assert.equal(await field(dlg('变更'),'标题').inputValue(),sqlTitle,'Resource configuration discarded the change draft')
  assert((await field(dlg('变更'),'执行 SQL').inputValue()).includes(table),'SQL draft lost')
  await select(dlg('变更'),'数据库资源','db')
  await dlg('变更').getByRole('button',{name:'保存',exact:true}).click();await dlg('变更').waitFor({state:'hidden'})
  const card=page.locator('.record').filter({hasText:sqlTitle})
  await card.getByRole('button',{name:'SQL 预检',exact:true}).click();await dlg('SQL 结构与影响范围预检（不执行）').getByRole('button',{name:'读取目标库并预检'}).click()
  await dlg('SQL 结构与影响范围预检（不执行）').getByText('计划执行',{exact:false}).waitFor();await dlg('SQL 结构与影响范围预检（不执行）').locator('.el-dialog__headerbtn').click()
  await card.getByRole('button',{name:'标记就绪'}).click();await card.getByText('已就绪',{exact:true}).waitFor()
  const change=(await read('/api/changes')).find(c=>c.title===sqlTitle)
  const version=await post('/api/release-versions',{version:'sql-ui-'+suffix,title:'UI回归SQL版本-'+suffix,change_ids:[change.id]});await post(`/api/release-versions/${version.id}/ready`,{})
  const template=await post('/api/delivery/templates',{name:'UI回归SQL编排-'+suffix,mode:'version',nodes:[{id:'sql',kind:'mysql',name:'SQL执行'}]})
  await page.goto(base+'/ui#/orchestration');await page.getByRole('button',{name:'编排模板',exact:true}).click();await page.locator('article.card').filter({hasText:template.name}).getByRole('button',{name:'发起交付'}).click()
  await select(dlg('发起交付 · 范围与执行预览'),'发布版本',version.version+' · '+version.title)
  await dlg('发起交付 · 范围与执行预览').getByRole('button',{name:'检查范围与配置'}).click();await dlg('发起交付 · 范围与执行预览').locator('.sql-plan').getByText(new RegExp(`create table .*${table}`,'i')).waitFor()
  await dlg('发起交付 · 范围与执行预览').getByRole('button',{name:'提交执行单（不立即部署）'}).click()
  const execution=page.getByRole('dialog').filter({hasText:template.name})
  await execution.getByRole('button',{name:'继续执行 / 核对节点结果'}).click();await execution.getByText('回读验证通过',{exact:false}).waitFor()
  await execution.getByRole('button',{name:'确认编排完成'}).click()
  assert((await read('/api/sql-executions')).some(v=>v.change_id===change.id&&v.status==='success'),'SQL ledger missing')
  await execution.locator('.el-dialog__headerbtn').click()
  const zentao='UI回归禅道-'+suffix
  await post('/api/connections',{name:zentao,kind:'zentao',endpoint:`http://127.0.0.1:${mock.address().port}`,username:'qa',password:'qa',config:{product_ids:'1',sync_minutes:'0'}})
  await page.goto(base+'/ui#/changes?tab=work');await page.getByRole('button',{name:'同步禅道 BUG',exact:true}).click()
  await select(dlg('同步禅道 BUG（开源 16.0）'),'禅道连接',zentao);await dlg('同步禅道 BUG（开源 16.0）').getByRole('button',{name:'立即同步'}).click();await dlg('同步禅道 BUG（开源 16.0）').getByText('上次同步：成功',{exact:false}).waitFor()
  await dlg('同步禅道 BUG（开源 16.0）').locator('.el-dialog__headerbtn').click();await dlg('同步禅道 BUG（开源 16.0）').waitFor({state:'hidden'});await page.getByText('UI回归禅道BUG-'+suffix,{exact:true}).waitFor()
  await page.setViewportSize({width:900,height:800});await page.waitForFunction(()=>document.documentElement.scrollWidth<=innerWidth,{},{timeout:5000});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false)
  assert.deepEqual(errors,[]);await page.screenshot({path:'runtime/integrations-ui-qa.png',fullPage:true})
  console.log(JSON.stringify({ok:true,checks:['MySQL connection UI','environment resource binding UI','SQL structured editor','read-only preflight UI','version SQL snapshot','actual SQL node execution','ledger','ZenTao sync UI','browser errors','900px layout'],table}))
}catch(error){await page.screenshot({path:'runtime/integrations-ui-failure.png',fullPage:true});throw error}
finally{mock.close();await browser.close()}
