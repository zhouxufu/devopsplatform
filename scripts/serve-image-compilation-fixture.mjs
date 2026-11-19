// In-memory UI fixture: never starts a pipeline or contacts a code/registry host.
import http from 'node:http'
import {readFile} from 'node:fs/promises'
import path from 'node:path'
const root=path.resolve('web/dist'),port=Number(process.env.IMAGE_BUILD_FIXTURE_PORT||19828),calls=[],writes=[],unknown=[]
let app={name:'image-fixture',git_url:'https://example.invalid/image-fixture.git',branch:'main',artifact_type:'image',image_name:'image-fixture',build_tool:'command',build_cmd:'',artifact_glob:'',steps:[{step:'git-checkout',params:{}},{step:'docker-build',params:{DOCKERFILE_SOURCE:'template',DOCKERFILE_TEMPLATE:'java21',DOCKERFILE_ARTIFACT_PATH:'app.jar',PUSH:'false',CONTEXT:'.'}}],tags:[]},delivery={runner:'local',variable_groups:[],build_environment:{mode:'local'}}
if(process.env.IMAGE_BUILD_FIXTURE_REPAIR==='1'){
 app={...app,build_tool:'gradle',gradle_executable:'auto',gradle_tasks:'clean :app:bootJar',artifact_glob:'app/build/libs/app.jar',steps:[{step:'git-checkout',params:{}},{step:'java-build',params:{COMMAND:'./gradlew clean :app:bootJar'}},...app.steps.filter(s=>s.step==='docker-build')]}
 app.steps[2].params.DOCKERFILE_ARTIFACT_PATH=app.artifact_glob
}
if(process.env.IMAGE_BUILD_FIXTURE_MAVEN==='1'){
 app={...app,artifact_type:'jar',build_tool:'maven',maven_executable:'mvn',maven_goals:'clean package',maven_threads:'1C',maven_arguments:'',maven_profiles:'dev',maven_properties:'maven.test.skip=true',artifact_glob:'target/app.jar',steps:[]}
}
const failedRun={id:3,service:app.name,status:'failed',branch:'main',version:'1111111111111111111111111111111111111111',actor:'fixture',stages:[{name:'拉取源码',status:'success'},{name:'Java 构建',status:'failed'},{name:'Docker 镜像构建',status:'pending'}]}
const env={name:'test',display_name:'测试',purpose:'test'}
http.createServer(async(req,res)=>{
 const u=new URL(req.url,'http://fixture'),p=u.pathname.replace(/^\/api\/plugins\/[^/]+/,'')
 const json=(v,status=200)=>{res.writeHead(status,{'Content-Type':'application/json','Cache-Control':'no-store'});res.end(JSON.stringify(v))}
 if(p==='/__fixture/status')return json({calls,writes,unknown,app,delivery})
 if(p==='/favicon.ico'){res.writeHead(204);res.end();return}
 if(p.startsWith('/api/'))calls.push(req.method+' '+p)
 let input={};if(req.method!=='GET'){try{let raw='';for await(const c of req)raw+=c;input=JSON.parse(raw||'{}')}catch{return json({error:'invalid fixture input'},400)}}
 if(p==='/api/me')return json({username:'alice',role:'admin',auth_enabled:true})
 if(p==='/api/services'&&req.method==='GET')return json([app])
 if(p==='/api/services'&&req.method==='POST'){writes.push({path:p,input});app={...input};return json(app)}
 if(p==='/api/services/summary')return json([{name:app.name,delivery,environments:[]}])
 if(p==='/api/services/'+app.name+'/delivery'){if(req.method==='PUT'){writes.push({path:p,input});delivery=input}return json(delivery)}
 if(p==='/api/environments')return json([env])
 if(req.method==='GET'&&p==='/api/pipeline/runs/3')return json(failedRun)
 if(req.method==='GET'&&p==='/api/pipeline/runs/3/log')return json({log:process.env.IMAGE_BUILD_FIXTURE_MAVEN==='1'?'[WARNING] maven.ustc.edu.cn: Name or service not known\n[ERROR] java.lang.IllegalStateException: Could not acquire lock(s)':'Downloading https://services.gradle.org/distributions/gradle-fixture-bin.zip\nCaused by: java.net.SocketTimeoutException: connect timed out'})
 if(req.method==='GET'&&p==='/api/pipeline/runs/3/capabilities')return json({can_retry:true,can_stop:false,commit:failedRun.version})
 if(p==='/api/git/branches')return json({branches:[{name:'main'},{name:'test'}],default_branch:'main'})
 if(p==='/api/steps')return json([{id:'git-checkout',name:'拉取源码',kind:'source',params:[]},{id:'java-build',name:'Java 构建',kind:'build',params:[{name:'COMMAND',label:'构建命令',type:'shell'},{name:'PRE',label:'前置命令',type:'shell'}]},{id:'docker-build',name:'Docker 镜像构建',kind:'package',params:[]}])
 if(p==='/api/pipeline/runs/page')return json({items:[],total:0,page:1,page_size:20})
 if(req.method==='GET'&&['/api/registry/applications','/api/connections','/api/variable-groups','/api/runners','/api/host-groups','/api/builds','/api/artifacts','/api/releases','/api/runs','/api/plugins','/api/pipeline/runs','/api/services/'+app.name+'/environments'].includes(p))return json([])
 const file=p==='/'?'deploy.html':p==='/deploy.html'?'deploy.html':/^\/assets\/[^/]+\.(js|css)$/.test(p)?'assets/'+path.basename(p):''
 if(file){try{const body=await readFile(path.join(root,file));res.writeHead(200,{'Content-Type':file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html'});res.end(body);return}catch{}}
 unknown.push(req.method+' '+p);return json({error:'Unsupported fixture route '+p},405)
}).listen(port,'127.0.0.1',()=>console.log('Synthetic image compilation editor: http://127.0.0.1:'+port+'/deploy.html#/svc/image-fixture/settings'))
