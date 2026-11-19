// Fully intercepted source fixtures: no real Bug, SQL, deployment or account writes.
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import path from 'node:path'
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright')
const dist=process.env.CONSOLE_UI_DIST||path.resolve('web/dist'),base='http://127.0.0.1:9800'
const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH})
const errors=[],unexpected=[],queries=[]
const labels={fixed2:'服务端代码修复',fixed3:'客户端代码修复'}
const rows=Object.keys(labels).map((resolution,i)=>({id:i+7,source:'zentao:issues',external_id:String(i+100),kind:'bug',title:'方案显示测试 '+i,external_status:'resolved',resolution,source_fields_read_at:1}))
try{
 const page=await browser.newPage({viewport:{width:1440,height:960}})
 page.on('pageerror',e=>errors.push(e.message))
 await page.route('**/*',async route=>{
  const req=route.request(),u=new URL(req.url()),p=u.pathname.replace('/api/plugins/research',''),reply=json=>route.fulfill({json})
  if(u.origin!==base){unexpected.push(u.href);return route.abort()}
  let file
  if(u.pathname==='/api/plugins/research/ui')file='research.html'
  else if(/\/assets\/[^/]+\.(js|css)$/.test(u.pathname))file='assets/'+path.basename(u.pathname)
  if(file)return route.fulfill({body:await readFile(path.join(dist,file)),contentType:file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html'})
  if(p==='/api/research/work-items/attach'&&req.method()==='POST'){
   assert.deepEqual(req.postDataJSON(),{connection:'issues',external_id:'100'});return reply(rows[0])
  }
  if(req.method()==='GET'){
   if(p==='/api/plugins')return reply([{name:'research'}])
   if(p==='/api/me')return reply({username:'alice',role:'member',auth_enabled:true})
   if(p==='/api/research/overview')return reply({actor:'alice',aliases:['alice'],connections:[{name:'issues',authorized:true}]})
   if(p==='/api/research/work-items/sources')return reply([{name:'issues',products:[1]}])
   if(p==='/api/research/work-items/page'){
    queries.push(Object.fromEntries(u.searchParams));return reply({items:rows,total:2,page:1,total_exact:true,has_more:false,read_identity:'personal',field_labels:{resolution:labels}})
   }
   if(p==='/api/research/work-items/7')return reply({item:rows[0],remote:{bug:{id:'100',resolution:'fixed2',status:'resolved',steps:'测试说明'},resolutions:labels,actions:{}},source_identity:'personal',activity:[],pending_operations:[]})
   if(p==='/api/work-items/7/delivery-content')return reply({changes:[]})
   if(p==='/api/research/work-items/7/delivery-records')return reply({items:[],has_more:false})
   if(p==='/api/research/work-items/7/tracking')return reply({versions:[],deliveries:[]})
   if(p==='/api/services')return reply([])
  }
  unexpected.push(req.method()+' '+p);return route.fulfill({status:404,json:{error:'unexpected fixture'}})
 })
 await page.goto(base+'/api/plugins/research/ui#/work')
 for(const label of Object.values(labels))await page.locator('tbody').getByText(label,{exact:true}).waitFor()
 assert.equal(await page.locator('tbody').getByText(/^fixed[23]$/).count(),0)
 await page.getByRole('button',{name:'更多',exact:true}).first().click()
 await page.getByText('级别、优先级、方案筛选',{exact:true}).click()
 const resolution=page.locator('.r-filters .el-select').filter({hasText:'全部方案'})
 await resolution.click();await page.getByRole('option',{name:labels.fixed2,exact:true}).click()
 await page.getByRole('button',{name:'应用筛选',exact:true}).click()
 await page.waitForFunction(()=>document.querySelector('.r-table')?.getAttribute('aria-busy')==='false')
 assert.equal(queries.at(-1).resolution,'fixed2','filter sent the label instead of the source code')
 await page.getByRole('button',{name:rows[0].title,exact:true}).click()
 await page.locator('.r-side-details').getByText(labels.fixed2,{exact:true}).waitFor()
 assert.equal(await page.locator('.r-side-details').getByText('fixed2',{exact:true}).count(),0)
 await page.screenshot({path:'/tmp/research-resolution-labels-detail.png',fullPage:true})
 assert.deepEqual(errors,[]);assert.deepEqual(unexpected,[])
 console.log('PASS native custom labels in list/detail/filter; filtering retains fixed2; fixture-only reads and attach')
}finally{await browser.close()}
