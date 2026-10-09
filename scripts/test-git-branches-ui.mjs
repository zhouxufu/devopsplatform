// Read-only shell/standalone QA. All branch probes and writes are mocked;
// no existing application's repository, credentials or build is touched.
import assert from 'node:assert/strict'
const base=process.env.SHELL_UI_TEST_URL||'http://127.0.0.1:9800'
assert.equal(new URL(base).hostname,'127.0.0.1')
const embedded=process.env.SHELL_UI_TEST_EMBEDDED==='1'
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright')
const browser=await chromium.launch({headless:true,...(process.env.CHROME_PATH?{executablePath:process.env.CHROME_PATH}:{})})
const page=await browser.newPage({viewport:{width:1440,height:1000}})
const ui=embedded?page.frameLocator('iframe[src*="/api/plugins/deploy/ui"]'):page
const errors=[],writes=[],probes=[],buildRequests=[]
page.on('pageerror',e=>errors.push(e.message));page.on('dialog',d=>d.accept())
const app={name:'branch-qa-api',git_url:'https://example.invalid/api.git',branch:'main',artifact_type:'jar',build_tool:'maven',maven_executable:'mvn',maven_goals:'package',artifact_glob:'target/*.jar'}
const apps=[app,{...app,name:'branch-qa-web',git_url:'https://example.invalid/web.git'}]
const work={id:991001,title:'分支连续性 QA',kind:'requirement',status:'open',owner:'local',description:''}
let fail=false,slow=false,emptyBranches=false,releaseSlow
await page.route('**/*',async route=>{
 const request=route.request(),url=new URL(request.url()),p=url.pathname
 if(!['127.0.0.1','localhost'].includes(url.hostname))return route.abort()
 if(p.endsWith('/api/git/branches')){
  const body=request.postDataJSON();probes.push(body)
  if(slow){slow=false;await new Promise(resolve=>{releaseSlow=resolve})}
  if(fail)return route.fulfill({status:400,json:{error:'Git SSH 主机指纹校验失败'}})
  const names=emptyBranches?[]:body.service==='branch-qa-web'?['main','feature/menu','web-only']:['main','feature/menu','feature/nav','api-only']
  return route.fulfill({json:{branches:names.map(name=>({name,commit:'a'.repeat(40)})),default_branch:'main',synced_at:1}})
 }
 if(p.endsWith('/api/pipeline/runs')&&request.method()==='POST'){buildRequests.push(request.postDataJSON());return route.fulfill({status:409,json:{error:'QA：未实际发起构建'}})}
 if(!['GET','HEAD','OPTIONS'].includes(request.method())){writes.push(p);return route.abort()}
 if(p.endsWith('/api/services'))return route.fulfill({json:apps})
 if(p.endsWith('/api/connections'))return route.fulfill({json:[{name:'qa-credential',kind:'git',endpoint:'https://example.invalid',config:{auth_mode:'https'}}]})
 if(/\/api\/services\/[^/]+\/delivery$/.test(p))return route.fulfill({json:{runner:'local',git_connection:'qa-credential',variable_groups:[]}})
 if(/\/api\/services\/[^/]+\/resources$/.test(p))return route.fulfill({json:{requirements:[],bindings:[]}})
 if(p.endsWith('/delivery-content'))return route.fulfill({json:{changes:[],digest:'qa'}})
 if(p.endsWith('/api/work-items'))return route.fulfill({json:[work]})
 if(p.endsWith('/api/work-items/page'))return route.fulfill({json:{items:[work],total:1,page:1,page_size:20}})
 if(p.endsWith('/api/work-items/people'))return route.fulfill({json:[]})
 if(p.endsWith('/api/work-items/'+work.id))return route.fulfill({json:work})
 if(p.endsWith('/api/me'))return route.fulfill({json:{username:'local',role:'admin'}})
 if(['/api/pipeline/runs','/api/artifacts','/api/deploys','/api/changes','/api/release-versions','/api/delivery/runs','/api/work-item-sync','/api/work-item-sources'].some(end=>p.endsWith(end)))return route.fulfill({json:[]})
 return route.continue()
})
const visibleDialog=name=>ui.getByRole('dialog',{name,exact:true})
const branchControl=dialog=>dialog.locator('.branch-field')
const selected=async dialog=>(await branchControl(dialog).locator('.el-select__selected-item:not(.is-transparent)').allTextContents()).map(s=>s.trim()).find(Boolean)||''
async function chooseBranch(dialog,text){
 const control=branchControl(dialog),input=control.getByRole('combobox')
 await input.fill(text);const list=await input.getAttribute('aria-controls')
 await ui.locator('#'+list).getByRole('option',{name:text,exact:true}).click()
 await dialog.locator('.el-dialog__header').click()
}
async function chooseApp(dialog,name){
 const control=dialog.locator('label').filter({hasText:/^影响应用/}).locator('.el-select')
 // Click the dropdown arrow, not the wrapper center (which can hit a selected tag's ×).
 await control.locator('.el-select__suffix').click();const list=await control.getByRole('combobox').getAttribute('aria-controls')
 await ui.locator('#'+list).getByRole('option',{name,exact:true}).click();await dialog.locator('.el-dialog__header').click()
}
try{
 if(embedded)await page.goto(base)
 else await page.goto(base+(process.env.SHELL_UI_TEST_PATH||'/api/plugins/deploy/ui')+'#/')
 const card=ui.locator('article.app-card').filter({has:ui.getByRole('heading',{name:app.name,exact:true})})
 await card.getByRole('button',{name:'构建',exact:true}).click()
 const build=visibleDialog('构建 '+app.name)
 await build.waitFor()
 assert.equal(await selected(build),'main')
 await build.getByRole('button',{name:'同步分支'}).click()
 await build.getByText(/已同步 4 个分支/).waitFor()
 assert.equal(await selected(build),'main','sync overwrote selected branch')
 await chooseBranch(build,'feature/nav')
 fail=true;await build.getByRole('button',{name:'同步分支'}).click();await build.getByRole('alert').waitFor()
 assert.equal(await selected(build),'feature/nav','failed sync erased branch')
 fail=false;const manual=branchControl(build).getByRole('combobox');await manual.fill('feature/manual');const manualList=await manual.getAttribute('aria-controls');await ui.locator('#'+manualList).getByRole('option',{name:'feature/manual',exact:true}).waitFor();await manual.press('Enter')
 assert.equal(await selected(build),'feature/manual','Enter did not confirm manual branch')
 await build.getByRole('button',{name:'开始构建',exact:true}).click()
 await page.waitForTimeout(150)
 assert.equal(buildRequests[0].branch,'feature/manual')
 assert.equal(buildRequests[0].service,app.name)
 await build.getByRole('button',{name:'取消',exact:true}).click()
 await ui.getByRole('button',{name:'＋ 接入应用',exact:true}).click()
 const wizard=visibleDialog('接入应用')
 await wizard.getByPlaceholder('https://.../repo.git 或 git@...:repo.git').fill('https://example.invalid/draft.git')
 const credentials=wizard.locator('.fld').filter({has:ui.locator('label').filter({hasText:/^Git 代码凭据$/})}).getByRole('combobox')
 await credentials.click();let optionList=await credentials.getAttribute('aria-controls')
 await ui.locator('#'+optionList).getByRole('option',{name:'qa-credential · HTTPS',exact:true}).click()
 await wizard.getByRole('button',{name:'同步分支'}).click();await wizard.getByText(/已同步 4 个分支/).waitFor()
 assert(probes.some(p=>p.git_url==='https://example.invalid/draft.git'&&p.connection==='qa-credential'&&!p.service),'draft did not use selected credential')
 emptyBranches=true;await wizard.getByRole('button',{name:'同步分支'}).click();await wizard.getByText(/已同步 0 个分支/).waitFor()
 const newBranch=branchControl(wizard).getByRole('combobox');await newBranch.fill('feature/empty-list');const newList=await newBranch.getAttribute('aria-controls');await ui.locator('#'+newList).getByRole('option',{name:'feature/empty-list',exact:true}).waitFor();await newBranch.press('Enter');await wizard.locator('.el-dialog__header').click()
 assert.equal(await selected(wizard),'feature/empty-list','empty repository list dropped manual branch');emptyBranches=false
 await wizard.getByRole('button',{name:'取消',exact:true}).click()
 await ui.getByRole('link',{name:'变更',exact:true}).click()
 await ui.getByRole('button',{name:work.title,exact:true}).click()
 await ui.getByRole('button',{name:'编辑交付内容',exact:true}).click()
 const editor=visibleDialog('交付内容 · '+work.title)
 await editor.getByRole('button',{name:'＋ 添加代码与应用',exact:true}).click()
 await chooseApp(editor,'branch-qa-api');await chooseApp(editor,'branch-qa-web')
 await editor.getByRole('button',{name:'同步分支'}).click()
 await editor.getByText(/已同步 2 个共同分支/).waitFor()
 await chooseBranch(editor,'feature/menu')
 assert(probes.some(p=>p.service==='branch-qa-api')&&probes.some(p=>p.service==='branch-qa-web'))
 // Scope changes must invalidate an in-flight result without losing input.
 slow=true;await editor.getByRole('button',{name:'同步分支'}).click();await page.waitForTimeout(80)
 await chooseApp(editor,'branch-qa-web')
 releaseSlow?.();await page.waitForTimeout(150)
 assert.equal(await selected(editor),'feature/menu')
 assert.equal(await editor.getByText(/已同步 2 个共同分支/).count(),0)
 await editor.getByRole('button',{name:'同步分支'}).click();await editor.getByText(/已同步 4 个分支/).waitFor()
 for(const width of [1440,900,700]){await page.setViewportSize({width,height:1000});await page.waitForTimeout(80);assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth+1),false)}
 await editor.getByRole('button',{name:'关闭',exact:true}).click()
 await ui.getByRole('button',{name:'编辑交付内容',exact:true}).click()
 await branchControl(editor).locator('.el-select__selected-item').filter({hasText:'feature/menu'}).waitFor()
 assert.equal(await selected(editor),'feature/menu','close/reopen discarded branch draft')
 assert.deepEqual(writes,[]);assert.deepEqual(errors,[])
 console.log(JSON.stringify({ok:true,embedded,checks:['branch sync','filter','manual branch','selected value preserved on failure','per-run override','new-app draft credential','multi-repository intersection','stale-response cancellation','draft close/reopen','responsive','no real writes or repository requests']}))
}catch(e){await page.screenshot({path:'runtime/git-branches-ui-failure.png'});console.log(await page.evaluate(()=>({width:innerWidth,scroll:document.documentElement.scrollWidth,overflow:[...document.querySelectorAll('body *')].filter(el=>el.getBoundingClientRect().right>innerWidth+1).slice(0,15).map(el=>({tag:el.tagName,cls:el.className,right:el.getBoundingClientRect().right}))})));throw e}finally{releaseSlow?.();await browser.close()}
