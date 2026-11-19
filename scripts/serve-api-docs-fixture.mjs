// Controlled in-memory UI fixture: no real builds, remote requests or deployments.
import {createServer} from '../web/node_modules/vite/dist/node/index.js'
import path from 'node:path'
const port=Number(process.env.API_DOCS_FIXTURE_PORT||19976)
const doc={openapi:'3.0.3',info:{title:'交易服务接口',version:'v1.4.0'},paths:{'/api/orders':{post:{summary:'创建订房订单',tags:['订单'],requestBody:{content:{'application/json':{schema:{$ref:'#/components/schemas/Order'}}}},responses:{'201':{description:'订单创建成功',content:{'application/json':{schema:{$ref:'#/components/schemas/Order'},example:{orderId:'DEMO-001'}}}}}}},'/api/events':{get:{summary:'订房处理事件',tags:['订单'],responses:{'200':{description:'订单处理事件流',content:{'text/event-stream':{schema:{type:'object',properties:{event:{type:'string',description:'事件类型'},orderId:{type:'string',description:'订单编号'}}},example:{event:'completed',orderId:'DEMO-001'}}}}}}}},components:{schemas:{Order:{type:'object',required:['houseId'],properties:{houseId:{type:'integer',description:'房源编号'},customer:{type:'object',properties:{id:{type:'integer',description:'客户编号'},name:{type:'string',description:'客户名称'}}},amount:{type:'number',description:'订金金额'}}}}}}
const settingsFields=[{key:'config_file',label:'仓库内配置文件',required:true,default:'src/main/resources/smart-doc.json'},{key:'output_file',label:'生成的 OpenAPI 文件',required:true,default:'target/api-docs/openapi.json',help:'需与 smart-doc outPath 一致'},{key:'plugin_version',label:'项目使用的 Maven 插件版本',required:true}]
let enabled=true,source={provider:'smart-doc',revision:1,on_build:false,required:false,settings:{config_file:'src/main/resources/smart-doc.json',output_file:'target/api-docs/openapi.json',plugin_version:'2.7.3'}}
const providers=()=>[{id:'smart-doc',name:'Smart-doc 接口文档',version:'1.0.0',state:enabled?'running':'stopped',nodes:enabled?[{version:1,key:'api-documentation',name:'生成接口文档',executor:'api.document.generate.v1',configuration:'application-binding'}]:[]}]
const calls=[]
let vite
const fixtureHandler=async(req,res,next)=>{
 const u=new URL(req.url,'http://fixture'),p=u.pathname
 if(p==='/__workflow-node') {const html=await vite.transformIndexHtml(req.url,'<html lang="zh-CN"><head><meta charset="UTF-8"><title>编排插件节点验收</title></head><body><div id="app"></div><script type="module" src="/__workflow-node.js"></script></body></html>');res.setHeader('Content-Type','text/html');return res.end(html)}
 if(p==='/__workflow-node.js'){res.setHeader('Content-Type','text/javascript');return res.end(`import {createApp} from '/src/shared/app.js';import {h,ref} from '/node_modules/.vite/deps/vue.js';import Editor from '/src/deploy/components/DeliveryNodeEditor.vue';import {Button} from '/node_modules/.vite/deps/@arco-design_web-vue.js';import {initTheme} from '/src/shared/composables/theme.js';import '/src/shared/theme.css';initTheme();createApp({setup(){const nodes=ref([{id:'node1',name:'发布应用',kind:'deploy',service:'orders'}]);return()=>h('main',{style:'max-width:1000px;margin:24px auto;background:var(--panel);padding:24px'},[h('h2','编排插件节点验收'),h(Button,{onClick:async()=>{await fetch('/fixture/toggle',{method:'POST'})}},()=> '切换插件启停（夹具）'),h(Editor,{nodes:nodes.value,apps:[{value:'orders',label:'交易服务'}],users:[],changes:[]}),h('pre',JSON.stringify(nodes.value,null,2))])}}).mount('#app')`)}
 if(p.startsWith('/api/')||p.startsWith('/fixture/')){
  const chunks=[];for await(const chunk of req)chunks.push(chunk);let input={};try{input=JSON.parse(Buffer.concat(chunks).toString()||'{}')}catch{}
  res.setHeader('Content-Type','application/json');const json=v=>res.end(JSON.stringify(v));calls.push([req.method,p]);
  if(p==='/fixture/toggle'){enabled=!enabled;return json({enabled})}
  if(p==='/fixture/state')return json({enabled,calls,source})
  if(p==='/api/workflow-node-providers')return json(providers())
  if(p==='/api/me')return json({username:'fixture-admin',role:'admin',auth_enabled:false})
  if(p==='/api/services')return json([{name:'orders',application_id:'app-orders',build_tool:'maven'},{name:'payments',application_id:'app-payments',build_tool:'maven'}])
  if(p==='/api/hosts')return json([])
  if(p==='/api/extensions')return json([{manifest:{id:'smart-doc',name:'Smart-doc 接口文档',version:'1.0.0',capabilities:['api.document.generate.v1','workflow.node.v1']},state:enabled?'running':'stopped'}])
  if(p==='/api/extensions/smart-doc/invoke')return json({data:{tools:['maven'],fields:settingsFields}})
  if(p==='/api/api-documents/source'){if(req.method==='PUT'){if(input.revision!==source.revision){res.statusCode=409;return json({error:'配置已变化'})};source={...input,revision:input.revision+1}}return json(source)}
  if(p==='/api/api-documents')return json(u.searchParams.get('service')==='orders'?[{id:1,application_id:'app-orders',service:'orders',run_id:138,artifact_id:41,artifact_digest:'sha256:fixture-artifact',commit:'a'.repeat(40),provider:'smart-doc',provider_version:'1.0.0',created_at:Math.floor(Date.now()/1000)}]:[])
  if(p==='/api/api-documents/1'){const operations=Object.entries(doc.paths).flatMap(([path,methods])=>Object.entries(methods).map(([method,definition])=>({id:method+' '+path,path,method:method.toUpperCase(),summary:definition.summary,tags:definition.tags,sse:path.includes('events'),definition})));return json({id:1,info:doc.info,operations,document:doc,digest:'sha256:fixture-document'})}
  res.statusCode=404;return json({error:'fixture: unexpected endpoint '+p})
 }
 next()
}
vite=await createServer({root:path.resolve('web'),configFile:path.resolve('web/vite.config.js'),plugins:[{name:'api-docs-fixture',configureServer(server){server.middlewares.use(fixtureHandler)}}],server:{host:'127.0.0.1',port,strictPort:true}})
await vite.listen();console.log(`API documents: http://127.0.0.1:${port}/api-center.html#/documents?service=orders`);console.log(`Workflow nodes: http://127.0.0.1:${port}/__workflow-node`)
