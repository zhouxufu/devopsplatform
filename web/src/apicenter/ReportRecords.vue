<script setup>
import {ref,computed,onMounted,onBeforeUnmount,watch} from 'vue'
import {useRoute,useRouter} from 'vue-router'
import PageSurface from '../shared/components/PageSurface.vue'
import PageHeader from '../shared/components/PageHeader.vue'
import OptionPicker from '../shared/components/OptionPicker.vue'
import DateRangeFilter from '../shared/components/DateRangeFilter.vue'
import DataTable from '../shared/components/DataTable.vue'
import HelpTip from '../shared/components/HelpTip.vue'
import {Badge,Button,Input,Checkbox,Field,FieldLabel,Spinner,Accordion,AccordionItem,AccordionTrigger,AccordionContent} from '../shared/shadcn-ui.js'
import {reportStates,reportSources,reportRelation,reportTime,documentDestination,openDocumentWorkspace} from './reportModel.js'
const route=useRoute(),router=useRouter()
const service=ref(String(route.query.service||'')),environment=ref(String(route.query.environment||'')),actor=ref(String(route.query.actor||'')),source=ref(String(route.query.source||'')),state=ref(String(route.query.state||'')),protocol=ref(String(route.query.protocol||'')),mine=ref(route.query.mine==='1'),start=ref(String(route.query.start||'')),end=ref(String(route.query.end||'')),page=ref(Number(route.query.page)||1)
const me=ref(null),services=ref([]),environments=ref([]),rows=ref([]),total=ref(0),loading=ref(false),error=ref(''),detail=ref(null),detailLoading=ref(false),detailError=ref(''),selectedID=ref(String(route.query.report||'')),changeKind=ref(''),changeSearch=ref(''),cancelBusy=ref(false)
let epoch=0,detailEpoch=0,controller
const columns=[{id:'time',label:'上报时间',size:190,required:true},{id:'target',label:'应用 / 环境',size:180,required:true},{id:'actor',label:'上报人 / 来源',size:155},{id:'result',label:'上报结果',size:180,required:true},{id:'relation',label:'当前关系',size:180},{id:'changes',label:'接口变化',size:160},{id:'actions',label:'操作',size:90,required:true,pin:'right'}]
const changeColumns=[{id:'kind',label:'变化',size:90},{id:'operation',label:'功能 / 接口',size:360,flex:true},{id:'fields',label:'字段变化',size:350},{id:'actions',label:'操作',size:115}]
const differences=computed(()=>(detail.value?.comparison?.changes||[]).filter(v=>(!changeKind.value||v.kind===changeKind.value)&&(!changeSearch.value||`${v.summary} ${v.method} ${v.path}`.toLowerCase().includes(changeSearch.value.toLowerCase()))))
const report=computed(()=>detail.value?.report)
const appName=value=>services.value.find(s=>s.name===value)?.display_name||value
const envName=value=>environments.value.find(e=>e.name===value)?.display_name||value||'未区分环境'
const kindName=value=>({added:'新增',modified:'修改',removed:'删除'})[value]||value
function query(){return {...(selectedID.value&&report.value?{detail_service:report.value.service,detail_environment:report.value.environment,detail_protocol:report.value.protocol}:{}),service:service.value,environment:environment.value,actor:actor.value,source:source.value,state:state.value,protocol:protocol.value,mine:mine.value?'1':'',start:start.value,end:end.value,page:String(page.value),...(selectedID.value?{report:selectedID.value}:{})}}
function remember(){router.replace({path:'/reports',query:Object.fromEntries(Object.entries(query()).filter(([,v])=>v!==''))});if(window.parent!==window)window.parent.postMessage({type:'workspace.label',label:report.value&&selectedID.value?'上报记录 · '+appName(report.value.service)+' · '+envName(report.value.environment)+' · R'+report.value.number:'上报记录'},location.origin)}
async function request(url,options){const r=await fetch(url,options);const v=await r.json();if(!r.ok)throw Error(r.status===401?'登录已过期，请重新登录后重试':v.error||'读取失败');return v}
async function load(reset=false){if(reset)page.value=1;remember();controller?.abort();controller=new AbortController();const seq=++epoch;loading.value=true;error.value='';const q=new URLSearchParams(Object.fromEntries(Object.entries(query()).filter(([k,v])=>v!==''&&!['report','start','end','detail_service','detail_environment','detail_protocol'].includes(k))));for(const [k,v] of [['start',start.value],['end',end.value]])if(v)q.set(k,String(Math.floor(new Date(v).getTime()/1000)));try{const v=await request('api/api-document-reports?'+q,{signal:controller.signal});if(seq!==epoch)return;rows.value=v.items;total.value=v.total}catch(e){if(seq===epoch&&e.name!=='AbortError')error.value=e.message}finally{if(seq===epoch)loading.value=false}}
async function open(id){selectedID.value=id;remember();const seq=++detailEpoch;detailLoading.value=true;detailError.value='';detail.value=null;changeKind.value='';changeSearch.value='';try{const v=await request('api/api-document-reports/'+encodeURIComponent(id));if(seq===detailEpoch){detail.value=v;remember()}}catch(e){if(seq===detailEpoch)detailError.value=e.message}finally{if(seq===detailEpoch)detailLoading.value=false}}
function back(){detailEpoch++;selectedID.value='';detail.value=null;detailError.value='';remember()}
async function cancel(){if(cancelBusy.value)return;cancelBusy.value=true;try{await request('api/api-document-reports/'+report.value.id+'/cancel',{method:'POST'});await open(report.value.id);await load()}catch(e){detailError.value=e.message}finally{cancelBusy.value=false}}
function viewOperation(v){const target={...report.value,snapshot_id:v.kind==='removed'?report.value.baseline_id:report.value.snapshot_id};openDocumentWorkspace(documentDestination(target,{operation:v.id}))}
onMounted(async()=>{window.document.title='上报记录';try{[services.value,environments.value,me.value]=await Promise.all([request('api/services'),request('api/environments'),request('/api/me')]);await load();if(selectedID.value)await open(selectedID.value)}catch(e){error.value=e.message}})
watch(()=>route.query.report,id=>{if(id&&id!==selectedID.value)open(String(id));else if(!id&&selectedID.value)back()})
onBeforeUnmount(()=>{epoch++;detailEpoch++;controller?.abort()})
</script>
<template>
 <main class="min-h-screen bg-background text-foreground"><PageSurface width="full" density="compact">
  <PageHeader title="上报记录"><template #actions><Button v-if="selectedID" variant="outline" @click="back">返回上报记录</Button><Button variant="outline" :disabled="loading||detailLoading" @click="selectedID?open(selectedID):load()"><Spinner v-if="loading||detailLoading"/>刷新</Button></template></PageHeader>
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
    <template #cell-changes="{record}"><span v-if="['saved','historical','unchanged'].includes(record.state)">新增 {{record.changes.added||0}} · 修改 {{record.changes.modified||0}} · 删除 {{record.changes.removed||0}}</span><span v-else>—</span></template>
    <template #cell-actions="{record}"><Button variant="link" :disabled="loading" @click="open(record.id)">查看</Button></template>
    <template #empty><p class="p-6 text-center text-muted-foreground">暂无匹配的上报记录</p></template>
   </DataTable>
   <div class="flex items-center justify-end gap-3"><span class="mr-auto text-sm text-muted-foreground">共 {{total}} 次上报</span><Button variant="outline" :disabled="loading||page<=1" @click="page--;load()">上一页</Button><span>{{page}} / {{Math.max(1,Math.ceil(total/20))}}</span><Button variant="outline" :disabled="loading||page*20>=total" @click="page++;load()">下一页</Button></div>
  </section>
  <section v-if="selectedID" class="space-y-4">
   <p v-if="detailLoading" role="status"><Spinner/>正在读取原上报记录</p><p v-if="detailError" role="alert" class="text-destructive">{{detailError}} <Button variant="outline" @click="open(selectedID)">重新核对</Button></p>
   <template v-if="report">
    <div class="flex flex-wrap items-center gap-3"><Badge :variant="report.state==='failed'?'destructive':'secondary'">{{reportStates[report.state]}}</Badge><span>{{appName(report.service)}} / {{envName(report.environment)}} · {{report.protocol}}</span><span>{{reportRelation(report)}}</span></div>
    <div class="text-sm text-muted-foreground">{{reportTime(report.created_at)}} · {{report.actor_name||report.actor||'上报人未记录'}} · {{reportSources[report.source]}} · 上报记录 R{{report.number||report.id}}</div>
    <p v-if="report.error" role="status">{{report.error}}</p>
    <div class="flex flex-wrap gap-2"><Button v-if="report.snapshot_id" variant="outline" @click="openDocumentWorkspace(documentDestination(report))">查看对应文档 #{{report.snapshot_id}}</Button><Button v-if="report.current_id" variant="outline" @click="openDocumentWorkspace(documentDestination(report,{current:true}))">查看当前文档 #{{report.current_id}}</Button><Button v-if="report.state==='preview'&&report.actor===me?.username" variant="outline" :disabled="cancelBusy" @click="cancel"><Spinner v-if="cancelBusy"/>取消本人预览</Button></div>
    <div class="text-sm text-muted-foreground">文档范围：{{report.source==='edit'?'按原基线保存的文档修订':'完整文档上报'}} · 来源 Commit：{{report.commit||'未关联'}}<span v-if="report.run_id"> · 构建记录 #{{report.run_id}}</span> · 运行版本一致性未核验</div>
    <template v-if="detail.comparison">
     <div class="flex flex-wrap items-center gap-3"><span>本次变化 · {{report.baseline_id?'#'+report.baseline_id:'首次空基线'}} → #{{report.snapshot_id}}</span><OptionPicker v-model="changeKind" class="w-36" :options="[{value:'added',label:'新增'},{value:'modified',label:'修改'},{value:'removed',label:'删除'}]" all-label="全部变化"/><Input v-model="changeSearch" class="max-w-sm" placeholder="搜索本次变化的功能或路径"/></div>
     <p v-if="detail.comparison.truncated" role="status">变化清单超过展示上限，请查看对应文档核对完整定义。</p>
     <DataTable :rows="differences" :columns="changeColumns" row-key="id" table-key="api-report-changes">
      <template #cell-kind="{record}"><Badge variant="outline">{{kindName(record.kind)}}</Badge></template>
      <template #cell-operation="{record}">{{record.summary||record.path}}<div class="break-all font-mono text-xs text-muted-foreground">{{record.method}} {{record.path}}</div></template>
      <template #cell-fields="{record}"><Accordion type="single" collapsible><AccordionItem value="fields"><AccordionTrigger>字段差异（{{record.fields?.length||0}}）</AccordionTrigger><AccordionContent><div v-for="(field,i) in record.fields||[]" :key="i" class="mb-2 text-sm"><code class="break-all">{{field.path}}</code><div class="grid gap-2 sm:grid-cols-2"><div><span class="text-muted-foreground">修改前</span><pre class="whitespace-pre-wrap break-all text-xs">{{field.before_present?field.before:'（不存在）'}}</pre></div><div><span class="text-muted-foreground">修改后</span><pre class="whitespace-pre-wrap break-all text-xs">{{field.after_present?field.after:'（不存在）'}}</pre></div></div></div><span v-if="!record.fields?.length">查看接口定义了解详情</span></AccordionContent></AccordionItem></Accordion></template>
      <template #cell-actions="{record}"><Button variant="link" @click="viewOperation(record)">{{record.kind==='removed'?'查看删除前定义':'查看接口'}}</Button></template>
      <template #empty><p class="p-6 text-center text-muted-foreground">{{changeSearch||changeKind?'没有匹配的接口变化':'本次接口定义没有变化'}}</p></template>
     </DataTable>
    </template>
   </template>
  </section>
 </PageSurface></main>
</template>
