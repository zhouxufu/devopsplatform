// Fully intercepted fixture: no actual Bug resolution, uploads or registration.
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import path from 'node:path'
import {safeWorkReturn,workOrigin} from '../web/src/research/navigation.js'
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright')
const base='http://127.0.0.1:9800',dist=process.env.CONSOLE_UI_DIST||path.resolve('web/dist')
assert.equal(safeWorkReturn('https://evil.invalid'),'/work')
assert.equal(safeWorkReturn('//evil.invalid'),'/work')
assert.equal(safeWorkReturn('/work/7'),'/work')
assert.equal(safeWorkReturn('/?view=work'),'/?view=work')
assert.equal(workOrigin({fullPath:'/'},true),'/?view=work')
const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH})
const page=await browser.newPage({viewport:{width:1440,height:960}}),errors=[],unexpected=[],writes=[],queries=[],dialogs=[]
page.on('pageerror',e=>errors.push(e.message))
page.on('console',m=>{if(m.type()==='warning'&&m.text().includes('Runtime directive'))errors.push(m.text())})
page.on('dialog',async d=>{dialogs.push(d.message());await d.dismiss()})
let identity='personal',uncertain=false,holdOptions=false,releaseOptions
let draftFail=true,deliveryChanges=[],deliveryDigest='draft-1'
const work={id:7,source:'zentao:issues',external_id:'26087',kind:'bug',title:'菜单修复',status:'open',external_status:'active',owner:'alice',assigned_to:'alice',resolved_by:'alice',description:'<p>源描述</p>',release_required:true,metadata_revision:3}
const bug={id:26087,title:work.title,status:'active',steps:work.description,assignedTo:'alice'}
let pending=[]
const options=()=>({bug:{...bug},digest:'fresh-digest',assignedTo:'qa',resolutions:{fixed:'已修复',duplicate:'重复 Bug'},users:{alice:'Alice',qa:'测试'},builds:{trunk:'主干'}})
await page.route('**/*',async route=>{
 const req=route.request(),url=new URL(req.url()),p=url.pathname.replace('/api/plugins/research',''),method=req.method(),reply=json=>route.fulfill({json})
 if(url.origin!==base)return route.abort()
 if(url.pathname==='/api/plugins/research/ui')return route.fulfill({body:await readFile(path.join(dist,'research.html')),contentType:'text/html'})
 if(/\/assets\/[^/]+\.(js|css)$/.test(url.pathname))return route.fulfill({body:await readFile(path.join(dist,'assets',path.basename(url.pathname))),contentType:url.pathname.endsWith('.js')?'text/javascript':'text/css'})
 if(!['fetch','xhr'].includes(req.resourceType()))return route.abort()
 if(method==='GET'){
  if(p.endsWith('/tracking'))return reply({deliveries:[],versions:[]})
  if(p.endsWith('/delivery-records'))return reply({items:[],has_more:false})
  if(p==='/api/me')return reply({username:'alice',role:'member',auth_enabled:true})
  if(p==='/api/plugins')return reply([{name:'research'}])
  if(p==='/api/services')return reply([])
  if(p==='/api/research/overview')return reply({actor:'alice',aliases:['alice'],connections:[{name:'issues',account:'alice',authorized:true}]})
  if(p==='/api/research/work-items/sources')return reply([{name:'issues',products:[1]}])
  if(p==='/api/research/tasks'||p==='/api/research/deliveries')return reply({items:[],total:0,page:1})
  if(p==='/api/research/work-items/page'){queries.push(url.searchParams);return reply({items:[{...work}],total:40,page:Number(url.searchParams.get('page')),page_size:20,total_exact:true,has_more:url.searchParams.get('page')!=='2',read_identity:identity,people:{alice:'Alice',qa:'测试'},products:{1:'产品 A'}})}
  if(p==='/api/research/work-items/7')return reply({item:{...work},remote:{bug:{...bug},digest:'fresh-digest'},source_identity:identity,activity:[],pending_operations:pending})
  if(p==='/api/research/work-items/7/action-options'){if(holdOptions)await new Promise(resolve=>releaseOptions=resolve);return reply(options())}
  if(p==='/api/work-items/7/delivery-content')return reply({changes:deliveryChanges,digest:deliveryDigest})
 }
 if(method==='PUT'&&p==='/api/work-items/7/delivery-content'){
  const body=req.postDataJSON();writes.push({p,body});assert.equal(body.digest,deliveryDigest)
  if(draftFail)return route.fulfill({status:409,json:{error:'模拟草稿冲突'}})
  deliveryChanges=body.changes.map((c,i)=>({...c,id:5+i,status:'draft',owner:'alice'}));deliveryDigest='draft-2';return reply({changes:deliveryChanges,digest:deliveryDigest})
 }
 if(method==='POST'){
  if(p==='/api/research/work-items/attach'){writes.push({p,body:req.postDataJSON()});return reply({...work})}
  if(p==='/api/research/work-items/7/actions'){
   const body=req.postDataJSON();writes.push({p,body});assert.equal(body.action,'resolve');assert.equal(body.expected_digest,'fresh-digest');assert.equal(body.metadata.revision,3)
   if(uncertain){pending=[{id:body.operation_id,state:'uncertain',message:'结果待核对',request:body}];return reply(pending[0])}
   work.external_status='resolved';bug.status='resolved';work.release_required=body.metadata.release_required;work.metadata_revision++
   return reply({id:body.operation_id,state:'success',message:'已同步'})
  }
  if(p.startsWith('/api/research/operations/')&&p.endsWith('/reconcile')){writes.push({p,body:req.postDataJSON()});pending=[];return reply({id:p.split('/').at(-2),state:'success',message:'已核对'})}
 }
 unexpected.push(method+' '+p);return route.fulfill({status:404,json:{error:'unexpected fixture route'}})
})
const dialog=()=>page.getByRole('dialog')
const choose=async(label,option)=>{await dialog().locator('label').filter({hasText:label}).locator('.el-select').click();await page.getByRole('option',{name:option,exact:true}).click()}
const actionWrites=()=>writes.filter(w=>w.p.endsWith('/actions'))
try{
 await page.goto(base+'/api/plugins/research/ui')
 await page.getByRole('button',{name:'我的工作项',exact:true}).click()
 await page.getByRole('button',{name:'菜单修复',exact:true}).waitFor()
 assert.match(page.url(),/#\/\?view=work$/)
 await page.getByPlaceholder('搜索标题或编号').fill('菜单')
 await page.getByRole('button',{name:'搜索',exact:true}).click()
 await page.getByRole('button',{name:'下一页',exact:true}).click()
 await page.waitForFunction(()=>document.querySelector('.r-pagination')?.textContent.includes('2 / 2'))
 const origin=page.url()
 await page.getByRole('button',{name:'解决',exact:true}).click()
 await dialog().getByText('处理说明',{exact:true}).waitFor()
 assert.equal(page.url(),origin,'resolve navigated away from my work list')
 await dialog().getByRole('button',{name:'人工调整',exact:true}).click()
 await choose('生产交付判定','不独立交付')
 await dialog().getByRole('button',{name:'确认并同步来源平台',exact:true}).click()
 assert.equal(actionWrites().length,0,'invalid form sent a write')
 await dialog().getByText('请填写人工调整原因',{exact:true}).waitFor()
 await dialog().locator('label').filter({hasText:'调整原因'}).locator('input').fill('重复问题')
 await dialog().locator('label').filter({hasText:'处理说明'}).locator('textarea').fill('已核对菜单权限')
 await dialog().getByRole('button',{name:'取消',exact:true}).click()
 assert.equal(dialogs.length,1,'dirty close did not request confirmation')
 assert.equal(await dialog().locator('textarea').inputValue(),'已核对菜单权限','dismissed discard lost draft')
 await dialog().getByRole('button',{name:'确认并同步来源平台',exact:true}).click()
 await dialog().waitFor({state:'hidden'})
 await page.getByRole('button',{name:'关闭',exact:true}).waitFor()
 assert.equal(page.url(),origin)
 assert.equal(await page.getByPlaceholder('搜索标题或编号').inputValue(),'菜单')
 assert.equal(queries.at(-1).get('page'),'2')
 assert.equal(actionWrites().length,1)
 assert.equal(actionWrites()[0].body.metadata.release_required,false)
 assert.equal(actionWrites()[0].body.metadata.no_release_reason,'重复问题')
 // Detail and editing paths retain the original inbox tab and list state.
 await page.getByRole('button',{name:'菜单修复',exact:true}).click()
 await page.getByRole('heading',{name:'菜单修复',exact:true}).waitFor()
 assert.equal(new URLSearchParams(page.url().split('?').slice(1).join('?')).get('return'),'inbox')
 await page.getByRole('button',{name:'← 返回原列表',exact:true}).click()
 await page.getByPlaceholder('搜索标题或编号').waitFor()
 assert.equal(page.url(),origin)
 assert.equal(await page.getByPlaceholder('搜索标题或编号').inputValue(),'菜单')
 // Refreshing a detail also returns to the work tab instead of the task tab.
 await page.getByRole('button',{name:'菜单修复',exact:true}).click()
 await page.getByRole('heading',{name:'菜单修复',exact:true}).waitFor();await page.reload()
 await page.getByRole('button',{name:'← 返回原列表',exact:true}).click()
 await page.getByRole('button',{name:'我的工作项',exact:true}).waitFor()
 assert.equal(await page.getByRole('button',{name:'我的工作项',exact:true}).getAttribute('aria-pressed'),'true')
 await page.getByRole('button',{name:'菜单修复',exact:true}).waitFor()
 // Standalone list also stays in place, and unresolved receipts never replay.
 work.external_status='active';bug.status='active';work.metadata_revision=3;work.release_required=true
 await page.goto(base+'/api/plugins/research/ui#/work')
 uncertain=true
 await page.getByRole('button',{name:'解决',exact:true}).click()
 await dialog().getByText('处理说明',{exact:true}).waitFor()
 await dialog().getByRole('button',{name:'确认并同步来源平台',exact:true}).click()
 await dialog().getByText('结果待核对',{exact:true}).waitFor()
 assert.equal(await dialog().getByRole('button',{name:'确认并同步来源平台',exact:true}).count(),0)
 await dialog().getByRole('button',{name:'核对结果（不重放写操作）',exact:true}).click()
 await dialog().waitFor({state:'hidden'})
 assert.match(page.url(),/#\/work$/)
 assert.equal(actionWrites().length,2,'reconciliation replayed resolve')
 // Resolve and changes share one dialog; saving drafts precedes the source
 // action, and a local failure never resolves the Bug.
 uncertain=false
 await page.getByRole('button',{name:'解决',exact:true}).click()
 await dialog().getByRole('button',{name:'维护交付内容',exact:true}).click()
 await dialog().getByRole('button',{name:'自定义记录 · 0',exact:true}).click()
 await dialog().getByRole('button',{name:'＋ 添加自定义记录',exact:true}).click()
 await dialog().getByPlaceholder('选填；留空按类型和应用自动命名').fill('人工更新外部菜单')
 const memo='1. 登录外部平台\n2. 更新菜单并核对\n<script>文本不是脚本</script>'
 await dialog().getByPlaceholder(/记录平台暂不支持/).fill(memo)
 await dialog().getByRole('button',{name:'解决信息',exact:true}).click()
 await dialog().getByRole('button',{name:'维护交付内容',exact:true}).click()
 assert.equal(await dialog().getByPlaceholder(/记录平台暂不支持/).inputValue(),memo)
 assert.equal(await dialog().getByRole('button',{name:'保存交付草稿',exact:true}).count(),1,'one shared dialog footer')
 await dialog().getByRole('button',{name:'返回解决信息',exact:true}).click()
 await dialog().getByRole('button',{name:'确认并同步来源平台',exact:true}).click()
 await dialog().getByText('变更未全部保存，尚未提交 Bug 解决操作。',{exact:true}).waitFor()
 assert.equal(actionWrites().length,2)
 assert.equal(await dialog().getByPlaceholder(/记录平台暂不支持/).inputValue(),memo)
 for(const width of [900,390]){await page.setViewportSize({width,height:800});const footer=await dialog().locator('.el-dialog__footer').boundingBox();assert.ok(footer.y+footer.height<=800);assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false)}
 await page.setViewportSize({width:1440,height:960});await page.evaluate(async()=>{await new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r)));await Promise.all(document.getAnimations().filter(a=>a.effect?.getComputedTiming().iterations!==Infinity).map(a=>a.finished.catch(()=>{})))});await page.screenshot({path:'/tmp/research-resolve-changes-qa.png'})
 draftFail=false
 await dialog().getByRole('button',{name:'返回解决信息',exact:true}).click()
 await dialog().getByRole('button',{name:'确认并同步来源平台',exact:true}).click()
 await dialog().waitFor({state:'hidden'})
 assert.equal(actionWrites().length,3)
 assert.equal(deliveryChanges.length,1);assert.equal(deliveryChanges[0].kind,'manual');assert.equal(deliveryChanges[0].reference,memo)
 assert.deepEqual(writes.slice(-2).map(w=>w.p),['/api/work-items/7/delivery-content','/api/research/work-items/7/actions'])
 work.external_status='active';bug.status='active';work.metadata_revision=3
 await page.reload();await page.getByRole('button',{name:'解决',exact:true}).waitFor()
 // Loading may be canceled; late options never resurrect the modal.
 holdOptions=true;uncertain=false
 await page.getByRole('button',{name:'解决',exact:true}).click()
 await dialog().getByText('正在读取最新状态和解决选项…',{exact:true}).waitFor()
 await dialog().getByRole('button',{name:'取消',exact:true}).click()
 await dialog().waitFor({state:'hidden'});releaseOptions?.();holdOptions=false
 await page.getByRole('button',{name:'解决',exact:true}).click()
 await dialog().getByText('处理说明',{exact:true}).waitFor()
 for(const width of [1440,900,700,390]){await page.setViewportSize({width,height:800});const footer=await dialog().locator('.el-dialog__footer').boundingBox();assert.ok(footer.y+footer.height<=800);assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false)}
 await page.setViewportSize({width:1440,height:960});await page.evaluate(()=>Promise.all(document.getAnimations().filter(a=>a.effect?.getComputedTiming().iterations!==Infinity).map(a=>a.finished.catch(()=>{}))));await page.screenshot({path:'/tmp/research-resolve-dialog-qa.png'})
 await dialog().getByRole('button',{name:'取消',exact:true}).click();await dialog().waitFor({state:'hidden'})
 identity='public';await page.reload();await page.getByRole('button',{name:'解决',exact:true}).click()
 await dialog().getByText('当前使用公共身份读取，请先验证本人来源账号后解决 Bug。',{exact:true}).waitFor()
 assert.equal(await dialog().getByRole('button',{name:'确认并同步来源平台',exact:true}).count(),0)
 assert.equal(actionWrites().length,3)
 assert.deepEqual(unexpected,[]);assert.deepEqual(errors,[])
 console.log('PASS resolve modal, inbox return origin, retained filters/page, validation/discard, uncertainty without replay, canceled loading, source guards, inline custom records, draft failure blocks source action, save-before-resolve ordering and responsive footer; fixture only')
}finally{await browser.close()}
