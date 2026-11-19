// Disposable UI fixture. No credentials, repositories or business writes.
import {createServer} from '../web/node_modules/vite/dist/node/index.js'
import path from 'node:path'
const calls=[],writes=[]
const work=id=>({id,external_id:String(26682+id),source:'tracker:qa',kind:'bug',title:['10年内的房子，应该是大于等于2016','用户总结需求的时候，如果用户没有浏览记录，提示补充筛选条件','跨环境菜单配置同步后，需要检查父子层级和权限标识是否一致'][id%3]+' · 验收 '+id,status:'closed',external_status:'closed',resolved_by:'alice',updated_at:1792972800,source_closed_at:id%4?'2026-10-'+String(1+id%26).padStart(2,'0')+' 08:00:00':'',change_count:id%4?1:0,ready_count:1})
let vite
vite=await createServer({root:path.resolve('web'),configFile:path.resolve('web/vite.config.js'),plugins:[{name:'work-picker-layout-fixture',configureServer(server){server.middlewares.use(async(req,res,next)=>{
 const u=new URL(req.url,'http://fixture')
 const json=(v,status=200)=>{res.statusCode=status;res.setHeader('Content-Type','application/json');res.end(JSON.stringify(v))}
 if(u.pathname==='/__fixture/status')return json({calls,writes})
 if(u.pathname.startsWith('/api/')){
  calls.push({method:req.method,path:u.pathname,query:Object.fromEntries(u.searchParams)})
  if(req.method!=='GET'){writes.push(req.url);return json({error:'Fixture forbids business writes'},403)}
  if(u.pathname==='/api/work-items/people')return u.searchParams.get('scenario')==='options-error'?json({error:'模拟人员读取失败'},503):json(['alice','bob'])
  if(u.pathname==='/api/work-items/people-labels')return json({'tracker:qa':{alice:'研发同学',bob:'测试同学'}})
  if(u.pathname==='/api/work-items/candidate-sources')return json(['tracker:qa','manual'])
  if(u.pathname==='/api/research/work-items/sources')return json([{name:'验收来源'}])
  if(u.pathname==='/api/work-items/candidates/selected')return json({items:u.searchParams.get('scenario')==='missing'?[]:(u.searchParams.get('ids')||'').split(',').filter(Boolean).map(Number).map(work)})
  if(['/api/work-items/candidates','/api/research/work-items/page'].includes(u.pathname)){
   const page=Number(u.searchParams.get('page')||1),size=Number(u.searchParams.get('page_size')||20),q=u.searchParams.get('q')||''
   let rows=Array.from({length:64},(_,i)=>work(i+1)).filter(w=>!q||w.title.includes(q)||w.external_id.includes(q))
   if(u.searchParams.get('time_field')==='closed'&&(u.searchParams.get('source_start')||u.searchParams.get('source_end'))){const start=u.searchParams.get('source_start'),end=u.searchParams.get('source_end');rows=rows.filter(w=>w.source_closed_at&&(!start||w.source_closed_at>=start+' 00:00:00')&&(!end||w.source_closed_at<=end+' 23:59:59'))}
   const numbers=(u.searchParams.get('numbers')||'').split(/[,\s]+/).filter(Boolean)
   if(numbers.length)rows=rows.filter(w=>numbers.includes(w.external_id))
   if(u.pathname.endsWith('/page'))rows=rows.map(w=>({...w,source:'zentao:验收来源',people:{alice:'研发同学'},source_updated_at:'2026-10-26 08:00:00'}))
   return json({items:rows.slice((page-1)*size,page*size),page,page_size:size,total:rows.length,total_exact:true,people:{alice:'研发同学',bob:'测试同学'},products:{1:'示例产品'}})
  }
  if(/^\/api\/work-items\/\d+$/.test(u.pathname))return json({...work(Number(u.pathname.split('/').at(-1))),description:'此处为隔离验收的详情内容，不读取真实来源工作项。'})
  return json({error:'Unknown fixture API'},404)
 }
 if(u.pathname==='/__work-picker'){
  res.setHeader('Content-Type','text/html');return res.end(await vite.transformIndexHtml(req.url,'<html lang="zh-CN"><head><meta charset="UTF-8"><title>需求选择布局预览</title></head><body><div id="app"></div><script type="module" src="/src/qa/workPickerLayoutFixture.js"></script></body></html>'))
 }
 next()
})}}],server:{host:'127.0.0.1',port:19982,strictPort:true}})
await vite.listen();console.log('Work picker fixture http://127.0.0.1:19982/__work-picker')
