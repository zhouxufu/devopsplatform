<script setup>
import ListPagination from '../shared/components/ListPagination.vue'
import {ref,computed,onMounted,onBeforeUnmount,watch,provide} from 'vue'
import {useRoute,useRouter} from 'vue-router'
import {createViewNavigation} from '../shared/viewHistory.js'
import {reportListLocation} from './reportNavigation.js'
import PageSurface from '../shared/components/PageSurface.vue'
import PageHeader from '../shared/components/PageHeader.vue'
import OptionPicker from '../shared/components/OptionPicker.vue'
import DateRangeFilter from '../shared/components/DateRangeFilter.vue'
import DataTable from '../shared/components/DataTable.vue'
import HelpTip from '../shared/components/HelpTip.vue'
import WorkItemPicker from '../deploy/components/WorkItemPicker.vue'
import FieldDiffDetails from './FieldDiffDetails.vue'
import {toast} from '../shared/composables/toast.js'
import {Badge,Button,Input,Checkbox,Field,FieldLabel,Spinner} from '../shared/shadcn-ui.js'
import {reportStates,reportSources,reportRelation,reportTime,documentDestination,openDocumentWorkspace} from './reportModel.js'
const route=useRoute(),router=useRouter(),navigateView=createViewNavigation(router)
const service=ref(String(route.query.service||'')),environment=ref(String(route.query.environment||'')),actor=ref(String(route.query.actor||'')),source=ref(String(route.query.source||'')),state=ref(String(route.query.state||'')),protocol=ref(String(route.query.protocol||'')),mine=ref(route.query.mine==='1'),start=ref(String(route.query.start||'')),end=ref(String(route.query.end||'')),page=ref(Number(route.query.page)||1),pageSize=ref([20,50,100].includes(Number(route.query.page_size))?Number(route.query.page_size):20)
const me=ref(null),services=ref([]),environments=ref([]),rows=ref([]),total=ref(0),loading=ref(false),error=ref(''),detail=ref(null),detailLoading=ref(false),detailError=ref(''),selectedID=ref(String(route.query.report||'')),cancelBusy=ref(false),returnBusy=ref(false)
let epoch=0,detailEpoch=0,controller
const columns=[{id:'time',label:'上报时间',size:190,required:true},{id:'target',label:'应用 / 环境',size:180,required:true},{id:'actor',label:'上报人 / 来源',size:155},{id:'result',label:'上报结果',size:180,required:true},{id:'relation',label:'当前关系',size:180},{id:'workItems',label:'关联需求 / Bug',size:230},{id:'changes',label:'接口变化',size:160},{id:'actions',label:'操作',size:90,required:true,pin:'right'}]
const changeColumns=[{id:'kind',label:'变化',size:90},{id:'operation',label:'功能 / 接口',size:360,flex:true},{id:'fields',label:'字段变化',size:170},{id:'actions',label:'操作',size:115}]
const differences=computed(()=>detail.value?.comparison?.changes||[])
const changePage=ref(1),changePageSize=ref(20)
const changePages=computed(()=>Math.max(1,Math.ceil(differences.value.length/changePageSize.value)))
const pagedDifferences=computed(()=>differences.value.slice((changePage.value-1)*changePageSize.value,changePage.value*changePageSize.value))
watch(changePages,total=>{if(detail.value?.comparison)changePage.value=Math.min(changePage.value,total)})
watch(selectedID,()=>{changePage.value=1})
const report=computed(()=>detail.value?.report)
const selectingLinks=ref(false),linksBusy=ref(false),linksError=ref(''),linksServer=ref(null),linksDraft=ref(null),linksRevision=ref(0)
const canLink=computed(()=>report.value&&!report.value.association_error&&(me.value?.role==='admin'||me.value?.username===report.value.actor))
provide('api',request)
function editLinks(){linksServer.value=null;linksError.value='';linksRevision.value=report.value.association?.revision||0;linksDraft.value=[...(report.value.association?.items||[])];selectingLinks.value=true}
function chooseLinks(items){linksDraft.value=items;selectingLinks.value=false;saveLinks()}
async function saveLinks(){if(linksBusy.value||!report.value||!canLink.value)return;const id=report.value.id;linksBusy.value=true;linksError.value='';try{const association=await request('api/api-document-reports/'+id+'/work-items',{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({revision:linksRevision.value,work_item_ids:linksDraft.value.map(w=>w.id)})});if(report.value?.id!==id)return;report.value.association=association;const row=rows.value.find(w=>w.id===id);if(row)row.association=association;linksDraft.value=null;toast('需求 / Bug 关联已保存','success')}catch(e){linksError.value=e.message+'；本次选择已保留。';if(e.status===409){try{const current=await request('api/api-document-reports/'+id);if(report.value?.id===id)linksServer.value=current.report.association}catch{}}}finally{linksBusy.value=false}}
function mergeLinkSelections(){if(!linksServer.value)return;linksDraft.value=[...new Map([...linksServer.value.items,...linksDraft.value].map(w=>[w.id,w])).values()];linksRevision.value=linksServer.value.revision;report.value.association=linksServer.value;linksServer.value=null;linksError.value='已合并双方选择，请核对后保存。'}
function useServerLinks(){if(!linksServer.value)return;report.value.association=linksServer.value;linksDraft.value=null;linksServer.value=null;linksError.value=''}
const removeLinkGuard=router.beforeEach(to=>{if((linksBusy.value||linksDraft.value!==null||selectingLinks.value)&&(to.path!=='/reports'||String(to.query.report||'')!==selectedID.value)){toast('关联尚未保存，请先保存或取消编辑','warning');return false}return true})
onBeforeUnmount(removeLinkGuard)
function viewWorkItem(item){const hash='#/work/'+item.id;if(window.parent!==window)window.parent.postMessage({type:'workspace.open',plugin:'research',hash},location.origin);else window.open('/api/plugins/research/ui'+hash,'_blank','noopener')}
watch(()=>report.value?.id,()=>{if(!linksBusy.value){linksDraft.value=null;linksError.value='';selectingLinks.value=false}})
watch([linksDraft,linksBusy,selectingLinks],()=>{if(window.parent!==window)window.parent.postMessage({type:'workspace.dirty',dirty:linksDraft.value!==null,busy:linksBusy.value||selectingLinks.value},location.origin)})

