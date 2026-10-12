<script setup>
import {ref,watch,onBeforeUnmount,nextTick,computed} from 'vue'
import {useRoute,useRouter} from 'vue-router'
import {ElPopover} from 'element-plus'
import {ElButton,ElInput} from '../../shared/components/ui.js'
import Sel from '../../shared/components/Sel.vue'
import DateRangeFilter from '../../shared/components/DateRangeFilter.vue'
import Stages from '../../shared/components/Stages.vue'
import {buildState,buildScroll} from '../buildNavigation.js'
const props=defineProps({service:String,kind:{type:String,default:'runs'},environments:{type:Array,default:()=>[]},me:{type:Object,default:()=>({})}})
const emit=defineEmits(['open','publish','action'])
const route=useRoute(),router=useRouter()
const filters=buildState(props.service+':'+props.kind,{q:'',status:'',branch:'',actor:'',environment:'',start:'',end:'',page:1,page_size:20})
const page=ref({items:[],total:0,page:1,page_size:20}),loading=ref(false),error=ref(''),peek=ref(null)
const scroll=buildScroll(props.service+':'+props.kind)
const now=ref(Date.now()),clock=setInterval(()=>now.value=Date.now(),1000)
const canManage=r=>r.actor===props.me.username||props.me.role==='admin'||props.me.username==='local'
let seq=0,timer,debounce,initial=true
const endpoint=computed(()=>({runs:'pipeline/runs',artifacts:'artifacts',releases:'release-orders'}[props.kind]))
const statuses=[{value:'success',label:'成功'},{value:'failed',label:'失败'},{value:'running',label:'运行中'},{value:'canceled',label:'已取消'},{value:'interrupted',label:'已中断'},{value:'pending_approval',label:'待审批'},{value:'waiting_next_batch',label:'批次暂停'},{value:'waiting_deploy',label:'待部署'}]
const status=value=>statuses.find(s=>s.value===value)?.label||value
const date=value=>value?new Date(value*1000).toLocaleString('zh-CN',{hour12:false}):'—'
const duration=r=>r.ended_at?Math.max(0,r.ended_at-r.started_at)+'s':'运行中'
const stage=r=>r.stages?.find(s=>['failed','running','waiting'].includes(s.status))?.name||((r.stages||[]).filter(s=>s.status==='success').length+' / '+(r.stages||[]).length)
const canApprove=r=>r.actor!==props.me.username&&String(r.target_snapshot?.approvers||props.environments.find(e=>e.environment===r.environment)?.approvers||'').split(/[,;\s]+/).includes(props.me.username)
const pages=computed(()=>Math.max(1,Math.ceil(page.value.total/filters.value.page_size)))
async function load(quiet=false){const request=++seq;clearTimeout(timer);if(!quiet)loading.value=true;error.value=''
 try{const q=new URLSearchParams({service:props.service});for(const [k,v] of Object.entries(filters.value))if(v!==''&&v!=null)q.set(k,['start','end'].includes(k)?String(Math.floor(new Date(v).getTime()/1000)):String(v))
  const r=await fetch('api/'+endpoint.value+'/page?'+q),v=await r.json();if(!r.ok)throw Error(v.error||'记录读取失败');if(!Array.isArray(v.items)||!Number.isInteger(v.total))throw Error('分页响应无效，请重试');if(request!==seq)return;page.value=v
  if(initial){initial=false;await scroll.restore()}
  if(v.items.some(r=>['running','queued','rolling_back'].includes(r.status)))timer=setTimeout(()=>load(true),3000)
 }catch(e){if(request===seq)error.value=e.message}finally{if(request===seq)loading.value=false}
}
function reset(){filters.value.page=1}
watch(filters,()=>{seq++;loading.value=true;clearTimeout(timer);clearTimeout(debounce);debounce=setTimeout(load,200)},{deep:true})
watch(()=>props.service,()=>load(),{immediate:true})
onBeforeUnmount(()=>{seq++;clearTimeout(timer);clearTimeout(debounce);clearInterval(clock)})
function open(r){if(loading.value)return;scroll.remember();if(props.kind==='runs')router.push({path:'/run/'+r.id,query:{return:route.fullPath}});else if(props.kind==='artifacts')peek.value=r;else emit('open',r)}
function publish(a){if(loading.value)return;scroll.remember();emit('publish',a)}
function source(r){if(loading.value)return;scroll.remember();router.push({path:'/run/'+r.source_id,query:{return:route.fullPath}})}
function action(r,a){if(loading.value)return;emit('action',r,a)}
</script>
<template>
 <section class="build-records">
  <div class="record-filters">
   <ElInput v-model="filters.q" clearable :placeholder="kind==='artifacts'?'搜索代码版本、分支或摘要':'搜索代码版本、分支或人员'" @input="reset"/>
   <Sel v-if="kind!=='artifacts'" v-model="filters.status" :options="statuses" all-label="全部状态" @change="reset"/>
   <ElInput v-if="kind==='runs'" v-model="filters.branch" clearable placeholder="分支（准确名称）" class="narrow" @input="reset"/>
   <ElInput v-if="kind!=='artifacts'" v-model="filters.actor" clearable placeholder="发起人" class="narrow" @input="reset"/>
   <Sel v-if="kind==='releases'" v-model="filters.environment" :options="environments.map(e=>({value:e.environment||e.name,label:e.display_name||e.name}))" all-label="全部环境" @change="reset"/>
   <DateRangeFilter v-model:start="filters.start" v-model:end="filters.end" label="时间" width="320px" @change="reset"/>
   <span v-if="loading" role="status">正在读取…</span>
  </div>
  <p v-if="error" role="alert" class="record-error">{{error}} <ElButton size="small" @click="load()">重试</ElButton></p>
  <div class="record-scroll" :aria-busy="loading"><table>
   <thead><tr><th>编号 / 代码版本</th><th v-if="kind==='runs'">分支 / 阶段</th><template v-if="kind==='artifacts'"><th>来源构建</th><th>分支</th><th>曾发布环境</th></template><th v-if="kind==='releases'">环境 / 批次</th><th>{{kind==='artifacts'?'类型':'状态'}}</th><th v-if="kind!=='artifacts'">发起人{{kind==='releases'?' / 审批人':''}}</th><th v-if="kind==='runs'">耗时</th><th>时间</th><th>操作</th></tr></thead>
   <tbody><tr v-for="r in page.items" :key="r.id">
    <td><button class="op-btn" @click="open(r)">#{{r.id}} · {{r.version||'待解析 Commit'}}</button><small v-if="kind==='runs'&&r.artifact_id">制品 #{{r.artifact_id}}</small></td>
    <td v-if="kind==='runs'"><span>{{r.branch||'仓库默认分支'}}</span><small><ElPopover trigger="click" placement="bottom" :width="400"><template #reference><button class="op-btn" :aria-label="'查看运行 '+r.id+' 阶段'">{{stage(r)}}</button></template><Stages :stages="r.stages||[]"/></ElPopover></small></td>
    <td v-if="kind==='artifacts'"><button v-if="r.source==='pipeline-run'" class="op-btn" :disabled="loading" @click="source(r)">构建 #{{r.source_id}}</button><span v-else>{{r.source||'历史制品'}} #{{r.source_id}}</span></td><template v-if="kind==='artifacts'"><td>{{r.metadata?.branch||'未记录'}}</td><td>{{r.published_environments?.length?r.published_environments.join(' / '):'尚无成功发布记录'}}</td></template>
    <td v-if="kind==='releases'">{{r.environment}}<small>{{r.succeeded}} 成功 / {{r.failed}} 失败 / {{r.total}} 台</small></td>
    <td :class="'record-status '+r.status">{{kind==='artifacts'?r.kind:status(r.status)}}</td>
    <td v-if="kind!=='artifacts'">{{r.actor||'local'}}<small v-if="r.approver">{{r.approver}}</small></td>
    <td v-if="kind==='runs'">{{duration(r)}}</td><td>{{date(r.created_at||r.started_at)}}</td>
    <td class="record-actions"><button class="op-btn" @click="open(r)">详情</button><button v-if="kind==='artifacts'" class="op-btn" @click="publish(r)">发布此制品</button><template v-if="kind==='releases'"><button v-if="r.status==='pending_approval'&&canApprove(r)" class="op-btn" @click="action(r,'approve')">批准</button><button v-if="r.status==='pending_approval'&&canApprove(r)" class="op-btn" @click="action(r,'reject')">拒绝</button><button v-if="['pending_approval','queued','waiting_next_batch'].includes(r.status)&&canManage(r)&&!r.recovery" class="op-btn" @click="action(r,'cancel')">取消</button><button v-if="r.status==='waiting_next_batch'&&canManage(r)" class="op-btn" :disabled="r.continue_after*1000>now" @click="action(r,'continue')">{{r.continue_after*1000>now?'观察中（'+Math.ceil((r.continue_after*1000-now)/1000)+'s）':r.recovery?'继续回退批次':'继续下一批'}}</button><button v-if="['success','waiting_next_batch','failed','canceled'].includes(r.status)&&canManage(r)&&!r.recovery" class="op-btn" @click="action(r,'rollback')">回退</button></template></td>
   </tr><tr v-if="!page.items.length"><td :colspan="kind==='runs'?7:kind==='artifacts'?7:6" class="record-empty">{{loading?'正在读取…':error?'暂时无法读取记录':'没有匹配记录，可调整筛选条件'}}</td></tr></tbody>
  </table></div>
  <footer><span>共 {{page.total}} 条</span><Sel v-model="filters.page_size" :options="[20,50,100].map(value=>({value,label:value+' 条 / 页'}))" @change="reset"/><ElButton size="small" :disabled="loading||filters.page<=1" @click="filters.page--">上一页</ElButton><span>{{filters.page}} / {{pages}}</span><ElButton size="small" :disabled="loading||filters.page>=pages" @click="filters.page++">下一页</ElButton></footer>
  <aside v-if="peek" class="artifact-peek" aria-label="制品详情"><div><h3>制品 #{{peek.id}} · {{peek.version}}</h3><ElButton size="small" @click="peek=null">关闭</ElButton></div><p>代码版本 / 分支：{{peek.version}} / {{peek.metadata?.branch||'未记录'}}</p><p>类型：{{peek.kind}} · 大小：{{peek.size||0}} 字节</p><p>摘要</p><code>{{peek.digest}}</code><p>存储位置</p><code>{{peek.uri}}</code><ElButton type="primary" @click="publish(peek)">发布此制品</ElButton></aside>
 </section>
