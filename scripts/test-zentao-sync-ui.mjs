// Read-only on the real shell: all sync POSTs are mocked; other writes abort.
import assert from 'node:assert/strict'
const base=process.env.SHELL_UI_TEST_URL||'http://127.0.0.1:9800'
assert.equal(new URL(base).hostname,'127.0.0.1')
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright')
const browser=await chromium.launch({headless:true,...(process.env.CHROME_PATH?{executablePath:process.env.CHROME_PATH}:{})})
const page=await browser.newPage({viewport:{width:1440,height:1000}})
const errors=[],unexpectedWrites=[],bodies=[]
page.on('pageerror',e=>errors.push(e.message))
await page.route('**/api/**',async route=>{
 const request=route.request(),path=new URL(request.url()).pathname
 if(request.method()==='GET'&&path.endsWith('/api/connections'))return route.fulfill({json:[
  {name:'UI只读默认连接',kind:'zentao',endpoint:'http://example.test/zentao',config:{product_ids:'14'}},
  {name:'UI只读激活连接',kind:'zentao',endpoint:'http://example.test/zentao',config:{product_ids:'14',bug_status:'active'}}
 ]})
 if(request.method()==='POST'&&path.includes('/api/issue-sync/')){bodies.push(request.postDataJSON());return route.fulfill({json:{status:'success',detail:'UI mocked synchronization only'}})}
 if(!['GET','HEAD','OPTIONS'].includes(request.method())){unexpectedWrites.push(path);return route.abort()}
 return route.continue()
})
const choose=async(field,option)=>{
 await field.locator('.el-select').click()
 const popup=await field.getByRole('combobox').getAttribute('aria-controls')
 await page.locator(`[id="${popup}"]`).getByRole('option',{name:option,exact:true}).click()
}
try{
 await page.goto(base+'/api/plugins/deploy/ui#/changes?tab=work')
 await page.getByRole('button',{name:'同步禅道 BUG',exact:true}).click()
 const dialog=page.getByRole('dialog',{name:'同步禅道 BUG（开源 16.0）',exact:true})
 const connection=dialog.locator('label').filter({hasText:/^禅道连接/})
 const status=dialog.locator('label').filter({hasText:/^本次 Bug 状态/})
 await status.getByText('沿用连接默认：已关闭',{exact:true}).waitFor()
 await choose(connection,'UI只读激活连接')
 await status.getByText('沿用连接默认：激活（未解决）',{exact:true}).waitFor()
 await choose(status,'已关闭')
 await dialog.locator('label').filter({hasText:/^更新或关闭日期基线/}).locator('input').fill('2026-10-06')
 await dialog.getByRole('button',{name:'立即同步',exact:true}).click()
 await page.getByText('禅道同步完成',{exact:true}).waitFor()
 assert.deepEqual(bodies,[{baseline:'2026-10-06',status:'closed'}])
 await choose(status,'沿用连接默认：激活（未解决）')
 await choose(connection,'UI只读默认连接')
 await status.getByText('沿用连接默认：已关闭',{exact:true}).waitFor()
 for(const width of [1440,900,700]){
  await page.setViewportSize({width,height:900})
  const box=await dialog.boundingBox();assert(box.x>=0&&box.x+box.width<=width+1,'Sync modal overflow')
 }
 await dialog.locator('.el-dialog__headerbtn').click()
 await dialog.waitFor({state:'hidden'})
 assert.deepEqual(errors,[]);assert.deepEqual(unexpectedWrites,[])
 console.log(JSON.stringify({ok:true,checks:['closed default','connection-configured default','one-off status override','request payload','cancel','three viewport widths','all writes mocked']}))
}finally{await browser.close()}
