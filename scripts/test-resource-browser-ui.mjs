// All requests are intercepted. Exercises browser behavior without user DB writes.
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import path from 'node:path'
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright')
const base='http://127.0.0.1:19823',dist=process.env.WORKBENCH_UI_DIST||path.resolve('web/dist')
const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH})
const errors=[],unexpected=[],checks=[]
try{
 for(const kind of ['dbms','redis']){
  const page=await browser.newPage({viewport:{width:1440,height:1000}})
  page.on('pageerror',e=>errors.push(e.message));page.on('dialog',d=>d.accept())
  if(kind==='dbms')await page.addInitScript(()=>{window.testClipboard=[];Object.defineProperty(navigator,'clipboard',{value:undefined,configurable:true});document.execCommand=command=>{if(command==='copy'){window.testClipboard.push(document.activeElement.value);return true}return false}})
  const reads=[],requests=[],env={name:'test',display_name:'测试',approvers:'reviewer',protected:false}
  let approval=false,ticket=null,slow=false,releaseSlow,gridFixture=false
  const policy=()=>({query_approval:approval,change_approval:true})
  const target={connection:'conn-a',database:'qa',db:'0',key_prefix:'menu:'}
  function result(body){
   if(kind==='dbms'){
    if(body.sql==='SHOW TABLES')return {columns:['table_name'],rows:[{table_name:'MixedCase'},{table_name:'users'}],metadata:'tables',target}
    if(body.sql.startsWith('DESCRIBE'))return {columns:['field','type','key_type'],rows:[{field:'id',type:'bigint',key_type:'PRI'},{field:'name',type:'varchar',key_type:''}],metadata:'schema',table:'MixedCase',target}
    if(body.sql.startsWith('SHOW INDEX'))return {columns:['index_name','position','field'],rows:[{index_name:'PRIMARY',position:'1',field:'id'}],metadata:'indexes',table:'MixedCase',target}
    if(body.sql.includes('AS custom_value'))return {columns:['custom_value'],rows:[{custom_value:'console result'}],target}
    if(gridFixture)return {columns:['session_id','state_key','item_index','state_data','created_at','updated_at'],rows:Array.from({length:51},(_,i)=>({session_id:'1212:s_find_home_'+i+'_long_session_identifier_that_should_not_squeeze_other_columns',state_key:'agent_state',item_index:i,state_data:JSON.stringify({session_id:'long_session_id',payload:'large JSON preview '.repeat(100)}),created_at:'2026-10-13 11:20:43',updated_at:'2026-10-13 11:21:43'})),truncated:true,elapsed_ms:81,table:'MixedCase',target}
    const offset=Number(body.sql.match(/OFFSET (\d+)/)?.[1]||0)
    return {columns:['id','name'],rows:Array.from({length:offset?3:100},(_,i)=>({id:offset+i+1,name:'row '+(offset+i+1)})),limit_reached:!offset,table:'MixedCase',target}
   }
   const rows=body.commands.split('\n').map(command=>{
    const argv=command.match(/"(?:[^"\\]|\\.)*"|\S+/g).map(v=>v.startsWith('"')?JSON.parse(v):v),[op,key,cursor]=argv
    let value,truncated=false
    if(op==='SCAN'){
     if(argv[3]==='menu:cap*')value=['9',Array.from({length:1000},(_,i)=>'menu:cap'+i)]
     else if(key==='0')value=['11',['menu:a','menu:a','menu:hash','menu:list','menu:zset','menu:set','menu:gone']]
     else if(key==='11')value=['22',[]]
     else value=['0',['menu:a','menu:b']]
    }else if(op==='TYPE')value=key==='menu:gone'?'none':key==='menu:hash'?'hash':key==='menu:list'?'list':key==='menu:zset'?'zset':key==='menu:set'?'set':'string'
    else if(op==='TTL')value=key==='menu:gone'?-2:120
    else if(op==='GET'){value='{"message":"preview","unsafe":"<script>alert(1)</script>"}';truncated=true}
    else if(op==='HSCAN')value=cursor==='0'?['8',['a','first','b','second']]:['0',['a','updated','c','third']]
    else if(op==='SSCAN')value=cursor==='0'?['8',['x','y']]:['0',['y','z']]
    else if(op==='ZSCAN')value=['0',['first','1.5','second','2']]
    else if(op==='LRANGE')value=Number(cursor)<100?Array.from({length:50},(_,i)=>'item '+(Number(cursor)+i)):[]
    else throw Error('unexpected command '+op)
    return {command,argv,result:value,truncated}
   })
   return {columns:['command','result','truncated'],rows,target}
  }
  await page.route('**/*',async route=>{
   const request=route.request(),url=new URL(request.url()),p=url.pathname,method=request.method(),reply=json=>route.fulfill({json})
   if(url.origin!==base)return route.abort()
   if(/\/assets\/[^/]+\.(js|css)$/.test(p)){const file='assets/'+path.basename(p);return route.fulfill({body:await readFile(path.join(dist,file)),contentType:file.endsWith('.js')?'text/javascript':'text/css'})}
   if(p===`/ui/${kind}`)return route.fulfill({body:await readFile(path.join(dist,'workbench.html')),contentType:'text/html'})
   if(method==='GET'){
    if(p==='/api/me')return reply({username:'alice',role:'admin',auth_enabled:true})
    if(p.endsWith('/services')||p.endsWith('/applications'))return reply([{name:'orders'}])
    if(p.endsWith('/environments'))return reply([env])
    if(p.endsWith('/connections'))return reply([{name:'conn-a',kind:kind==='dbms'?'mysql':'redis',config:{}},{name:'conn-b',kind:kind==='dbms'?'mysql':'redis',config:{}}])
    if(p.endsWith('/query-target'))return reply({...target,connection:url.searchParams.get('connection')||'qa'})
    if(p.endsWith('/resources'))return reply({requirements:[{alias:kind==='dbms'?'db':'cache',kind:kind==='dbms'?'mysql':'redis'}]})
    if(p.endsWith('/policy'))return reply(policy())
    if(p.endsWith('/query-requests'))return reply(ticket?[ticket]:[])
    return reply([])
   }
   if(method==='POST'&&p.endsWith('/query')){const body=request.postDataJSON();reads.push(body);if(slow){slow=false;await new Promise(resolve=>{releaseSlow=resolve})}return reply(result(body)).catch(()=>{})}
   if(method==='POST'&&p.endsWith('/query-requests')){const body=request.postDataJSON();requests.push(body);ticket={id:900001,kind,query:body,status:'pending_approval',owner:'alice',approvers:{test:'reviewer'}};return reply(ticket)}
   if(method==='POST'&&p.endsWith('/query-requests/900001/execute')){assert.equal(ticket.status,'approved');reads.push(ticket.query);ticket={...ticket,status:'completed'};return reply({ticket,result:result(ticket.query)})}
   unexpected.push(method+' '+p);return route.abort()
  })
  async function choose(label,option){const field=page.locator('label:visible').filter({hasText:new RegExp('^'+label)}).first();const control=field.locator('.el-select');await control.click();const id=await control.getByRole('combobox').getAttribute('aria-controls');await page.locator('#'+id).getByRole('option',{name:option,exact:true}).click();await page.getByRole('heading',{name:kind==='dbms'?'数据库工作台':'Redis 工作台',exact:true}).click()}
  await page.goto(base+'/ui/'+kind);await choose(kind==='dbms'?'数据库连接':'Redis 连接','conn-a');await page.getByText(/实际目标：conn-a/).waitFor()
  assert.equal(reads.length,0,'opening workbench scanned data')
  const panel=page.getByRole('region',{name:kind==='dbms'?'数据库表浏览':'Redis Key 浏览'})
  if(kind==='dbms'){
   await panel.getByRole('button',{name:'加载表列表',exact:true}).click();await panel.getByRole('button',{name:'MixedCase',exact:true}).waitFor();assert.equal(reads.length,1)
   await panel.getByRole('button',{name:'MixedCase',exact:true}).click();await panel.getByText('row 100',{exact:true}).waitFor()
   await panel.locator('.data-detail .cell').first().click();await panel.getByText('已复制',{exact:true}).waitFor();assert.deepEqual(await page.evaluate(()=>window.testClipboard),['1']);assert.match(reads.at(-1).sql,/FROM `MixedCase` ORDER BY `id` ASC LIMIT 100 OFFSET 0/);await page.screenshot({path:'/tmp/dbms-browser-data.png',fullPage:true})
   await panel.getByRole('button',{name:'下一页',exact:true}).click();await panel.getByText('row 101',{exact:true}).waitFor();assert.equal(await panel.getByRole('button',{name:'下一页',exact:true}).isDisabled(),true)
   await panel.getByRole('button',{name:'数据',exact:true}).click()
   const dataPane=panel.locator('.data-detail')
   await dataPane.getByRole('button',{name:'＋ 条件',exact:true}).click()
   await dataPane.getByLabel('筛选运算 1',{exact:true}).selectOption('>=')
   await dataPane.getByLabel('筛选值 1',{exact:true}).fill('9007199254740993')
   await dataPane.getByRole('button',{name:'＋ 条件',exact:true}).click()
   await dataPane.getByLabel('筛选字段 2',{exact:true}).selectOption('name')
   await dataPane.getByLabel('筛选运算 2',{exact:true}).selectOption('LIKE')
   await dataPane.getByLabel('筛选值 2',{exact:true}).fill("%O'Neil%")
   const beforeFilter=reads.length
   assert.equal(reads.length,beforeFilter,'editing criteria executed a query')
   await dataPane.getByLabel('排序字段',{exact:true}).selectOption('name')
   await dataPane.getByLabel('排序方向',{exact:true}).selectOption('DESC')
   await dataPane.getByRole('button',{name:'应用筛选',exact:true}).click()
   await dataPane.getByText('2 条条件已应用',{exact:true}).waitFor()
   assert.match(reads.at(-1).sql,/`id` >= 9007199254740993/)
   assert.match(reads.at(-1).sql,/`name` LIKE '%O''Neil%'/)
   assert.match(reads.at(-1).sql,/ORDER BY `name` DESC/)
   await dataPane.getByRole('button',{name:'下一页',exact:true}).click();await dataPane.getByText('row 101',{exact:true}).waitFor()
   assert.match(reads.at(-1).sql,/WHERE .*O''Neil.*OFFSET 100/)
   await dataPane.locator('.column-picker>summary').click();await dataPane.getByRole('checkbox',{name:'id',exact:true}).check();await dataPane.locator('.column-picker>summary').click()
   await dataPane.getByRole('button',{name:'应用筛选',exact:true}).click();await page.waitForFunction(()=>document.querySelector('.data-detail .browser-pagination')?.textContent.includes('第 1 页'))
   assert.match(reads.at(-1).sql,/^SELECT `id` FROM/)
   await dataPane.getByRole('button',{name:'WHERE 条件',exact:true}).click();await dataPane.getByRole('textbox',{name:'WHERE 条件',exact:true}).fill('id > 1 OR name IS NULL')
   await dataPane.getByRole('button',{name:'应用筛选',exact:true}).click();await dataPane.getByText('WHERE 条件已应用',{exact:true}).waitFor()
   assert.match(reads.at(-1).sql,/WHERE \(id > 1 OR name IS NULL\)/)
   await page.getByRole('button',{name:'SQL 查询',exact:true}).click()
   const consolePane=panel.getByRole('region',{name:'自定义 SQL 查询'}),editor=consolePane.getByRole('textbox',{name:'查询 SQL',exact:true})
   const custom="SELECT COALESCE(name,'fallback') AS custom_value FROM MixedCase WHERE id > 0 ORDER BY id DESC LIMIT 20"
   await editor.fill(custom);await consolePane.getByRole('button',{name:'执行只读查询',exact:true}).click();await consolePane.getByText('console result',{exact:true}).waitFor()
   assert.equal(reads.at(-1).sql,custom)
   assert.equal(await page.locator('.el-notification:visible').count(),0,'native data controls bubbled into a Redis operation');await page.screenshot({path:'/tmp/workbench-redesign-sql-1440.png',fullPage:true})
   assert.equal(await page.locator('main.query-main').evaluate(el=>el.scrollHeight>el.clientHeight+2),false,'SQL workbench scrolls as one long page')
   await page.getByRole('button',{name:'数据浏览',exact:true}).click();await dataPane.getByRole('button',{name:'刷新',exact:true}).click();await dataPane.getByText('row 100',{exact:true}).waitFor()
   await page.getByRole('button',{name:'SQL 查询',exact:true}).click();assert.equal(await editor.inputValue(),custom);await consolePane.getByText('console result',{exact:true}).waitFor()
   await editor.fill(custom+';\nSELECT id FROM menu');await editor.evaluate((el,n)=>el.setSelectionRange(0,n),custom.length);await consolePane.getByRole('button',{name:'执行只读查询',exact:true}).click();await consolePane.getByText('console result',{exact:true}).waitFor();assert.equal(reads.at(-1).sql,custom,'execute button ignored selected SQL')
   await editor.fill(custom)
   slow=true;const pendingQuery=page.waitForRequest(r=>r.url().endsWith('/query'))
   await consolePane.getByRole('button',{name:'执行只读查询',exact:true}).click();await pendingQuery
   await consolePane.getByRole('button',{name:'取消查询',exact:true}).click()
   await page.locator('.el-notification').filter({hasText:'查询已取消'}).waitFor();releaseSlow()
   await page.locator('.el-notification').filter({hasText:'查询已取消'}).locator('.el-notification__closeBtn').click()
   await page.getByRole('button',{name:'数据浏览',exact:true}).click()
   checks.push('AND filters / precise numeric values / raw WHERE / sorting / projection / filtered pagination / explicit SQL pane / draft and result preservation / selection execution / independent scroll')
   await panel.getByRole('button',{name:'结构',exact:true}).click();await panel.getByText('bigint',{exact:true}).waitFor()
   await panel.getByRole('button',{name:'索引',exact:true}).click();await panel.getByText('PRIMARY',{exact:true}).waitFor();assert.equal(reads.at(-1).sql,'SHOW INDEX FROM `MixedCase`')
   await panel.getByRole('button',{name:'数据',exact:true}).click()
   await dataPane.getByRole('button',{name:'重置',exact:true}).click()
   gridFixture=true
   await page.setViewportSize({width:1440,height:780})
   await panel.getByRole('button',{name:'数据',exact:true}).click()
   await dataPane.getByRole('button',{name:'按 item_index 排序',exact:true}).waitFor()
   const grid=dataPane.locator('.result-scroll')
   const layout=await grid.evaluate(el=>({height:el.clientHeight,overflow:el.scrollWidth>el.clientWidth,headerHeight:el.querySelector('thead').getBoundingClientRect().height,itemWidth:[...el.querySelectorAll('th')].find(th=>th.textContent.trim()==='item_index').getBoundingClientRect().width,rows:[...el.querySelectorAll('tbody tr')].filter(tr=>tr.getBoundingClientRect().bottom<=el.getBoundingClientRect().bottom&&tr.getBoundingClientRect().top>=el.getBoundingClientRect().top).length}))
   await page.screenshot({path:'/tmp/workbench-grid-1440.png',fullPage:true})
   console.log('grid layout',layout)
   assert(layout.headerHeight<=40,'grid header wraps or grows taller than one line')
   assert(layout.itemWidth>=120,'numeric column squeezed below its minimum width')
   assert(layout.overflow,'wide result must scroll inside its own viewport')
   assert(layout.rows>=6,'default layout leaves too few rows visible')
   await page.screenshot({path:'/tmp/workbench-grid-1440.png',fullPage:true})
   await dataPane.getByRole('button',{name:'放大结果',exact:true}).click()
   assert.equal(await panel.locator('aside').isVisible(),false)
   assert.equal(await dataPane.getByRole('region',{name:'表数据筛选'}).isVisible(),false)
   assert(await grid.evaluate(el=>el.clientHeight)>layout.height+70,'focus mode failed to enlarge result height')
   await grid.evaluate(el=>el.scrollLeft=el.scrollWidth)
   await dataPane.getByRole('button',{name:'详情',exact:true}).first().click()
   await dataPane.locator('.expanded-row').waitFor()
   assert.equal(await dataPane.locator('.expanded-row dt').count(),6)
   await page.screenshot({path:'/tmp/workbench-grid-focused-1440.png',fullPage:true})
   await dataPane.getByRole('button',{name:'还原布局',exact:true}).click()
   assert.equal(await panel.locator('aside').isVisible(),true)
   for(const width of [900,700]){
    await page.setViewportSize({width,height:780})
    assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth+2),false,'wide table leaks horizontal page overflow')
    assert(await grid.locator('thead').evaluate(el=>el.getBoundingClientRect().height)<=40)
   }
   gridFixture=false
   await page.setViewportSize({width:1440,height:1000})
   checks.push('wide JSON grid / single-line headers / minimum column widths / visible rows / internal horizontal scroll / focus and restore / row details / narrow layouts')
   await panel.getByRole('textbox',{name:'筛选表名'}).fill('users');assert.equal(await panel.getByRole('button',{name:'MixedCase',exact:true}).count(),0)
   checks.push('table tree / case preservation / primary order / pagination / schema / indexes')
  }else{
   await panel.getByRole('button',{name:'搜索',exact:true}).click();await panel.getByText('已加载 6 个 Key',{exact:false}).waitFor();assert.equal(reads.length,1);assert.equal(await panel.locator('.object-list button').count(),6)
   await panel.getByRole('button',{name:'加载更多',exact:true}).click();await page.waitForFunction(()=>document.querySelector('.redis-key-browser .browser-hint')?.textContent.includes('6 个'))
   await panel.getByRole('button',{name:'加载更多',exact:true}).click();await panel.getByText('已加载 7 个 Key',{exact:false}).waitFor();assert.equal(reads.length,3);assert.equal(await panel.getByRole('button',{name:'加载更多',exact:true}).isDisabled(),true)
   assert.match(reads[1].commands,/SCAN 11 /);assert.match(reads[2].commands,/SCAN 22 /)
   await panel.getByRole('button',{name:'menu:a',exact:true}).click();await panel.locator('.string-preview').waitFor();assert.equal(reads.length,5);await panel.getByText('仅预览前 64 KiB，内容已截断。',{exact:true}).waitFor();assert.equal(await panel.locator('script').count(),0)
   await panel.getByRole('button',{name:'menu:hash',exact:true}).click();await panel.getByText('first',{exact:true}).waitFor();await panel.getByRole('button',{name:'加载更多内容',exact:true}).click();await panel.getByText('updated',{exact:true}).waitFor();assert.equal(await panel.locator('tbody tr').count(),3);await page.screenshot({path:'/tmp/redis-browser-hash.png',fullPage:true})
   await panel.getByRole('button',{name:'menu:set',exact:true}).click();await panel.getByText('已展示 2 项',{exact:false}).waitFor();await panel.getByRole('button',{name:'加载更多内容',exact:true}).click();await panel.getByText('已展示 3 项',{exact:false}).waitFor()
   await panel.getByRole('button',{name:'menu:zset',exact:true}).click();await panel.getByText('1.5',{exact:true}).waitFor()
   await panel.getByRole('button',{name:'menu:list',exact:true}).click();await panel.getByText('item 49',{exact:true}).waitFor();await panel.getByRole('button',{name:'加载更多内容',exact:true}).click();await panel.getByText('item 99',{exact:true}).waitFor();await panel.getByRole('button',{name:'加载更多内容',exact:true}).click();await panel.getByText('已展示 100 项 · 本轮读取结束',{exact:true}).waitFor()
   await panel.getByRole('button',{name:'menu:gone',exact:true}).click();await panel.getByText('Key 已过期或被删除，请重新搜索。',{exact:true}).waitFor()
   await panel.getByRole('textbox',{name:'Key 搜索模式'}).fill('menu:cap*');await panel.getByRole('button',{name:'搜索',exact:true}).click();await panel.getByText('已达到浏览上限（1000 个或 2 MiB）',{exact:false}).waitFor();assert.equal(await panel.getByRole('button',{name:'加载更多',exact:true}).isDisabled(),true)
   checks.push('one SCAN per click / empty batch / duplicate keys / cap / five types / TTL / truncation / expired keys')
  }
  const savedReads=reads.length
  await page.getByRole('button',{name:'＋ 新查询',exact:true}).click();assert.equal(await panel.locator('.object-list button').count()===0,kind==='redis')
  await page.locator('.query-tabs').getByRole('button',{name:'查询 1',exact:true}).click();assert.equal(reads.length,savedReads);assert(await panel.locator('.object-list button').count()>0)
  await choose(kind==='dbms'?'数据库连接':'Redis 连接','conn-b');assert.equal(await panel.locator('.object-list button').count(),0,'old target data leaked')
  slow=true
  await panel.getByRole('button',{name:kind==='dbms'?'加载表列表':'搜索',exact:true}).click();await page.waitForFunction(()=>[...document.querySelectorAll('.resource-browser button')].some(b=>b.disabled));assert.equal(await panel.getByRole('button',{name:kind==='dbms'?'读取中…':'搜索',exact:true}).isDisabled(),true);releaseSlow()
  await page.waitForFunction(()=>document.querySelector('.resource-browser .object-list button')&&!document.querySelector('.resource-browser .object-list button').disabled)
  approval=true
  await page.locator('nav[aria-label="工作台导航"]').getByRole('button',{name:'执行记录',exact:true}).click();await page.locator('nav[aria-label="工作台导航"]').getByRole('button',{name:'查询',exact:true}).click();await page.getByText('查询需审批',{exact:false}).waitFor()
  const beforeApproval=reads.length
  await panel.getByRole('button',{name:kind==='dbms'?'申请读取表列表':'申请加载更多',exact:true}).click();await page.getByRole('heading',{name:'查询申请 #900001',exact:true}).waitFor();assert.equal(reads.length,beforeApproval);assert.equal(requests.length,1)
  ticket={...ticket,status:'approved'};await page.locator('nav[aria-label="工作台导航"]').getByRole('button',{name:'执行记录',exact:true}).click();await page.mouse.move(1400,10);await page.getByRole('button',{name:'执行已批准查询',exact:true}).click();await panel.locator('.object-list button').first().waitFor();assert.equal(await page.locator('.query-tabs>div').count(),2,'approved browser read created another session');assert.equal(reads.length,beforeApproval+1)
  for(const width of [1440,900,700]){await page.setViewportSize({width,height:1000});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false,'horizontal overflow');await page.screenshot({path:`/tmp/workbench-browser-${kind}-${width}.png`,fullPage:true})}
  if(kind==='redis'){
   approval=false
   await choose('查询方式','应用资源');await choose('应用','orders');await choose('逻辑 Redis 资源','cache')
   await panel.getByRole('textbox',{name:'直接查看 Key'}).fill('menu:hash')
   await panel.getByRole('button',{name:'查看 Key',exact:true}).click();await panel.getByText('first',{exact:true}).waitFor()
   await panel.getByRole('button',{name:'新建清理草稿',exact:true}).click()
   assert.equal(await page.locator('.editor textarea').first().inputValue(),'UNLINK "menu:hash"')
   assert.equal(unexpected.length,0,'draft creation executed a write')
   checks.push('Redis browser creates a scoped draft without executing writes')
  }
  checks.push(kind+' session isolation / request serialization / approval / responsive')
  await page.close()
 }
 assert.deepEqual(errors,[]);assert.deepEqual(unexpected,[])
 console.log(JSON.stringify({ok:true,checks}))
}finally{for(const page of browser.contexts().flatMap(c=>c.pages()))await page.screenshot({path:'/tmp/resource-browser-test-state.png',fullPage:true}).catch(()=>{});await browser.close()}
