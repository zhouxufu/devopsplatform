// Fixture-only API calls: no actual connections, bindings, builds or deployments.
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import path from 'node:path'
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright')
const base='http://127.0.0.1:9800',dist=process.env.CONSOLE_UI_DIST
assert(dist,'set CONSOLE_UI_DIST')
const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH})
const page=await browser.newPage({viewport:{width:1440,height:960}})
const errors=[],unexpected=[];let saved
page.on('pageerror',e=>errors.push(e.message))
const resources={requirements:[{alias:'config',kind:'nacos'},{alias:'database',kind:'mysql'},{alias:'cache',kind:'redis'}],bindings:[{alias:'config',environment:'dev',connection:'nacos-dev',config:{data_id:'app-dev.yml',group:'DEFAULT_GROUP'}},{alias:'database',environment:'dev',connection:'mysql-dev',config:{}},{alias:'cache',environment:'dev',connection:'redis-dev',config:{}}]}
await page.route('**/*',async route=>{
 const r=route.request(),u=new URL(r.url()),api=u.pathname.replace('/api/plugins/deploy','')
 if(u.origin!==base)return route.abort()
 if(api.startsWith('/api/')){
  if(r.method()!=='GET'){
   if(api.endsWith('/resources')&&r.method()==='PUT'){saved=r.postDataJSON();return route.fulfill({json:saved})}
   unexpected.push(r.method()+' '+api);return route.abort()
  }
  let body=[]
  if(api==='/api/services')body=[{name:'layout-fixture',artifact_type:'jar',build_tool:'gradle'}]
  else if(api.endsWith('/resources'))body=resources
  else if(api==='/api/me')body={username:'fixture',role:'admin'}
  else if(api==='/api/environments')body=[{name:'dev',display_name:'开发'},{name:'test',display_name:'测试'}]
  else if(api.endsWith('/connections'))body=[{name:'nacos-dev',kind:'nacos',endpoint:'http://example.invalid:8848/nacos'},{name:'mysql-dev',kind:'mysql',config:{database:'fixture'}},{name:'redis-dev',kind:'redis',endpoint:'redis://example.invalid:6379'}]
  else if(api.endsWith('/delivery'))body={runner:'local',variable_groups:[]}
  return route.fulfill({json:body})
 }
 let f=api==='/ui'||api==='/ui/'?'deploy.html':/\/assets\/[^/]+\.(js|css)$/.test(api)?'assets/'+path.basename(api):null
 if(f)return route.fulfill({body:await readFile(path.join(dist,f)),contentType:f.endsWith('.js')?'text/javascript':f.endsWith('.css')?'text/css':'text/html'})
 return route.abort()
})
try{
 await page.goto(base+'/api/plugins/deploy/ui#/svc/layout-fixture/settings')
 await page.getByRole('button',{name:'应用资源绑定',exact:true}).click()
 await page.getByRole('button',{name:'配置应用资源',exact:true}).click()
 const dialog=page.getByRole('dialog',{name:'应用环境资源绑定',exact:true})
 const row=dialog.locator('.nacos-binding').first();await row.waitFor()
 await page.waitForTimeout(400)
 const footer=dialog.locator('.el-dialog__footer'),body=dialog.locator('.el-dialog__body')
 for(const theme of ['light','dark']){
  await page.evaluate(t=>{localStorage.setItem('lp-theme',t);window.dispatchEvent(new StorageEvent('storage',{key:'lp-theme',newValue:t,storageArea:localStorage}))},theme)
  for(const width of [1440,1100,900,700,390]){
   await page.setViewportSize({width,height:760});await page.waitForTimeout(200)
   await body.evaluate(e=>e.scrollTop=0)
   const d=await dialog.boundingBox(),f=await footer.boundingBox(),b=await body.boundingBox()
   assert(d.y>=0&&d.y+d.height<=761,'dialog clipped '+width+' '+JSON.stringify(d))
   assert(b.y+b.height<=f.y+1,'body overlaps footer '+width)
   assert(await footer.getByRole('button',{name:'保存绑定',exact:true}).isVisible())
   assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth+1),false,'page overflow '+width)
   assert.equal(await dialog.evaluate(e=>e.scrollWidth>e.clientWidth+1),false,'dialog overflow '+width)
   if(width===1440){
    const controls=await row.locator('.el-select__wrapper,.el-input__wrapper').all()
    const boxes=await Promise.all(controls.map(x=>x.boundingBox()))
    assert.equal(boxes.length,4)
    assert(Math.max(...boxes.map(x=>x.y+x.height))-Math.min(...boxes.map(x=>x.y+x.height))<=1,'Nacos controls misaligned')
   }
   await page.screenshot({path:'/tmp/devops-binding-layout-'+theme+'-'+width+'.png'})
  }
 }
 await page.setViewportSize({width:1440,height:960})
 await dialog.getByRole('button',{name:'资源管理',exact:true}).click()
 await page.getByRole('button',{name:'管理资源组',exact:true}).waitFor()
 await page.getByRole('button',{name:'刷新连接',exact:true}).click()
 await dialog.getByRole('button',{name:'资源管理',exact:true}).click()
 await row.getByRole('textbox',{name:'config绑定1 Data ID',exact:true}).fill('updated.yml')
 await row.getByRole('textbox',{name:'config绑定1 Group',exact:true}).fill('QA_GROUP')
 await footer.getByRole('button',{name:'保存绑定',exact:true}).click()
 await dialog.waitFor({state:'hidden'})
 assert.equal(saved.bindings[0].config.data_id,'updated.yml');assert.equal(saved.bindings[0].config.group,'QA_GROUP')
 assert.deepEqual(saved.bindings.slice(1),resources.bindings.slice(1),'unrelated bindings changed')
 assert.deepEqual(errors,[]);assert.deepEqual(unexpected,[])
 console.log(JSON.stringify({ok:true,checks:['compact toolbar','aligned Nacos row','fixed footer','light/dark 1440/1100/900/700/390','no overflow','binding payload preserved','no actual resource writes']}))
}catch(e){console.log((await page.locator('body').innerText()).slice(0,2500));await page.screenshot({path:'/tmp/devops-binding-layout-failure.png'});throw e}finally{await browser.close()}