</template>
<style scoped>
.record-filters{display:flex;align-items:center;gap:8px;flex-wrap:wrap;padding:10px 0}.record-filters>.el-input{flex:1;min-width:160px;max-width:320px}.record-filters>.narrow{max-width:155px}.record-scroll{overflow:auto;border:1px solid var(--border);border-radius:8px}table{border-collapse:collapse;width:100%;font-size:12px;min-width:820px}th,td{padding:11px 12px;border-bottom:1px solid var(--border);text-align:left}th{background:var(--panel2);color:var(--muted)}small{display:block;color:var(--muted);font-size:11px;margin-top:4px}.record-actions{white-space:nowrap}.record-actions button{margin-right:10px}footer{display:flex;align-items:center;gap:8px;justify-content:flex-end;margin-top:12px;font-size:12px}footer>span:first-child{margin-right:auto}.record-empty{text-align:center;padding:36px;color:var(--muted)}.record-error,.record-status.failed{color:var(--error)}.record-status.success{color:var(--ok)}.record-status.running{color:var(--brand)}.artifact-peek{position:fixed;inset:0 0 0 auto;width:min(560px,94vw);box-sizing:border-box;overflow:auto;background:var(--panel);z-index:100;padding:24px;border-left:1px solid var(--border);box-shadow:var(--shadow-float)}.artifact-peek>div{display:flex;justify-content:space-between;align-items:center}.artifact-peek code{display:block;overflow-wrap:anywhere;white-space:pre-wrap;margin-bottom:16px}.artifact-peek p{font-size:13px}
</style>
