// Synthetic browser QA only. Does not access repositories, business DBs or hosts.
import http from 'node:http'
import {readFile} from 'node:fs/promises'
import path from 'node:path'
const port=Number(process.env.APPLICATION_DEFAULTS_FIXTURE_PORT||19827),root=path.resolve('web/dist')
const envs=[{name:'test',display_name:'测试',purpose:'test',sort_order:1},{name:'prod',display_name:'生产',purpose:'production',protected:true,approvers:'qa',sort_order:2}]
let app={name:'orders',artifact_type:'jar',build_tool:'maven',maven_executable:'mvn',maven_goals:'clean package',maven_threads:'1C',maven_update_snapshots:true,artifact_glob:'target/*.jar',git_url:'https://example.invalid/orders.git',branch:'main',steps:[],tags:[]}
const targets=envs.map(e=>({service:'orders',environment:e.name,display_name:e.display_name,protected:!!e.protected,target_type:'ssh-jar',remote_dir:'/opt/orders',start_cmd:'true',stop_cmd:'',host_group:e.name+'-group',hosts:[e.name+'-1'],strategy:'rolling',batch_size:1,max_parallel:1,deploy_config:{java_mode:'managed',java_version:'8',java_port:'8080',java_jvm_options:'-Xms256m\n-Xmx512m'},runtime_env_keys:['EXAMPLE']}))
if(process.env.APPLICATION_DEFAULTS_FIXTURE_JAVA_BUNDLE==='1'){
 app={...app,artifact_type:'java-bundle',build_tool:'command',build_cmd:'bash deploy/jpom/build-release.sh',artifact_glob:'build/jpom-release',steps:[{step:'git-checkout',params:{}},{step:'java-build',params:{}},{step:'package-java-bundle',params:{ROOT:'build/jpom-release',MAIN_JAR:'lib/estate-gis-service.jar',REQUIRED_PATHS:'bin/estate-gis-service.sh\ntools/changchun-traffic'}}]}
 for(const t of targets){t.target_type='ssh-java-bundle';t.remote_dir='/isolated/gis';t.deploy_config={...t.deploy_config,java_version:'21',java_bundle_jar:'lib/estate-gis-service.jar'};t.host_group='';t.hosts=[t.environment+'-1']}
}
if(process.env.APPLICATION_DEFAULTS_FIXTURE_KIND==='image')app={...app,artifact_type:'image',image_name:'fixture/orders',steps:[{step:'git-checkout'},{step:'java-build'},{step:'docker-build',params:{DOCKERFILE_SOURCE:'template',DOCKERFILE_TEMPLATE:'java21',DOCKERFILE_ARTIFACT_PATH:'target/*.jar',CONTEXT:'.',PUSH:'false'}}]}
if(process.env.APPLICATION_DEFAULTS_FIXTURE_BUILD_TOOL)app.build_tool=process.env.APPLICATION_DEFAULTS_FIXTURE_BUILD_TOOL
const policies={test:{revision:2,stages:[],defaults:{artifact_source:'existing',applications:{orders:{artifact_source:'build',code_policy:{mode:'integration',base_branch:'',target_branch:'test'}},portal:{artifact_source:'existing',code_policy:{}}}}},prod:{revision:5,stages:[],defaults:{artifact_source:'existing',applications:{orders:{artifact_source:'existing',code_policy:{mode:'isolated',base_branch:'main',target_branch:'release/{version}'}}}}}}
const calls=[],writes=[],unknown=[]
let buildReadFailure=false
const server=http.createServer(async(req,res)=>{
 const u=new URL(req.url,'http://fixture'),p=u.pathname.replace(/^\/api\/resources\//,'/api/').replace(/^\/api\/plugins\/[^/]+/,'')
 const json=(v,status=200)=>{res.writeHead(status,{'Content-Type':'application/json','Cache-Control':'no-store'});res.end(JSON.stringify(v))}
 if(p==='/__fixture/embedded'){res.writeHead(200,{'Content-Type':'text/html','Cache-Control':'no-store'});res.end('<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"></head><body style="margin:0"><iframe title="部署配置隔离验收" style="display:block;width:100%;height:100dvh;border:0" src="/deploy.html#/svc/orders/settings?settings_section=command"></iframe></body></html>');return}
 if(p==='/favicon.ico'){res.writeHead(204);res.end();return}
 if(p==='/__fixture/status')return json({calls,writes,unknown,policies,targets,app})
 if(p==='/__fixture/build'){buildReadFailure=u.searchParams.get('error')==='1';return json({buildReadFailure})}
 if(p.startsWith('/api/'))calls.push(req.method+' '+p)
 let input={};if(req.method!=='GET'){try{let raw='';for await(const chunk of req)raw+=chunk;input=JSON.parse(raw||'{}')}catch{return json({error:'invalid fixture input'},400)}}
 if(p==='/api/me')return json({username:'alice',role:'admin',auth_enabled:true})
 if(p==='/api/users')return json([{username:'alice',display_name:'开发同学',enabled:true},{username:'qa',display_name:'测试同学',enabled:true}])
 if(p==='/api/services'){if(req.method==='POST'){if(process.env.APPLICATION_DEFAULTS_FIXTURE_REJECT_SAVE==='1')return json({error:'登录已过期，请重新登录'},401);writes.push({path:p,input});app={...input};return json(app)}return json([app])}
 if(p==='/api/services/orders/build-environments'){if(buildReadFailure)return json({error:'模拟构建环境读取失败'},503);return json({environment_required:false,environments:[],documentation_environment_required:false,documentation_environments:[]})}
 if(p==='/api/pipeline/runs')return json([])
 if(p==='/api/pipeline/runs/page')return json({items:[],page:1,total:0,page_size:20})
 if(p==='/api/services/summary')return json([{name:app.name,environments:[],delivery:{}}])
 if(p==='/api/registry/applications')return json([{name:app.name,identifier:app.name,display_name:app.name,application_id:'app-fixture-orders'}])
 if(p==='/api/environments')return json(envs)
 if(p==='/api/host-groups')return json(envs.map(e=>({name:e.name+'-group',environment:e.name,hosts:[e.name+'-1']})))
 if(p==='/api/hosts')return json(envs.map(e=>({name:e.name+'-1',environment:e.name,user:'worker'})))
 if(p==='/api/services/orders/environments')return json(targets)
 if(p==='/api/services/orders/delivery'){if(req.method==='PUT')writes.push({path:p,input});return json({runner:'local',variable_groups:[],build_environment:{mode:'local'}})}
 const d=p.match(/^\/api\/services\/orders\/environments\/(test|prod)\/delivery-default$/)
 if(d){const policy=policies[d[1]];return json({build_tool:app.build_tool,revision:policy.revision,artifact_source:policy.defaults.artifact_source,value:policy.defaults.applications.orders||null})}
 const target=p.match(/^\/api\/services\/orders\/environments\/(test|prod)$/)
 if(target&&req.method==='PUT'){
  const policy=policies[target[1]],patch=input.delivery_default
  if(patch&&patch.revision!==policy.revision)return json({error:'环境策略已改变，请重新载入代码与制品策略'},409)
  writes.push({path:p,input});if(patch){if(patch.value)policy.defaults.applications.orders=patch.value;else delete policy.defaults.applications.orders;policy.revision++}
  const saved=targets.find(t=>t.environment===target[1]);Object.assign(saved,input);delete saved.delivery_default;return json(saved)
 }
 const policy=p.match(/^\/api\/environments\/(test|prod)\/delivery-policy$/)
 if(policy)return json(policies[policy[1]])
 if(p==='/api/git/branches')return json({branches:[{name:'main'},{name:'test'},{name:'develop'}],default_branch:'main'})
 if(p==='/api/delivery/templates')return json([])
 if(['/api/builds','/api/artifacts','/api/releases','/api/deploys','/api/runs','/api/connections','/api/variable-groups','/api/runners','/api/steps','/api/plugins','/api/delivery/runs','/api/audit'].includes(p))return json([])
 if(p.startsWith('/api/releases/page')||p.startsWith('/api/build-records'))return json({items:[],page:1,total:0,page_size:20})
 let file=p==='/'?'deploy.html':p==='/deploy.html'?'deploy.html':/^\/assets\/[^/]+\.(js|css)$/.test(p)?'assets/'+path.basename(p):''
 if(file&&req.method==='GET'){try{let body=await readFile(path.join(root,file));if(file.endsWith('.html')&&process.env.APPLICATION_DEFAULTS_FIXTURE_THEME==='dark')body=Buffer.from(body.toString().replace('<head>','<head><script>localStorage.setItem("lp-theme","dark")</script>'));res.writeHead(200,{'Content-Type':file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html'});res.end(body);return}catch{}}
 unknown.push(req.method+' '+p);return json({error:'Unknown fixture route '+p},404)
})
server.listen(port,'127.0.0.1',()=>console.log('Application defaults fixture: http://127.0.0.1:'+port+'/deploy.html#/svc/orders/environment'))
