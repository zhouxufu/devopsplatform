// Synthetic local preview entry; not included in production build inputs.
import {h,onMounted,provide,ref} from 'vue'
import {Button} from '@arco-design/web-vue'
import {createApp} from '../shared/app.js'
import Picker from '../deploy/components/WorkItemPicker.vue'
import {initTheme,setTheme} from '../shared/composables/theme.js'
import '../shared/theme.css'
initTheme()
const params=new URLSearchParams(location.search)
setTheme({mode:params.get('theme')||'light',typography:params.get('size')||'standard'})
createApp({setup(){
 const show=ref(false),ids=ref([1]),confirmed=ref('')
 provide('api',async url=>{const r=await fetch('/'+url+(url.includes('?')?'&':'?')+'scenario='+encodeURIComponent(params.get('scenario')||''));const body=await r.json();if(!r.ok)throw Error(body.error);return body})
 onMounted(()=>show.value=true)
 return()=>h('main',{style:'padding:24px'},[
  h('h2','需求与 BUG 选择 · 隔离布局验收'),h(Button,{onClick:()=>show.value=true},()=> '重新打开选择'),h('p',{role:'status'},confirmed.value),
  h(Picker,{show:show.value,ids:ids.value,me:{username:'alice',auth_enabled:true},environments:[{name:'test',display_name:'测试',purpose:'test'},{name:'prod',display_name:'生产',purpose:'production'}],onClose:()=>show.value=false,onConfirm:rows=>{ids.value=rows.map(w=>w.id);confirmed.value='确认的选择：'+ids.value.join(',');show.value=false}})
 ])
}}).mount('#app')
