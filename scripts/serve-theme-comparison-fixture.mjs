// Real deployment Vue entry + synthetic read-only API + compiled theme packages.
import {createServer} from '../web/node_modules/vite/dist/node/index.js'
import {readFile} from 'node:fs/promises'
import path from 'node:path'
import {themeReferences} from '../web/src/qa/themeReferences.js'
const root=process.env.THEME_AUDIT_DIR,port=Number(process.env.THEME_PREVIEW_PORT||19986)
if(!root||!path.isAbsolute(root))throw Error('Explicit isolated THEME_AUDIT_DIR required')
const report=JSON.parse(await readFile(path.join(root,'compiled/report.json'),'utf8')),calls=[],rejected=[]
const apps=[['estate-ai-seek','智能搜索服务','image',['AI','后端']],['estate-business','业务服务','jar',['核心','后端']],['estate-console','管理控制台','static',['前端']]].map(([name,display_name,artifact_type,tags])=>({name,display_name,identifier:name,application_id:'theme-'+name,artifact_type,tags,git_url:'https://example.invalid/'+name+'.git',branch:'test-release',build_tool:artifact_type==='jar'?'maven':'command',artifact_glob:artifact_type==='jar'?'target/*.jar':'dist',image_name:name,steps:[]}))
let vite
const handler=async(req,res,next)=>{
 const u=new URL(req.url,'http://fixture'),p=u.pathname
 const send=(body,type='application/json',status=200)=>{res.statusCode=status;res.setHeader('Content-Type',type);res.setHeader('Cache-Control','no-store');res.end(type==='application/json'?JSON.stringify(body):body)}
 if(p==='/__fixture/status')return send({calls,rejected})
 if(p==='/__theme_audit/report.json')return send(report)
 if(p.startsWith('/__theme_audit/shots/')&&p.endsWith('.png')){const file=path.basename(p);if(!report.some(t=>t.compiled&&['light','dark'].some(m=>file===t.id+'-'+m+'.png'))&&file!=='christmas-official.png'&&!themeReferences.some(t=>Object.values(t.screenshots).includes(file)))return send({error:'unknown screenshot'},'application/json',404);try{return send(await readFile(path.join(root,'compiled/shots',file)),'image/png')}catch(error){if(error.code==='ENOENT')return send({error:'screenshot not captured yet'},'application/json',404);throw error}}
 if(p.startsWith('/__theme_audit/')&&p.endsWith('.css')){const id=path.basename(p,'.css');if(!report.some(t=>t.id===id&&t.compiled))return send({error:'unknown theme'},'application/json',404);return send(await readFile(path.join(root,'compiled',id+'.css')),'text/css')}
 if(p==='/__theme_preview/deploy'){
  const theme=report.find(t=>t.id===u.searchParams.get('theme')&&t.compiled);if(!theme)return send({error:'theme unavailable'},'application/json',404)
  let html=await readFile('web/deploy.html','utf8');html=html.replace('<head>','<head><base href="/">').replace('src="/src/deploy/main.js"','src="/__theme_bootstrap.js?'+u.searchParams.toString()+'"')
  return send(await vite.transformIndexHtml(req.url,html),'text/html')
 }
 if(p==='/__theme_bootstrap.js'){
  const id=u.searchParams.get('theme'),mode=u.searchParams.get('mode')==='dark'?'dark':'light'
  if(!report.some(t=>t.id===id&&t.compiled))return send('','text/javascript',404)
  return send(`localStorage.setItem('lp-theme',${JSON.stringify(mode)});localStorage.setItem('lp-theme-typography','standard');await import('/src/deploy/main.js');const link=document.createElement('link');link.rel='stylesheet';link.href='/__theme_audit/${id}.css';link.onload=()=>{for(const e of [document.documentElement,document.body])for(let i=1;i<=10;i++){e.style.removeProperty('--primary-'+i);e.style.removeProperty('--arcoblue-'+i)}document.body.removeAttribute('data-console-embedded');document.querySelector('.layout')?.classList.remove('embedded');document.documentElement.dataset.previewTheme=${JSON.stringify(id)};document.body.dataset.previewReady='true'};document.head.append(link);`,'text/javascript')
 }
 if(!p.startsWith('/api/'))return next()
 calls.push(req.method+' '+p)
 if(req.method!=='GET'){rejected.push(req.method+' '+p);return send({error:'只读主题夹具禁止构建、发布或业务写入'},'application/json',403)}
 if(p==='/api/me')return send({username:'theme-fixture',display_name:'主题预览',role:'admin',auth_enabled:false})
 if(p==='/api/services'||p==='/api/registry/applications')return send(apps)
 if(p==='/api/services/summary')return send(apps.map((a,i)=>({name:a.name,latest_run:{id:11+i,status:i===1?'failed':'success',version:['7fb50c5','68c2ac4','ef64bb9'][i],started_at:1792630000,branch:a.branch},latest_artifact:i===1?null:{id:31+i,version:'7fb50c5',type:a.artifact_type},delivery:{runner:'local',build_environment:{mode:'local'}},environments:[{environment:'test',display_name:'测试环境'}]})))
 if(p.endsWith('/delivery'))return send({runner:'local',build_environment:{mode:'local'},variable_groups:[]})
 if(p==='/api/environments')return send([{name:'dev',display_name:'开发环境'},{name:'test',display_name:'测试环境'},{name:'prod',display_name:'生产环境'}])
 if(p.startsWith('/api/'))return send([])
}
vite=await createServer({root:path.resolve('web'),configFile:path.resolve('web/vite.config.js'),plugins:[{name:'theme-comparison-fixture',configureServer(s){s.middlewares.use(handler)}}],server:{host:'127.0.0.1',port,strictPort:true}})
await vite.listen();console.log('Read-only theme comparison http://127.0.0.1:'+port+'/theme-comparison-qa.html')
