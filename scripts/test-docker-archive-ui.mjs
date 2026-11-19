// Every API is mocked; configuration writes are captured, never sent to 9800.
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import path from 'node:path'
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright')
const base='http://127.0.0.1:9800',dist=process.env.CONSOLE_UI_DIST
assert(dist,'set CONSOLE_UI_DIST to the candidate build')
const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH})
const page=await browser.newPage({viewport:{width:1440,height:960}})
const errors=[],writes=[]
page.on('pageerror',e=>errors.push(e.message))
let app={name:'archive-fixture',artifact_type:'image',git_url:'https://example.invalid/app.git',branch:'main',image_name:'app',build_tool:'command',build_environment:{mode:'local'},steps:[{step:'git-checkout',params:{}},{step:'shell-script',params:{SCRIPT:'echo fixture-build'}},{step:'docker-build',params:{PUSH:'true'}}]}
await page.route('**/*',async route=>{
 const r=route.request(),u=new URL(r.url()),api=u.pathname.replace('/api/plugins/deploy','')
 if(u.origin!==base)return route.abort()
 if(api.startsWith('/api/')){
  if(r.method()!=='GET'){
   if(api==='/api/services'){app=r.postDataJSON();writes.push(app);return route.fulfill({json:app})}
   if(api.endsWith('/delivery')&&r.method()==='PUT')return route.fulfill({json:r.postDataJSON()})
   throw Error('unexpected write '+api)
  }
  let body=[]
  if(api==='/api/services')body=[app]
  else if(api.endsWith('/delivery'))body={runner:'local',variable_groups:[],build_environment:{mode:'local'}}
  else if(api==='/api/me')body={username:'fixture',role:'admin'}
  else if(api==='/api/runners')body=[{name:'local',enabled:true}]
  return route.fulfill({json:body})
 }
 let f=api==='/ui'||api==='/ui/'?'deploy.html':/\/assets\/[^/]+\.(js|css)$/.test(api)?'assets/'+path.basename(api):null
 if(f)return route.fulfill({body:await readFile(path.join(dist,f)),contentType:f.endsWith('.js')?'text/javascript':f.endsWith('.css')?'text/css':'text/html'})
 return route.abort()
})
try{
 await page.goto(base+'/api/plugins/deploy/ui#/svc/archive-fixture/settings')
 await page.getByRole('button',{name:'构建与制品',exact:true}).click()
 const card=page.locator('.build-command-card')
 await card.locator('.el-select').first().click()
 await page.getByRole('option',{name:'镜像文件（上传 / 导入，无需仓库）',exact:true}).click()
 assert((await card.innerText()).includes('仅支持 Docker 主机，不支持 SAE'))
 await card.locator('.el-select').nth(1).click()
 await page.getByRole('option',{name:'自定义 Dockerfile',exact:true}).click()
 await page.getByRole('button',{name:'保存配置',exact:true}).click()
 await page.getByText('平台托管 Dockerfile 必须非空，且不超过 32 KiB',{exact:true}).waitFor()
 assert.equal(writes.length,0,'empty Dockerfile must not be saved')
 await page.getByRole('button',{name:'填入 Java 21 示例',exact:true}).click()
 const content=page.getByRole('textbox',{name:'平台托管 Dockerfile',exact:true})
 assert((await content.inputValue()).includes('COPY path/to/app.jar'))
 const managed='FROM example\nCOPY generated/app.jar /app/app.jar\nENTRYPOINT ["java","-jar","/app/app.jar"]'
 await content.fill(managed)
 await card.locator('.el-select').nth(1).click()
 await page.getByRole('option',{name:'仓库文件',exact:true}).click()
 await page.getByRole('textbox',{name:'仓库 Dockerfile 路径',exact:true}).waitFor()
 assert.equal(await content.count(),0)
 await card.locator('.el-select').nth(1).click()
 await page.getByRole('option',{name:'自定义 Dockerfile',exact:true}).click()
 assert.equal(await content.inputValue(),managed,'source switching must preserve draft')
 await page.getByRole('button',{name:'凭据与变量',exact:true}).click()
 assert.equal(await page.getByText('镜像仓库',{exact:true}).count(),0)
 await page.getByRole('button',{name:'保存配置',exact:true}).click()
 await page.waitForTimeout(300)
 assert.equal(writes.length,1)
 assert.equal(writes[0].steps[2].params.PUSH,'false')
 assert.equal(writes[0].steps[2].params.DOCKERFILE_SOURCE,'platform')
 assert.equal(writes[0].steps[2].params.DOCKERFILE_CONTENT,managed)
 assert.equal(writes[0].steps[1].params.SCRIPT,'echo fixture-build','switching mode must preserve build steps')
 await page.goto(base+'/api/plugins/deploy/ui#/svc/archive-fixture/settings')
 await page.getByRole('button',{name:'构建与制品',exact:true}).click()
 await card.locator('.el-select').nth(1).click()
 await page.getByRole('option',{name:'基础模板（无需编写 Dockerfile）',exact:true}).click()
 assert.equal(await page.getByRole('textbox',{name:'平台托管 Dockerfile',exact:true}).count(),0)
 await page.getByRole('textbox',{name:'模板产物路径',exact:true}).fill('build/libs/app.jar')
 await page.getByRole('button',{name:'保存配置',exact:true}).click()
 await page.waitForTimeout(300)
 assert.equal(writes.length,2)
 assert.equal(writes[1].steps[2].params.DOCKERFILE_SOURCE,'template')
 assert.equal(writes[1].steps[2].params.DOCKERFILE_ARTIFACT_PATH,'build/libs/app.jar')
 await page.goto(base+'/api/plugins/deploy/ui#/svc/archive-fixture/settings')
 for(const theme of ['light','dark']){
  await page.getByRole('button',{name:'构建与制品',exact:true}).click()
  await page.evaluate(t=>{localStorage.setItem('lp-theme',t);window.dispatchEvent(new StorageEvent('storage',{key:'lp-theme',newValue:t,storageArea:localStorage}))},theme)
  for(const width of [1440,900,390]){
   await page.setViewportSize({width,height:960})
   await page.waitForTimeout(200)
   await page.screenshot({path:'/tmp/devops-docker-archive-'+theme+'-'+width+'.png'})
  }
 }
 assert.deepEqual(errors,[])
 console.log(JSON.stringify({ok:true,checks:['archive mode visible','registry optional','managed Dockerfile editor','empty content blocked','source switching preserves draft','Java template explicit','build steps preserved','mock-only save','light/dark responsive screenshots']}))
}finally{await browser.close()}
