<script setup>
import DateRangeFilter from '../../shared/components/DateRangeFilter.vue'
import { ElPopover } from 'element-plus'
import {computed,inject,nextTick,onBeforeUnmount,ref,watch} from 'vue'
import Modal from '../../shared/components/Modal.vue'
import Sel from '../../shared/components/Sel.vue'
import HelpTip from '../../shared/components/HelpTip.vue'
import RichDescription from './RichDescription.vue'
import {ElButton,ElInput} from '../../shared/components/ui.js'
const props=defineProps({show:Boolean,ids:{type:Array,default:()=>[]},seed:{type:Array,default:()=>[]},me:{type:Object,default:()=>({})},environments:{type:Array,default:()=>[]},versionID:{type:Number,default:0}})
const emit=defineEmits(['close','confirm']),api=inject('api')
const limitNotice=ref('')
const showNumbers=ref(false)
const filters=ref({q:'',kind:'bug',status:'closed',source:'',person:'',person_role:'resolved_by',environment:'',delivery:'',planning:'',start:'',end:'',numbers:''})
const items=ref([]),people=ref([]),sources=ref([]),page=ref(1),size=ref(20),total=ref(0),loading=ref(false),hydrating=ref(false),error=ref(''),optionsError=ref(''),selectionError=ref(''),selected=ref(new Map()),peek=ref(null),peekLoading=ref(false),numberInput=ref('')
let session=0,sequence=0,peekSequence=0,selectionSequence=0,optionsSequence=0,timer,initialized=false
const chosen=computed(()=>[...selected.value.values()])
const allOnPage=computed(()=>items.value.length>0&&items.value.every(i=>selected.value.has(i.id)))
const canMine=computed(()=>props.me.username&&props.me.username!=='local'&&props.me.auth_enabled!==false)
const canConfirm=computed(()=>!hydrating.value&&!selectionError.value&&chosen.value.length<=500&&chosen.value.every(w=>w.checked&&!w.missing))
const statusLabel=w=>({closed:'已关闭',resolved:'已解决',active:'激活',open:'进行中'}[w.external_status||w.status]||w.status)
const pages=computed(()=>Math.max(1,Math.ceil(total.value/size.value)))
const date=n=>n?new Date(n*1000).toLocaleDateString('zh-CN'):'—'
const roles=[{value:'resolved_by',label:'由谁解决'},{value:'related',label:'相关人员'},{value:'assigned_to',label:'指派给'},{value:'opened_by',label:'由谁创建'},{value:'closed_by',label:'由谁关闭'},{value:'owner',label:'自定义创建人'}]
async function loadPage(){
 const generation=session,request=++sequence;loading.value=true;error.value='';items.value=[]
 const q=new URLSearchParams({page:String(page.value),page_size:String(size.value),version_id:String(props.versionID)})
 for(const [k,v] of Object.entries(filters.value))if(v)q.set(k,['start','end'].includes(k)?String(Math.floor(new Date(v+(k==='start'?'T00:00:00':'T23:59:59')).getTime()/1000)):v)
 try{const data=await api('api/work-items/candidates?'+q);if(generation!==session||request!==sequence||!props.show)return;if(!Array.isArray(data.items)||!Number.isInteger(data.total)||data.total<0||!Number.isInteger(data.page)||data.page<1)throw Error('候选列表响应无效');items.value=data.items;total.value=data.total;page.value=data.page}
 catch(e){if(generation===session&&request===sequence&&props.show){error.value=e.message;total.value=0}}
 finally{if(generation===session&&request===sequence)loading.value=false}
}
async function hydrate(){
 const generation=session,request=++selectionSequence;hydrating.value=true;selectionError.value=''
 const ids=[...selected.value.keys()]
 try{
  if(ids.length){const data=await api('api/work-items/candidates/selected?'+new URLSearchParams({ids:ids.join(','),environment:filters.value.environment,version_id:String(props.versionID)}));if(generation!==session||request!==selectionSequence||!props.show)return;const map=new Map(data.items.map(w=>[w.id,{...w,checked:true}]));for(const id of ids)if(selected.value.has(id))selected.value.set(id,map.get(id)||{id,title:'记录不存在，请移除 #'+id,missing:true,checked:true})}
 }catch(e){if(generation===session&&request===selectionSequence)selectionError.value='已选记录校验失败：'+e.message}
 finally{if(generation===session&&request===selectionSequence)hydrating.value=false}
}
function toggle(w){limitNotice.value='';if(selected.value.has(w.id)){selected.value.delete(w.id);selectionError.value=''}else if(selected.value.size<500)selected.value.set(w.id,{...w,checked:true});else limitNotice.value='最多选择 500 项，当前选择未改变，请先移除部分记录'}
function togglePage(){limitNotice.value='';if(allOnPage.value){for(const w of items.value)selected.value.delete(w.id)}else{const added=items.value.filter(w=>!selected.value.has(w.id));if(selected.value.size+added.length>500){limitNotice.value='选择本页将超过 500 项，未改变当前选择；可逐项选择或先移除部分记录';return}for(const w of added)selected.value.set(w.id,{...w,checked:true})}}
function preset(kind){filters.value.kind=kind==='requirements'?'requirement':'bug';filters.value.status=kind==='requirements'?'':'closed';filters.value.delivery=filters.value.environment?'not_recorded':'';filters.value.planning=''}
function mine(){filters.value.person_role='related';filters.value.person=filters.value.person===props.me.username?'':props.me.username}
function go(n){page.value=n;loadPage()}
function applyNumbers(){if(!filters.value.source||filters.value.source==='manual'){error.value='请先选择来源平台，再筛选外部编号';return}filters.value.numbers=numberInput.value;showNumbers.value=false}
async function preview(w){
 const generation=session,request=++peekSequence;peek.value={...w};peekLoading.value=true
 try{const full=await api('api/work-items/'+w.id);if(generation===session&&request===peekSequence&&props.show)peek.value=full}catch(e){if(generation===session&&request===peekSequence)peek.value={...w,description:'详情读取失败：'+e.message}}finally{if(generation===session&&request===peekSequence)peekLoading.value=false}
}
function confirm(){if(canConfirm.value)emit('confirm',chosen.value)}
async function loadOptions(){
 const generation=session,request=++optionsSequence;optionsError.value=''
 try{const [p,s]=await Promise.all([api('api/work-items/people'),api('api/work-items/candidate-sources')]);if(generation!==session||request!==optionsSequence||!props.show)return;if(!Array.isArray(p)||!Array.isArray(s))throw Error('筛选项响应无效');people.value=p;sources.value=s}
 catch(e){if(generation===session&&request===optionsSequence)optionsError.value='人员或来源列表读取失败：'+e.message}
}
watch(()=>props.show,async opened=>{
 showNumbers.value=false
 session++;sequence++;peekSequence++;selectionSequence++;clearTimeout(timer);peek.value=null;error.value='';selectionError.value='';limitNotice.value=''
 if(!opened)return
 const seed=new Map(props.seed.map(w=>[w.id,w]));selected.value=new Map(props.ids.map(id=>[id,seed.get(id)||{id,title:'正在加载 #'+id}]))
 if(!initialized){const prod=props.environments.find(e=>e.name==='prod');filters.value.environment=prod?.name||'';filters.value.delivery=prod?'not_recorded':'';initialized=true}
 loadOptions()
 await nextTick();loadPage();hydrate()
})
watch(filters,()=>{
 if(!props.show)return
 page.value=1;sequence++;clearTimeout(timer);loading.value=true;items.value=[]
 timer=setTimeout(loadPage,250)
},{deep:true})
watch(size,()=>{if(props.show){page.value=1;loadPage()}})
watch(()=>filters.value.environment,()=>{if(props.show)hydrate()})
onBeforeUnmount(()=>{session++;sequence++;peekSequence++;clearTimeout(timer)})
</script>
<template>
<Modal :show="show" title="选择需求与 BUG" width="min(1320px, 96vw)" @close="emit('close')">
 <section class="work-picker">
  <div class="picker-presets"><ElButton size="small" @click="preset('bugs')">待上线 BUG</ElButton><ElButton size="small" @click="preset('requirements')">选择需求</ElButton><ElButton size="small" :disabled="!canMine" :type="filters.person===me.username?'primary':'default'" @click="mine">只看我的</ElButton><HelpTip text="人员按来源平台账号筛选。平台账号与来源账号不一致时，请直接选择来源人员。已关闭不等于未上线；无平台成功记录须核对历史发布。" /><span>已选 {{selected.size}} / 500 项，跨页保留</span></div>
  <div class="picker-filters">
   <ElInput v-model="filters.q" placeholder="搜索标题、外部编号或 #平台编号" clearable aria-label="搜索候选需求" />
   <Sel v-model="filters.kind" :options="[{value:'bug',label:'BUG'},{value:'requirement',label:'需求'}]" all-label="全部类型" width="110px" aria-label="候选类型" />
   <Sel v-model="filters.status" :options="[{value:'closed',label:'已关闭'},{value:'resolved',label:'已解决'},{value:'active',label:'激活'},{value:'open',label:'未关闭'}]" all-label="全部状态" width="120px" aria-label="候选状态" />
   <Sel v-model="filters.source" :options="sources.map(value=>({value,label:value==='manual'?'自定义':value}))" all-label="全部来源" filterable width="180px" aria-label="候选来源" />
   <Sel v-model="filters.person_role" :options="roles" width="125px" aria-label="候选人员关系" /><Sel v-model="filters.person" :options="people" filterable all-label="全部人员" width="170px" aria-label="候选人员" />
  </div>
  <div class="picker-filters">
   <label>上线环境<Sel v-model="filters.environment" :options="environments.map(e=>({value:e.name,label:e.display_name||e.name}))" all-label="请选择" width="125px" /></label>
   <Sel v-model="filters.delivery" :disabled="!filters.environment" :options="[{value:'not_recorded',label:'无成功记录（待核对）'},{value:'recorded',label:'已有成功记录'}]" all-label="全部上线记录" width="210px" aria-label="候选上线记录" />
   <Sel v-model="filters.planning" :options="[{value:'unplanned',label:'未被其他版本排期'},{value:'planned',label:'其他版本已排期'}]" all-label="全部排期" width="190px" aria-label="候选排期" />
   <DateRangeFilter v-model:start="filters.start" v-model:end="filters.end" type="date" label="更新时间" width="340px" />
   <ElPopover v-model:visible="showNumbers" trigger="click" placement="bottom-end" :width="360" popper-class="work-number-popover">
    <template #reference><ElButton size="small" :aria-expanded="showNumbers">编号清单{{filters.numbers?'（已筛选）':''}}</ElButton></template>
    <div class="number-filter" @keydown.esc.stop.prevent="showNumbers=false"><small>请先选择来源平台；按来源内的外部编号筛选。</small><ElInput v-model="numberInput" type="textarea" :rows="3" placeholder="批量外部编号，逗号、空格或换行分隔（最多 200 项）" /><div><ElButton size="small" @click="applyNumbers">按编号清单筛选</ElButton><ElButton size="small" @click="numberInput='';filters.numbers='';showNumbers=false">清除编号清单</ElButton></div></div>
   </ElPopover>
  </div>
  <p class="candidate-note">“无成功记录”不代表确定未上线，请核对历史发布。只统计所选环境中平台成功交付的需求快照；其他版本排期仅提示，不替你禁选。</p>
  <p v-if="optionsError" role="alert">{{optionsError}} <ElButton size="small" @click="loadOptions">重新读取筛选项</ElButton></p>
  <p v-if="limitNotice" class="candidate-note attention" role="status">{{limitNotice}}</p>
  <div class="picker-columns">
   <div class="picker-main">
    <div class="picker-table-scroll" :aria-busy="loading" tabindex="0"><table class="picker-table"><thead><tr><th><input type="checkbox" aria-label="选择或取消本页候选" :checked="allOnPage" :disabled="loading||!items.length" @change="togglePage" /></th><th>编号 / 类型</th><th>标题</th><th>状态 / 解决人</th><th>交付准备</th><th>上线 / 排期</th></tr></thead><tbody>
     <tr v-for="w in items" :key="w.id"><td><input type="checkbox" :aria-label="'选择 '+(w.external_id||'#'+w.id)" :checked="selected.has(w.id)" :disabled="hydrating" @change="toggle(w)" /></td><td>{{w.external_id||'#'+w.id}}<small>{{w.kind==='bug'?'BUG':'需求'}} · {{w.source==='manual'?'自定义':w.source}}</small></td><td><button class="picker-title" :title="w.title" @click="preview(w)">{{w.title}}</button><small>{{date(w.updated_at)}}</small></td><td>{{statusLabel(w)}}<small>{{w.resolved_by||'暂无解决人'}}</small></td><td :class="{attention:!w.change_count||w.ready_count<w.change_count}">{{w.change_count?w.ready_count+' / '+w.change_count+' 项就绪':'待补充变更'}}</td><td><small>{{w.successful_delivery?'已有成功记录':'历史上线待核对'}}</small><small v-if="w.planned_count">{{w.planned_count}} 个其他版本已排期</small></td></tr>
     <tr v-if="!items.length"><td colspan="6" class="picker-empty"><span v-if="loading">正在查询候选…</span><template v-else-if="error"><p role="alert">{{error}}</p><ElButton @click="loadPage">重试</ElButton></template><span v-else>没有匹配项，可调整状态、人员或日期；已选清单不会丢失。</span></td></tr>
    </tbody></table></div>
    <div class="picker-pagination"><span>共 {{total}} 项</span><Sel v-model="size" :options="[20,50,100].map(value=>({value,label:value+' 项 / 页'}))" width="120px" /><ElButton size="small" :disabled="loading||page<=1" @click="go(page-1)">上一页</ElButton><span>{{page}} / {{pages}}</span><ElButton size="small" :disabled="loading||page>=pages" @click="go(page+1)">下一页</ElButton></div>
   </div>
   <aside class="picker-selected" aria-label="跨页已选清单"><div class="selected-heading"><b>已选 {{selected.size}} 项</b><ElButton size="small" :disabled="hydrating" @click="selected.clear();selectionError=''">清空</ElButton></div><p v-if="hydrating" role="status">正在核对已选记录…</p><div v-if="selectionError" role="alert">{{selectionError}}<ElButton size="small" @click="hydrate">重新核对</ElButton></div><div class="selected-list"><article v-for="w in chosen" :key="w.id"><span :title="w.title">{{w.external_id||'#'+w.id}} · {{w.title}}</span><button :aria-label="'移除 '+(w.external_id||'#'+w.id)" @click="selected.delete(w.id);selectionError=''">×</button><small v-if="w.missing" class="attention">记录已失效，请移除</small><small v-else-if="w.change_count===0" class="attention">待补充变更，保存草稿后完善</small></article><p v-if="!chosen.length">勾选左侧记录，可跨页累计。</p></div></aside>
  </div>
  <details v-if="peek" open class="candidate-preview"><summary>{{peek.title}} <ElButton size="small" @click.prevent="peek=null;peekSequence++">关闭预览</ElButton></summary><p v-if="peekLoading">正在读取详情…</p><RichDescription v-else :content="peek.description||''" :format="peek.description_format||''" :source-url="peek.url||''" title="工作项详情" /></details>
  <footer class="picker-footer"><span>仅调整版本草稿；确认不保存版本、不触发构建或发布。</span><ElButton @click="emit('close')">取消，保留原选择</ElButton><ElButton type="primary" :disabled="!canConfirm" @click="confirm">确认选择 {{selected.size}} 项</ElButton></footer>
 </section>