const appName=value=>services.value.find(s=>s.name===value)?.display_name||value
const envName=value=>environments.value.find(e=>e.name===value)?.display_name||value||'未区分环境'
const kindName=value=>({added:'新增',modified:'修改',removed:'删除'})[value]||value
function query(){return {...(selectedID.value&&report.value?{detail_service:report.value.service,detail_environment:report.value.environment,detail_protocol:report.value.protocol}:{}),service:service.value,environment:environment.value,actor:actor.value,source:source.value,state:state.value,protocol:protocol.value,mine:mine.value?'1':'',start:start.value,end:end.value,page:String(page.value),page_size:String(pageSize.value),...(selectedID.value?{report:selectedID.value}:{})}}
function remember(mode='replace'){const navigation=navigateView({path:'/reports',query:Object.fromEntries(Object.entries(query()).filter(([,v])=>v!==''))},mode==='push');if(window.parent!==window)window.parent.postMessage({type:'workspace.label',label:report.value&&selectedID.value?'上报记录 · '+appName(report.value.service)+' · '+envName(report.value.environment)+' · R'+report.value.number:'上报记录'},location.origin);return navigation}
async function request(url,options){const r=await fetch(url,options);const v=await r.json();if(!r.ok){const e=Error(r.status===401?'登录已过期，请重新登录后重试':v.error||'读取失败');e.status=r.status;throw e}return v}
async function load(reset=false,sync=true){if(reset)page.value=1;if(sync)remember();controller?.abort();controller=new AbortController();const seq=++epoch;loading.value=true;error.value='';const q=new URLSearchParams(Object.fromEntries(Object.entries(query()).filter(([k,v])=>v!==''&&!['report','start','end','detail_service','detail_environment','detail_protocol'].includes(k))));for(const [k,v] of [['start',start.value],['end',end.value]])if(v)q.set(k,String(Math.floor(new Date(v).getTime()/1000)));try{const v=await request('api/api-document-reports?'+q,{signal:controller.signal});if(seq!==epoch)return;rows.value=v.items;total.value=v.total}catch(e){if(seq===epoch&&e.name!=='AbortError')error.value=e.message}finally{if(seq===epoch)loading.value=false}}
async function open(id,push=true){if(linksBusy.value||linksDraft.value!==null){toast('请先保存或取消关联编辑','warning');return}const changed=selectedID.value!==id,seq=++detailEpoch;detail.value=null;selectedID.value=id;detailLoading.value=true;detailError.value='';try{await remember(push&&changed?'push':'replace');if(seq!==detailEpoch)return;const v=await request('api/api-document-reports/'+encodeURIComponent(id));if(seq===detailEpoch){detail.value=v;remember()}}catch(e){if(seq===detailEpoch)detailError.value=e.message}finally{if(seq===detailEpoch)detailLoading.value=false}}

