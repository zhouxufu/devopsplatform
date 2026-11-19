import {h,ref} from 'vue'
import {createApp} from '../shared/app.js'
import {initTheme,setTheme} from '../shared/composables/theme.js'
import ReleaseRecovery from '../deploy/components/ReleaseRecovery.vue'
initTheme();setTheme({mode:new URLSearchParams(location.search).get('theme')||'light'})
const now=Math.floor(Date.now()/1000)
const rows=ref(['scheduled','checking','manual'].map((state,i)=>({
 order:{id:i+1,number:i+6,service:'fixture',environment:'dev',artifact_id:21,status:'needs_review',deploy_ids:[i+1],target_snapshot:{target_type:'ssh-jar',deploy_config:{java_mode:'managed'}}},
 deploy:{id:i+1,service:'fixture',environment:'dev',host:'测试主机 '+(i+1),version:'original-commit',artifact_id:21,status:'needs_review',auto_recheck:{state,attempts:i+1,next_at:now+30,lease_until:state==='checking'?now+120:0,message:state==='manual'?'自动核对已超过 15 分钟，仍无法确认原发布结果。连接中断或检查超时。':'原进程仍未通过就绪检查'}}
})))
window.fixtureSettle=()=>{rows.value[0].order.status='success';rows.value[0].deploy.status='success'}
createApp({setup(){return()=>h('main',{class:'mx-auto max-w-[1100px] p-6 space-y-5'},[
 h('h1',{class:'text-xl font-semibold'},'原发布自动核对 · 隔离状态验证'),
 ...rows.value.map(row=>h(ReleaseRecovery,{key:row.order.id,order:row.order,deploys:[row.deploy],me:{username:'fixture',role:'admin'}}))
 ])}}).mount('#app')
