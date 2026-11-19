// Isolated navigation acceptance: real shell/workspaces, synthetic read-only APIs.
import {createServer} from '../web/node_modules/vite/dist/node/index.js'
import {readFile} from 'node:fs/promises'
import path from 'node:path'
const calls=[],rejected=[],unknown=[],fixturePort=Number(process.env.NAVIGATION_FIXTURE_PORT||19979)
const service={name:'orders',application_id:'navigation-orders',artifact_type:'jar',git_url:'https://example.invalid/orders.git',branch:'main',build_tool:'maven',maven_goals:'clean package',artifact_glob:'target/*.jar',steps:[],tags:['测试']}
const environment={name:'test',display_name:'测试环境',purpose:'testing',protected:false}
const run={id:11,service:'orders',status:'success',branch:'main',version:'abc1234',actor:'alice',started_at:1792630000,ended_at:1792630010,stages:[],artifact_id:41}
const artifact={id:41,service:'orders',type:'jar',version:'abc1234',digest:'sha256:fixture',build_id:11,created_at:1792630010}
const change={id:9,kind:'code',title:'菜单跳转修复',service:'orders',owner:'alice',status:'ready',reference:'main',created_at:1792630000,work_items:[],run_id:11,artifact_id:41}
const plugins=['deploy','research','api-center','dbms','redis','logs','monitor'].map(name=>({name,display:({'api-center':'接口中心',deploy:'应用与构建',research:'工作项',dbms:'数据库工作台',redis:'Redis 工作台',logs:'日志',monitor:'监控'})[name],url:'/api/plugins/'+name+'/ui'}))
const pageNames={deploy:'deploy',research:'research','api-center':'api-center',dbms:'workbench',redis:'workbench',logs:'logs',monitor:'monitor'}
let vite
const handler=async(req,res,next)=>{
 const u=new URL(req.url,'http://fixture'),original=u.pathname,p=original.replace(/^\/api\/plugins\/[^/]+/,'').replace(/^\/api\/resources\//,'/api/').replace(/^\/api\/workbench\/(?:dbms|redis)\//,'/api/')
 const json=(value,status=200)=>{res.statusCode=status;res.setHeader('Content-Type','application/json');res.setHeader('Cache-Control','no-store');res.end(JSON.stringify(value))}
 if(original==='/__fixture/status')return json({calls,rejected,unknown})
 const match=original.match(/^\/api\/plugins\/([^/]+)\/ui$/)
 if(match&&pageNames[match[1]]){const html=(await readFile('web/'+pageNames[match[1]]+'.html','utf8')).replace('<head>','<head><base href="/api/plugins/'+match[1]+'/">');res.setHeader('Content-Type','text/html');return res.end(await vite.transformIndexHtml(req.url,html))}
 if(!original.startsWith('/api/'))return next()
 calls.push(req.method+' '+original+u.search)
 if(req.method!=='GET'){rejected.push(req.method+' '+original);return json({error:'导航夹具拒绝写入'},405)}
 if(p==='/api/me')return json({username:'alice',display_name:'导航验收',role:'admin',auth_enabled:false})
 if(p==='/api/plugins')return json(plugins)
 if(p==='/api/setup')return json({completed:true,environments:[environment]})
 if(p==='/api/environments')return json(original.startsWith('/api/plugins/monitor/')?['test']:[environment])
 if(original.startsWith('/api/plugins/monitor/')&&p==='/api/services')return json(['orders'])
 if(original.startsWith('/api/plugins/logs/')&&p==='/api/services')return json({orders:{}})
 if(p==='/api/services'||p==='/api/registry/services'||p==='/api/registry/applications')return json([service])
 if(p==='/api/services/summary')return json([{name:'orders',latest_run:run,latest_artifact:artifact,environments:[],delivery:{runner:'local',variable_groups:[],build_environment:{mode:'local'}}}])
 if(p==='/api/changes/page')return json({items:u.searchParams.get('q')&&!change.title.includes(u.searchParams.get('q'))?[]:[change],total:1,page:1,page_size:20})
 if(p==='/api/changes/owners')return json(['alice'])
 if(p==='/api/changes/9')return json(change)
 if(p==='/api/changes')return json([change])
 if(p==='/api/pipeline/runs/page')return json({items:[run],total:1,page:1,page_size:20})
 if(p==='/api/pipeline/runs/11')return json(run)
 if(p==='/api/pipeline/runs/11/log')return json({log:'合成构建已结束，不执行实际构建。'})
 if(p==='/api/pipeline/runs/11/capabilities')return json({can_retry:false,can_stop:false})
 if(p==='/api/pipeline/runs')return json([run])
 if(p==='/api/artifacts')return json([artifact])
 if(p==='/api/artifacts/41')return json(artifact)
 if(p==='/api/services/orders/delivery')return json({runner:'local',variable_groups:[],build_environment:{mode:'local'}})
 if(p==='/api/runners')return json([{name:'local',kind:'local',enabled:true}])
 if(p==='/api/services/orders/resources')return json({requirements:[]})
 if(p==='/api/work-items/page'||p==='/api/release-versions/page'||p==='/api/delivery/runs/page')return json({items:[],total:0,page:1,page_size:20})
 if(p==='/api/research/overview')return json({actor:'alice',aliases:['alice'],connections:[]})
 if(p.startsWith('/api/research/inbox'))return json({items:[],total:0,page:1,page_size:20})
 if(p==='/api/log-sources')return json([])
 if(p==='/api/otlp/services')return json([])
 if(p==='/api/alerts/status')return json({})
 if(p==='/api/metrics')return json({points:[]})
 if(['/api/alert-notifications','/api/machines','/api/otlp/names'].includes(p))return json([])
 if(p==='/api/overview')return json({services:[],agents:[],events:0})
 if(p==='/api/api-documents/snapshots')return json([])
 if(p==='/api/api-testing/context')return json({environments:[],targets:[],variables:[],enabled:true})
 if(p==='/api/api-testing/cases'||p==='/api/api-testing/runs')return json([])
 if(p==='/api/policy')return json({})
 if(['/api/data-sync/tasks','/api/query-requests','/api/delivery/templates','/api/delivery/runs/owners'].includes(p))return json([])
 const empty=['/api/users','/api/registry/deployments','/api/work-items','/api/work-item-sync','/api/work-item-sources','/api/release-versions','/api/delivery/runs','/api/services/orders/environments','/api/host-groups','/api/connections','/api/variable-groups','/api/steps','/api/builds','/api/deploys','/api/releases','/api/alerts']
 if(empty.includes(p))return json([])
 unknown.push(original);return json([])
}
vite=await createServer({root:path.resolve('web'),configFile:path.resolve('web/vite.config.js'),plugins:[{name:'navigation-fixture',configureServer(server){server.middlewares.use(handler)}}],server:{host:'127.0.0.1',port:fixturePort,strictPort:true}})
await vite.listen();console.log('Read-only navigation fixture http://127.0.0.1:'+fixturePort+'/shell.html')