</Modal>
</template>
<style scoped>
.work-picker{max-height:82dvh;display:flex;flex-direction:column;min-height:0;gap:10px}.picker-presets,.picker-filters,.picker-pagination,.picker-footer,.selected-heading{display:flex;align-items:center;gap:8px;flex-wrap:wrap}.picker-presets>span{margin-left:auto;color:var(--muted);font-size:12px}.picker-filters>.el-input{flex:1;min-width:220px}.picker-filters label{display:flex;align-items:center;gap:6px;font-size:12px}.candidate-note{margin:0;color:var(--muted);font-size:12px;line-height:1.6}.picker-columns{display:grid;grid-template-columns:minmax(0,1fr) 245px;gap:14px;min-height:0;flex:1;overflow:hidden}.picker-main{display:flex;flex-direction:column;min-width:0;min-height:0}.picker-table-scroll{overflow:auto;min-height:180px;max-height:48dvh;border:1px solid var(--border);border-radius:8px}.picker-table{width:100%;min-width:800px;table-layout:fixed;border-collapse:collapse;font-size:12px}.picker-table th,.picker-table td{padding:10px 8px;text-align:left;border-bottom:1px solid var(--border);overflow-wrap:anywhere}.picker-table th{position:sticky;top:0;background:var(--panel2);z-index:1;color:var(--muted)}.picker-table th:nth-child(1){width:32px}.picker-table th:nth-child(2){width:116px}.picker-table th:nth-child(3){width:auto}.picker-table th:nth-child(4){width:112px}.picker-table th:nth-child(5){width:112px}.picker-table th:nth-child(6){width:130px}.picker-table small,.selected-list small{display:block;color:var(--muted);font-size:11px;margin-top:4px}.picker-title{background:none;border:0;padding:0;color:var(--text);cursor:pointer;font-weight:600;text-align:left;display:-webkit-box;-webkit-line-clamp:2;-webkit-box-orient:vertical;overflow:hidden}.picker-title:hover{color:var(--brand)}input[type=checkbox]{accent-color:var(--brand);width:16px;height:16px;cursor:pointer}.picker-pagination{padding-top:10px;font-size:12px;color:var(--muted)}.picker-pagination>span:first-child{margin-right:auto}.picker-empty{text-align:center!important;height:150px;color:var(--muted)}.picker-selected{border:1px solid var(--border);border-radius:8px;padding:12px;display:flex;flex-direction:column;min-height:0}.selected-heading{justify-content:space-between;font-size:13px}.selected-list{overflow:auto;min-height:0;max-height:48dvh}.selected-list article{position:relative;padding:10px 22px 10px 0;border-bottom:1px solid var(--border)}.selected-list article>span{display:-webkit-box;-webkit-line-clamp:2;-webkit-box-orient:vertical;overflow:hidden;font-size:12px}.selected-list article>button{position:absolute;right:0;top:10px;border:0;color:var(--muted);background:none;cursor:pointer;font-size:20px}.selected-list p{font-size:12px;color:var(--muted)}.attention{color:var(--warn)!important}.picker-footer{border-top:1px solid var(--border);padding-top:12px;flex-shrink:0}.picker-footer>span{margin-right:auto;max-width:50%;font-size:12px;color:var(--muted)}.number-filter{display:grid;gap:12px}.number-filter small{font-size:12px;color:var(--muted)}.number-filter>div{display:flex;gap:8px}.candidate-preview{overflow:auto;max-height:180px;flex-shrink:0;border:1px solid var(--border);padding:8px;border-radius:6px}.candidate-preview summary{display:flex;align-items:center;justify-content:space-between;gap:8px}.candidate-preview :deep(.description-body){max-height:140px}:focus-visible{outline:2px solid var(--brand);outline-offset:2px}@media(max-width:850px){.picker-columns{grid-template-columns:1fr}.picker-selected{max-height:150px;min-height:80px}.selected-list{max-height:100px}.picker-table-scroll{max-height:32dvh}.picker-footer>span{max-width:none;width:100%}.work-picker{max-height:84dvh;overflow:auto}.picker-presets>span{margin-left:0}.picker-filters>.el-input{min-width:180px}.candidate-preview{max-height:120px}}
</style>