function clearDetail(){detailEpoch++;selectedID.value='';detail.value=null;detailError.value='';detailLoading.value=false}
async function back(){
 if(returnBusy.value)return
 if(linksBusy.value||linksDraft.value!==null||selectingLinks.value){toast('请先保存或取消关联编辑','warning');return}
 returnBusy.value=true;detailEpoch++
 try{await navigateView(reportListLocation(query()),true)}catch(e){detailError.value=e.message}finally{returnBusy.value=false}
}
async function cancel(){if(cancelBusy.value)return;cancelBusy.value=true;try{await request('api/api-document-reports/'+report.value.id+'/cancel',{method:'POST'});await open(report.value.id);await load()}catch(e){detailError.value=e.message}finally{cancelBusy.value=false}}
function viewOperation(v){const target={...report.value,snapshot_id:v.kind==='removed'?report.value.baseline_id:report.value.snapshot_id};openDocumentWorkspace(documentDestination(target,{operation:v.id}))}
onMounted(async()=>{window.document.title='上报记录';try{[services.value,environments.value,me.value]=await Promise.all([request('api/services'),request('api/environments'),request('/api/me')]);await load();if(selectedID.value)await open(selectedID.value,false)}catch(e){error.value=e.message}})
watch(()=>route.query,async q=>{
 let changed=false
 for(const [key,target] of Object.entries({service,environment,actor,source,state,protocol,start,end})){const value=String(q[key]||'');if(target.value!==value){target.value=value;changed=true}}
 const nextSize=[20,50,100].includes(Number(q.page_size))?Number(q.page_size):20;if(pageSize.value!==nextSize){pageSize.value=nextSize;changed=true}
 const nextPage=Number(q.page)||1,nextMine=q.mine==='1';if(nextPage!==page.value||nextMine!==mine.value){page.value=nextPage;mine.value=nextMine;changed=true}
 if(q.report&&String(q.report)!==selectedID.value)await open(String(q.report),false)
 else if(!q.report&&selectedID.value)clearDetail()
 if(changed)await load(false,false)
})
onBeforeUnmount(()=>{epoch++;detailEpoch++;controller?.abort()})
</script>
<template>
 <main class="min-h-screen bg-background text-foreground"><PageSurface width="full" density="compact">
  <PageHeader title="上报记录"><template #actions><Button v-if="selectedID" variant="outline" :disabled="returnBusy" @click="back()"><Spinner v-if="returnBusy"/>返回上报记录</Button><Button variant="outline" :disabled="loading||detailLoading||linksBusy||linksDraft!==null" @click="selectedID?open(selectedID):load()"><Spinner v-if="loading||detailLoading"/>刷新</Button></template></PageHeader>
  <section v-show="!selectedID" class="space-y-4">
   <div class="flex flex-wrap items-end gap-3">
    <Field class="w-44"><FieldLabel>应用</FieldLabel><OptionPicker v-model="service" :options="services.map(s=>({value:s.name,label:s.display_name||s.name}))" all-label="全部应用"/></Field>
    <Field class="w-40"><FieldLabel>环境</FieldLabel><OptionPicker v-model="environment" :options="environments.map(e=>({value:e.name,label:e.display_name||e.name}))" all-label="全部环境"/></Field>
    <Field class="w-32"><FieldLabel>协议</FieldLabel><OptionPicker v-model="protocol" :options="[{value:'http',label:'HTTP'},{value:'dubbo',label:'Dubbo'}]" all-label="全部协议"/></Field>
    <Field class="w-36"><FieldLabel>来源</FieldLabel><OptionPicker v-model="source" :options="Object.entries(reportSources).map(([value,label])=>({value,label}))" all-label="全部来源"/></Field>
    <Field class="w-36"><FieldLabel>结果</FieldLabel><OptionPicker v-model="state" :options="Object.entries(reportStates).map(([value,label])=>({value,label}))" all-label="全部结果"/></Field>
    <Field class="w-40"><FieldLabel>上报账号</FieldLabel><Input v-model="actor" placeholder="账号精确匹配" @keydown.enter="load(true)"/></Field>
   </div>
   <div class="flex flex-wrap items-center gap-3"><DateRangeFilter v-model:start="start" v-model:end="end" label="上报时间"/><Field orientation="horizontal" class="w-auto"><Checkbox id="reports-mine" v-model="mine"/><FieldLabel for="reports-mine">我上报的</FieldLabel></Field><Button :disabled="loading" @click="load(true)"><Spinner v-if="loading"/>查询</Button><HelpTip text="每次上报单独留回执；相同内容复用文档。历史版本不会补造缺失的上报人或失败记录。"/></div>
   <p v-if="error" role="alert" class="text-destructive">{{error}} <Button variant="outline" @click="load()">重试</Button></p>
   <p v-if="loading" role="status"><Spinner/>正在读取上报记录</p>
   <DataTable :rows="rows" :columns="columns" row-key="id" table-key="api-report-records">
    <template #cell-time="{record}"><span>{{reportTime(record.created_at)}}</span><div class="text-xs text-muted-foreground">R{{record.number||record.id.slice(0,8)}}</div></template>
    <template #cell-target="{record}">{{appName(record.service)}}<div class="text-xs text-muted-foreground">{{envName(record.environment)}} · {{record.protocol==='dubbo'?'Dubbo':'HTTP'}}</div></template>
    <template #cell-actor="{record}">{{record.actor_name||record.actor||'未记录'}}<div class="text-xs text-muted-foreground">{{reportSources[record.source]||record.source}}</div></template>
    <template #cell-result="{record}"><Badge :variant="record.state==='failed'?'destructive':'secondary'">{{reportStates[record.state]||record.state}}</Badge><span v-if="record.snapshot_id"> · #{{record.snapshot_id}}</span></template>
    <template #cell-relation="{record}">{{reportRelation(record)}}</template>
    <template #cell-workItems="{record}"><span v-if="record.association_error" class="text-destructive">关联读取失败</span><span v-else-if="!record.association?.items?.length" class="text-muted-foreground">未关联</span><div v-for="item in (record.association?.items||[]).slice(0,2)" :key="item.id"><Button variant="link" class="h-auto whitespace-normal px-0 text-left" @click="viewWorkItem(item)">{{item.kind==='bug'?'Bug':'需求'}} #{{item.external_id||item.id}} · {{item.title}}</Button></div><Button v-if="record.association?.items?.length>2" variant="link" @click="open(record.id)">查看全部 {{record.association.items.length}} 项</Button></template>
    <template #cell-changes="{record}"><span v-if="['saved','historical','unchanged'].includes(record.state)">新增 {{record.changes.added||0}} · 修改 {{record.changes.modified||0}} · 删除 {{record.changes.removed||0}}</span><span v-else>—</span></template>
    <template #cell-actions="{record}"><Button variant="link" :disabled="loading" @click="open(record.id)">查看</Button></template>
    <template #empty><p class="p-6 text-center text-muted-foreground">暂无匹配的上报记录</p></template>
   </DataTable>
   <ListPagination :page="page" :total="total" :page-size="pageSize" :disabled="loading" @update:page="page=$event;load()" @update:page-size="pageSize=$event;load(true)"/>
  </section>
  <section v-if="selectedID" class="space-y-4">
   <p v-if="detailLoading" role="status"><Spinner/>正在读取原上报记录</p><p v-if="detailError" role="alert" class="text-destructive">{{detailError}} <Button variant="outline" @click="open(selectedID)">重新核对</Button></p>
   <template v-if="report">
    <p v-if="report.error" role="status">{{report.error}}</p>
    <Button v-if="report.state==='preview'&&report.actor===me?.username" variant="outline" :disabled="cancelBusy||linksBusy||linksDraft!==null" @click="cancel"><Spinner v-if="cancelBusy"/>取消本人预览</Button>
    <section class="space-y-3" aria-label="本次上报关联需求或Bug"><div class="flex flex-wrap items-center gap-2"><h3 class="text-sm font-medium">关联需求 / Bug</h3><Badge v-if="report.association?.source" variant="outline">{{report.association.source==='reported'?'随本次上报关联':'手动关联'}}</Badge><HelpTip text="仅记录这次上报对应的需求或Bug，不需要审批，也不修改文档版本、字段历史来源或需求状态。"/><Button v-if="canLink&&linksDraft===null" variant="outline" size="sm" @click="editLinks">{{report.association?.items?.length?'修改关联':'关联需求 / Bug'}}</Button></div>
     <p v-if="report.association_error" role="alert" class="text-destructive">{{report.association_error}}</p>
     <div v-if="linksDraft===null&&report.association?.items?.length" class="flex flex-wrap gap-2"><Button v-for="item in report.association?.items||[]" :key="item.id" variant="link" class="h-auto whitespace-normal px-0 text-left" @click="viewWorkItem(item)">{{item.kind==='bug'?'Bug':'需求'}} #{{item.external_id||item.id}} · {{item.title}}</Button></div>
     <div v-else-if="linksDraft!==null" class="space-y-3"><div class="flex flex-wrap gap-2"><Badge v-for="item in linksDraft" :key="item.id" variant="outline">{{item.kind==='bug'?'Bug':'需求'}} #{{item.external_id||item.id}} · {{item.title}}</Badge><span v-if="!linksDraft.length" class="text-sm text-muted-foreground">未选择关联项，保存将清空本记录的关联。</span></div><p v-if="linksError" role="alert" class="text-destructive">{{linksError}}</p><div v-if="linksServer" class="space-y-2 text-sm"><p>服务器当前关联：{{linksServer.items.map(w=>'#'+(w.external_id||w.id)+' '+w.title).join('、')||'未关联'}}</p><Button variant="outline" @click="mergeLinkSelections">合并双方选择</Button><Button variant="ghost" @click="useServerLinks">采用服务器关联</Button></div><div class="flex flex-wrap gap-2"><Button variant="outline" :disabled="linksBusy" @click="selectingLinks=true">重新选择</Button><Button :disabled="linksBusy" @click="saveLinks"><Spinner v-if="linksBusy"/>保存关联</Button><Button variant="ghost" :disabled="linksBusy" @click="linksDraft=null;linksError=''">取消编辑</Button></div></div>
    </section>
    <template v-if="detail.comparison">
     <p v-if="detail.comparison.truncated" role="status">变化清单超过展示上限，请查看对应文档核对完整定义。</p>
     <DataTable :rows="pagedDifferences" :columns="changeColumns" :max-height="0" row-key="id" table-key="api-report-changes">
      <template #cell-kind="{record}"><Badge variant="outline">{{kindName(record.kind)}}</Badge></template>
      <template #cell-operation="{record}">{{record.summary||record.path}}<div class="break-all font-mono text-xs text-muted-foreground">{{record.method}} {{record.path}}</div></template>
      <template #cell-fields="{record}"><span>{{record.fields?.length||0}} 处定义差异</span><div class="text-xs text-muted-foreground">展开右侧行详情查看字段</div></template><template #expanded="{record}"><FieldDiffDetails :record="record"/></template>
      <template #cell-actions="{record}"><Button variant="link" @click="viewOperation(record)">{{record.kind==='removed'?'查看删除前定义':'查看接口'}}</Button></template>
      <template #empty><p class="p-6 text-center text-muted-foreground">本次接口定义没有变化</p></template>
     </DataTable>
     <ListPagination aria-label="接口变化分页" :page="changePage" :total="differences.length" :page-size="changePageSize" :partial="detail.comparison.truncated" :disabled="detailLoading" @update:page="changePage=$event" @update:page-size="changePageSize=$event;changePage=1"/>
    </template>
   </template>
  </section>
 <WorkItemPicker :show="selectingLinks" purpose="document-report" :ids="(linksDraft||[]).map(w=>w.id)" :seed="linksDraft||[]" :me="me||{}" :environments="environments" @close="selectingLinks=false" @confirm="chooseLinks"/>
 </PageSurface></main>
</template>
