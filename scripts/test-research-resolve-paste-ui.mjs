// All requests are intercepted: never uploads or resolves a real source Bug.
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import path from 'node:path'
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright')
const base='http://127.0.0.1:9800',dist=process.env.CONSOLE_UI_DIST||path.resolve('web/dist')
const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH})
const png=Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/l9sAAAAASUVORK5CYII=','base64')
let holdUpload=false,releaseUpload,failUpload=false,uploaded=0,sourceWrites=[]
const errors=[],unexpected=[]
const work={id:7,source:'zentao:issues',kind:'bug',external_id:'25766',title:'粘贴截图测试',external_status:'active',metadata_revision:0}
const bug={id:'25766',title:work.title,status:'active',assignedTo:'alice',resolution:''}
try{
 const page=await browser.newPage({viewport:{width:1440,height:960}})
 page.on('pageerror',e=>errors.push(e.message));page.on('dialog',d=>d.dismiss())
 await page.route('**/*',async route=>{
  const r=route.request(),u=new URL(r.url()),p=u.pathname.replace('/api/plugins/research',''),reply=json=>route.fulfill({json})
  if(u.origin!==base){unexpected.push(u.href);return route.abort()}
  let file;if(u.pathname==='/api/plugins/research/ui')file='research.html';else if(/\/assets\/[^/]+\.(js|css)$/.test(u.pathname))file='assets/'+path.basename(u.pathname)
  if(file)return route.fulfill({body:await readFile(path.join(dist,file)),contentType:file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html'})
  if(r.method()==='GET'){
   if(p==='/api/me')return reply({username:'alice',role:'member',auth_enabled:true})
   if(p==='/api/plugins')return reply([{name:'research'}])
   if(p==='/api/research/overview')return reply({actor:'alice',aliases:['alice'],connections:[{name:'issues',authorized:true}]})
   if(p==='/api/research/work-items/sources')return reply([{name:'issues',products:[1]}])
   if(p==='/api/research/work-items/page')return reply({items:[work],total:1,page:1,has_more:false,total_exact:true,read_identity:'personal'})
   if(p==='/api/research/work-items/7')return reply({item:work,remote:{bug},source_identity:'personal',pending_operations:[]})
   if(p.endsWith('/action-options'))return reply({bug,digest:'baseline',resolutions:{fixed:'已修复'},users:{alice:'研发'},builds:{trunk:'主干'}})
   if(/^\/api\/work-item-attachments\/[a-f0-9]{32}$/.test(p))return route.fulfill({body:png,contentType:'image/png'})
  }
  if(r.method()==='POST'&&p==='/api/work-item-attachments'){
   assert.match(r.headers()['content-type'],/^multipart\/form-data/)
   if(holdUpload)await new Promise(resolve=>releaseUpload=resolve)
   if(failUpload)return route.fulfill({status:502,json:{error:'模拟图片上传失败'}})
   const id=(++uploaded).toString(16).padStart(32,'0');return reply({id,name:'截图.png',reference:'attachment:'+id,size:png.length})
  }
  if(r.method()==='POST'&&p.endsWith('/actions')){const body=r.postDataJSON();sourceWrites.push(body);work.external_status='resolved';bug.status='resolved';return reply({state:'success',id:body.operation_id})}
  unexpected.push(r.method()+' '+p);return route.fulfill({status:404,json:{error:'unexpected fixture'}})
 })
 const paste=async(textbox,count=1,itemsOnly=false)=>textbox.evaluate((el,{data,count,itemsOnly})=>{
  const transfer=new DataTransfer();for(let i=0;i<count;i++)transfer.items.add(new File([Uint8Array.from(atob(data),c=>c.charCodeAt(0))],'截图'+i+'.png',{type:'image/png'}))
  if(itemsOnly){const e=new Event('paste',{bubbles:true,cancelable:true});Object.defineProperty(e,'clipboardData',{value:{files:[],items:[...transfer.items]}});el.dispatchEvent(e)}
  else el.dispatchEvent(new ClipboardEvent('paste',{bubbles:true,cancelable:true,clipboardData:transfer}))
 },{data:png.toString('base64'),count,itemsOnly})
 await page.goto(base+'/api/plugins/research/ui#/work')
 await page.getByRole('button',{name:'解决',exact:true}).click()
 const dialog=page.getByRole('dialog'),composer=dialog.getByRole('region',{name:'处理说明',exact:true}),text=composer.getByRole('textbox',{name:'处理说明',exact:true})
 await text.fill('处理说明保持不变')
 assert.equal(await dialog.getByText('附加图片',{exact:true}).count(),0,'separate upload field remains')
 holdUpload=true;await paste(text,2)
 await composer.getByRole('status').getByText('图片上传中，请稍候…',{exact:true}).waitFor()
 assert.equal(await dialog.getByRole('button',{name:'确认并同步来源平台'}).isDisabled(),true)
 assert.equal(await dialog.getByRole('button',{name:'取消',exact:true}).isDisabled(),true)
 await page.waitForFunction(()=>document.querySelector('.comment-composer')?.getAttribute('aria-busy')==='true')
 holdUpload=false;releaseUpload()
 await composer.getByRole('button',{name:'移除图片 2',exact:true}).waitFor()
 assert.equal(await text.inputValue(),'处理说明保持不变')
 await composer.getByRole('button',{name:'移除图片 1',exact:true}).click()
 await paste(text,1,true)
 await page.waitForFunction(()=>document.querySelectorAll('.comment-attachments li').length===2&&!document.querySelector('.comment-composer[aria-busy=true]'))
 // Drag/drop uses the same queue; file picker remains a secondary option.
 await text.evaluate((el,data)=>{const transfer=new DataTransfer();transfer.items.add(new File([Uint8Array.from(atob(data),c=>c.charCodeAt(0))],'拖入.png',{type:'image/png'}));el.dispatchEvent(new DragEvent('drop',{bubbles:true,cancelable:true,dataTransfer:transfer}))},png.toString('base64'))
 await composer.getByRole('button',{name:'移除图片 3',exact:true}).waitFor()
 await composer.locator('input[type=file]').setInputFiles({name:'选择.png',mimeType:'image/png',buffer:png})
 await composer.getByRole('button',{name:'移除图片 4',exact:true}).waitFor()
 assert.equal(await text.evaluate(el=>{const d=new DataTransfer();d.setData('text/plain','普通文本');return el.dispatchEvent(new ClipboardEvent('paste',{bubbles:true,cancelable:true,clipboardData:d}))}),true,'plain text paste was intercepted')
 const before=uploaded;failUpload=true;await paste(text)
 await page.getByText('模拟图片上传失败',{exact:true}).waitFor();assert.equal(uploaded,before)
 assert.equal(await composer.locator('.comment-attachments li').count(),4,'failed upload removed accepted pictures')
 failUpload=false
 await composer.locator('input[type=file]').setInputFiles({name:'过大.png',mimeType:'image/png',buffer:Buffer.alloc(8*1024*1024+1)})
 await page.getByText('仅支持 PNG、JPEG、GIF；每张图片最多 8 MiB',{exact:true}).waitFor();assert.equal(uploaded,before)
 assert.equal(sourceWrites.length,0,'pasting auto-submitted a source action')
 for(const width of [1440,900,390]){await page.setViewportSize({width,height:900});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);const footer=await dialog.locator('.el-dialog__footer').boundingBox();assert(footer.y+footer.height<=900)}
 await page.setViewportSize({width:1440,height:960});await composer.scrollIntoViewIfNeeded();await page.screenshot({path:'/tmp/research-resolve-paste.png'})
 await dialog.getByRole('button',{name:'确认并同步来源平台',exact:true}).click();await dialog.waitFor({state:'hidden'})
 assert.equal(sourceWrites.length,1);assert.equal(sourceWrites[0].comment,'处理说明保持不变')
 assert.deepEqual(sourceWrites[0].attachments,[2,3,4,5].map(id=>id.toString(16).padStart(32,'0')))
 assert.deepEqual(errors,[]);assert.deepEqual(unexpected,[])
 console.log('PASS resolve paste/multiple images/items fallback/drop/picker/preview removal/loading/error/limits/plain text/ID-only submit/responsive; fixture only')
}finally{await browser.close()}
