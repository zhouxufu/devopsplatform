// Fully intercepted fixture: no source Bugs, rules or deployment data changed.
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import path from 'node:path'
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright')
const base='http://127.0.0.1:9800',dist=path.resolve('web/dist')
const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH})
const page=await browser.newPage({viewport:{width:1440,height:960}})
const errors=[],unexpected=[],writes=[],queries=[]
page.on('pageerror',e=>errors.push(e.message))
page.on('dialog',d=>d.accept())
const work={id:7,source:'zentao:issues',external_id:'26543',kind:'bug',title:'测试阶段修复',description:'<p>测试问题</p>',status:'open',external_status:'active',owner:'alice',assigned_to:'alice',resolved_by:'bob',release_required:false,release_origin:'rule',release_reason:'所属项目 #165 · 测试项目',metadata_revision:0,following:false}
const main={...work,id:8,external_id:'26557',title:'主项生产修复',release_required:true,external_status:'closed'}
let changes=[],digest='empty',failFollow=false
let rules={enabled:false,default:'unknown',projects:[],revision:0}
await page.route('**/*',async route=>{
 const req=route.request(),u=new URL(req.url()),p=u.pathname.replace('/api/plugins/research',''),method=req.method(),reply=json=>route.fulfill({json})
 if(u.origin!==base)return route.abort()
 if(u.pathname==='/api/plugins/research/ui')return route.fulfill({body:await readFile(path.join(dist,'research.html')),contentType:'text/html'})
 if(/\/assets\/[^/]+\.(js|css)$/.test(u.pathname))return route.fulfill({body:await readFile(path.join(dist,'assets',path.basename(u.pathname))),contentType:u.pathname.endsWith('.js')?'text/javascript':'text/css'})
 if(!['fetch','xhr'].includes(req.resourceType()))return route.abort()
 if(method==='GET'){
  if(p==='/api/me')return reply({username:'alice',role:'admin',auth_enabled:true})
  if(p==='/api/plugins')return reply([{name:'research'}])
  if(p==='/api/services')return reply([])
  if(p==='/api/research/overview')return reply({actor:'alice',aliases:['alice'],connections:[{name:'issues',account:'alice',authorized:true,auth_status:'connected',credential_saved:true}]})
  if(p==='/api/research/work-items/sources')return reply([{name:'issues',products:[1]}])
  if(p==='/api/research/work-items/page'){
   queries.push(u.searchParams)
   const items=u.searchParams.get('main_candidate')==='1'?[main]:u.searchParams.get('relation')==='following'&&!work.following?[]:[{...work}]
   return reply({items,total:items.length,page:1,page_size:20,total_exact:true,has_more:false,read_identity:'personal',people:{alice:'Alice',bob:'Bob'},products:{1:'产品 A'}})
  }
  if(p==='/api/research/work-items/7')return reply({item:{...work},remote:{bug:{id:26543,product:1,title:work.title,steps:work.description,status:work.external_status,assignedTo:work.assigned_to},digest:'fresh'},source_identity:'personal',activity:[],pending_operations:[]})
  if(p==='/api/research/work-items/7/action-options')return reply({bug:{id:26543,product:1,title:work.title,status:work.external_status,steps:work.description},digest:'fresh',assignedTo:'qa',resolutions:{fixed:'已修复'},users:{alice:'Alice',qa:'QA'},builds:{trunk:'主干'}})
  if(p==='/api/work-items/7/delivery-content')return reply({changes,digest})
  if(p==='/api/research/work-items/7/tracking')return reply({deliveries:[],versions:[]})
  if(p==='/api/research/work-items/7/delivery-records')return reply({items:[],has_more:false})
  if(p==='/api/research/tasks'||p==='/api/research/deliveries')return reply({items:[],total:0,page:1})
  if(p==='/api/research/connections/issues/account-hint')return reply({account:'alice',linked:true})
  if(p==='/api/research/connections/issues/release-rules')return reply(rules)
 }
 if(method==='PUT'&&p==='/api/research/work-items/7/follow'){
  writes.push({p,body:req.postDataJSON()})
  if(failFollow)return route.fulfill({status:409,json:{error:'模拟关注失败'}})
  work.following=req.postDataJSON().following;return reply({following:work.following})
 }
 if(method==='PUT'&&p==='/api/work-items/7/delivery-content'){
  const body=req.postDataJSON();writes.push({p,body});assert.equal(body.digest,digest)
  changes=body.changes.map((c,i)=>({...c,id:10+i,status:'draft',owner:'alice'}));digest='saved';return reply({changes,digest})
 }
 if(method==='PUT'&&p==='/api/research/connections/issues/release-rules'){
  const body=req.postDataJSON();writes.push({p,body});assert.equal(body.revision,rules.revision);rules={...body,revision:rules.revision+1};return reply(rules)
 }
 if(method==='POST'&&p==='/api/research/work-items/attach'){const body=req.postDataJSON();writes.push({p,body});return reply(body.external_id==='26557'?main:work)}
 if(method==='POST'&&p==='/api/research/work-items/7/actions'){
  const body=req.postDataJSON();writes.push({p,body});assert.equal(body.action,'resolve');assert.equal(body.metadata.release_required,null,'default rule result became a manual override');assert.equal(body.keep_following,true)
  work.external_status='resolved';return reply({id:body.operation_id,state:'success'})
 }
 unexpected.push(method+' '+p);return route.fulfill({status:404,json:{error:'unexpected fixture route'}})
})
const choose=async(container,label,option)=>{await container.locator('label').filter({hasText:label}).locator('.el-select').click();await page.getByRole('option',{name:option,exact:true}).click()}
try{
 await page.goto(base+'/api/plugins/research/ui#/work')
 await page.getByRole('button',{name:work.title,exact:true}).waitFor()
 await page.getByPlaceholder('搜索标题或编号').fill('测试')
 await page.getByRole('button',{name:'关注 26543',exact:true}).click()
 await page.getByRole('button',{name:'取消关注 26543',exact:true}).waitFor()
 assert.equal(await page.getByPlaceholder('搜索标题或编号').inputValue(),'测试')
 await page.getByRole('button',{name:'我关注的',exact:true}).click()
 await page.waitForFunction(()=>document.querySelector('[aria-label="工作项快捷视图"] button[aria-pressed=true]')?.textContent.includes('我关注的'))
 await page.getByRole('button',{name:work.title,exact:true}).waitFor()
 assert.equal(queries.at(-1).get('relation'),'following');assert.equal(queries.at(-1).get('mine'),'')
 await page.getByRole('button',{name:'与我相关的待上线',exact:true}).click()
 await page.waitForTimeout(100)
 assert.equal(queries.at(-1).get('relation'),'related');assert.equal(queries.at(-1).get('delivery'),'pending_release');assert.equal(queries.at(-1).get('person'),'')
 await page.getByRole('button',{name:'全部',exact:true}).click()
 await page.getByRole('button',{name:'解决',exact:true}).click()
 const dialog=page.getByRole('dialog')
 await dialog.getByText('所属项目 #165 · 测试项目',{exact:true}).waitFor()
 assert.equal(await dialog.getByText('生产交付判定',{exact:true}).count(),0,'resolve forced a release decision')
 await dialog.getByRole('button',{name:'维护交付内容',exact:true}).click()
 await dialog.getByRole('button',{name:'自定义记录 · 0',exact:true}).click()
 await dialog.getByRole('button',{name:'＋ 添加自定义记录',exact:true}).click()
 await dialog.getByPlaceholder('选填；留空按类型和应用自动命名').fill('补充菜单核对')
 await dialog.getByPlaceholder(/记录平台暂不支持/).fill('手工核对菜单，不执行脚本')
 await dialog.getByText('开发／测试可以继续；生产前需关联主项，或在版本中明确选入本条变更。',{exact:true}).waitFor()
 await choose(dialog,'生产交付归属','随其他工作项交付')
 await dialog.locator('.owner-results button').filter({hasText:'26557 · 主项生产修复'}).click()
 assert.ok(queries.some(q=>q.get('main_candidate')==='1'),'main picker excluded closed/unreleased work')
 assert.equal(page.url(),base+'/api/plugins/research/ui#/work','ownership selection changed page')
 await dialog.getByRole('button',{name:'解决信息',exact:true}).click()
 await dialog.getByRole('button',{name:'确认并同步来源平台',exact:true}).click()
 await dialog.waitFor({state:'hidden'})
 const saved=writes.find(w=>w.p.endsWith('/delivery-content')).body
 assert.equal(saved.changes.length,1);assert.deepEqual(saved.changes[0].delivery_ownership,{mode:'follow',origin_work_item_id:7,work_item_id:8})
 assert.ok(writes.findIndex(w=>w.p.endsWith('/delivery-content'))<writes.findIndex(w=>w.p.endsWith('/actions')),'source resolved before drafts saved')
 assert.equal(await page.getByPlaceholder('搜索标题或编号').inputValue(),'测试')
 // Failed follow writes keep the existing state rather than optimistic success.
 failFollow=true
 await page.getByRole('button',{name:'取消关注 26543',exact:true}).click()
 await page.getByText('模拟关注失败',{exact:true}).waitFor()
 assert.equal(work.following,true)
 await page.goto(base+'/api/plugins/research/ui#/settings')
 await page.getByText('生产交付识别规则（管理员）',{exact:true}).click()
 await page.getByRole('button',{name:'＋ 项目规则',exact:true}).waitFor()
 await page.getByLabel('启用项目识别规则').check()
 await page.getByRole('button',{name:'＋ 项目规则',exact:true}).click()
 await page.getByRole('textbox',{name:'所属项目 ID',exact:true}).fill('75')
 await page.getByRole('textbox',{name:'项目名称',exact:true}).fill('生产环境Bug/需求')
 await page.getByRole('button',{name:'保存上线规则',exact:true}).click()
 await page.waitForFunction(()=>document.querySelector('.el-message__content')?.textContent.includes('上线规则已保存'))
 assert.equal(rules.projects[0].project_id,'75');assert.equal(rules.projects[0].decision,'independent');assert.equal(rules.revision,1)
 await page.screenshot({path:'/tmp/research-release-policy-settings-qa.png'})
 assert.deepEqual(unexpected,[]);assert.deepEqual(errors,[])
 console.log('Research rules, follow filters, per-change ownership and resolve continuity UI passed; all requests intercepted.')
}finally{await browser.close()}
