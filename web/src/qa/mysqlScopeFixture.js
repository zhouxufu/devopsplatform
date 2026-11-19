// Real official components with synthetic endpoints; excluded from production inputs.
import {h,ref,reactive,onMounted} from 'vue'
import {Form,Button,Card} from '@arco-design/web-vue'
import {createApp} from '../shared/app.js'
import {initTheme,setTheme} from '../shared/composables/theme.js'
import Bindings from '../deploy/components/ResourceBindingsFields.vue'
import Resources from '../deploy/pages/ResourcesView.vue'
import Workbench from '../workbench/App.vue'
import '../shared/theme.css'
initTheme();const params=new URLSearchParams(location.search);setTheme({mode:params.get('theme')||'light'})
createApp({setup(){
 const connections=ref([]),fields=ref(null),status=ref('')
 const resources=reactive({requirements:[{alias:'db',kind:'mysql'}],bindings:[{alias:'db',environment:'test',connection:'shared-instance',config:{}},{alias:'db',environment:'prod',connection:'shared-instance',config:{database:'orders_prod'}}]})
 onMounted(async()=>connections.value=await fetch('/api/resources/connections').then(r=>r.json()))
 const save=async()=>{const error=fields.value.validate();if(error){await fields.value.focusInvalid(error);status.value=error.message;return}status.value=JSON.stringify(resources.bindings)}
 return()=>params.get('page')==='workbench'?h(Workbench):params.get('page')==='resources'?h(Resources,{plugin:'dbms'}):h('main',{style:'padding:24px;max-width:1280px;margin:auto'},[
  h(Card,{title:'应用资源 · 数据库按环境选择'},()=>h(Form,{layout:'vertical',model:resources},()=>[h(Bindings,{ref:fields,resources,connections:connections.value,environments:[{name:'test',display_name:'测试'},{name:'prod',display_name:'生产'}]}),h(Button,{type:'primary',onClick:save,style:'margin-top:24px'},()=> '保存绑定（隔离验收）'),h('p',{role:'status',style:'overflow-wrap:anywhere'},status.value)]))
 ])
}}).mount('#app')
