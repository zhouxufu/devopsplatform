// Synthetic in-memory browser fixture. Never contacts a Runner or deployment host.
import http from 'node:http'
import {readFile} from 'node:fs/promises'
import path from 'node:path'
const service={name:'modal-fixture',artifact_type:'image',branch:'main',build_tool:'command',steps:[]}
const numberMode=process.env.RELEASE_NUMBER_FIXTURE==='1'
const variantMode=process.env.BUILD_VARIANT_FIXTURE==='1'
if(variantMode)Object.assign(service,{build_tool:process.env.BUILD_VARIANT_TOOL||'maven',maven_executable:'auto',maven_goals:'clean package',gradle_executable:'auto',gradle_tasks:'clean bootJar',artifact_glob:'build/libs/*.jar'})
const envs=[{name:'test',display_name:'测试'},{name:'prod',display_name:'生产',protected:true,require_reason:true,approvers:'reviewer'}]
const targets=envs.map(e=>({...e,service:service.name,environment:e.name,target_type:'docker-host',host_group:'fixture',hosts:['synthetic-host'],batch_size:1,max_parallel:1,deploy_config:{},runtime_env_keys:['SPRING_PROFILES_ACTIVE']}))
const artifacts=[1,2,3].map(id=>({id,service:service.name,version:'fixture-v'+id,kind:'docker-image-archive',digest:'sha256:fixture'+id,source:'pipeline',source_id:id,size:123,metadata:{branch:'main'}}))
const history=Array.from({length:65},(_,i)=>({id:(numberMode?100:0)+65-i,...(numberMode?{number:65-i}:{}),service:service.name,version:'fixture-v1',environment:'test',status:'failed',actor:'fixture',started_at:1792455482-i,total:1,failed:1,succeeded:0,deploy_ids:[]}))
const writes=[],unknown=[],calls=[];let failReads=false
const variants={test:{name:'test',...(service.build_tool==='maven'?{maven_profiles:'test'}:{gradle_project_properties:'env=test'})},prod:{name:'prod',...(service.build_tool==='maven'?{maven_profiles:'prod'}:{gradle_project_properties:'env=prod'})}}
http.createServer(async(req,res)=>{
 const u=new URL(req.url,'http://fixture'),p=u.pathname
 const json=(v,status=200)=>{res.writeHead(status,{'Content-Type':'application/json','Cache-Control':'no-store'});res.end(JSON.stringify(v))}
 if(p==='/__fixture/status')return json({writes,unknown,calls})
 if(p==='/__fixture/control'){failReads=u.searchParams.get('fail_reads')==='1';if(u.searchParams.get('tool'))service.build_tool=u.searchParams.get('tool');return json({failReads})}
 if(p.startsWith('/api/'))calls.push(req.method+' '+p)
 const defaults=p.match(/^\/api\/services\/modal-fixture\/environments\/(test|prod)\/delivery-default$/)
 if(defaults&&req.method==='GET')return json({revision:1,build_tool:service.build_tool,artifact_source:'existing',value:variantMode?{artifact_source:'existing',code_policy:{},build_variant:variants[defaults[1]]}:null})
 if(req.method==='PUT'&&p.match(/^\/api\/services\/modal-fixture\/environments\/(test|prod)$/)){let raw='';for await(const c of req)raw+=c;const b=JSON.parse(raw);writes.push({fixture:'environment-configuration',...b});return json(b)}
 if(req.method==='GET'&&p.match(/^\/api\/artifacts\/\d+\/build-compatibility$/)){const id=Number(p.split('/')[3]);const expected=u.searchParams.get('environment');const compatible=!variantMode||(id===2?'prod':'test')===expected;return failReads?json({error:'模拟校验不可用'},503):json({compatible,reason:compatible?'':'制品构建方案与目标环境不兼容，请按目标环境重新构建'})}
 if(req.method==='POST'&&p==='/api/releases'){let raw='';for await(const c of req)raw+=c;const b=JSON.parse(raw);writes.push(b);const r={...b,id:100+writes.length,...(numberMode?{number:history.length+1}:{}),version:artifacts.find(a=>a.id===b.artifact_id)?.version,status:b.environment==='prod'?'pending_approval':'queued',actor:'fixture',total:1,started_at:1792456000,deploy_ids:[]};history.unshift(r);return json(r)}
 if(req.method!=='GET')return json({error:'Fixture rejects actions'},405)
 if(p==='/api/me')return json({username:'fixture',role:'admin'})
 if(p==='/api/services')return json([service])
 if(p==='/api/environments')return json(envs)
 if(p==='/api/services/modal-fixture/environments')return json(targets)
 if(p==='/api/services/modal-fixture/delivery')return json({runner:'local',variable_groups:[]})
 if(p==='/api/artifacts')return json(artifacts)
 if(/^\/api\/releases\/\d+$/.test(p))return json(history.find(r=>r.id===Number(p.split('/').at(-1)))||null)
 if(p==='/api/releases')return failReads?json({error:'模拟读取失败'},503):json(history)
 if(p==='/api/deploys')return json([])
 if(p==='/api/pipeline/runs/page')return json({items:[],total:0,page:1,page_size:20})
 if(['/api/artifacts/page','/api/release-orders/page'].includes(p)){
  const list=p==='/api/artifacts/page'?artifacts:history
  const filtered=list.filter(r=>(!u.searchParams.get('q')||r.version.includes(u.searchParams.get('q')))&&(!u.searchParams.get('status')||r.status===u.searchParams.get('status')))
  const page=Number(u.searchParams.get('page')||1),size=Number(u.searchParams.get('page_size')||20)
  return json({items:filtered.slice((page-1)*size,page*size),total:filtered.length,page,page_size:size})
 }
 if(['/api/pipeline/runs','/api/builds','/api/connections','/api/variable-groups','/api/runners','/api/host-groups','/api/steps','/api/plugins'].includes(p))return json([])
 if(p==='/favicon.ico'){res.writeHead(204);res.end();return}
 const f=p==='/deploy.html'?'deploy.html':/^\/assets\/[^/]+\.(js|css)$/.test(p)?'assets/'+path.basename(p):''
 if(f){try{const b=await readFile(path.resolve('web/dist',f));res.writeHead(200,{'Content-Type':f.endsWith('.js')?'text/javascript':f.endsWith('.css')?'text/css':'text/html'});res.end(b);return}catch{}}
 unknown.push(req.method+' '+p);json({error:'Unsupported fixture route'},404)
}).listen(Number(process.env.PORT||19829),'127.0.0.1',()=>console.log(`Release modal QA: http://127.0.0.1:${process.env.PORT||19829}/deploy.html#/svc/modal-fixture/release`))
