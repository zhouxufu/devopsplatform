// All business APIs are fixture-only: no repository, build, deployment or user
// configuration is mutated. Optional dist validates the candidate before 9800.
import assert from 'node:assert/strict'
import {readFile,mkdir} from 'node:fs/promises'
import path from 'node:path'
const base=process.env.CONSOLE_UI_TEST_URL||'http://127.0.0.1:9800'
assert.equal(new URL(base).hostname,'127.0.0.1')
const dist=process.env.CONSOLE_UI_DIST,output='/tmp/devops-basic-build-qa'
await mkdir(output,{recursive:true})
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright')
const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH})
const page=await browser.newPage({viewport:{width:1440,height:960}})
page.setDefaultTimeout(12000)
const errors=[],unexpected=[],writes=[],queries=[]
page.on('pageerror',e=>errors.push(e.message));page.on('dialog',d=>d.accept())
const app={name:'qa-orders',application_id:'qa-1',artifact_type:'jar',git_url:'https://example.invalid/a.git',branch:'main',build_tool:'gradle',gradle_executable:'gradle',gradle_tasks:'clean build',artifact_glob:'build/libs/*.jar',steps:[]}
const delivery={runner:'local',variable_groups:[],build_environment:{mode:'local'}}
const targets=[{environment:'test',display_name:'测试',target_type:'ssh-jar',host_group:'test-group',hosts:['qa-host'],remote_dir:'/opt/qa',deploy_config:{java_mode:'managed',java_version:'21'}}]
const run=n=>({id:n,service:app.name,version:'a00000'+n,branch:'feature/menu',actor:'alice',status:'failed',started_at:1791780000,ended_at:1791780012,stages:[{name:'拉取源码',status:'success'},{name:'编译',status:'failed'}]})
const artifact={id:17,service:app.name,version:'a000017',kind:'jar',source:'pipeline-run',source_id:17,metadata:{branch:'feature/menu'},created_at:1791780000,digest:'sha256:fixture',uri:'/fixture/artifact.jar',published_environments:['test']}
let rejectBuild=true,createdApp,noTargets=false
await page.route('**/*',async route=>{
 const request=route.request(),u=new URL(request.url()),p=u.pathname,method=request.method()
 if(u.origin!==new URL(base).origin)return route.abort()
 if(p.includes('/api/plugins/deploy/api/')||p==='/api/registry/applications'){
  const api=p.replace('/api/plugins/deploy','')
  if(!['GET','HEAD','OPTIONS'].includes(method)){
   if(api==='/api/git/branches')return route.fulfill({json:{branches:[{name:'main',commit:'a'.repeat(40)},{name:'feature/menu',commit:'b'.repeat(40)}],default_branch:'main'}})
   if(api==='/api/pipeline/runs'){writes.push({api,body:request.postDataJSON()});await new Promise(r=>setTimeout(r,150));return route.fulfill({status:rejectBuild?409:200,json:rejectBuild?{error:'Fixture: Runner 忙，请稍后再试'}:{id:17}})}
   if(api==='/api/services'&&method==='POST'){const body=request.postDataJSON();writes.push({api,body});if(body.name!==app.name)createdApp=body;await new Promise(r=>setTimeout(r,150));return route.fulfill({json:{ok:true}})}
   if(api.endsWith('/delivery')&&method==='PUT'){writes.push({api,body:request.postDataJSON()});return route.fulfill({json:request.postDataJSON()})}
   unexpected.push(method+' '+api);return route.abort()
  }
  let body=[]
  if(api==='/api/services')body=[app,...Array.from({length:25},(_,i)=>({...app,name:'qa-app-'+String(i).padStart(2,'0')})),...(createdApp?[createdApp]:[])]
  else if(api==='/api/services/summary')body=[{name:app.name,latest_run:run(1),latest_artifact:artifact,environments:targets,delivery}]
  else if(api==='/api/me')body={username:'alice',role:'admin'}
  else if(api==='/api/registry/applications')body=[]
  else if(api.endsWith('/delivery'))body=delivery
  else if(api==='/api/environments')body=[{name:'test',display_name:'测试'}]
  else if(api.endsWith('/environments'))body=noTargets?[]:targets
  else if(api==='/api/runners')body=[{name:'local',kind:'local',enabled:true,concurrency:1}]
  else if(api==='/api/steps')body=[{id:'shell-script',name:'自定义 Shell 脚本',category:'构建',kind:'build',params:[{name:'SCRIPT',type:'shell',label:'脚本（Bash）',default:'pwd'}]}]
  else if(api==='/api/pipeline/runs/page'){queries.push(Object.fromEntries(u.searchParams));const n=Number(u.searchParams.get('page')||1);body={items:Array.from({length:20},(_,i)=>run(87-(n-1)*20-i)),total:87,page:n,page_size:20}}
  else if(api==='/api/artifacts/page')body={items:[artifact],total:1,page:1,page_size:20}
  else if(api==='/api/release-orders/page')body={items:[],total:0,page:1,page_size:20}
  else if(api==='/api/artifacts')body=[artifact]
  else if(api==='/api/artifacts/17')body=artifact
  else if(/\/api\/pipeline\/runs\/\d+\/capabilities$/.test(api))body={can_retry:true,can_stop:false,commit:'a'.repeat(40),runner:'local'}
  else if(/\/api\/pipeline\/runs\/\d+\/log$/.test(api))body={log:'━━ 拉取源码（git-checkout）━━\nclone fixture\n━━ 编译（gradle）━━\nfixture failure: timeout\n'}
  else if(/\/api\/pipeline\/runs\/\d+$/.test(api))body=run(Number(api.split('/').pop()))
  return route.fulfill({json:body})
 }
 if(!['GET','HEAD','OPTIONS'].includes(method)){unexpected.push(method+' '+p);return route.abort()}
 if(dist){let f;if(p==='/api/plugins/deploy/ui'||p==='/api/plugins/deploy/ui/')f='deploy.html';else if(/\/assets\/[^/]+\.(js|css)$/.test(p))f='assets/'+path.basename(p);if(f)return route.fulfill({body:await readFile(path.join(dist,f)),contentType:f.endsWith('.js')?'text/javascript':f.endsWith('.css')?'text/css':'text/html'})}
 return route.continue()
})
async function goto(hash){await page.goto(base+'/api/plugins/deploy/ui#'+hash)}
try{
 await goto('/');await page.getByRole('heading',{name:'基础构建',exact:true}).waitFor();await page.getByText('qa-orders',{exact:true}).first().waitFor()
 assert.equal(await page.locator('article.app-card').count(),0,'table must be default')
 assert((await page.locator('table').innerText()).includes('#1'),'latest status uses per-app summary')
 await goto('/svc/qa-orders/build');await page.getByRole('button',{name:'开始构建',exact:true}).waitFor()
 assert.equal(await page.getByRole('button',{name:'开始构建',exact:true}).count(),1)
 await page.getByRole('button',{name:'下一页',exact:true}).click();await page.getByRole('button',{name:'#67 · a0000067',exact:true}).waitFor()
 await page.getByRole('button',{name:'#67 · a0000067',exact:true}).click();await page.getByRole('heading',{name:'构建 #67',exact:true}).waitFor()
 await page.getByRole('button',{name:'编译 · 失败',exact:true}).click();assert.equal((await page.locator('.run-log').innerText()).includes('clone fixture'),false)
 await page.getByPlaceholder('搜索当前阶段日志').fill('timeout');await page.getByText('1 行命中',{exact:true}).waitFor()
 await page.getByRole('button',{name:'暂停跟随',exact:true}).click();await page.getByRole('button',{name:'继续跟随',exact:true}).waitFor()
 await page.getByRole('button',{name:'按原源码与配置重试',exact:true}).click();const retry=page.getByRole('dialog',{name:'确认重试构建',exact:true});await retry.waitFor();assert((await retry.innerText()).includes('不自动发布'));await retry.locator('.el-dialog__headerbtn').click()
 await page.getByRole('button',{name:'返回构建记录',exact:true}).click();await page.getByRole('button',{name:'#67 · a0000067',exact:true}).waitFor();assert.equal(queries.at(-1).page,'2','back lost paging')
 await page.getByRole('button',{name:'开始构建',exact:true}).click();const launch=page.getByRole('dialog',{name:'构建 qa-orders',exact:true});await launch.waitFor();await launch.getByRole('button',{name:'覆盖本次构建参数',exact:true}).click();await launch.locator('label').filter({hasText:/^Tasks/}).locator('input').fill('assemble')
 await launch.getByRole('button',{name:'开始构建',exact:true}).click();await launch.getByRole('alert').waitFor();assert.equal(writes.at(-1).body.gradle_tasks,'assemble');assert.equal(writes.at(-1).body.deploy_environment,'');assert.equal(app.gradle_tasks,'clean build');await launch.getByRole('button',{name:'取消',exact:true}).click()
 await page.getByRole('button',{name:'编辑配置',exact:true}).click();await page.locator('.settings-workspace').waitFor();assert.equal(await page.getByRole('dialog').count(),0,'settings must be workspace not modal');await page.getByRole('button',{name:'构建与制品',exact:true}).click();const task=page.locator('.build-command-card .fld').filter({has:page.locator('label').filter({hasText:/^Tasks$/})}).locator('input');await task.fill('test');await page.getByText('● 有未保存修改',{exact:true}).waitFor();await page.getByRole('button',{name:'代码来源',exact:true}).click();await page.getByRole('button',{name:'构建与制品',exact:true}).click();assert.equal(await task.inputValue(),'test');await page.getByRole('button',{name:'取消',exact:true}).click();await page.getByRole('button',{name:'编辑配置',exact:true}).click();await page.getByRole('button',{name:'构建与制品',exact:true}).click();assert.equal(await task.inputValue(),'clean build');await page.getByRole('button',{name:'取消',exact:true}).click()
 await page.getByRole('button',{name:'制品',exact:true}).click();await page.getByRole('columnheader',{name:'来源构建',exact:true}).waitFor();await page.getByRole('columnheader',{name:'分支',exact:true}).waitFor();await page.getByRole('columnheader',{name:'曾发布环境',exact:true}).waitFor();await page.getByRole('button',{name:'发布此制品',exact:true}).click();await page.locator('.release-bar').waitFor();assert((await page.locator('.release-bar').innerText()).includes('a000017'));assert.equal(unexpected.length,0)
 for(const theme of ['light','dark']){await page.evaluate(t=>{localStorage.setItem('lp-theme',t);window.dispatchEvent(new StorageEvent('storage',{key:'lp-theme',newValue:t,storageArea:localStorage}))},theme);await goto('/svc/qa-orders/build');await page.getByRole('button',{name:'编辑配置',exact:true}).waitFor();assert.equal(await page.evaluate(()=>document.documentElement.classList.contains('dark')),theme==='dark');for(const width of [1440,900,700,390]){await page.setViewportSize({width,height:960});await page.waitForTimeout(250);assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth+1),false,'overflow '+width);await page.screenshot({path:output+'/build-'+theme+'-'+width+'.png'})}}
 await page.setViewportSize({width:1440,height:960});await goto('/svc/qa-orders/environment');await page.getByRole('button',{name:'编辑配置',exact:true}).click();await page.locator('.target-workspace').waitFor();assert.equal(await page.getByRole('dialog').count(),0);await page.getByRole('button',{name:'启动与健康检查',exact:true}).click();await page.locator('input[placeholder="8889"]').fill('8080');await page.getByRole('button',{name:'应用变量',exact:true}).click();await page.getByRole('button',{name:'启动与健康检查',exact:true}).click();assert.equal(await page.locator('input[placeholder="8889"]').inputValue(),'8080');await page.getByRole('button',{name:'取消',exact:true}).click();await page.getByRole('button',{name:'编辑配置',exact:true}).waitFor()
 await goto('/');await page.getByRole('button',{name:'＋ 接入应用',exact:true}).click();const wizard=page.getByRole('dialog',{name:'接入应用',exact:true});await wizard.getByPlaceholder('例如 order-api').fill('qa-new-app');await wizard.getByPlaceholder('https://.../repo.git 或 git@...:repo.git').fill('https://example.invalid/new.git');await wizard.getByRole('button',{name:'下一步',exact:true}).click();await wizard.getByRole('button',{name:'下一步',exact:true}).click();await wizard.getByRole('button',{name:'完成接入',exact:true}).click();await page.waitForURL('**#/svc/qa-new-app/build');await wizard.waitFor({state:'hidden'});await page.getByRole('heading',{name:'qa-new-app',exact:true}).waitFor();assert(page.url().endsWith('/svc/qa-new-app/build'));assert.equal(createdApp.build_tool,'maven');assert.equal(writes.filter(w=>w.api==='/api/services'&&w.body.name==='qa-new-app').length,1)
 noTargets=true;await page.evaluate(()=>sessionStorage.setItem('lp-deploy-environment','test'));await goto('/svc/qa-orders/overview');await page.reload();await page.getByRole('heading',{name:'qa-orders',exact:true}).waitFor();const notice=page.locator('.el-message').filter({hasText:'当前环境 test 尚未配置该应用的发布目标'});await notice.waitFor();assert.equal(await notice.count(),1);assert.equal(await page.locator('.context-warning').count(),0,'persistent banner returned');assert.equal(await page.getByRole('button',{name:'＋ 配置环境',exact:true}).count(),1);assert.equal(await page.getByRole('button',{name:'配置部署环境',exact:true}).count(),0);assert.equal(await page.locator('.overview-next').count(),0,'redundant configuration strip returned');await notice.locator('.el-message__closeBtn').click();await notice.waitFor({state:'hidden'});await page.getByRole('button',{name:'构建',exact:true}).click();await page.getByRole('button',{name:'概览',exact:true}).click();await page.waitForTimeout(200);assert.equal(await notice.count(),0,'tab switch repeats dismissed notice')
 await goto('/svc/qa-orders/advanced');await page.getByRole('heading',{name:'高级构建流水线',exact:true}).waitFor();assert.equal(await page.locator('.advanced-notice').count(),0);await page.getByRole('button',{name:'＋ 添加构建步骤',exact:true}).click();const script=page.locator('textarea').first();await script.fill('printf fixture-script');await page.getByRole('button',{name:'保存流水线',exact:true}).click();await page.locator('.el-message').filter({hasText:'流水线已保存；未执行任何步骤'}).waitFor();assert.equal(writes.at(-1).body.steps[0].step,'shell-script');assert.equal(writes.at(-1).body.steps[0].params.SCRIPT,'printf fixture-script');assert.equal(writes.filter(w=>w.api==='/api/pipeline/runs').length,1,'saving a script executed it')
 await page.screenshot({path:output+'/advanced-shell.png'});assert.deepEqual(errors,[]);assert.deepEqual(unexpected,[])
 console.log(JSON.stringify({ok:true,checks:['default app table','per-app latest summary','one build entry','server paging and back','stage/search/follow logs','retry explanation','one-off parameters preserve defaults','full-page grouped settings','draft continuity and cancel','exact artifact publishing','light/dark 1440/900/700/390','no real writes']}))
}catch(e){await page.screenshot({path:output+'/failure.png'});console.log(await page.getByRole('dialog').allTextContents());throw e}finally{await browser.close()}
