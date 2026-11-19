// Fully intercepted browser fixture. No business API or database is contacted.
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import path from 'node:path'
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright')
const base='http://127.0.0.1:19823',dist=process.env.CONSOLE_UI_DIST||path.resolve('web/dist')
const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH}),page=await browser.newPage({viewport:{width:1440,height:1000}})
const errors=[],unexpected=[],saves=[],freezes=[],compares=[]
page.on('pageerror',e=>errors.push(e.message));page.on('dialog',d=>d.accept())
let task,serial=0
const key=code=>JSON.stringify([code])
const treeData=()=>{
 const nodes=[{key:key('system'),label:'系统管理',kind:'unchanged',depth:0,source_id:'1',target_id:'100'},{key:key('users'),parent_key:key('system'),label:'用户管理',kind:'insert',depth:1,source_id:'2',target_id:'901'},{key:key('add'),parent_key:key('users'),label:'新增用户',kind:'insert',depth:2,source_id:'3',target_id:'902'},{key:key('keep'),parent_key:key('system'),label:'目标独有菜单',kind:'target_only',depth:1,target_id:'800'}]
 return {task,columns:['menu_id','pid','code','name'],differences:[{key:key('users'),kind:'insert',before:null,after:{menu_id:'901',pid:'100',code:'users',name:'用户管理'}},{key:key('add'),kind:'insert',before:null,after:{menu_id:'902',pid:'901',code:'add',name:'新增用户'}}],tree_nodes:nodes,unchanged:1,target_only:1}
}
await page.route('**/*',async route=>{
 const req=route.request(),url=new URL(req.url()),p=url.pathname.replace(/\/api\/plugins\/(dbms|deploy)/,'').replace('/api/workbench/dbms/','/api/'),reply=json=>route.fulfill({json})
 if(url.origin!==base)return route.abort()
 let file;if(/\/api\/plugins\/(dbms|deploy)\/ui$/.test(url.pathname))file=url.pathname.includes('/dbms/')?'workbench.html':'deploy.html';else if(/\/assets\/[^/]+\.(js|css)$/.test(p))file='assets/'+path.basename(p)
 if(file)return route.fulfill({body:await readFile(path.join(dist,file)),contentType:file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html'})
 if(req.method()==='GET'){
  if(p==='/api/me')return reply({username:'local',role:'admin',auth_enabled:true})
  if(p==='/api/registry/applications'||p==='/api/services')return reply([{name:'menu-api'}])
  if(p==='/api/environments')return reply(['dev','test','prod'].map(name=>({name,display_name:name})))
  if(p==='/api/policy')return reply({query_approval:false,change_approval:true})
  if(p==='/api/services/menu-api/resources')return reply({requirements:[{kind:'mysql',alias:'db'}],bindings:[]})
  if(p==='/api/data-sync/tasks')return reply(task?[task]:[])
  return reply([])
 }
 if(req.method()==='POST'&&p==='/api/data-sync/tasks'){task={...req.postDataJSON(),id:++serial,revision:1,owner:'local'};saves.push(task);return reply(task)}
 if(req.method()==='POST'&&p.endsWith('/compare')){const scope=req.postDataJSON();compares.push(scope);return reply({...task.mode==='tree'?treeData():{task,columns:['id','name'],differences:[{key:key('1'),kind:'update',before:{id:'1',name:'旧名称'},after:{id:'1',name:'新名称'}}],unchanged:0,target_only:0},...scope,digest:'comparison-digest'})}
 if(req.method()==='POST'&&p.endsWith('/freeze')){const body=req.postDataJSON();freezes.push(body);return reply({id:22,sql:{content:'INSERT INTO menu (menu_id,pid,code,name) VALUES (901,100,\'users\',\'用户管理\');',data_sync:{source_environment:body.source_environment,target_environment:body.target_environment}}})}
 unexpected.push(req.method()+' '+p);return route.abort()
})
const field=(root,label)=>root.locator('label').filter({hasText:new RegExp('^'+label)})
async function choose(root,label,option){const f=field(root,label);await f.locator('.el-select').click();const id=await f.getByRole('combobox').getAttribute('aria-controls');await page.locator(`[id="${id}"]`).getByRole('option',{name:option,exact:true}).click()}
async function scope(root){await choose(root,'本次来源环境','test');await choose(root,'本次目标环境','prod');await root.getByRole('button',{name:'对比数据（只读）',exact:true}).click()}
try{
 await page.goto(base+'/api/plugins/dbms/ui#/sync')
 const root=page.locator('.sync-tasks');await root.getByRole('button',{name:'＋ 新建任务',exact:true}).click()
 await field(root,'任务名称').locator('input').fill('菜单迁移');await choose(root,'应用','menu-api');await choose(root,'逻辑数据库资源','db');await choose(root,'数据结构','树状层级（菜单）')
 await field(root,'对比查询 SQL').locator('textarea').fill('SELECT menu_id,pid,code,name FROM menu')
 await field(root,'匹配键').locator('input').fill('code');await field(root,'ID 字段').locator('input').fill('menu_id');await field(root,'父 ID 字段').locator('input').fill('pid')
 await root.getByRole('button',{name:'保存任务',exact:true}).click();await root.getByText('任务已保存',{exact:true}).waitFor()
 assert.equal(saves[0].mode,'tree');assert.deepEqual(saves[0].keys,['code']);assert.deepEqual(saves[0].tree,{id_field:'menu_id',parent_field:'pid',label_field:'name',root_value:'0'});assert.equal(saves[0].source_sql,saves[0].target_sql)
 await scope(root);const diff=root.locator('.sync-diff');await diff.getByRole('tree').waitFor()
 assert.equal(await diff.getByRole('treeitem').count(),3);assert.equal(await diff.getByText('目标独有菜单',{exact:true}).count(),0)
 await diff.getByRole('checkbox',{name:'选择 新增用户',exact:true}).check();assert.equal(await diff.getByRole('checkbox',{name:'选择 用户管理',exact:true}).isChecked(),true)
 await diff.getByRole('button',{name:'新增用户',exact:true}).click();await diff.getByText('来源 ID 3 → 目标 ID 902；ID 与父 ID 已自动映射。',{exact:true}).waitFor()
 assert.equal(await diff.getByLabel('迁移后 pid',{exact:true}).count(),0);assert.equal(await diff.getByLabel('迁移后 menu_id',{exact:true}).count(),0)
 await diff.getByLabel('迁移后 name',{exact:true}).fill('新增用户按钮')
 await diff.getByRole('checkbox',{name:'选择 用户管理',exact:true}).uncheck();assert.equal(await diff.getByRole('checkbox',{name:'选择 新增用户',exact:true}).isChecked(),false)
 await diff.getByRole('checkbox',{name:'选择 新增用户',exact:true}).check()
 await diff.getByRole('button',{name:'收起 系统管理',exact:true}).click();assert.equal(await diff.getByRole('treeitem').count(),1)
 await diff.getByRole('button',{name:'展开 系统管理',exact:true}).click();assert.equal(await diff.getByRole('treeitem').count(),3)
 await diff.getByRole('checkbox',{name:'仅差异及相关父节点',exact:true}).uncheck();await diff.getByRole('button',{name:'目标独有菜单',exact:true}).click();await diff.getByText('此菜单仅存在于目标环境，将保留。',{exact:true}).waitFor()
 await diff.getByRole('button',{name:'新增用户',exact:true}).click()
 await page.screenshot({path:'/tmp/data-sync-tree-desktop.png',fullPage:true})
 // Both light and dark palettes must use theme tokens for highlighted cells.
 for(const dark of [false,true]){await page.evaluate(dark=>document.documentElement.classList.toggle('dark',dark),dark);const color=await diff.locator('.field-table .changed').first().evaluate(e=>getComputedStyle(e).backgroundColor);const token=await page.evaluate(()=>getComputedStyle(document.documentElement).getPropertyValue('--brand-soft').trim());assert(token);assert.notEqual(color,'rgba(0, 0, 0, 0)');await page.screenshot({path:'/tmp/data-sync-tree-'+(dark?'dark':'light')+'.png',fullPage:true})}
 for(const width of [1440,900,390]){await page.setViewportSize({width,height:1000});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth+1),false,`overflow at ${width}`)}
 await page.screenshot({path:'/tmp/data-sync-tree-mobile.png',fullPage:true});await page.setViewportSize({width:1440,height:1000})
 await root.getByRole('button',{name:/^生成同步草稿/}).click();await root.locator('.result').waitFor();assert.equal(freezes.length,1);assert.equal(freezes[0].rows.length,2);assert.equal(freezes[0].rows.find(r=>r.key===key('add')).after.name,'新增用户按钮');assert.equal(freezes[0].digest,'comparison-digest')
 // Flat mode stays independent and renders a real row table and field details.
 await root.getByRole('button',{name:'＋ 新建任务',exact:true}).click();await field(root,'任务名称').locator('input').fill('表数据');await choose(root,'应用','menu-api');await choose(root,'逻辑数据库资源','db')
 assert.equal(await field(root,'父 ID 字段').count(),0)
 await field(root,'对比查询 SQL').locator('textarea').fill('SELECT id,name FROM category');await field(root,'匹配键').locator('input').fill('id');await root.getByRole('button',{name:'保存任务',exact:true}).click();await root.getByText('任务已保存',{exact:true}).waitFor();await scope(root)
 await diff.locator('.sync-table').waitFor();assert.equal(await diff.getByRole('tree').count(),0);await diff.getByLabel('迁移后 name',{exact:true}).fill('编辑后名称');await diff.getByRole('checkbox',{name:'选择 ["1"]',exact:true}).check();await root.getByRole('button',{name:/^生成同步草稿/}).click();await root.locator('.result').waitFor();assert.equal(freezes[1].rows[0].after.name,'编辑后名称')
 assert.equal(saves.length,2);assert.deepEqual(errors,[]);assert.deepEqual(unexpected,[])
 console.log(JSON.stringify({ok:true,checks:['table/tree mode saved','custom ID/parent fields','stable business key matching','unchanged ancestor context','parent auto-selection','parent deselection removes dependent children','target-only preserved','expand/collapse','mapped IDs locked','field editing','frozen explicit scope','flat table regression','responsive 1440/900/390'],noRealWrites:true}))
}finally{await browser.close()}
