// Disposable visual fixture; no source credentials, remote requests or writes.
import {createServer} from '../web/node_modules/vite/dist/node/index.js'
import path from 'node:path'
let vite
vite=await createServer({root:path.resolve('web'),configFile:path.resolve('web/vite.config.js'),plugins:[{name:'activity-reconciliation-fixture',configureServer(server){server.middlewares.use(async(req,res,next)=>{
 if(req.url==='/__activity-fixture'){
  res.setHeader('Content-Type','text/html');return res.end(await vite.transformIndexHtml(req.url,'<html lang="zh-CN"><head><meta charset="UTF-8"><title>历史记录合并验收</title></head><body><div id="app"></div><script type="module" src="/__activity-fixture.js"></script></body></html>'))
 }
 if(req.url==='/__activity-fixture.js'){
  res.setHeader('Content-Type','text/javascript');return res.end(`import {createApp} from '/src/shared/app.js';import {h,ref} from '/node_modules/.vite/deps/vue.js';import Activity from '/src/research/components/ActivityTimeline.vue';import {Button} from '/node_modules/.vite/deps/@arco-design_web-vue.js';import {initTheme} from '/src/shared/composables/theme.js';import '/src/shared/theme.css';initTheme();
const first='a'.repeat(32),second='b'.repeat(32);
const local=id=>({id:id===first?1:2,operation_id:id,source_actor:'source-user',actor:'platform-account',kind:'assignTo',content:'后端权限，请处理',at:1792713469});
const source=id=>({actor:'source-user',action:'assigned',comment:'<p>后端权限，请处理</p><br>[delivery-operation:'+id+']',date:'2026-10-23 07:57:48',history:[{field:'assignedTo',old:'qa',new:'dev'}]});
createApp({setup(){const repeated=ref(false),available=ref(true);return()=>h('main',{style:'max-width:1080px;margin:24px auto;background:var(--panel);padding:24px'},[h('h2','历史记录合并验收（合成数据）'),h('div',{style:'display:flex;gap:12px'},[h(Button,{onClick:()=>repeated.value=!repeated.value},()=>repeated.value?'恢复一次保存':'模拟第二次相同保存'),h(Button,{onClick:()=>available.value=!available.value},()=>available.value?'模拟来源不可用':'恢复来源')]),h(Activity,{activities:repeated.value?[local(first),local(second)]:[local(first)],remote:available.value?{users:{'source-user':'来源姓名'},actions:{1:{actor:'source-user',action:'opened',comment:'',date:'2026-10-16 11:07:53'},2:source(first),...(repeated.value?{3:source(second)}:{})}}:null})])}}).mount('#app')`)
 }
 next()
})}}],server:{host:'127.0.0.1',port:19979,strictPort:true}})
await vite.listen();console.log('Activity fixture http://127.0.0.1:19979/__activity-fixture')
