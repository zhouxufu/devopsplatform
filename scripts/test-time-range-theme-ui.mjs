// Actual shell and date picker. Business writes and external requests are blocked.
import assert from 'node:assert/strict'
import { readFile, mkdir } from 'node:fs/promises'
import path from 'node:path'
const base=process.env.CONSOLE_UI_TEST_URL||'http://127.0.0.1:9800',dist=process.env.CONSOLE_UI_DIST
assert.equal(new URL(base).hostname,'127.0.0.1')
const { chromium }=await import(process.env.PLAYWRIGHT_MODULE||'playwright')
const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH})
const context=await browser.newContext({viewport:{width:1440,height:960}}),page=await context.newPage(),errors=[],writes=[],checked=[]
page.on('pageerror',e=>errors.push(e.message))
await context.route('**/*',async route=>{
 const request=route.request(),url=new URL(request.url()),p=url.pathname
 if(url.origin!==new URL(base).origin)return route.abort()
 if(!['GET','HEAD','OPTIONS'].includes(request.method())){writes.push(request.method()+' '+p);return route.abort()}
 if(dist){let file
  if(p==='/')file='shell.html'
  else if(/\/assets\/[^/]+\.(js|css)$/.test(p))file='assets/'+path.basename(p)
  else if(/^\/api\/plugins\/(deploy|logs|monitor)\/ui\/?$/.test(p))file=p.split('/')[3]+'.html'
  if(file)return route.fulfill({body:await readFile(path.join(dist,file)),contentType:file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html'})
 }
 return route.continue()
})
const recent=page.locator('.seg>button'),picker=page.locator('.seg .el-date-editor')
async function selectPlugin(id){
 const plugin=await page.request.get(base+'/api/plugins').then(r=>r.json()).then(v=>v.find(p=>p.name===id))
 assert(plugin,'test requires activated '+id)
 await page.getByRole('button',{name:'打开插件工作区',exact:true}).click()
 await page.locator('.pm').getByRole('button',{name:plugin.display,exact:true}).click()
 await recent.waitFor()
}
async function theme(dark){
 if(await page.locator('html').evaluate(e=>e.classList.contains('dark'))!==dark)await page.getByRole('button',{name:dark?'切换深色主题':'切换浅色主题',exact:true}).click()
 await page.waitForTimeout(200)
}
async function contrast(dark,state){
 const values=await recent.evaluate(button=>{
  const style=getComputedStyle(button),canvas=document.createElement('canvas');canvas.width=canvas.height=1
  const c=canvas.getContext('2d')
  const rgb=color=>{c.clearRect(0,0,1,1);c.fillStyle=color;c.fillRect(0,0,1,1);return [...c.getImageData(0,0,1,1).data]}
  const luminance=color=>rgb(color).slice(0,3).map(v=>{v/=255;return v<=.04045?v/12.92:((v+.055)/1.055)**2.4}).reduce((sum,v,i)=>sum+v*[.2126,.7152,.0722][i],0)
  const background=rgb(style.backgroundColor)[3]?style.backgroundColor:getComputedStyle(button.closest('.contextbar')).backgroundColor
  const a=luminance(style.color),b=luminance(background)
  return {ratio:(Math.max(a,b)+.05)/(Math.min(a,b)+.05),background,color:style.color,text:button.innerText,opacity:style.opacity}
 })
 assert(values.ratio>=4.5,`${dark?'dark':'light'} ${state}: unreadable time shortcut ${JSON.stringify(values)}`)
 assert.match(values.text,/最近15分钟/);assert.equal(values.opacity,'1')
 checked.push(`${dark?'dark':'light'}-${state}`)
}
try{
 await page.goto(base);await page.frameLocator('iframe[src*="/api/plugins/deploy/ui"]').getByRole('heading',{name:'基础构建',exact:true}).waitFor();await selectPlugin('monitor')
 for(const dark of [false,true,false]){
  await theme(dark);await recent.click();assert.equal(await recent.getAttribute('aria-pressed'),'true')
  await contrast(dark,'selected');await recent.hover();await contrast(dark,'hover')
  await recent.focus();await page.keyboard.press('Tab');await page.keyboard.press('Shift+Tab');assert.equal(await recent.evaluate(e=>document.activeElement===e),true);assert.equal(await recent.evaluate(e=>getComputedStyle(e).outlineStyle),'solid');await contrast(dark,'focus')
  await picker.click()
  const popup=page.locator('.el-picker-panel:visible');await popup.waitFor()
  await popup.getByText('确定',{exact:true}).waitFor();assert.equal(await popup.getByText('September',{exact:true}).count(),0)
  await popup.getByText('今天',{exact:true}).click()
  await popup.waitFor({state:'hidden'})
  assert.equal(await recent.getAttribute('aria-pressed'),'false');await contrast(dark,'unselected')
  await recent.click();assert.equal(await recent.getAttribute('aria-pressed'),'true')
  await mkdir('/tmp/devops-time-range-ui',{recursive:true});await page.mouse.move(1300,500)
  await page.screenshot({path:`/tmp/devops-time-range-ui/monitor-${dark?'dark':'light'}.png`})
 }
 await selectPlugin('logs');await contrast(false,'logs-selected')
 await page.getByRole('button',{name:'打开插件工作区',exact:true}).click();await contrast(false,'launcher-open')
 assert.equal(await recent.isVisible(),true)
 await page.getByRole('button',{name:'打开插件工作区',exact:true}).click()
 await theme(true);await contrast(true,'logs-selected');await theme(false);await contrast(false,'logs-return-light')
 for(const width of [1440,900,700,390]){await page.setViewportSize({width,height:960});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false)}
 assert.deepEqual(errors,[]);assert.deepEqual(writes,[])
 console.log(JSON.stringify({ok:true,checked,noBusinessWrites:true,checks:['dynamic and fixed range','contrast >= 4.5','hover and keyboard focus','Chinese calendar','launcher overlay','logs and monitor','theme switching','four widths']}))
}finally{await browser.close()}
