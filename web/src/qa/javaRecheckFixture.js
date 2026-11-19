import {h,ref} from 'vue'
import {createApp} from '../shared/app.js'
import {initTheme,setTheme} from '../shared/composables/theme.js'
import {Button} from '../shared/shadcn-ui.js'
import ReleaseRecovery from '../deploy/components/ReleaseRecovery.vue'
import InstanceManager from '../deploy/components/InstanceManager.vue'
import BuildRunDialog from '../deploy/components/BuildRunDialog.vue'
initTheme();setTheme({mode:new URLSearchParams(location.search).get('theme')||'light'})
const state=ref(await fetch('/api/state').then(r=>r.json())),show=ref(false),me={username:'fixture-admin',role:'admin'}
async function refresh(){state.value=await fetch('/api/state').then(r=>r.json())}
createApp({setup(){return()=>h('main',{class:'mx-auto max-w-[1400px] p-6 space-y-4'},[
 h('h1',{class:'text-xl font-semibold'},'原发布结果核对 · 隔离验证'),
 h(Button,{onClick:()=>{show.value=true}},()=> '构建并发布'),
 h(ReleaseRecovery,{order:state.value.order,deploys:[state.value.deploy],me,onUpdated:refresh,onRefresh:refresh}),
 h(InstanceManager,{service:'fixture',target:state.value.target,releases:[state.value.order],deploys:[state.value.deploy],me,onRefresh:refresh}),
 h(BuildRunDialog,{show:show.value,service:{name:'fixture',display_name:'测试应用',build_tool:'maven',branch:'public-dev'},delivery:{},targets:[state.value.target],releases:[state.value.order],deploys:[state.value.deploy],me,mode:'deploy',environment:'dev',onClose:()=>{show.value=false},onRefresh:refresh})
 ])}}).mount('#app')
