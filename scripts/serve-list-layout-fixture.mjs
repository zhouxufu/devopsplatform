// Isolated layout/interaction acceptance: synthetic records, no external services or business writes.
import http from 'node:http'
import {readFile} from 'node:fs/promises'
import path from 'node:path'

const port=Number(process.env.LIST_LAYOUT_PORT||19848),dist=path.resolve('web/dist'),calls=[],rejected=[]
const actor={username:'designer',actor:'designer',display_name:'布局验收',role:'admin',auth_enabled:true,aliases:[],connections:[]}
const services=[{name:'orders',display_name:'订单服务',application_id:'fixture-orders',artifact_type:'jar',tags:[],steps:[]}]
const environments=[{name:'test',display_name:'测试环境',purpose:'test'},{name:'prod',display_name:'生产环境',purpose:'production'}]
const works=Array.from({length:48},(_,i)=>({id:i+1,source:'manual',kind:i%2?'bug':'requirement',title:(i===0?'跨应用配置同步与菜单权限调整，检查较长标题及交付内容的显示效果':'工作项布局验收 '+(i+1)),status:i%3?'active':'resolved',owner:i%2?'reviewer':'designer',assigned_to:'designer',resolved_by:'reviewer',severity:2,priority:2,release_required:true,updated_at:1793073600,linked_items:[],ownership:'independent',preparation:'ready',ready_count:1,change_count:1,delivery_status:'not_started'}))
const changes=works.map((w,i)=>({id:w.id,title:i===0?'跨应用配置同步与菜单权限调整，验证长标题、关联项及固定操作列':'变更布局验收 '+w.id,kind:i%2?'code':'nacos',status:i%3?'draft':'ready',service:'orders',owner:w.owner,reference:'feature/layout',work_items:[w],updated_at:w.updated_at}))
const versions=Array.from({length:24},(_,i)=>({id:i+1,title:'多应用发布范围与配置同步 '+(i+1),version:'v2026.10.'+(i+1),status:i%2?'draft':'ready',owner:i%2?'designer':'reviewer',application_count:2,work_item_count:3,change_count:5,updated_at:1793073600,work_items:[],changes:[]}))
function paged(rows,u){
 const q=u.searchParams,query=q.get('q'),kind=q.get('kind'),status=q.get('status'),owner=q.get('owner'),person=q.get('person'),role=q.get('person_role')||'assigned_to'
 rows=rows.filter(w=>(!query||[w.title,w.id,w.version,w.service].join(' ').includes(query))&&(!kind||w.kind===kind)&&(!status||w.status===status)&&(!owner||w.owner===owner)&&(!person||w[role]==person||role==='related'&&[w.owner,w.assigned_to,w.resolved_by].includes(person))&&(!q.get('severity')||String(w.severity)===q.get('severity'))&&(!q.get('priority')||String(w.priority)===q.get('priority')))
 if(q.get('association')==='independent')rows=rows.filter(w=>!w.work_items?.length)
 if(q.get('association')==='linked')rows=rows.filter(w=>w.work_items?.length)
 if(q.get('order')?.endsWith('_asc'))rows=[...rows].reverse()
 const size=Number(q.get('page_size'))||20,page=Math.max(1,Math.min(Number(q.get('page'))||1,Math.ceil(rows.length/size)||1))
 return {items:rows.slice((page-1)*size,page*size),total:rows.length,page,page_size:size,total_exact:true,has_more:page*size<rows.length,source_errors:[],checked_at:1793073600}
}
http.createServer(async(req,res)=>{
 const u=new URL(req.url,'http://fixture'),original=u.pathname,p=original.replace(/^\/api\/plugins\/[^/]+/,'')
 const json=(data,status=200)=>{res.writeHead(status,{'Content-Type':'application/json','Cache-Control':'no-store'});res.end(JSON.stringify(data))}
 if(original==='/__fixture/status')return json({calls,rejected})
 calls.push(req.method+' '+original+u.search)
 if(req.method!=='GET'){rejected.push(req.method+' '+original);return json({error:'布局夹具拒绝业务写入'},405)}
 // A narrow iframe exercises the actual embedded viewport without changing the user's browser settings.
 if(original==='/preview'){
  const width=Math.min(1920,Math.max(320,Number(u.searchParams.get('width'))||1280)),mode=u.searchParams.get('mode')==='dark'?'dark':'light'
  const typography=['small','large'].includes(u.searchParams.get('typography'))?u.searchParams.get('typography'):'standard'
  const surface=['research','changes','delivery','versions'].includes(u.searchParams.get('surface'))?u.searchParams.get('surface'):'changes'
  const entry=['research','versions'].includes(surface)?'research':'deploy',hash={research:'/work',changes:'/changes',delivery:'/changes?tab=work',versions:'/versions'}[surface]
  res.writeHead(200,{'Content-Type':'text/html; charset=utf-8','Cache-Control':'no-store'})
  return res.end(`<!doctype html><html lang="zh-CN"><title>列表布局验收</title><style>body{margin:0;background:${mode==='dark'?'#17171a':'#f7f8fa'};font:14px sans-serif}header{padding:12px;color:${mode==='dark'?'white':'#4e5969'}}iframe{display:block;border:0;width:${width}px;max-width:${width>1280?'none':'100%'};height:calc(100vh - 44px)}</style><header>列表布局预览 · 合成数据 · ${width}px · ${mode} <output id="metrics"></output></header><iframe title="列表预览" src="/api/plugins/${entry}/ui#${hash}"></iframe><script>document.querySelector('iframe').addEventListener('load',e=>e.target.contentWindow.postMessage({type:'theme.sync',mode:'${mode}',dark:${mode==='dark'},typography:'${typography}',preset:'default',accent:'blue'},location.origin));setInterval(()=>{const d=document.querySelector('iframe').contentDocument;if(!d?.querySelector('.console-table'))return;const actions=[...d.querySelectorAll('.console-table tbody tr')].filter(r=>r.getBoundingClientRect().width>0).every(r=>[...r.lastElementChild.querySelectorAll('button')].every(b=>b.getBoundingClientRect().right<=r.lastElementChild.getBoundingClientRect().right-4));document.querySelector('#metrics').textContent=JSON.stringify({width:d.documentElement.clientWidth,scroll:d.documentElement.scrollWidth,typography:d.documentElement.dataset.consoleTypography,actionsFit:actions,titleWidths:[...d.querySelectorAll('.console-table th')].filter(e=>e.getBoundingClientRect().width>0&&['标题','名称','工作项 / 范围'].some(t=>e.textContent.includes(t))).map(e=>Math.round(e.getBoundingClientRect().width))})},500)</script></html>`)
 }
 if(p==='/api/me'||p==='/api/research/overview')return json(actor)
 if(p==='/api/plugins')return json(['research','deploy'].map(name=>({name,display:name==='deploy'?'部署平台':'研发协作',url:'/api/plugins/'+name+'/ui'})))
 if(p==='/api/services')return json(services)
 if(p==='/api/environments')return json(environments)
 if(p==='/api/research/work-items/sources')return json([{name:'QA',products:[1,2]}])
 if(p==='/api/work-items/delivery-page'||p==='/api/research/work-items/page')return json({...paged(works,u),products:{1:'订单产品',2:'后台产品'},people:{designer:'测试研发',reviewer:'测试审核'}})
 if(p==='/api/changes/page')return json(paged(changes,u))
 if(p==='/api/release-versions/page')return json(paged(versions,u))
 if(p.endsWith('/owners'))return json(['designer','reviewer'])
 if(p==='/api/changes')return json(changes)
 if(p==='/api/work-items')return json(works)
 if(p==='/api/release-versions')return json(versions)
 if(/^\/api\/changes\/\d+$/.test(p))return json(changes.find(w=>w.id===Number(p.split('/').at(-1))))
 if(/^\/api\/(research\/)?work-items\/\d+$/.test(p))return json(works.find(w=>w.id===Number(p.split('/').at(-1))))
 if(p.endsWith('/delivery-content'))return json({changes:[],digest:'fixture-only'})
 if(p.startsWith('/api/'))return json([])
 const plugin=original.match(/^\/api\/plugins\/([^/]+)/)?.[1]
 const file=original==='/'?'shell.html':p==='/ui'?(plugin==='research'?'research':'deploy')+'.html':/\/assets\/[^/]+\.(js|css)$/.test(original)?'assets/'+path.basename(original):null
 if(file)try{const content=await readFile(path.join(dist,file));res.writeHead(200,{'Content-Type':file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html','Cache-Control':'no-store'});return res.end(content)}catch{}
 json({error:'Not found'},404)
}).listen(port,'127.0.0.1',()=>console.log(`List layout fixture: http://127.0.0.1:${port}/preview?surface=changes (synthetic data only)`))
