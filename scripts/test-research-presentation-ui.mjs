// Every request is intercepted, including uploads and source comments.
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import path from 'node:path'
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright')
const base='http://127.0.0.1:19823',dist=process.env.CONSOLE_UI_DIST||path.resolve('web/dist'),browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH})
const png=Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/l9sAAAAASUVORK5CYII=','base64')
const errors=[],unexpected=[],writes=[],uploaded=[],imageRequests=[]
try{
 const page=await browser.newPage({viewport:{width:1440,height:1000}})
 page.on('pageerror',e=>errors.push(e.message));page.on('dialog',d=>d.accept())
 const long=Array.from({length:20},(_,i)=>`<p>复现步骤第 ${i+1} 行：完整 Bug 说明。</p>`).join('')
 const item={id:7,source:'zentao:issues',kind:'bug',title:'费用结算显示异常',external_id:'26087',description:long,description_format:'html',status:'open',external_status:'resolved',resolution:'fixed2',release_required:true,metadata_revision:0,url:'http://source.invalid/zentao/bug-view-26087.html'}
 const remote={bug:{id:26087,title:item.title,status:'resolved',resolution:'fixed2',steps:long+'<p><img src="{42.png}" alt="费用截图"></p><img src="http://tracker.invalid/beacon.png">'},resolutions:{fixed2:'前端代码修复'},users:{alice:'张三',qa:'李四'},actions:{1:{actor:'qa',action:'opened',date:'2026-10-14 09:00:00',comment:''},2:{actor:'alice',action:'resolved',date:'2026-10-14 10:00:00',comment:'<p>前端枚举已改</p><script>window.bad=1</script><br>[delivery-operation:'+'a'.repeat(32)+']',history:[{field:'resolution',old:'',new:'fixed2'},{field:'activatedDate',old:'2026-09-28 10:05:16',new:'2026-10-07 15:04:19'},{field:'activatedCount',old:2,new:3},{field:'resolvedDate',old:'2026-10-07 09:53:47',new:'0000-00-00'},{field:'closedDate',old:'',new:'0000-00-00'}]}}}
 let activities=[],changes=[{id:7,kind:'manual',title:'人工处理',reference:'处理步骤',status:'draft',owner:'alice'}],upload=0,failImage=false
 await page.route('**/*',async route=>{
  const r=route.request(),u=new URL(r.url()),p=u.pathname.replace('/api/plugins/research',''),method=r.method(),reply=json=>route.fulfill({json})
  if(u.origin!==base){unexpected.push('remote '+u.href);return route.abort()}
  let file;if(u.pathname==='/api/plugins/research/ui')file='research.html';else if(/\/assets\/[^/]+\.(js|css)$/.test(u.pathname))file='assets/'+path.basename(u.pathname)
  if(file)return route.fulfill({body:await readFile(path.join(dist,file)),contentType:file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html'})
  if(/\/images\/42$/.test(p)||/\/work-item-attachments\/[a-f0-9]{32}$/.test(p)){imageRequests.push(p);if(failImage)return route.fulfill({status:403,json:{error:'test image unavailable'}});return route.fulfill({body:png,contentType:'image/png'})}
  if(method==='GET'){
   if(p.endsWith('/delivery-records'))return reply({items:[],has_more:false})
  if(p==='/api/me')return reply({username:'alice',role:'admin',auth_enabled:true})
   if(p.endsWith('/research/overview'))return reply({connections:[{name:'issues',authorized:true}]})
   if(p.endsWith('/research/work-items/7'))return reply({item,remote:item.source==='manual'?null:remote,source_identity:'personal',activity:activities,pending_operations:[]})
   if(p.endsWith('/action-options'))return reply({bug:remote.bug,digest:'baseline',resolutions:remote.resolutions})
   if(p.endsWith('/delivery-content'))return reply({changes,digest:'v1'})
   if(p.endsWith('/services'))return reply([])
   return reply([])
  }
  if(p==='/api/work-item-attachments'&&method==='POST'){
   const id=(++upload).toString(16).padStart(32,'0');uploaded.push(id);return reply({id,reference:'attachment:'+id,name:'截图.png',size:png.length})
  }
  const body=r.postDataJSON();writes.push({p,body})
  if(p.endsWith('/actions')){assert.equal(body.action,'comment');assert(body.attachments.length>0);activities.unshift({id:activities.length+1,actor:'alice',kind:'comment',at:1792029600,content:body.comment+'\n\n'+body.attachments.map(id=>'![评论图片](attachment:'+id+')').join('\n')});return reply({state:'success',id:body.operation_id})}
  if(p.endsWith('/comment')){activities.unshift({id:activities.length+1,actor:'alice',kind:'comment',at:1792029600,content:body.comment+'\n\n'+body.attachments.map(id=>'![评论图片](attachment:'+id+')').join('\n')});return reply({ok:true})}
  if(p.endsWith('/delivery-content')&&method==='PUT'){assert.equal(body.changes.length,1);assert.equal(body.changes[0].id,7,'editing an unbound record generated a new identity');changes=[{...changes[0],...body.changes[0]}];delete changes[0].service;return reply({ok:true})}
  unexpected.push(method+' '+p);return route.abort()
 })
 await page.goto(base+'/api/plugins/research/ui#/work/7')
 await page.locator('.r-side-details').getByText('前端代码修复',{exact:true}).waitFor()
 const description=page.locator('.description-panel').first()
 assert.equal(await description.getByRole('button',{name:'展开完整说明',exact:true}).count(),0)
 assert((await description.innerText()).includes('复现步骤第 20 行'))
 await description.locator('.thumbnail img').waitFor();assert.equal(await description.locator('.thumbnail').count(),1)
 assert(imageRequests.some(p=>p.endsWith('/images/42')))
 assert.equal(await page.locator('.activity-entry').count(),2)
 const details=page.locator('.activity-entry').filter({hasText:'前端枚举已改'})
 const toggle=details.getByRole('button',{name:'查看变更详情',exact:true})
 assert.equal(await toggle.getAttribute('aria-expanded'),'false')
 assert.equal(await details.locator('.activity-history').count(),0)
 assert((await details.locator('.activity-meta').innerText()).includes('方案为 前端代码修复'))
 assert((await page.locator('.activity-entry').first().innerText()).includes('创建了 Bug'),'history must start at creation')
 await toggle.click()
 assert.equal(await details.locator('.activity-history').isVisible(),true)
 const history=await details.locator('.activity-history').innerText()
 assert(history.includes('激活时间')&&history.includes('激活次数'))
 assert(!history.includes('0000-00-00')&&!history.includes('activatedDate'))
 assert(!history.includes('关闭时间'),'empty-to-empty dates add noise')
 await details.getByRole('button',{name:'收起变更详情',exact:true}).click()
 await page.getByRole('button',{name:'按时间倒序',exact:true}).click()
 assert((await page.locator('.activity-entry').first().innerText()).includes('解决了 Bug'))
 await page.getByRole('button',{name:'按时间正序',exact:true}).click()
 assert.equal(await page.locator('.activity-entry').filter({hasText:'暂无说明'}).count(),0)
 assert.equal(await page.locator('.activity-entry').filter({hasText:'delivery-operation'}).count(),0)
 assert.equal(await page.evaluate(()=>window.bad),undefined)
 await page.getByRole('button',{name:'只看评论',exact:true}).click();assert.equal(await page.locator('.activity-entry').count(),1)
 await page.getByRole('button',{name:'全部活动',exact:true}).click()
 await description.getByRole('button',{name:'查看图片 1 · 费用截图',exact:true}).click()
 await page.getByRole('dialog',{name:'图片预览',exact:true}).locator('img').waitFor()
 await page.getByRole('button',{name:'关闭预览',exact:true}).click()
 failImage=true
 await description.locator('.thumbnail img').evaluate(el=>el.src+='?failure=1')
 await description.getByText('图片加载失败，点击重试',{exact:true}).waitFor()
 failImage=false
 await description.getByRole('button',{name:'查看图片 1 · 费用截图',exact:true}).click()
 await page.getByRole('dialog',{name:'图片预览',exact:true}).locator('img').waitFor()
 await page.waitForFunction(()=>{const img=document.querySelector('.description-panel .thumbnail img');return img?.naturalWidth>0&&!img.hidden})
 await page.getByRole('button',{name:'关闭预览',exact:true}).click()
 const composer=page.getByRole('region',{name:'发表评论',exact:true})
 const input=composer.locator('input[type=file]')
 await input.setInputFiles({name:'截图.png',mimeType:'image/png',buffer:png})
 await composer.getByRole('button',{name:'移除图片 1',exact:true}).waitFor();await composer.getByRole('button',{name:'移除图片 1',exact:true}).click()
 assert.equal(await composer.locator('.comment-attachments li').count(),0)
 await composer.getByRole('textbox',{name:'评论内容'}).evaluate((el,data)=>{const f=new File([Uint8Array.from(atob(data),c=>c.charCodeAt(0))],'粘贴截图.png',{type:'image/png'});const d=new DataTransfer();d.items.add(f);el.dispatchEvent(new ClipboardEvent('paste',{clipboardData:d,bubbles:true,cancelable:true}))},png.toString('base64'))
 await composer.getByRole('button',{name:'移除图片 1',exact:true}).waitFor()
 await composer.getByRole('button',{name:'发送评论',exact:true}).click()
 await page.waitForFunction(()=>document.querySelector('.activity-entry .thumbnail'))
 assert.equal(writes.filter(w=>w.p.endsWith('/actions')).length,1)
 assert.deepEqual(writes.find(w=>w.p.endsWith('/actions')).body.attachments,[uploaded.at(-1)])
 assert.equal(await composer.locator('.comment-attachments li').count(),0)
 await page.screenshot({path:'/tmp/research-comments-redesign-1440.png',fullPage:true})
 await page.getByRole('button',{name:'维护交付内容',exact:true}).click()
 await page.getByRole('button',{name:'自定义记录 · 1',exact:true}).click()
 const editor=page.locator('.delivery-editor');await editor.locator('textarea').first().fill('处理步骤已更新')
 await page.getByRole('button',{name:'保存交付草稿',exact:true}).click();await page.getByRole('button',{name:'自定义记录 · 1',exact:true}).waitFor()
 await page.getByRole('button',{name:'保存交付草稿',exact:true}).click()
 assert.equal(writes.filter(w=>w.p.endsWith('/delivery-content')).length,1,'unchanged save rewrote records')
 await page.getByRole('dialog').getByRole('button',{name:'关闭',exact:true}).click()
 await page.getByRole('dialog').waitFor({state:'hidden'})
 assert.equal(await page.locator('.delivery-editor:visible').count(),0)
 item.source='manual';item.description_format='markdown';item.description='手工 Bug';await page.reload()
 await page.getByRole('textbox',{name:'评论内容'}).fill('自定义工作项评论')
 await page.getByRole('region',{name:'发表评论'}).locator('input[type=file]').setInputFiles({name:'本地.png',mimeType:'image/png',buffer:png})
 await page.getByRole('button',{name:'移除图片 1',exact:true}).waitFor()
 await page.getByRole('button',{name:'发送评论',exact:true}).click()
 await page.getByText('自定义工作项评论',{exact:true}).waitFor()
 assert(writes.some(w=>w.p.endsWith('/comment')&&w.body.attachments.length===1))
 for(const width of [1440,900,700]){await page.setViewportSize({width,height:1000});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth+2),false)}
 assert.deepEqual(errors,[]);assert.deepEqual(unexpected,[])
 console.log(JSON.stringify({ok:true,checks:['full Bug description','safe thumbnails and enlargement','compact translated timeline','native custom resolution','upload/remove/paste','image-only native comment once','manual comment images','unbound custom record updates and repeat save','responsive'],noRealWrites:true}))
}finally{await browser.close()}
