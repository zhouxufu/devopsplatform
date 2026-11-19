import {h,provide,ref} from 'vue'
import {createApp} from '../shared/app.js'
import {initTheme,setTheme} from '../shared/composables/theme.js'
import Resources from '../deploy/pages/ResourcesView.vue'
import Editor from '../deploy/components/ApplicationResourcesEditor.vue'
import {Button} from '../shared/shadcn-ui.js'
initTheme();const params=new URLSearchParams(location.search);if(params.get('theme'))setTheme({mode:params.get('theme')})
const api=async(url,opt)=>{const r=await fetch('/'+url.replace(/^\//,''),opt),v=await r.json();if(!r.ok)throw Error(v.error||'Fixture failed');return v}
createApp({setup(){provide('api',api);provide('resourcePlugin','platform');const show=ref(false);return()=>h('main',{class:'min-w-0 mx-auto max-w-[1480px] p-6 space-y-4'},[h('div',{class:'flex justify-end'},h(Button,{variant:'outline',onClick:()=>show.value=true},()=> '应用资源配置验收')),h(Resources,{plugin:params.get('plugin')||'platform',agentOnly:params.get('agent')==='1'}),h(Editor,{show:show.value,initialService:'orders',services:[{name:'orders',display_name:'订单服务'}],onClose:()=>show.value=false})])}}).mount('#app')
