// Fixture only: no real credentials, source writes or login changes.
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import path from 'node:path'
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright')
const base='http://127.0.0.1:9800',dist=path.resolve('web/dist')
const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH})
const page=await browser.newPage({viewport:{width:1440,height:960}}),errors=[],unexpected=[],writes=[],dialogs=[]
page.on('pageerror',e=>errors.push(e.message));page.on('dialog',d=>{dialogs.push(d.message());d.dismiss()})
let status='unverified',saved=false,fail=false
const work={id:7,source:'zentao:issues',external_id:'7',kind:'bug',title:'菜单修复',status:'open',external_status:'active',owner:'sync',description:'<p>Bug</p>'}
await page.route('**/*',async route=>{
 const req=route.request(),url=new URL(req.url()),p=url.pathname.replace('/api/plugins/research',''),method=req.method(),reply=json=>route.fulfill({json})
 if(url.origin!==base)return route.abort()
 if(url.pathname==='/api/plugins/research/ui')return route.fulfill({body:await readFile(path.join(dist,'research.html')),contentType:'text/html'})
 if(/\/assets\/[^/]+\.(js|css)$/.test(url.pathname))return route.fulfill({body:await readFile(path.join(dist,'assets',path.basename(url.pathname))),contentType:url.pathname.endsWith('.js')?'text/javascript':'text/css'})
 if(!['fetch','xhr'].includes(req.resourceType()))return route.abort()
 if(method==='GET'){
  if(p==='/api/me')return reply({username:'platform-alice',display_name:'Alice',role:'member',auth_enabled:true})
  if(p==='/api/plugins')return reply([{name:'research'}])
  if(p==='/api/research/overview')return reply({actor:'platform-alice',aliases:['alice'],connections:[{name:'issues',account:saved?'alice':'',credential_saved:saved,authorized:saved&&status!=='needs_verification',auth_status:status,public_read_configured:true}]})
  if(p==='/api/research/connections/issues/account-hint')return reply({account:'alice',linked:true})
  if(p==='/api/services')return reply([])
  if(p==='/api/research/work-items/7')return reply({item:work,remote:status==='connected'?{bug:{id:'7',title:work.title,status:'active',steps:work.description},digest:'fresh'}:null,source_error:status==='connected'?'':'来源认证已失效，请重新验证本人来源账号',activity:[],pending_operations:[]})
 }
 if(method==='POST'&&p==='/api/research/connections/issues/authorize'){
  const body=req.postDataJSON();assert.equal(body.account,'alice');writes.push({kind:'authorize',blank:body.password===''});if(fail){status='needs_verification';return route.fulfill({status:409,json:{error:'账号认证未通过，请重新验证'}})}
  saved=true;status='connected';return reply({ok:true})
 }
 if(method==='DELETE'&&p==='/api/research/connections/issues/authorize'){writes.push({kind:'revoke'});saved=false;status='unverified';return reply({ok:true})}
 unexpected.push(method+' '+p);return route.fulfill({status:404,json:{error:'unexpected fixture route'}})
})
try{
 await page.goto(base+'/api/plugins/research/ui#/settings?connection=issues&return_to=%2Fwork%2F7')
 const account=page.getByLabel('本人来源账号'),password=page.getByLabel('本人来源密码')
 await assert.doesNotReject(()=>account.waitFor());await page.getByText('已关联账号：alice；首次使用仍需验证。',{exact:true}).waitFor();assert.equal(await account.inputValue(),'alice')
 const save=page.getByRole('button',{name:'验证并返回原工作项',exact:true});assert.equal(await save.isDisabled(),true)
 await password.fill('fixture-secret');await save.click();await page.getByRole('heading',{name:'菜单修复',exact:true}).waitFor();assert.match(page.url(),/#\/work\/7$/)
 await page.goto(base+'/api/plugins/research/ui#/settings?connection=issues')
 const check=page.getByRole('button',{name:'检查本人连接',exact:true});await check.waitFor();assert.equal(await password.inputValue(),'');assert.equal(await check.isEnabled(),true);await check.click()
 await page.getByText('已验证本人来源账号，后续复用有效会话',{exact:true}).waitFor()
 status='needs_verification';await page.reload();await page.getByText('认证失效 · 请重新验证',{exact:true}).last().waitFor()
 const verify=page.getByRole('button',{name:'验证并保存本人账号',exact:true});assert.equal(await verify.isDisabled(),true)
 fail=true;await password.fill('incorrect-fixture');await verify.click();await page.getByText('账号认证未通过，请重新验证',{exact:true}).waitFor();assert.equal(await password.inputValue(),'')
 fail=false;await password.fill('fixture-secret');await verify.click();await check.waitFor()
 for(const width of [1440,900,700]){await page.setViewportSize({width,height:960});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false)}
 await page.setViewportSize({width:1440,height:960});await page.screenshot({path:'/tmp/research-accounts-qa.png',fullPage:true})
 await page.getByRole('button',{name:'移除本人凭据',exact:true}).click();await page.getByText('未验证本人账号',{exact:true}).last().waitFor();assert.equal(await password.inputValue(),'')
 assert.deepEqual(writes,[{kind:'authorize',blank:false},{kind:'authorize',blank:true},{kind:'authorize',blank:false},{kind:'authorize',blank:false},{kind:'revoke'}]);assert.deepEqual(unexpected,[]);assert.deepEqual(errors,[]);assert.deepEqual(dialogs,[])
 console.log('PASS identity prefill without authorization, saved-secret check, expiry/password reset, original-work return, revoke, three widths; fixture only')
}finally{await browser.close()}
