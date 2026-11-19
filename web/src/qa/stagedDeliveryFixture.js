import {h,provide,ref} from 'vue'
import {createApp} from '../shared/app.js'
import {initTheme,setTheme} from '../shared/composables/theme.js'
import DeliveryNodeEditor from '../deploy/components/DeliveryNodeEditor.vue'
import CodePreparationPanel from '../deploy/components/CodePreparationPanel.vue'
import SQLPlanPreview from '../deploy/components/SQLPlanPreview.vue'
initTheme();setTheme({mode:new URLSearchParams(location.search).get('theme')||'light'})
const nodes=ref([{id:'sql',name:'SQL 变更 · 订单服务',kind:'mysql',service:'orders',change_ids:[11,12]},{id:'deploy',name:'发布订单服务',kind:'deploy',service:'orders'}])
const changes=[{id:11,title:'增加订单渠道字段',kind:'mysql'},{id:12,title:'初始化历史订单渠道',kind:'mysql'}]
const preparations=ref([{id:'code',kind:'code-prepare',name:'合并并构建',application_scope:'delivery',policies:{},build_concurrency:2}])
const request={source_type:'changes',source_change_ids:[11,12],environment:'test'}
const job={id:'fixture',state:'building',lease_until:Math.floor(Date.now()/1000)+3600,build_concurrency:2,message:'构建完成 1/4，进行中 2，并发上限 2',request,builds:[{service:'orders',status:'success',run_id:1,artifact_id:8},{service:'billing',status:'running',run_id:2},{service:'accounts',status:'running',run_id:3},{service:'shipping',status:'queued',message:'等待本次交付的构建名额'}],repositories:[]}
const api=async(url)=>{if(url==='api/delivery/code-preparations/fixture')return structuredClone(job);throw Error('Fixture prohibits action: '+url)}
window.fixtureState=()=>({nodes:JSON.parse(JSON.stringify(nodes.value)),preparations:JSON.parse(JSON.stringify(preparations.value))})
createApp({setup(){provide('api',api);return()=>h('main',{class:'mx-auto max-w-[1400px] p-6 space-y-6'},[
 h('h1',{class:'text-xl font-semibold'},'交付阶段与构建 · 隔离交互验证'),
 h('section',{'aria-label':'执行顺序'},[h('h2',{class:'mb-3 font-semibold'},'执行顺序'),h(DeliveryNodeEditor,{nodes:nodes.value,apps:['orders'],changes,locked:true,compact:true})]),
 h('section',{'aria-label':'构建配置'},[h('h2',{class:'mb-3 font-semibold'},'构建配置'),h(DeliveryNodeEditor,{nodes:preparations.value,research:true,apps:['orders']})]),
 h(CodePreparationPanel,{initialId:'fixture',managed:true,canManage:false,request,apps:['orders','billing','accounts','shipping']}),
 h(SQLPlanPreview,{plans:[{node_id:'after',connection:'fixture-db',database:'delivery_qa',previous_nodes:['before'],deferred_data:true,statements:[{change_id:12,sql:'UPDATE orders SET channel = 1 WHERE id > 0',action:'update',expected_rows:-1,max_affected_rows:100}],mode:'dml'}],baselines:[]})
 ])}}).mount('#app')
