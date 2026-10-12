<script setup>
import DateRangeFilter from '../../shared/components/DateRangeFilter.vue'
import { computed, inject, onMounted, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute, useRouter, onBeforeRouteLeave } from 'vue-router'
import PageHeader from '../../shared/components/PageHeader.vue'
import Sel from '../../shared/components/Sel.vue'
import HelpTip from '../../shared/components/HelpTip.vue'
import { ElButton, ElInput } from '../../shared/components/ui.js'
import { toast } from '../../shared/composables/toast.js'
import DeliveryNodeEditor from '../components/DeliveryNodeEditor.vue'
import SQLPlanPreview from '../components/SQLPlanPreview.vue'
import RedisPlanPreview from '../components/RedisPlanPreview.vue'
import NacosPlanPreview from '../components/NacosPlanPreview.vue'

const api=inject('api'), globalEnvironment=inject('globalEnvironment'), route=useRoute(), router=useRouter()
const screen=ref('list'), area=ref('runs'), phase=ref(1), busy=ref(false), reading=ref(false), me=ref({})
const templates=ref([]), services=ref([]), versions=ref([]), environments=ref([]), changes=ref([]), owners=ref([])
const page=ref({items:[],total:0,page:1,page_size:20})
const filters=ref({q:'',status:'',environment:'',version_id:'',owner:'',start:'',end:'',page:1,page_size:20})
const request=ref({template_id:'',version_id:'',environment:'',reason:'',artifacts:{},nodes:[]})
const template=ref({}), preview=ref(null), previewInput=ref(''), candidates=ref({}), binding=ref(null), codeConfirmed=ref(false)
const candidateQueries=ref({}), candidatePages=ref({}), candidateTotals=ref({}), candidateLoading=ref({})
const candidateRequests={}
const selected=ref(null), children=ref({}), control=ref({state:'idle'}), nodeIndex=ref(0), detailTab=ref('progress')
const nacosExecutions=ref([]), redisExecutions=ref([]), recoveryDraft=ref(null), rollbackReasons=ref({})
const stopReason=ref('')
const logRelease=ref(0), logs=ref({items:[],total:0,page:1}), logBusy=ref(false), pollError=ref(''), now=ref(Date.now())
let listSeq=0, candidateSeq=0, detailSeq=0, mounted=false, polling=false, timer, filterTimer
let returnScreen='list', sourceVersion=Number(route.query.version)||0, listScroll=0
let logOrigin
function closeLogs(){logRelease.value=0;logOrigin?.focus?.()}
function keyboard(event){if(event.key==='Escape'&&logRelease.value){closeLogs();event.preventDefault()}}
const clone=v=>JSON.parse(JSON.stringify(v))
const post=(path,body={})=>api(path,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)})
const states={pending_approval:'待审核',approved:'待执行',running:'执行中',success:'成功',failed:'失败',rejected:'已驳回',canceled:'已取消',pending:'待执行',waiting:'发布中',waiting_next_batch:'等待下一批',queued:'排队中',executing:'执行中 / 中断待核对',rolled_back:'已回退','rollback-failed':'回退失败'}
const kinds={deploy:'应用发布',manual:'人工确认',mysql:'SQL / 数据同步',nacos:'Nacos 配置',redis:'Redis 命令','data-sync':'不可执行的数据同步旧节点'}
const selectedTemplate=computed(()=>templates.value.find(t=>t.id===request.value.template_id))
const version=computed(()=>versions.value.find(v=>v.id===request.value.version_id))
const appNames=computed(()=>selectedTemplate.value?.mode==='fixed' ? selectedTemplate.value.services||[] : [...new Set((version.value?.changes||[]).filter(c=>['code','artifact'].includes(c.kind)).map(c=>c.service).filter(Boolean))])
const activeStep=computed(()=>selected.value?.steps?.find(s=>s.status!=='success'))
const currentNode=computed(()=>selected.value?.steps?.[nodeIndex.value])
const autoRunning=computed(()=>['running','pausing'].includes(control.value.state))
const canManage=item=>!!item&&(me.value.username==='local'||me.value.role==='admin'||me.value.username===item.owner)
const canReview=computed(()=>selected.value?.status==='pending_approval'&&selected.value.owner!==me.value.username&&String(selected.value.plan.environment.approvers||'').split(',').map(v=>v.trim()).includes(me.value.username))
const mappingMatches=computed(()=>binding.value&&appNames.value.every(name=>binding.value.bindings?.some(b=>b.service===name&&b.artifact_id===request.value.artifacts[name])))
const previewFresh=computed(()=>preview.value?.preflight_token&&previewInput.value===JSON.stringify(request.value)&&preview.value.preflight_expires*1000>now.value&&!preview.value.blockers?.length)
const executionRows=computed(()=>selected.value?.steps||[])
const templateOptions=computed(()=>templates.value.filter(t=>!sourceVersion||t.mode==='version'||t.id===request.value.template_id).map(t=>({value:t.id,label:t.name+' · '+(t.mode==='version'?'按发布版本':'固定服务')})))
const readyVersions=computed(()=>versions.value.filter(v=>['ready','completed'].includes(v.status)).map(v=>({value:v.id,label:v.version+' · '+v.title})))
const unsaved=computed(()=>screen.value==='template'?!!template.value.name:(!!request.value.reason?.trim()||Object.values(request.value.artifacts||{}).some(Boolean)||(request.value.template_id&&JSON.stringify(request.value.nodes)!==JSON.stringify(selectedTemplate.value?.nodes||[]))))
onBeforeRouteLeave(()=>!busy.value&&(!unsaved.value||confirm('当前交付草稿尚未保存或提交。离开此页面会丢失未提交内容，确定离开？')))
function beforeUnload(event){if(busy.value||unsaved.value){event.preventDefault();event.returnValue=''}}
const formatTime=n=>n?new Date(n*1000).toLocaleString('zh-CN',{hour12:false}):'—'
function statusOf(step){return step?.release_id?children.value[step.release_id]?.status||step.status:step?.status}
function duration(step){if(!step?.started_at)return '';return Math.max(0,Math.floor(((step.ended_at||now.value/1000)-step.started_at)))+' 秒'}
function artifactOptions(name){const all=[...(candidates.value[name]||[])];for(const b of binding.value?.bindings||[]) if(b.service===name&&!all.some(a=>a.artifact_id===b.artifact_id))all.push(b);return all.map(a=>({value:a.artifact_id,label:'#'+a.artifact_id+' · '+a.version+' · '+(a.branch||'手动登记 / 无分支')+(a.build_id?' · 构建 #'+a.build_id:'')}))}
function artifactMetadata(name){return [...(candidates.value[name]||[]),...(binding.value?.bindings||[])].find(a=>a.artifact_id===request.value.artifacts[name])}
async function task(fn){if(busy.value)return;busy.value=true;try{await fn()}catch(e){toast(e.message,'error')}finally{busy.value=false}}
async function loadList(){
  const sequence=++listSeq;reading.value=true
  const q=new URLSearchParams()
  for(const [key,value] of Object.entries(filters.value)){if(value!==''&&value!=null)q.set(key,['start','end'].includes(key)?String(Math.floor(new Date(value).getTime()/1000)):String(value))}
  try{const result=await api('api/delivery/runs/page?'+q);if(sequence===listSeq)page.value=result}catch(e){if(sequence===listSeq)toast(e.message,'error')}finally{if(sequence===listSeq)reading.value=false}
}
async function load(){
  const result=await Promise.all([api('api/delivery/templates'),api('api/services'),api('api/release-versions?limit=500'),api('api/environments'),api('api/changes?status=ready&limit=500'),api('api/delivery/runs/owners')])
  ;[templates.value,services.value,versions.value,environments.value,changes.value,owners.value]=result
  if(sourceVersion&&!versions.value.some(v=>v.id===sourceVersion))versions.value.push(await api('api/release-versions/'+sourceVersion))
  await loadList()
}
async function loadCandidates(){
  const sequence=++candidateSeq, id=request.value.version_id
  try{
    const map=selectedTemplate.value?.mode==='version'&&id?await api('api/release-versions/'+id+'/artifacts'):null
    const apps=[...appNames.value], entries=await Promise.all(apps.map(async name=>[name,await api('api/delivery/artifacts?service='+encodeURIComponent(name))]))
    if(sequence!==candidateSeq)return
    binding.value=map;candidates.value=Object.fromEntries(entries.map(([name,p])=>[name,p.items||[]]));codeConfirmed.value=false
    candidateTotals.value=Object.fromEntries(entries.map(([name,p])=>[name,p.total||0]));candidatePages.value=Object.fromEntries(entries.map(([name])=>[name,1]));candidateQueries.value={}
    if(map?.bindings?.length)request.value.artifacts=Object.fromEntries(map.bindings.map(b=>[b.service,b.artifact_id]))
  }catch(e){if(sequence===candidateSeq)toast(e.message,'error')}
}
async function searchArtifacts(name,page=1){
  const generation=candidateSeq, sequence=(candidateRequests[name]||0)+1;candidateRequests[name]=sequence;candidateLoading.value[name]=true
  try{
    const p=await api('api/delivery/artifacts?'+new URLSearchParams({service:name,q:candidateQueries.value[name]||'',page}))
    if(generation!==candidateSeq||sequence!==candidateRequests[name])return
    const current=artifactMetadata(name);candidates.value[name]=p.items||[]
    if(current&&!candidates.value[name].some(a=>a.artifact_id===current.artifact_id))candidates.value[name].unshift(current)
    candidatePages.value[name]=p.page;candidateTotals.value[name]=p.total
  }catch(e){toast(e.message,'error')}finally{if(sequence===candidateRequests[name])candidateLoading.value[name]=false}
}
function startDelivery(){
  if(busy.value)return
  listScroll=window.scrollY
  // Re-entering a draft keeps its scope, artifacts and edited nodes.
  if(!request.value.template_id){
    request.value={template_id:templateOptions.value.length===1?templateOptions.value[0].value:'',version_id:sourceVersion||'',environment:globalEnvironment?.value||environments.value[0]?.name||'',reason:'',artifacts:{},nodes:[]}
    if(request.value.template_id)chooseTemplate(request.value.template_id)
  }
  screen.value='prepare';phase.value=1;logRelease.value=0
}
function chooseTemplate(id){
  const item=templates.value.find(t=>t.id===id);request.value.nodes=clone(item?.nodes||[]);request.value.artifacts={}
  if(item?.mode!=='version')request.value.version_id=''
  binding.value=null;loadCandidates()
}
function newDraft(){if(busy.value||!confirm('清空当前未提交的交付草稿？'))return;request.value.template_id='';startDelivery()}
function editTemplate(item){
  returnScreen=screen.value;template.value=item?clone(item):{name:'',mode:sourceVersion?'version':'fixed',services:[],nodes:[{id:crypto.randomUUID(),kind:'deploy',name:'发布应用',service:''}]};screen.value='template'
}
async function saveTemplate(){await task(async()=>{
  const saved=await api('api/delivery/templates'+(template.value.id?'/'+template.value.id:''),{method:template.value.id?'PUT':'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(template.value)})
  templates.value=await api('api/delivery/templates');screen.value=returnScreen
  if(returnScreen==='prepare'){request.value.template_id=saved.id;chooseTemplate(saved.id)}
  toast('编排已保存','success')
})}
async function pinArtifacts(){await task(async()=>{
  if(!codeConfirmed.value)return toast('请先人工确认制品覆盖本版本代码变更','warning')
  binding.value=await api('api/release-versions/'+request.value.version_id+'/artifacts',{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({bindings:appNames.value.map(service=>({service,artifact_id:request.value.artifacts[service],code_confirmed:true}))})})
  preview.value=null;toast('版本制品已关联；首次提交后锁定，后续环境复用','success')
})}
async function resolve(){await task(async()=>{
  if(selectedTemplate.value?.mode==='version'&&appNames.value.length&&!mappingMatches.value) return toast('请先核对并关联版本制品','warning')
  const input=JSON.stringify(request.value), result=await post('api/delivery/preview',JSON.parse(input))
  if(input!==JSON.stringify(request.value))return
  preview.value=result;previewInput.value=input;now.value=Date.now();phase.value=3
})}
async function submit(){await task(async()=>{
  if(!previewFresh.value)return toast('请重新预检后提交','warning')
  const result=await post('api/delivery/runs',{...clone(request.value),preflight_token:preview.value.preflight_token})
  request.value={template_id:'',version_id:'',environment:'',reason:'',artifacts:{},nodes:[]};preview.value=null
  await loadList();await openDetail(result)
  toast('执行单已冻结；审批或提交不会自动开始部署','success')
})}
async function refreshDetail(){
  const id=selected.value?.id, sequence=detailSeq
  if(!id)return
  const [run,ctrl,nacos,redis]=await Promise.all([api('api/delivery/runs/'+id),api('api/delivery/runs/'+id+'/control'),api('api/nacos-executions?run_id='+id),api('api/redis-executions?run_id='+id)])
  const entries=await Promise.all((run.steps||[]).filter(s=>s.release_id).map(async s=>[s.release_id,await api('api/releases/'+s.release_id)]))
  if(sequence!==detailSeq||selected.value?.id!==id||screen.value!=='detail')return
  selected.value=run;control.value=ctrl;nacosExecutions.value=nacos;redisExecutions.value=redis;children.value=Object.fromEntries(entries);pollError.value=''
}
async function openDetail(run){
  if(screen.value==='list')listScroll=window.scrollY
  ++detailSeq;selected.value={id:run.id};screen.value='detail';nodeIndex.value=0;detailTab.value='progress';logRelease.value=0;recoveryDraft.value=null
  try{await refreshDetail();nodeIndex.value=Math.max(0,selected.value.steps?.findIndex(s=>s.status!=='success')||0);await router.replace({path:'/orchestration',query:{run:String(run.id)}})}catch(e){toast(e.message,'error');screen.value='list';selected.value=null}
}
function back(){
  if(busy.value)return
  if(screen.value==='template'){screen.value=returnScreen;return}
  ++detailSeq;logRelease.value=0;screen.value='list';selected.value=null
  router.replace({path:'/orchestration',query:sourceVersion?{version:String(sourceVersion)}:{}})
  requestAnimationFrame(()=>window.scrollTo(0,listScroll));loadList()
}
async function action(op){await task(async()=>{
  if(op==='advance'&&!confirm(activeStep.value?.kind==='manual'?'确认已完成该人工节点的说明？':'执行 / 核对当前节点？可能连接真实资源，不会重跑已成功的写操作。'))return
  await post('api/delivery/runs/'+selected.value.id+'/'+op,{digest:selected.value.digest,confirm_manual:activeStep.value?.kind==='manual'})
  await refreshDetail()
})}
async function coordinate(op){await task(async()=>{
  if(!confirm(op==='start'?'启动自动推进？普通节点会顺序执行；人工确认、批次观察或失败时停止。审批通过不会自动启动。':'暂停后续节点？当前已启动的操作不会被强制终止。'))return
  await post('api/delivery/runs/'+selected.value.id+'/'+op,{digest:selected.value.digest,confirm:true});await refreshDetail()
})}
async function continueBatch(id){await task(async()=>{if(!confirm('核对当前批次后继续下一批？'))return;await post('api/releases/'+id+'/continue');await refreshDetail()})}
async function cancelBatch(id){await task(async()=>{if(!confirm('取消尚未发布的批次？已完成主机不回滚，当前交付将停止。'))return;await post('api/releases/'+id+'/cancel');await refreshDetail()})}
async function showLogs(id,logPage=1){
  const runID=selected.value?.id;if(!runID||logBusy.value)return
  if(!logRelease.value)logOrigin=document.activeElement
  logRelease.value=id;logBusy.value=true
  try{const result=await api('api/delivery/runs/'+runID+'/logs?release_id='+id+'&page='+logPage);if(selected.value?.id===runID&&logRelease.value===id)logs.value=result}catch(e){toast(e.message,'error')}finally{logBusy.value=false}
}
async function restoreNacos(changeID){await task(async()=>{recoveryDraft.value=await post('api/delivery/runs/'+selected.value.id+'/nacos-restore-draft',{change_id:changeID});detailTab.value='recovery';toast('恢复草稿已创建；需核对、就绪并重新审核，不会立即执行','success')})}
async function stopDelivery(){await task(async()=>{
  if(!stopReason.value.trim())return toast('请填写终止原因','warning')
  if(!confirm('仅终止后续编排并标记失败，不撤销 SQL / Redis，不代表中断的写操作没有生效。你已核对当前资源状态？'))return
  await post('api/delivery/runs/'+selected.value.id+'/stop',{digest:selected.value.digest,confirm:true,reason:stopReason.value.trim()})
  await refreshDetail()
})}
async function recoveryTemplate(run,nodes,apps,artifacts,reason){
  const saved=await post('api/delivery/templates',{name:'恢复 #'+run.id+' · '+Date.now().toString(36),mode:'fixed',services:apps,nodes})
  templates.value.push(saved);request.value={template_id:saved.id,version_id:'',environment:run.plan.environment.name,reason,artifacts,nodes}
  screen.value='prepare';phase.value=2;preview.value=null;logRelease.value=0;binding.value=null;await loadCandidates()
}
async function rollback(step){await task(async()=>{
  const reason=rollbackReasons.value[step.release_id]?.trim();if(!reason)return toast('请填写恢复原因','warning')
  const run=selected.value
  const candidate=await api('api/delivery/runs/'+run.id+'/rollback-candidate?release_id='+step.release_id)
  if(!confirm('生成 '+step.service+' 回退到 '+candidate.version+' 的交付草稿？只保存编排，不立即执行；须重新预检和审核。'))return
  await recoveryTemplate(run,[{id:crypto.randomUUID(),kind:'deploy',name:'回退 '+step.service,service:step.service}],[step.service],{[step.service]:candidate.artifact_id},'回退交付 #'+run.id+' / 子发布 #'+step.release_id+'：'+reason)
  toast('回退草稿已生成；请核对当前目标后预检提交','success')
})}
async function remainingDraft(){await task(async()=>{
  const run=selected.value, allowed=new Set(run.steps.filter(s=>s.kind==='deploy'&&s.status!=='success').map(s=>s.service))
  const nodes=run.plan.nodes.flatMap(n=>{
    const steps=run.steps.filter(s=>s.node_id===n.id&&s.status!=='success')
    if(!steps.length)return []
    if(n.kind==='deploy')return steps.filter(s=>allowed.has(s.service)).map(s=>({...clone(n),id:crypto.randomUUID(),service:s.service}))
    // Only never-started operations can be included. Failed/uncertain writes are never replayed.
    if(steps.every(s=>s.status==='pending'&&!s.started_at))return [clone(n)]
    return []
  })
  if(!nodes.length)return toast('没有可安全复制的剩余节点；失败或中断的写操作须先核对台账，再创建新变更','warning')
  if(!confirm('保存仅包含剩余范围的接续编排与交付草稿？成功节点及失败 / 中断的写操作不会复制。'))return
  await recoveryTemplate(run,nodes,[...allowed],Object.fromEntries(run.plan.services.filter(s=>allowed.has(s.service)).map(s=>[s.service,s.artifact.id])),'接续执行单 #'+run.id+' / 版本 '+(run.plan.version?.version||'固定服务')+'（仅剩余范围，需重新预检）')
})}
watch(()=>[request.value.template_id,request.value.version_id],()=>{if(mounted)loadCandidates()})
watch(request,()=>{preview.value=null},{deep:true,flush:'sync'})
watch(()=>request.value.artifacts,()=>{codeConfirmed.value=false},{deep:true,flush:'sync'})
watch(filters,()=>{if(!mounted)return;clearTimeout(filterTimer);filterTimer=setTimeout(loadList,250)},{deep:true})
watch(()=>route.query.run,id=>{if(mounted&&id&&Number(id)!==selected.value?.id&&!busy.value)openDetail({id:Number(id)})})
function resetPage(){filters.value.page=1}
async function tick(){
  now.value=Date.now()
  if(!mounted||polling||busy.value||document.hidden||screen.value!=='detail'||!selected.value?.plan)return
  if(['success','failed','rejected','canceled'].includes(selected.value.status)&&!logRelease.value&&!autoRunning.value)return
  polling=true
  try{await refreshDetail();if(logRelease.value)await showLogs(logRelease.value,logs.value.page)}catch(e){pollError.value='状态暂未更新：'+e.message}finally{polling=false}
}
onMounted(async()=>{
  window.addEventListener('keydown',keyboard)
  window.addEventListener('beforeunload',beforeUnload)
  try{me.value=await api('/api/me');await load();mounted=true;if(route.query.run)await openDetail({id:Number(route.query.run)});else if(sourceVersion)startDelivery()}catch(e){toast(e.message,'error')}
  timer=setInterval(tick,2000)
})
onBeforeUnmount(()=>{mounted=false;++detailSeq;++candidateSeq;++listSeq;clearInterval(timer);clearTimeout(filterTimer);window.removeEventListener('keydown',keyboard);window.removeEventListener('beforeunload',beforeUnload)})
</script>

<template>
<div class="delivery-page">
  <PageHeader title="部署编排" help="一次交付多个应用与变更。审批和开始执行分离；人工节点、批次观察和失败均会暂停，不自动重跑 SQL / Redis。">
    <template #navigation><nav v-if="screen==='list'" aria-label="部署编排视图"><button :class="{active:area==='runs'}" @click="area='runs'">执行与审核</button><button :class="{active:area==='templates'}" @click="area='templates'">编排模板</button></nav><span v-else class="breadcrumb">{{screen==='template'?'编排编辑':screen==='prepare'?'交付准备':'执行单 #'+selected?.id}}</span></template>
    <template #actions><template v-if="screen==='list'"><ElButton v-if="area==='templates'" @click="editTemplate()">＋ 新建编排</ElButton><ElButton type="primary" @click="startDelivery">发起交付</ElButton></template><ElButton v-else :disabled="busy" @click="back">← {{screen==='template'?'返回原步骤':'返回列表'}}</ElButton></template>
  </PageHeader>
  <p v-if="sourceVersion&&screen==='list'" class="source"><a :href="'#/changes?tab=versions&item='+sourceVersion">返回来源版本</a> · {{versions.find(v=>v.id===sourceVersion)?.version||sourceVersion}}</p>
  <p v-if="screen==='detail'&&!selected?.plan" role="status">正在读取交付快照与执行状态…</p>

  <template v-if="screen==='list'&&area==='runs'">
    <div class="filters" aria-label="执行单筛选">
      <ElInput v-model="filters.q" placeholder="搜索编排、版本或发起人" aria-label="搜索执行单" @input="resetPage" />
      <Sel v-model="filters.environment" :options="environments.map(e=>({value:e.name,label:e.display_name}))" all-label="全部环境" @change="resetPage" />
      <Sel v-model="filters.status" :options="Object.entries(states).filter(([s])=>['pending_approval','approved','running','success','failed','rejected','canceled'].includes(s)).map(([value,label])=>({value,label}))" all-label="全部状态" @change="resetPage" />
      <Sel v-model="filters.version_id" :options="versions.map(v=>({value:String(v.id),label:v.version}))" all-label="全部版本" filterable @change="resetPage" />
      <Sel v-model="filters.owner" :options="owners" all-label="全部发起人" filterable @change="resetPage" />
      <ElButton size="small" @click="filters.owner=me.username;resetPage()">只看我的</ElButton>
      <DateRangeFilter v-model:start="filters.start" v-model:end="filters.end" label="创建时间" @change="resetPage" />
    </div>
    <div class="table-scroll" :aria-busy="reading"><table class="run-table"><thead><tr><th>执行单 / 编排</th><th>环境 / 版本</th><th>进度</th><th>状态</th><th>发起人 / 审核人</th><th>创建时间</th><th>操作</th></tr></thead><tbody>
      <tr v-for="r in page.items" :key="r.id"><td>#{{r.id}} · {{r.name}}</td><td>{{r.environment_name||r.environment}}<small>{{r.version||'固定服务'}} · {{r.applications}} 个应用</small></td><td>{{r.completed}} / {{r.total_steps}}<small>{{r.current_node||'全部完成'}}</small></td><td><span class="status" :class="r.status">{{states[r.status]||r.status}}</span></td><td>{{r.owner}}<small>{{r.approver||'—'}}</small></td><td>{{formatTime(r.created_at)}}</td><td><ElButton size="small" @click="openDetail(r)">查看 / 处理</ElButton></td></tr>
      <tr v-if="!page.items.length"><td colspan="7" class="empty">{{reading?'正在读取执行单…':'没有匹配的执行单。可以调整筛选或发起交付。'}}</td></tr>
    </tbody></table></div>
    <div class="pagination"><span>共 {{page.total}} 条</span><Sel v-model="filters.page_size" :options="[20,50,100].map(n=>({value:n,label:n+' 条 / 页'}))" @change="resetPage" /><ElButton :disabled="reading||filters.page<=1" @click="filters.page--">上一页</ElButton><span>{{filters.page}} / {{Math.max(1,Math.ceil(page.total/filters.page_size))}}</span><ElButton :disabled="reading||filters.page*filters.page_size>=page.total" @click="filters.page++">下一页</ElButton></div>
  </template>
  <section v-if="screen==='list'&&area==='templates'" class="template-list">
    <p v-if="!templates.length" class="empty">尚无编排。新建固定服务或按发布版本推导的编排，再发起交付。</p>
    <article v-for="t in templates" :key="t.id" class="panel template-card"><h3>{{t.name}} <small>{{t.mode==='version'?'按发布版本':'固定服务'}}</small></h3><p>{{t.mode==='fixed'?(t.services||[]).join('、'):'由版本的代码 / 制品变更确定应用，同一应用只发布一次'}}</p><div class="flow"><span v-for="(n,i) in t.nodes" :key="n.id">{{i+1}}. {{n.name}}</span></div><div class="actions"><ElButton v-if="canManage(t)" @click="editTemplate(t)">编辑</ElButton><ElButton type="primary" @click="request.template_id=t.id;chooseTemplate(t.id);screen='prepare';phase=1;request.environment=request.environment||globalEnvironment?.value||environments[0]?.name||''">发起交付</ElButton></div></article>
  </section>

  <form v-if="screen==='template'" class="panel form" @submit.prevent="saveTemplate">
    <div class="grid"><label>编排名称<ElInput v-model="template.name" :disabled="busy" /></label><label>交付范围<Sel v-model="template.mode" :disabled="busy" :options="[{value:'fixed',label:'固定服务'},{value:'version',label:'由发布版本推导'}]" width="100%" /></label></div>
    <label v-if="template.mode==='fixed'">应用（多选）<Sel v-model="template.services" :disabled="busy" multiple filterable :options="services.map(s=>s.name)" width="100%" /></label>
    <p v-else>仅版本中的代码 / 制品变更触发应用发布，配置或数据变更不会让所有应用重启。</p>
    <h3>顺序步骤 <HelpTip text="每个应用只能安排一次。人工节点必须填写确认说明。数据同步先冻结为 SQL 变更再纳入编排。" /></h3>
    <DeliveryNodeEditor :nodes="template.nodes" :apps="services.map(s=>s.name)" :changes="changes" :disabled="busy" />
    <div class="footer"><ElButton :disabled="busy" @click="screen=returnScreen">返回，不保存</ElButton><ElButton type="primary" native-type="submit" :loading="busy">保存{{returnScreen==='prepare'?'并继续交付':''}}</ElButton></div>
  </form>

  <section v-if="screen==='prepare'" class="prepare">
    <nav class="phase-nav" aria-label="交付准备步骤"><button v-for="(label,i) in ['选择范围','制品与步骤','预检提交']" :key="label" :class="{active:phase===i+1}" :disabled="busy||(i>0&&!selectedTemplate)" @click="phase=i+1">{{i+1}}. {{label}}</button><ElButton size="small" :disabled="busy" @click="newDraft">重建草稿</ElButton></nav>
    <div v-show="phase===1" class="panel form">
      <div class="grid"><label>编排模板<Sel v-model="request.template_id" :disabled="busy" filterable :options="templateOptions" width="100%" @change="chooseTemplate" /></label><label>目标环境<Sel v-model="request.environment" :disabled="busy" :options="environments.map(e=>({value:e.name,label:e.display_name}))" width="100%" /></label></div>
      <ElButton :disabled="busy" @click="editTemplate()">＋ 新建编排，保存后继续</ElButton>
      <label v-if="selectedTemplate?.mode==='version'">发布版本<Sel v-model="request.version_id" :disabled="busy" filterable :options="readyVersions" width="100%" /></label>
      <label>发布原因<ElInput v-model="request.reason" :disabled="busy" placeholder="说明本次交付目的，生产按策略必填" /></label>
      <p v-if="selectedTemplate">{{selectedTemplate.mode==='version'?'应用来自版本关联需求 / BUG 的代码变更':'固定应用'}} · {{appNames.length}} 个应用 · {{request.nodes.length}} 个编排节点</p>
      <div class="footer"><ElButton type="primary" :disabled="busy||!selectedTemplate||!request.environment||(selectedTemplate.mode==='version'&&!request.version_id)" @click="phase=2">下一步：核对制品与步骤</ElButton></div>
    </div>
    <div v-show="phase===2" class="panel form">
      <h3>每个应用一个制品 <small v-if="binding?.locked">版本制品已锁定，各环境复用</small></h3>
      <p v-if="!appNames.length">当前范围没有代码应用；仅配置 / 数据变更不会创建应用部署。</p>
      <div v-for="name in appNames" :key="name" class="artifact-row"><b>{{name}}</b><div><Sel v-model="request.artifacts[name]" :disabled="busy||binding?.locked" :options="artifactOptions(name)" filterable width="100%" /><details v-if="!binding?.locked"><summary>按版本 / 分支筛选候选（共 {{candidateTotals[name]||0}} 条）</summary><div class="candidate-search"><ElInput v-model="candidateQueries[name]" :disabled="busy" placeholder="版本或分支关键字" @keyup.enter="searchArtifacts(name)" /><ElButton :loading="candidateLoading[name]" :disabled="busy" @click="searchArtifacts(name)">筛选</ElButton></div><div class="actions"><ElButton size="small" :disabled="busy||candidateLoading[name]||(candidatePages[name]||1)<=1" @click="searchArtifacts(name,candidatePages[name]-1)">上页候选</ElButton><span>{{candidatePages[name]||1}} / {{Math.max(1,Math.ceil((candidateTotals[name]||0)/50))}}</span><ElButton size="small" :disabled="busy||candidateLoading[name]||(candidatePages[name]||1)*50>=candidateTotals[name]" @click="searchArtifacts(name,(candidatePages[name]||1)+1)">下页候选</ElButton></div></details></div><small>{{artifactMetadata(name)?.digest||'尚未选择'}}<br />{{artifactMetadata(name)?.build_id?'来源构建 #'+artifactMetadata(name)?.build_id+' / '+artifactMetadata(name)?.branch:'手工登记制品：需自行核对代码来源'}}</small></div>
      <template v-if="selectedTemplate?.mode==='version'&&appNames.length&&!binding?.locked"><label class="checkbox"><input v-model="codeConfirmed" type="checkbox" :disabled="busy" /> 我已人工确认各制品覆盖本版本的代码变更（系统未自动验证代码覆盖）</label><ElButton :disabled="busy||!canManage(version)||!codeConfirmed||appNames.some(n=>!request.artifacts[n])" :loading="busy" @click="pinArtifacts">关联版本制品</ElButton></template>
      <details v-if="version"><summary>关联范围 · {{version.changes?.length||0}} 条变更</summary><ul><li v-for="c in version.changes" :key="c.id">#{{c.id}} {{c.title}} · {{c.service||'无应用部署'}} · {{c.reference||c.kind}}</li></ul></details>
      <div class="flow" aria-label="本次执行顺序"><span v-for="(n,i) in request.nodes" :key="n.id">{{i+1}}. {{n.name}} · {{kinds[n.kind]||n.kind}}</span></div>
      <details><summary>调整本次执行步骤（不修改模板） · {{request.nodes.length}} 个节点</summary><DeliveryNodeEditor :nodes="request.nodes" :apps="appNames" :changes="version?.changes||changes" :disabled="busy" /></details>
      <p class="hint">数据同步任务先对比并冻结为 SQL 变更，纳入 SQL 节点。不可用的旧节点会阻止预检提交。</p>
      <div class="footer"><ElButton :disabled="busy" @click="phase=1">上一步</ElButton><ElButton type="primary" :loading="busy" :disabled="!selectedTemplate||appNames.some(n=>!request.artifacts[n])" @click="resolve">预检范围、制品与目标</ElButton></div>
    </div>
    <div v-show="phase===3" class="panel form">
      <template v-if="preview"><h3>{{preview.blockers?.length?'预检未通过':'执行预检'}} · {{preview.environment?.display_name}}</h3><p v-for="b in preview.blockers" :key="b" class="error" role="alert">{{b}}</p><details v-if="preview.warnings?.length"><summary>注意事项 · {{preview.warnings.length}}</summary><p v-for="w in preview.warnings" :key="w">{{w}}</p></details>
      <article v-for="app in preview.services" :key="app.service"><b>{{app.service}} → {{app.artifact.version}} / #{{app.artifact.id}}</b><p>{{app.target.host_group||app.target.target_type}} · {{app.target.hosts?.length||1}} 个目标 · 每批 {{app.target.batch_size||1}} 台 · {{app.target.pause_between_batches?'批间观察暂停':'连续批次'}}</p></article>
      <details><summary>核对执行顺序 · {{preview.nodes.length}} 个节点</summary><ol><li v-for="n in preview.nodes" :key="n.id">{{n.name}} · {{kinds[n.kind]||n.kind}}<p v-if="n.instructions">{{n.instructions}}</p></li></ol></details>
      <p>{{preview.environment?.protected||preview.redis_plans?.length||preview.sql_plans?.length||preview.template?.workbench?'需要审核 · 发起人不可自审':'无需额外审核'}} · 提交和审批不会自动开始执行</p>
      <SQLPlanPreview :plans="preview.sql_plans" /><RedisPlanPreview :plans="preview.redis_plans" /><NacosPlanPreview :plans="preview.nacos_plans" />
      <p v-if="previewFresh">预检有效至 {{formatTime(preview.preflight_expires)}}。提交会再次核对配置，修改后须重新预检。</p><p v-else class="error">当前预检不可提交，请重新检查。</p></template>
      <p v-else>尚无有效预检。调整范围、制品或步骤会自动使旧预检失效。</p>
      <div class="footer"><ElButton :disabled="busy" @click="phase=2">上一步</ElButton><ElButton :loading="busy" @click="resolve">重新预检</ElButton><ElButton type="primary" :loading="busy" :disabled="!previewFresh" @click="submit">提交执行单，不立即部署</ElButton></div>
    </div>
  </section>

  <template v-if="screen==='detail'&&selected?.plan">
    <section class="run-summary panel"><div><h3>{{selected.plan.template.name}} <span class="status" :class="selected.status">{{states[selected.status]||selected.status}}</span></h3><p>{{selected.plan.environment.display_name}} · {{selected.plan.version?.version||'固定服务'}} · 发起人 {{selected.owner}} · 审核人 {{selected.approver||'—'}} · {{selected.steps.filter(s=>s.status==='success').length}} / {{selected.steps.length}} 步</p></div>
      <div class="actions"><template v-if="canReview"><ElButton :loading="busy" @click="action('reject')">驳回</ElButton><ElButton type="primary" :loading="busy" @click="action('approve')">审核通过</ElButton></template><template v-if="canManage(selected)">
      <ElButton v-if="['pending_approval','approved'].includes(selected.status)" :disabled="busy" @click="action('cancel')">取消</ElButton>
      <template v-if="['approved','running'].includes(selected.status)"><ElButton v-if="!autoRunning" type="primary" :loading="busy" @click="coordinate('start')">{{control.state==='paused'||control.state==='interrupted'?'恢复自动推进':'开始执行（自动推进）'}}</ElButton><ElButton v-else :loading="busy" :disabled="control.state==='pausing'" @click="coordinate('pause')">暂停后续步骤</ElButton><ElButton v-if="!autoRunning" :loading="busy" @click="action('advance')">{{activeStep?.kind==='manual'?'确认人工节点':'单步执行 / 核对'}}</ElButton></template></template></div>
    </section>
    <p v-if="pollError" class="error" role="status">{{pollError}}</p>
    <div class="control-state" role="status">{{control.message||'尚未启动自动推进'}} · 状态自动更新 <small>暂停不强制终止已启动操作</small></div>
    <nav class="detail-tabs"><button v-for="tab in [{id:'progress',name:'执行进度'},{id:'snapshot',name:'审核快照'},{id:'recovery',name:'故障与恢复'}]" :key="tab.id" :class="{active:detailTab===tab.id}" @click="detailTab=tab.id">{{tab.name}}</button></nav>
    <div v-if="detailTab==='progress'" class="execution-layout">
      <ol class="timeline panel"><li v-for="(s,i) in selected.steps" :key="s.node_id+':'+s.service"><button :class="{active:nodeIndex===i}" @click="nodeIndex=i"><span>{{i+1}}. {{s.name}}</span><small>{{states[statusOf(s)]||statusOf(s)}} {{duration(s)}}</small></button></li></ol>
      <section v-if="currentNode" class="panel node-detail"><h3>{{currentNode.name}}</h3><p>{{kinds[currentNode.kind]||currentNode.kind}} · {{states[statusOf(currentNode)]||statusOf(currentNode)}} · {{duration(currentNode)}}</p>
        <p v-if="currentNode.kind==='manual'" class="instructions">{{selected.plan.nodes.find(n=>n.id===currentNode.node_id)?.instructions}}</p><p v-if="currentNode.detail" class="instructions">{{currentNode.detail}}</p>
        <template v-if="currentNode.release_id"><p>子发布单 #{{currentNode.release_id}} · 成功 {{children[currentNode.release_id]?.succeeded||0}} / {{children[currentNode.release_id]?.total||0}}</p><div class="actions"><template v-if="children[currentNode.release_id]?.status==='waiting_next_batch'&&canManage(selected)"><ElButton type="primary" :loading="busy" @click="continueBatch(currentNode.release_id)">核对后继续下一批</ElButton><ElButton :loading="busy" @click="cancelBatch(currentNode.release_id)">取消剩余批次</ElButton></template><ElButton :loading="logBusy" @click="showLogs(currentNode.release_id)">查看发布日志</ElButton></div></template>
        <p v-else>节点状态与说明在此更新。SQL、Redis、Nacos 的预检和执行台账见“审核快照”；失败或中断不自动重跑。</p>
      </section><p v-else class="panel">所有步骤已完成。</p>
    </div>
    <section v-if="detailTab==='snapshot'" class="panel snapshot">
      <p>{{selected.plan.reason||'未填写发布原因'}}</p><details><summary>快照摘要与发布目标 · {{selected.plan.services.length}} 个应用</summary><code>{{selected.digest}}</code><article v-for="app in selected.plan.services" :key="app.service"><h4>{{app.service}} → {{app.artifact.version}} / #{{app.artifact.id}}</h4><code>{{app.artifact.digest}}</code><p>{{app.target.host_group||app.target.target_type}} · {{app.target.hosts?.join('、')||app.target.deploy_config?.app_id}} · 每批 {{app.target.batch_size||1}} 台</p><p>部署目录：{{app.target.remote_dir||'云资源'}}<br />启动：{{app.target.start_cmd||'平台生成'}}<br />健康检查：{{app.target.health_url||'未配置'}}<br />环境变量名称：{{app.target.runtime_env_keys?.join('、')||'无'}}</p></article></details>
      <details v-if="selected.plan.version"><summary>版本关联变更 · {{selected.plan.version.changes.length}} 条</summary><article v-for="c in selected.plan.version.changes" :key="c.id"><b>#{{c.id}} {{c.title}} · {{c.service||c.kind}}</b><p>{{c.reference}}</p><p>回退：{{c.rollback_plan||'未填写'}}</p></article></details>
      <SQLPlanPreview :plans="selected.plan.sql_plans" /><RedisPlanPreview :plans="selected.plan.redis_plans" :executions="redisExecutions" /><NacosPlanPreview :plans="selected.plan.nacos_plans" :executions="nacosExecutions" :can-restore="canManage(selected)&&['success','failed'].includes(selected.status)" @restore="restoreNacos" />
    </section>
    <section v-if="detailTab==='recovery'" class="panel form">
      <h3>先核对结果，再恢复</h3><p>多应用不会原子回退。已成功的 SQL / Redis 操作不会复制到接续草稿；失败或中断的写操作须核对实际结果，另建审核变更。</p>
      <div v-if="canManage(selected)&&selected.status==='running'&&!autoRunning" class="rollback-row"><ElInput v-model="stopReason" :disabled="busy" placeholder="核对结果后填写终止后续编排的原因" /><ElButton :loading="busy" @click="stopDelivery">终止后续编排，保留台账</ElButton></div>
      <table class="recovery-table"><thead><tr><th>节点</th><th>结果</th><th>可用操作</th></tr></thead><tbody><tr v-for="s in executionRows" :key="s.node_id+':'+s.service"><td>{{s.name}}</td><td>{{states[statusOf(s)]||statusOf(s)}}</td><td><ElButton v-if="s.release_id" size="small" @click="showLogs(s.release_id)">日志</ElButton><span v-else>{{s.status==='success'?'已完成，不重放':s.status==='pending'?'未开始': '核对执行台账，不自动重放'}}</span></td></tr></tbody></table>
      <ElButton v-if="canManage(selected)&&selected.status==='failed'" :loading="busy" @click="remainingDraft">保存剩余范围编排与接续草稿</ElButton>
      <details v-if="canManage(selected)"><summary>应用单独回退（生成新交付草稿，重新预检和审核）</summary><div v-for="s in executionRows.filter(s=>s.release_id&&children[s.release_id]?.status==='success')" :key="s.release_id" class="rollback-row"><b>{{s.service}}</b><ElInput v-model="rollbackReasons[s.release_id]" :disabled="busy" placeholder="填写回退原因；受环境审批策略约束" /><ElButton :loading="busy" @click="rollback(s)">生成回退草稿</ElButton></div><p v-if="!executionRows.some(s=>s.release_id&&children[s.release_id]?.status==='success')">暂无满足回退条件的成功子发布单。失败的实例请先检查实际状态。</p></details>
      <p>Nacos 可在“审核快照”的执行台账中创建恢复草稿；需重新核对、就绪和审核。</p>
      <div v-if="recoveryDraft" class="recovery-draft">已生成恢复草稿 #{{recoveryDraft.id}} · {{recoveryDraft.title}}<p>当前执行单保留在此。<a :href="'#/changes?tab=changes&item='+recoveryDraft.id" target="_blank" rel="noopener">在新页面查看恢复变更</a></p></div>
    </section>
  </template>
  <aside v-if="logRelease" class="log-drawer" role="complementary" aria-label="发布日志">
    <div class="log-header"><h3>发布日志 · #{{logRelease}}</h3><ElButton @click="closeLogs">关闭</ElButton></div><p>每台展示最近 64 KiB；日志自动更新，不离开当前交付。Esc 可关闭。</p>
    <article v-for="entry in logs.items" :key="entry.id"><h4>{{entry.host}} · {{states[entry.status]||entry.status}}</h4><pre>{{entry.log||'暂无日志'}}</pre></article><p v-if="!logs.items.length">{{logBusy?'正在读取日志…':'暂无主机日志'}}</p>
    <div class="pagination"><ElButton :disabled="logBusy||logs.page<=1" @click="showLogs(logRelease,logs.page-1)">上一页主机</ElButton><span>{{logs.page}} / {{Math.max(1,Math.ceil(logs.total/20))}}</span><ElButton :disabled="logBusy||logs.page*20>=logs.total" @click="showLogs(logRelease,logs.page+1)">下一页主机</ElButton></div>
  </aside>
</div>
</template>
<style scoped>
.delivery-page{width:100%;min-width:0;max-width:1600px;margin:auto}.breadcrumb,small,p{color:var(--muted)}p{font-size:13px;line-height:1.7}h3{margin:0;font-size:16px}h3 small{font-size:12px;font-weight:400}.panel{background:var(--panel);border:1px solid var(--border);border-radius:10px;padding:18px;min-width:0}.form{display:grid;gap:16px}.form label{display:grid;gap:6px;min-width:0;font-size:13px}.grid{display:grid;grid-template-columns:1fr 1fr;gap:20px}.actions,.footer,.pagination,.filters,.flow,.run-summary,.phase-nav,.log-header{display:flex;gap:10px;align-items:center;flex-wrap:wrap}.footer{justify-content:flex-end;border-top:1px solid var(--border);padding-top:14px;position:sticky;bottom:0;background:var(--panel);z-index:1}.filters{margin:12px 0;padding:10px;border:1px solid var(--border);border-radius:8px;background:var(--panel)}.filters>.el-input{flex:1;min-width:190px;max-width:320px}.table-scroll{overflow:auto;border:1px solid var(--border);border-radius:10px;background:var(--panel)}table{width:100%;border-collapse:collapse;font-size:13px;text-align:left}.run-table{min-width:960px}th,td{padding:12px;border-bottom:1px solid var(--border);vertical-align:middle}th{background:var(--panel2);color:var(--muted);font-weight:600}td small{display:block;font-size:12px;margin-top:5px}tr:last-child td{border-bottom:0}.pagination{margin:14px 0;justify-content:flex-end}.pagination>span:first-child{margin-right:auto}.empty{text-align:center;padding:48px 16px;color:var(--muted)}.status{display:inline-flex;background:var(--panel2);border-radius:5px;padding:4px 8px;font-size:12px}.status.failed,.error{color:var(--error)}.status.success{color:var(--success)}.status.running,.status.approved{color:var(--brand-ink)}.template-list{display:grid;grid-template-columns:repeat(auto-fit,minmax(320px,1fr));gap:14px}.template-card .actions{justify-content:flex-end;margin-top:14px}.flow span{padding:5px 8px;background:var(--panel2);border-radius:5px;font-size:12px}.phase-nav{margin:12px 0}.phase-nav>.el-button{margin-left:auto}.phase-nav button:not(.el-button),.detail-tabs button{padding:10px 14px;background:transparent;border:0;border-bottom:2px solid transparent;color:var(--muted);cursor:pointer}.phase-nav button.active,.detail-tabs button.active{color:var(--brand-ink);border-bottom-color:var(--brand);background:var(--brand-soft)}.artifact-row{display:grid;grid-template-columns:150px minmax(200px,1fr) minmax(220px,.7fr);gap:14px;align-items:center;border-bottom:1px solid var(--border);padding-bottom:12px}.artifact-row small{overflow-wrap:anywhere}.form .checkbox{display:flex;align-items:center;gap:8px}.hint{margin:0}.form details summary,.snapshot summary{cursor:pointer;font-weight:600;font-size:13px}.form details[open]>.node-editor{margin-top:14px}.run-summary{justify-content:space-between}.run-summary p{margin-bottom:0}.control-state{font-size:13px;padding:10px 0;color:var(--muted)}.control-state small{margin-left:10px}.detail-tabs{display:flex;gap:8px;margin:4px 0 14px}.execution-layout{display:grid;grid-template-columns:minmax(220px,30%) minmax(0,1fr);gap:16px}.timeline{list-style:none;margin:0;padding:8px}.timeline li button{border:0;background:transparent;color:var(--text);width:100%;text-align:left;border-radius:6px;padding:12px;cursor:pointer}.timeline li button span,.timeline li button small{display:block;line-height:1.7}.timeline li button.active{background:var(--brand-soft);color:var(--brand-ink)}.node-detail{min-height:230px}.instructions{white-space:pre-wrap;overflow-wrap:anywhere}.snapshot>details{margin-bottom:16px;border-bottom:1px solid var(--border);padding-bottom:16px}.snapshot article{padding:12px 0}.snapshot code{display:block;overflow-wrap:anywhere;font-size:12px;margin-top:10px}.rollback-row{display:flex;gap:10px;align-items:center;padding:12px 0}.rollback-row .el-input{flex:1}.log-drawer{position:fixed;right:0;top:0;bottom:0;width:min(620px,90vw);padding:20px;background:var(--panel);border-left:1px solid var(--border);box-shadow:var(--shadow);z-index:100;overflow:auto}.log-header{position:sticky;top:-20px;background:var(--panel);justify-content:space-between;padding:12px 0;z-index:1}.log-drawer pre{background:var(--panel2);padding:12px;font-size:12px;line-height:1.65;white-space:pre-wrap;overflow-wrap:anywhere;max-height:60vh;overflow:auto}.log-drawer h4{margin-bottom:8px}.recovery-table{display:block;overflow:auto}.source{margin:8px 0}a{color:var(--brand-ink)}:focus-visible{outline:2px solid var(--brand);outline-offset:2px}@media(max-width:850px){.artifact-row{grid-template-columns:1fr}.execution-layout,.grid{grid-template-columns:1fr}.panel{padding:14px}.run-summary .actions{width:100%}.rollback-row{flex-wrap:wrap}.detail-tabs{flex-wrap:wrap}.timeline{max-height:240px;overflow:auto}.filters>.el-input{max-width:none}}
.candidate-search{display:flex;gap:8px;margin:8px 0}.artifact-row details{margin-top:8px}.artifact-row summary{font-size:12px;color:var(--muted)}
</style>
