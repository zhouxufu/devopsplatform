// Synthetic source-scope acceptance only; excluded from production entries.
import {h,onMounted,provide,ref} from 'vue'
import {createApp} from '../shared/app.js'
import {Button} from '../shared/shadcn-ui.js'
import Picker from '../deploy/components/WorkItemPicker.vue'
import {initTheme,setTheme} from '../shared/composables/theme.js'
initTheme();setTheme({mode:new URLSearchParams(location.search).get('theme')||'light'})
createApp({setup(){
 const show=ref(false)
 provide('api',async(url,opt)=>{const r=await fetch('/'+url,opt),body=await r.json();if(!r.ok)throw Error(body.error);return body})
 onMounted(()=>show.value=true)
 return()=>h('main',{class:'w-full min-w-0 p-6'},[h(Button,{onClick:()=>show.value=true},()=> '选择工作项'),h(Picker,{show:show.value,me:{username:'alice',auth_enabled:true},environments:[],onClose:()=>show.value=false,onConfirm:()=>show.value=false})])
}}).mount('#app')
