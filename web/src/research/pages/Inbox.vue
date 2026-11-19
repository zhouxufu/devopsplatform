<script setup>
import PreparationScopeNotice from '../../deploy/components/PreparationScopeNotice.vue'
import {randomUUID} from '../../shared/uuid.js'
import {codePreparationRecovery} from '../../deploy/codePreparationRecovery.js'
import {releaseOrderLabel} from '../../deploy/buildNumber.js'
import DataTable from "../../shared/components/DataTable.vue"

import {useTableLayout} from '../../shared/composables/tableLayout.js'
const {pinTableActions}=useTableLayout()




import {computed, nextTick, onActivated, onDeactivated, onMounted, onUnmounted, ref, watch} from 'vue'
import {onBeforeRouteLeave, useRoute, useRouter} from 'vue-router'
import PageHeader from '../../shared/components/PageHeader.vue'
import WorkItems from './WorkItems.vue'
import DeliveryReviewContent from '../components/DeliveryReviewContent.vue'
import {api, post, stamp, openWorkspace} from '../api.js'
import {toast} from '../../shared/composables/toast.js'


import {browserView} from '../../shared/viewMemory.js'
import {inboxAttention} from '../sourceOperation.js'
import {exceptionCommentError} from '../exceptionAction.js'
import {attentionSelectionKey, canAttend, submitAttention} from '../attentionAction.js'
import {researchInboxTitle} from '../../shell/navigation.js'
import {taskActivityAge,taskActivityStamp} from '../taskPresentation.js'

defineOptions({name:'ResearchInbox'})
const route=useRoute(),router=useRouter()
const initialView=['todo','owned','work','done'].includes(route.query.view)?route.query.view:'todo'
const workStarted=ref(initialView==='work'),view=ref(initialView),q=ref(''),kind=ref(''),status=ref(''),page=ref(1),data=ref({items:[],total:0}),busy=ref(false),selected=ref(null),run=ref(null),bundle=ref(null),comment=ref(''),me=ref({}),scopeConfirmed=ref(false),codeTask=ref(null),selectedIds=ref([]),detailTab=ref('content'),receipt=ref(''),stale=ref(false),active=ref(true),now=ref(Date.now())
let sequence=0,timer,listScroll=0
const exceptionError=ref(''),scopeState=ref({review:null,loading:false,error:''}),preparationActions=ref(null),preparationActionError=ref('')
const attention=ref(['active','archived','all'].includes(route.query.attention)?route.query.attention:'active')
const ignorePending=ref(false),ignoreKeys=ref([]),attentionDialog=ref(false),attentionAction=ref('ignore'),attentionReason=ref(''),attentionErrors=ref([]),attentionFrozen=ref([]),attentionRequestID=ref('')
const actionLabels={ignore:'忽略并归档',archive:'归档关注',restore:'恢复显示'}
const ignoreCandidates=computed(()=>(data.value.items||[]).filter(t=>['ignore','archive','restore'].some(a=>canAttend(t,a))))
const chosenExceptions=computed(()=>ignoreCandidates.value.filter(t=>ignoreKeys.value.includes(attentionSelectionKey(t))))
const ignoreSelection=computed(()=>chosenExceptions.value.length===0?false:chosenExceptions.value.length===ignoreCandidates.value.length?true:'indeterminate')
watch([view,q,kind,status,page,attention],()=>ignoreKeys.value=[])
watch(()=>data.value.items,()=>{const keys=new Set(ignoreCandidates.value.map(attentionSelectionKey));ignoreKeys.value=ignoreKeys.value.filter(key=>keys.has(key))})
function chooseException(task,checked){const key=attentionSelectionKey(task);ignoreKeys.value=checked===true?[...new Set([...ignoreKeys.value,key])]:ignoreKeys.value.filter(k=>k!==key)}
function quickIgnore(tasks,action='ignore',reason=''){
 if(busy.value||ignorePending.value)return
 const candidates=tasks.filter(t=>canAttend(t,action))
 if(!candidates.length){toast('此记录当前不能'+actionLabels[action]+'，请核对最新状态后再处理','warning');return}
 attentionFrozen.value=candidates.map(t=>({...t}));attentionAction.value=action;attentionReason.value=reason;attentionErrors.value=[]
 attentionRequestID.value=randomUUID();attentionDialog.value=true
}
async function confirmAttention(){
 if(ignorePending.value)return
 const reason=attentionReason.value.trim()
 if([...reason].length<6||new TextEncoder().encode(reason).length>2000){attentionErrors.value=['请填写至少6字、最多2000字节的原因'];return}
 ++sequence;ignorePending.value=true
 try{
  const result=await submitAttention(attentionFrozen.value,attentionAction.value,reason,attentionRequestID.value,body=>post('research/attention',body))
  const keys=new Set(result.succeeded.map(attentionSelectionKey))
  ignoreKeys.value=ignoreKeys.value.filter(k=>!keys.has(k))
  attentionErrors.value=result.failed.map(t=>t.title+'：'+t.error)
  attentionFrozen.value=result.failed.map(t=>({...t}))
  if(result.succeeded.length){toast('已'+actionLabels[attentionAction.value]+' '+result.succeeded.length+' 项；原执行状态保留','success');window.parent.postMessage({type:'research.attention.changed'},location.origin)}
  if(!result.failed.length){attentionDialog.value=false;if(selected.value){selected.value=null;comment.value=''}}
 }catch(e){attentionErrors.value=[e.message+'；结果未确认，请核对列表和处理记录后再操作']}
 finally{ignorePending.value=false}
 await load(false,true)
}
watch(comment,()=>exceptionError.value='')
let viewMemory,viewReady=false
const viewDefaults={view:'todo',q:'',kind:'',status:'',page:1,attention:'active'}
watch([view,q,kind,status,page,attention],()=>{if(viewReady)viewMemory?.write({view:view.value,q:q.value,kind:kind.value,status:status.value,page:page.value,attention:attention.value},viewDefaults)},{flush:'post'})
async function warnLeave(){return !comment.value.trim()||await confirmAction('离开会丢弃未提交的处理意见，是否继续？')}
onBeforeRouteLeave(async to=>{if(to.query.return==='inbox')return true;if(!await warnLeave())return false})
watch([comment,busy,ignorePending],()=>{if(window.parent!==window)window.parent.postMessage({type:'workspace.dirty',dirty:!!comment.value.trim(),busy:busy.value||ignorePending.value},location.origin)})
async function backToList(){if(!await warnLeave())return;selected.value=null;comment.value='';const {task,...query}=route.query;await router.replace({path:'/',query});if(query.return==='home'&&window.parent!==window){window.parent.postMessage({type:'platform.home'},location.origin);return}await load(false,true);await nextTick();const main=document.querySelector('.research-layout main');if(main)main.scrollTop=listScroll}
const codeRecovery=computed(()=>codePreparationRecovery(codeTask.value,{now:now.value,busy:busy.value,protectedScope:codeTask.value?.protected,scopeConfirmed:scopeConfirmed.value,canManage:run.value?.owner===me.value.username||me.value.role==='admin',scopeReview:scopeState.value.review,scopeLoading:scopeState.value.loading,scopeError:scopeState.value.error}))
const labels={'work-loop':'开发与交付跟进',approval:'待审批',manual:'待确认','code-conflict':'代码待处理',exception:'异常待处理','code-prepare':'代码准备','code-verify':'人工合入核对'}
const states={developing:'开发中',awaiting_acceptance:'待人工验收',completed:'已完成',paused:'已暂停',waiting_human:'待人员处理',blocked:'代码准备待处理',pending_approval:'待审批',waiting_confirmation:'待确认',approved:'待执行',running:'执行中',success:'已完成',failed:'执行失败',rejected:'已拒绝',canceled:'已取消',waiting_next_batch:'待批次确认',rolled_back:'已回退',uncertain:'结果待核对',executing:'回写待核对',pending:'未开始',reviewing:'待会签'}
const summary=computed(()=>({...bundle.value?.summary||selected.value||{},...Object.fromEntries(Object.entries(selected.value||{}).filter(([key])=>key.startsWith('attention_'))),...(selected.value?.resolution_action?{resolution_action:selected.value.resolution_action}:{})}))
const confirmedIds=computed(()=>new Set((run.value?.steps?.find(s=>s.node_id===selected.value?.node_id)?.reviews||[]).filter(r=>r.actor===me.value.username&&r.approved).flatMap(r=>r.work_item_ids||[])))
const eligibleItems=computed(()=>{const n=run.value?.plan?.nodes?.find(n=>n.id===selected.value?.node_id),ids=n?.assigned_work_items?.[me.value.username];return (run.value?.plan?.scope?.work_items||[]).filter(w=>!ids||ids.includes(w.id))})
const canDecide=computed(()=>{if(run.value?.plan?.preparation_request&&(!preparationActions.value?.can_review||preparationActionError.value))return false;if(!run.value||!selected.value||selected.value.read_only||view.value==='done'||view.value==='owned'||stale.value)return false;if(selected.value.source_kind==='query')return run.value?.status==='pending_approval';if(!['approval','manual','verify'].includes(selected.value.kind)||['success','failed','rejected','canceled'].includes(run.value?.status))return false;const step=run.value?.steps?.find(s=>s.node_id===selected.value.node_id);if(step?.status==='success')return false;if(selected.value.kind==='manual'&&eligibleItems.value.length)return eligibleItems.value.some(w=>!confirmedIds.value.has(w.id));return !(step?.reviews||[]).some(r=>r.actor===me.value.username)})
const plan=computed(()=>run.value?.plan)
const recordTask=computed(()=>selected.value?.kind==='manual'&&plan.value?.nodes?.some(n=>n.id===selected.value.node_id&&n.change_ids?.length)&&plan.value?.template?.workbench!=='research')
const taskColumns=computed(()=>[
 ...(ignoreCandidates.value.length?[{id:'ignoreSelection',label:'选择异常',size:44,minSize:44,required:true,selection:true,pin:'left'}]:[]),
 {id:'c0',label:view.value==='owned'?'交付':'任务',size:280,minSize:220,required:true,flex:true,pin:'left'},
 {id:'c1',label:'当前阶段 / 状态',size:160,minSize:144},
 {id:'c2',label:'版本 / 环境 / 应用',size:200,minSize:180},
 {id:'c3',label:'当前处理人',size:128,minSize:112},
 {id:'c4',label:'下一步 / 阻塞原因',size:260,minSize:220},
 {id:'c5',label:'最近活动',size:112,minSize:104},
 {id:'c6',label:'操作',size:210,minSize:180,required:true,pin:'right'},
])

function mandatoryTask(t){return ['manual','approval','verify'].includes(t?.kind)&&!t.read_only&&!['success','failed','rejected','canceled'].includes(t.status)}
function state(t){
 const execution=t.status==='success'&&t.stage?.includes('前置')?'前置完成 · 未发布':states[t.status]||t.status||'正在读取'
 if(t.attention_state==='ignored')return '本人已忽略 · '+execution
 if(t.attention_state==='archived'&&!mandatoryTask(t))return '已归档 · '+execution
 if(mandatoryTask(t)&&t.status==='pending')return labels[t.kind]||execution
 if(t.resolution_action)return (t.resolution_action==='ignore'?'待办已忽略':'处理已记录')+(t.status==='failed'?' · 原发布失败':' · 原执行状态保留')
 return execution
}
async function load(reset=false,quiet=false){if(ignorePending.value||view.value==='work')return;if(reset)page.value=1;const seq=++sequence,requestedView=view.value;if(!quiet)busy.value=true;try{const result=await api((requestedView==='owned'?'research/deliveries?':'research/tasks?')+new URLSearchParams({view:requestedView,q:q.value,kind:kind.value,status:status.value,page:page.value,attention:inboxAttention(requestedView,attention.value)}));if(seq===sequence&&requestedView===view.value)data.value=result}catch(e){if(seq===sequence&&!quiet)toast(e.message,'error')}finally{if(seq===sequence)busy.value=false}}
function switchView(v){if(ignorePending.value||v===view.value)return;++sequence;busy.value=false;view.value=v;receipt.value='';router.replace({path:'/',query:{...route.query,view:v}});if(v==='work')workStarted.value=true;else{data.value={items:[],total:0};load(true)}}
async function readDetail(task){const target=selected.value;if(task.source_kind==='query'){run.value=await api('research/query-tasks/'+task.run_id);bundle.value={summary:{...task,status:run.value.status,stage:run.value.status==='approved'?'查询审核通过（尚未查询）':'查询审批',assignees:run.value.status==='pending_approval'?task.assignees:[run.value.owner],next_action:run.value.status==='approved'?'申请人返回查询窗口执行；审批不会自动查询':'查看查询审核结果'}};return}const b=await api('research/deliveries/'+(task.source_kind==='release'?'release':'delivery')+'/'+task.run_id);if(selected.value!==target)return;bundle.value=b;run.value=b.run||null
 preparationActions.value=null;preparationActionError.value=''
 if(b.run?.plan?.preparation_request&&task.kind!=='code-conflict'){
  try{const context=await api('research/preparations/'+task.run_id+'/action-context');if(selected.value===target)preparationActions.value=context}
  catch(e){if(selected.value===target)preparationActionError.value=e.message}
 }
}
async function open(task){if(ignorePending.value)return;if(task.work_loop_id){router.push({path:'/work/'+task.work_item_id,query:{return:'inbox',loop:task.work_loop_id}});return;}if(task.source_kind==='operation'){router.push({path:'/work/'+task.work_item_id,query:{return:'inbox',return_to:route.fullPath,operation:task.operation_id}});return}if(!selected.value)listScroll=document.querySelector('.research-layout main')?.scrollTop||0;selected.value=task;run.value=null;bundle.value=null;comment.value='';codeTask.value=null;preparationActions.value=null;preparationActionError.value='';scopeConfirmed.value=false;receipt.value='';stale.value=false;detailTab.value=view.value==='owned'||view.value==='done'?'progress':'content';busy.value=true;try{await readDetail(task);if(task.preparation_id&&task.kind==='code-conflict')codeTask.value=await api('delivery/code-preparations/'+task.preparation_id);selectedIds.value=eligibleItems.value.filter(w=>!confirmedIds.value.has(w.id)).map(w=>w.id);const main=document.querySelector('.research-layout main');if(main)main.scrollTop=0}catch(e){toast(e.message,'error');selected.value=null}finally{busy.value=false}}
function handoff(s){return (s.assignees?.length?'本次处理后交给 '+s.assignees.join('、')+' · ':'')+(s.stage||'')+'；'+(s.next_action||'查看交付记录继续追踪')}
async function decide(approve){if(busy.value||!canDecide.value)return;busy.value=true;try{const t=selected.value;const out=await post(t.source_kind==='query'?'research/query-tasks/'+t.run_id:'research/tasks/'+t.run_id+'/'+encodeURIComponent(t.node_id),{digest:t.digest,approve,comment:comment.value,work_item_ids:selectedIds.value});run.value=out;comment.value='';receipt.value='本次处理已保存，请继续追踪交付结果。';try{await readDetail(t)}catch{toast('处理已保存，交接状态暂未读取成功；请核对最新状态，不要重复提交','warning')};selectedIds.value=eligibleItems.value.filter(w=>!confirmedIds.value.has(w.id)).map(w=>w.id);receipt.value=approve?'本次处理已保存。'+handoff(summary.value):'已拒绝，发起人需核对原因；修改内容后重新审核。';toast(receipt.value,'success')}catch(e){toast(e.message,'error')}finally{busy.value=false}}
async function cancelWaitingPreparation(){
 const context=preparationActions.value,task=selected.value
 if(busy.value||!context?.can_cancel)return
 const reason=comment.value.trim()
 if([...reason].length<6||new TextEncoder().encode(reason).length>2000){toast('请在处理意见中填写至少6字、最多2000字节的终止原因','warning');await nextTick();focusControl(document.getElementById('preparation-comment'));return}
 busy.value=true
 try{
  if(!await confirmAction('终止原准备并保留冻结范围与审核历史。之后需按当前范围重新预览和确认；本次不执行构建或发布。','终止旧准备',{confirmButtonText:'确认终止旧准备'}))return
  await post('research/preparations/'+task.run_id+'/cancel',{confirm:true,expected_revision:context.revision,digest:context.digest,reason})
  comment.value='';selected.value={...task,status:'canceled',read_only:true};await readDetail(selected.value)
  receipt.value='旧准备已终止；可进入原流程按当前范围重新准备，仍需重新预览和确认。';toast(receipt.value,'success')
 }catch(e){toast(e.message,'error')}finally{busy.value=false}
}
async function cancelPreparation(){
 if(busy.value||!codeTask.value||!scopeState.value.review?.can_cancel)return
 const id=codeTask.value.id,revision=codeTask.value.revision
 if(!await confirmAction('确认终止原准备并释放未执行范围？原冻结内容与审核历史保留，已推送的代码不会回滚；不执行构建或部署。','终止旧准备',{confirmButtonText:'确认终止旧准备'}))return
 busy.value=true
 try{
  await post('delivery/code-preparations/'+encodeURIComponent(id)+'/cancel',{confirm:true,expected_revision:revision})
  codeTask.value=await api('delivery/code-preparations/'+encodeURIComponent(id));await readDetail(selected.value)
  if(run.value.status!=='canceled')throw Error('代码准备已取消，原交付终止状态待核对；请核对原单，不要重复提交')
  selected.value={...selected.value,status:'canceled',can_ignore:false};comment.value='';toast('旧准备已终止，冻结记录保留；可以按当前范围重新准备','success')
 }catch(e){toast(e.message,'error')}finally{busy.value=false}
}
async function resume(){
 if(!codeRecovery.value.enabled)return
 const id=selected.value.preparation_id,revision=codeTask.value.revision
 busy.value=true
 try{
  try{await confirmDialog.confirm('验证指定目标分支包含全部冻结源提交，通过后恢复同一个任务并继续构建。不会自动发布应用。',codeRecovery.value.actionLabel,{confirmButtonText:codeRecovery.value.actionLabel,cancelButtonText:'返回',closeOnClickModal:false})}catch{return}
  codeTask.value=await post('delivery/code-preparations/'+id+'/resume',{confirm:true,confirm_scope:scopeConfirmed.value,expected_revision:revision})
  await readDetail(selected.value)
  receipt.value='已开始验证合并结果，通过后继续原任务和构建。';toast(receipt.value,'success')
 }catch(e){toast(e.message,'error')}finally{busy.value=false}
}
async function resolveException(action='review'){
  if(busy.value)return
  exceptionError.value=exceptionCommentError(comment.value)
  if(exceptionError.value){toast(exceptionError.value,'warning');await nextTick();focusControl(document.getElementById('exception-comment'));return}
  if(!selected.value?.resolution_digest)return toast('异常已变化，请核对最新状态后处理','warning')
  if(action==='ignore'){quickIgnore([selected.value],'ignore',comment.value);return}
  busy.value=true
  try{
    if(action==='ignore')try{await confirmDialog.confirm('只移除此异常待办并保留忽略原因；原发布仍为失败，不会恢复服务、重试或解除执行锁。','忽略此异常待办',{confirmButtonText:'确认忽略',cancelButtonText:'返回编辑',type:'warning',closeOnClickModal:false})}catch{return}
    await post('research/exceptions/resolve',{id:selected.value.id,digest:selected.value.resolution_digest,comment:comment.value,action})
    selected.value={...selected.value,resolution_digest:'',resolution_action:action};comment.value=''
    receipt.value=action==='ignore'?'此异常待办已忽略，原因已存入处理记录；原发布失败状态保留，未执行恢复或重试。':'异常核对结果已记录并移入处理记录，原交付失败状态保留；没有执行任何恢复或重试。'
    toast(receipt.value,'success')
  }catch(e){toast(e.message,'error')}finally{busy.value=false}
}
async function reread(){if(busy.value)return;busy.value=true;try{await readDetail(selected.value);selected.value={...selected.value,digest:run.value?.digest||selected.value.digest};stale.value=false;if(selected.value.preparation_id)codeTask.value=await api('delivery/code-preparations/'+selected.value.preparation_id);toast('已核对最新状态，处理意见仍保留','success')}catch(e){toast(e.message,'error')}finally{busy.value=false}}
function execution(){const t=selected.value;if(t.source_kind==='release'){if(bundle.value?.parent_run_id)openWorkspace('deploy','#/orchestration?run='+bundle.value.parent_run_id);else openWorkspace('deploy','#/svc/'+encodeURIComponent(t.service)+'/release?release='+t.run_id);return}if(plan.value?.preparation_request){const p=plan.value.preparation_request;openWorkspace('deploy','#/orchestration?preparation='+t.run_id+'&'+(p.version_id?'version='+p.version_id:'work_items='+(p.work_item_ids||[]).join(',')));return}openWorkspace('deploy','#/orchestration?run='+t.run_id)}
async function poll(){now.value=Date.now();if(!active.value||document.hidden||busy.value||ignorePending.value||attentionDialog.value||view.value==='work')return;if(!selected.value){await load(false,true);return}if(selected.value.source_kind==='query')return;const t=selected.value;try{const b=await api('research/deliveries/'+(t.source_kind==='release'?'release':'delivery')+'/'+t.run_id);if(selected.value!==t)return;bundle.value=b;if(b.run?.digest&&b.run.digest!==run.value?.digest){stale.value=true;return}run.value=b.run||run.value;if(b.run?.plan?.preparation_request&&t.kind!=='code-conflict'){try{const context=await api('research/preparations/'+t.run_id+'/action-context');if(selected.value===t){preparationActions.value=context;preparationActionError.value=''}}catch(e){if(selected.value===t)preparationActionError.value=e.message}}if(t.preparation_id&&t.kind==='code-conflict'){const code=await api('delivery/code-preparations/'+t.preparation_id);if(selected.value===t)codeTask.value=code}}catch{/* Background failures do not erase data or user input. */}}
onActivated(()=>{active.value=true;if(me.value.username&&!selected.value&&view.value!=='work')load(false,true)})
onDeactivated(()=>active.value=false)
async function requestedTask(){const id=route.query.task;if(typeof id!=='string'||!id||view.value==='work')return;try{const result=await api((view.value==='owned'?'research/deliveries?':'research/tasks?')+new URLSearchParams({view:view.value,id,attention:'all'}));const task=result.items?.find(t=>t.id===id);if(task)await open(task);else toast('该任务已流转或不在本人当前范围，请查看最新列表','info')}catch(e){toast(e.message,'error')}}
watch(()=>[route.query.view,route.query.attention],([v,a])=>{
 if(!viewReady||route.path!=='/'||ignorePending.value)return
 let changed=false
 if(['todo','owned','work','done'].includes(v)&&v!==view.value){view.value=v;changed=true;workStarted.value=workStarted.value||v==='work'}
 const range=['active','archived','all'].includes(a)?a:'active'
 if(view.value==='owned'&&range!==attention.value){attention.value=range;changed=true}
 if(changed&&!route.query.task){selected.value=null;load(true)}
})
watch(()=>route.query.task,async id=>{if(!id||!viewReady||route.path!=='/')return;const targetView=route.query.view;if(['todo','owned','work','done'].includes(targetView))view.value=targetView;await requestedTask()})
onMounted(async()=>{try{me.value=await api('/api/me');viewMemory=browserView('research:inbox',me.value.username);const saved=viewMemory.read(viewDefaults);view.value=['todo','owned','work','done'].includes(route.query.view)?route.query.view:['todo','owned','work','done'].includes(saved.view)?saved.view:'todo';attention.value=['active','archived','all'].includes(route.query.attention)?route.query.attention:['active','archived','all'].includes(saved.attention)?saved.attention:'active';q.value=saved.q;kind.value=saved.kind;status.value=saved.status;page.value=saved.page;workStarted.value=view.value==='work';viewReady=true;await load();await requestedTask();timer=setInterval(poll,15000)}catch(e){toast(e.message,'error')}})
onUnmounted(()=>{clearInterval(timer);++sequence})

import OptionPicker from '../../shared/components/OptionPicker.vue'

import {confirmDialog} from '../../shared/shadcnFeedback.js'

import {focusControl} from '../../shared/focusControl.js'



import {Dialog,DialogContent,DialogHeader,DialogTitle,DialogDescription,DialogFooter,Badge,Button,Card,CardAction,CardContent,CardFooter,CardHeader,CardTitle,Checkbox,Empty,EmptyContent,EmptyDescription,EmptyHeader,Field,FieldLabel,Input,Pagination,PaginationContent,PaginationNext,PaginationPrevious,Spinner,Table,TableBody,TableCell,TableHead,TableHeader,TableRow,Tabs,TabsContent,TabsList,TabsTrigger,Textarea} from '../../shared/shadcn-ui.js'

import {InputGroup,InputGroupInput,InputGroupAddon,InputGroupButton} from '../../shared/shadcn-ui.js'

import {XIcon} from '@lucide/vue'

import {confirmAction} from "../../shared/shadcnFeedback.js"
</script>
<template>
<section v-if="!selected" class="inbox-list-surface">
 <PageHeader :title="researchInboxTitle(view)" compact :help="view==='work'?'':view==='done'?'处理记录是本人的操作历史，不代表交付已完成。':'列表自动更新任务状态；查看列表不会执行远程操作。'">
  <template #navigation>
   <Tabs class="inbox-tabs" :model-value="view" @update:model-value="switchView($event)">
    <TabsList variant="line" class="h-auto! max-w-full flex-wrap gap-y-1">
     <TabsTrigger :disabled="ignorePending" class="h-9" v-for="v in [{id:'todo',name:'待我处理'},{id:'owned',name:'我发起的'},{id:'work',name:'我的工作项'},{id:'done',name:'处理记录'}]" :key="v.id" :value="v.id">{{v.name}}</TabsTrigger>
    </TabsList>
   </Tabs>
  </template>
  <template #actions><Button v-if="view==='work'" @click="router.push('/work/new')">＋ 新建工作项</Button></template>
 </PageHeader>
 <section v-if="workStarted" v-show="view==='work'" class="inbox-work-panel"><WorkItems embedded initial-mine/></section>
 <div v-if="view!=='work'" class="grid min-w-0 gap-4">
  <div v-if="view==='todo'" class="flex min-w-0 flex-wrap items-center gap-1" aria-label="待办类型">
   <Button v-for="k in [{id:'',label:'全部待办'},{id:'manual',label:'待确认'},{id:'approval',label:'待审批'},{id:'code-conflict',label:'代码待处理'},{id:'exception',label:'异常待处理'}]" :key="k.id" :disabled="ignorePending" @click="kind=k.id;load(true)" :variant="kind===k.id?'secondary':'ghost'" :aria-pressed="kind===k.id" size="sm">{{k.label}}</Button>
  </div>
  <div class="inbox-filters min-w-0" :class="{'inbox-filters-with-select':view!=='todo'}" aria-label="任务筛选">
   <InputGroup class="min-w-0"><InputGroupInput :disabled="ignorePending" v-model="q" placeholder="搜索任务、应用、版本或发起人" aria-label="搜索任务、应用、版本或发起人" @keyup.enter="load(true)"/><InputGroupAddon align="inline-end"><InputGroupButton v-if="q" size="icon-xs" aria-label="清空输入" @click="q=''"><XIcon/></InputGroupButton></InputGroupAddon></InputGroup>
   <OptionPicker v-if="view==='owned'" v-model="status" :options="Object.entries(states).filter(([value])=>['pending_approval','waiting_confirmation','approved','running','success','failed','rejected','canceled'].includes(value)).map(([value,label])=>({value,label}))" all-label="全部状态" aria-label="交付状态" class="w-full" @update:model-value="load(true)"/>
   <OptionPicker v-else-if="view!=='todo'" v-model="kind" :options="Object.entries(labels).filter(([v])=>['approval','manual','code-conflict','exception'].includes(v)).map(([value,label])=>({value,label}))" all-label="全部任务" aria-label="任务类型" class="w-full"/>
   <Button @click="load(true)" :disabled="busy||ignorePending"><Spinner v-if="busy"/>搜索</Button>
  </div>
  <div v-if="view==='owned'" class="flex items-center gap-3"><span class="text-sm text-muted-foreground">个人关注</span><OptionPicker v-model="attention" :options="[{value:'active',label:'关注中'},{value:'archived',label:'已归档 / 已忽略'},{value:'all',label:'全部记录'}]" aria-label="个人关注范围" class="w-48" @update:model-value="load(true)"/></div>
  <div :aria-busy="busy" class="r-table inbox-table min-w-0 max-w-full">
   <DataTable class="[&_[data-slot=table-container]]:rounded-lg [&_[data-slot=table-container]]:border" :rows="data.items||[]" :columns="taskColumns" :loading="busy||ignorePending" table-key="research-pages-Inbox-0" row-key="id">
    <template #toolbar><div v-if="ignoreCandidates.length" class="mr-auto flex flex-wrap items-center gap-2"><span class="text-sm text-muted-foreground">已选 {{chosenExceptions.length}} 项</span><Button v-for="action in ['ignore','archive','restore'].filter(a=>ignoreCandidates.some(t=>canAttend(t,a)))" :key="action" variant="outline" size="sm" :disabled="busy||ignorePending||!chosenExceptions.some(t=>canAttend(t,action))" @click="quickIgnore(chosenExceptions,action)">{{actionLabels[action]}}</Button><span class="sr-only">只忽略本人异常待办，保留原发布状态与处理记录</span></div></template>
    <template #header-ignoreSelection><Checkbox :model-value="ignoreSelection" :disabled="busy||ignorePending" aria-label="选择本页可维护记录" @update:model-value="value=>ignoreKeys=value===true?ignoreCandidates.map(attentionSelectionKey):[]"/></template>
    <template #cell-ignoreSelection="{record}"><Checkbox v-if="ignoreCandidates.some(t=>t.id===record.id)" :model-value="ignoreKeys.includes(attentionSelectionKey(record))" :disabled="busy||ignorePending" :aria-label="'选择记录 '+record.title+' '+record.id" @update:model-value="value=>chooseException(record,value)"/></template>
    <template #cell-c0="{record}">
     <div class="min-w-0 space-y-1">
      <Button @click="open(record)" variant="link" class="h-auto min-w-0 justify-start whitespace-normal p-0 text-left leading-6" :title="record.title">{{record.title}}</Button>
      <p v-if="record.run_id||record.work_item_id||record.owner" class="text-xs text-muted-foreground break-words"><template v-if="record.run_id||record.work_item_id">{{record.run_id?(record.source_kind==='release'?'发布单':'执行单'):'工作项'}} {{record.source_kind==='release'?releaseOrderLabel({id:record.run_id,number:record.run_number}):'#'+(record.run_id||record.work_item_id)}}</template><template v-if="record.owner">{{record.run_id||record.work_item_id?' · ':''}}{{record.owner}}</template></p>
     </div>
    </template>
    <template #cell-c1="{record}"><div class="min-w-0 space-y-1.5"><p class="whitespace-normal break-words">{{record.stage||labels[record.kind]||record.kind}}</p><div class="flex flex-wrap items-center gap-1.5"><Badge :variant="record.status==='failed'?'destructive':'secondary'">{{state(record)}}</Badge><span v-if="record.progress" class="text-xs text-muted-foreground">{{record.progress}}</span></div></div></template>
    <template #cell-c2="{record}"><div class="min-w-0 space-y-1"><p class="whitespace-normal break-words">{{record.version||'独立交付'}}</p><p class="whitespace-normal break-words text-xs text-muted-foreground">{{record.environment||'—'}} · {{record.services?.join('、')||record.service||'—'}}</p></div></template>
    <template #cell-c3="{record}"><p class="whitespace-normal break-words">{{record.assignees?.join('、')||'—'}}</p></template>
    <template #cell-c4="{record}"><div class="min-w-0 space-y-1"><p class="whitespace-normal break-words leading-6">{{record.next_action||'核对当前任务并处理'}}</p><p v-if="record.attention_state==='archived'&&mandatoryTask(record)" class="text-xs text-muted-foreground">个人追踪已归档，任务仍待处理</p><p v-if="record.attention_reason" class="text-xs text-muted-foreground whitespace-normal break-words">个人处理原因：{{record.attention_reason}}</p><p v-if="record.blocked_reason" class="whitespace-normal break-words text-xs text-destructive">{{record.blocked_reason}}</p></div></template>
    <template #cell-c5="{record}"><span class="text-muted-foreground" :title="taskActivityStamp(record)">{{taskActivityAge(record,now)}}</span></template>
    <template #cell-c6="{record}"><div class="flex flex-wrap items-center gap-1"><Button @click="open(record)" :disabled="ignorePending" variant="outline" size="sm">{{view==='owned'||view==='done'?'追踪':['archived','ignored'].includes(record.attention_state)&&!mandatoryTask(record)?'查看记录':record.kind==='exception'?'核对异常':'处理'}}</Button><Button v-for="action in ['ignore','archive','restore'].filter(a=>canAttend(record,a))" :key="action" @click="quickIgnore([record],action)" :disabled="busy||ignorePending" variant="ghost" size="sm">{{actionLabels[action]}}</Button></div></template>
    <template #empty><Empty><EmptyHeader><EmptyDescription>{{busy?'正在读取任务…':me.auth_enabled===false?'当前未启用平台登录，无法识别本人待办；可先查看工作项':view==='owned'?'暂无本人发起的交付':view==='done'?'暂无处理记录；处理完成不等于交付上线':'当前没有需要本人处理的任务'}}</EmptyDescription></EmptyHeader></Empty></template>
   </DataTable>
  </div>
  <div class="flex min-w-0 flex-wrap items-center justify-between gap-3 text-sm">
   <span class="text-muted-foreground">共 {{data.total}} 项</span>
   <Pagination class="mx-0 w-auto" :disabled="busy||ignorePending" :page="page" :total="data.total" :items-per-page="20" @update:page="value=>{page=value;load()}"><PaginationContent><PaginationPrevious/><span class="px-2 text-sm">{{page}} / {{Math.max(1,Math.ceil(data.total/20))}}</span><PaginationNext/></PaginationContent></Pagination>
  </div>
 </div>
</section>
<template v-else><PageHeader :title="selected.title"><template #actions><Button @click="backToList" variant="ghost" size="default" type="button" :disabled="(busy)">← 返回原列表</Button><Button @click="reread" variant="outline" size="default" type="button" :disabled="(busy)"><Spinner v-if="busy"/>核对最新状态</Button></template></PageHeader><Card aria-live="polite" class="handoff-card"><CardContent class="min-w-0"><dl class="grid min-w-0 gap-4 sm:grid-cols-2"><div class="min-w-0 space-y-1"><dt class="text-sm text-muted-foreground">当前阶段</dt><dd class="min-w-0 break-words">{{summary.stage||labels[selected.kind]||'交付追踪'}}</dd></div><div class="min-w-0 space-y-1"><dt class="text-sm text-muted-foreground">状态</dt><dd class="min-w-0 break-words"><Badge variant="secondary">{{state(summary)}}</Badge></dd></div><div class="min-w-0 space-y-1"><dt class="text-sm text-muted-foreground">当前处理人</dt><dd class="min-w-0 break-words">{{summary.assignees?.join('、')||'—'}}</dd></div><div class="min-w-0 space-y-1"><dt class="text-sm text-muted-foreground">发起人</dt><dd class="min-w-0 break-words">{{selected.owner}}</dd></div></dl><p class="handoff-instructions">{{summary.next_action||selected.instructions||'请核对本次范围与处理要求。'}}</p><p v-if="summary.blocked_reason" class="r-error">{{summary.blocked_reason}}</p><p v-if="view==='done'&&selected.kind==='exception'" class="r-muted">{{selected.next_action}}</p><p v-if="receipt" class="r-receipt">{{receipt}}</p><p v-if="stale" class="r-error">审核快照已变化，请核对最新状态后再处理；输入的意见仍保留。</p></CardContent></Card><div v-if="!busy" class="mb-5 min-w-0 space-y-3" data-inbox-actions><template v-if="selected.kind==='exception'&&selected.resolution_digest&&view!=='done'&&selected.can_ignore"><label for="exception-comment" class="r-field">处理结果 / 忽略原因（必填）</label><Textarea v-model="comment" :disabled="busy" :aria-invalid="!!exceptionError" placeholder="填写核对结果；若不再跟进此异常，请填写忽略原因" v-bind="{&quot;id&quot;:&quot;exception-comment&quot;,&quot;aria-describedby&quot;:&quot;exception-comment-help&quot;}" :rows="({minRows:(2),maxRows:Math.max((2),16)}).minRows||4"/><small id="exception-comment-help" :class="exceptionError?'r-error':'r-muted'">{{exceptionError||`至少 6 个字符，当前 ${[...comment.trim()].length} 个；只关闭待办，不改变发布结果，不触发重试。`}}</small><div class="r-footer"><Button @click="resolveException('ignore')" variant="outline" size="default" type="button" :disabled="(busy)">忽略此异常待办</Button><Button @click="resolveException('review')" variant="default" size="default" type="button" :disabled="(busy)"><Spinner v-if="busy"/>记录异常处理结果</Button></div></template><p v-else-if="selected.kind==='exception'&&selected.resolution_digest&&view!=='done'" role="alert" class="text-sm text-muted-foreground">仍有执行结果待核对，请进入原交付处理查看日志与实例状态；核对前不能关闭此异常。</p><template v-else-if="canDecide||preparationActions||preparationActionError"><p v-if="selected.attention_state==='archived'&&mandatoryTask(selected)" class="text-sm text-muted-foreground">个人追踪已归档，当前确认任务仍需处理。</p><p v-if="preparationActionError||preparationActions?.review_blocker" role="alert" class="text-sm text-destructive">{{preparationActionError||preparationActions.review_blocker}}</p><Field v-if="canDecide||preparationActions?.can_cancel" class="r-field"><FieldLabel for="preparation-comment">处理意见 / 终止原因</FieldLabel><Textarea id="preparation-comment" v-model="comment" :disabled="busy" :placeholder="recordTask?'记录处理结果（选填）':preparationActions?.review_blocker?'填写终止旧准备的原因，至少6字':'拒绝时须填写原因'" :rows="({minRows:(2),maxRows:Math.max((2),16)}).minRows||4"/></Field><div class="r-footer flex-wrap"><Button v-if="preparationActions?.can_cancel" variant="outline" :disabled="busy" @click="cancelWaitingPreparation">终止旧准备</Button><Button v-if="canDecide" @click="recordTask?backToList():decide(false)" variant="outline" size="default" type="button" :disabled="(busy)"><Spinner v-if="busy"/>{{recordTask?'稍后处理':selected.kind==='manual'?'暂不能确认':'拒绝'}}</Button><Button v-if="canDecide" @click="decide(true)" variant="default" size="default" type="button" :disabled="(selected.kind==='manual'&amp;&amp;!recordTask&amp;&amp;!selectedIds.length&amp;&amp;eligibleItems.length>0)||(busy)"><Spinner v-if="busy"/>{{recordTask?'记录已处理，继续流程':selected.kind==='manual'?'确认本次交付内容':'同意'}}</Button></div><Button v-if="run?.status==='canceled'&&plan?.preparation_request" variant="default" :disabled="busy" @click="execution">进入原流程重新准备</Button><p v-if="preparationActions?.review_blocker&&!preparationActions?.can_cancel&&preparationActions?.cancel_blocker&&run?.status!=='canceled'" class="text-sm text-muted-foreground">{{preparationActions.cancel_blocker}}</p></template></div><div class="inbox-detail-navigation"><Tabs class="section-tabs" :model-value="detailTab" @update:model-value="detailTab=$event;"><div class="flex min-w-0 items-center gap-2"><TabsList class="h-auto! max-w-full flex-wrap gap-y-1"><TabsTrigger :value="&quot;content&quot;">交付内容</TabsTrigger><TabsTrigger :value="&quot;progress&quot;">进度与交接</TabsTrigger></TabsList></div><TabsContent :value="&quot;content&quot;" force-mount v-show="!(detailTab!==&quot;content&quot;)" class="min-w-0"></TabsContent><TabsContent :value="&quot;progress&quot;" force-mount v-show="!(detailTab!==&quot;progress&quot;)" class="min-w-0"></TabsContent></Tabs><Button v-for="action in ['ignore','archive','restore'].filter(a=>canAttend(selected,a))" :key="action" variant="outline" :disabled="busy||ignorePending" @click="quickIgnore([selected],action)">{{actionLabels[action]}}</Button><Button v-if="selected.source_kind!=='query'" @click="execution" variant="ghost" size="default" type="button">{{['pending_approval','approved'].includes(run?.status)&&run?.owner===me.username?'进入原流程处理 / 取消':plan?.template?.workbench==='research'?'继续当前交付流程':'进入原交付处理'}}</Button></div><section v-show="detailTab==='content'" class="r-surface"><p class="r-muted">{{selected.version||'独立交付'}} · {{summary.environment||'—'}} · {{selected.source_kind==='release'?releaseOrderLabel({id:selected.run_id,number:selected.run_number}):'#'+selected.run_id}} · {{selected.instructions}}</p><div v-if="plan?.scope" class="r-table"><div class="console-table min-w-0 max-w-full"><div class="min-w-0 max-w-full" :style="{maxHeight:({x:640}).y?({x:640}).y+'px':undefined}"><DataTable class="[&_[data-slot=table-container]]:rounded-lg [&_[data-slot=table-container]]:border" :rows="(((eligibleItems)||[]).map((w,rowIndex)=>({...w,__rowKey:w.id}))||[])" :columns="[{id:'c0',label:&quot;关联工作项&quot;,size:220,minSize:160,required:true,defaultHidden:false,flex:true,selection:false,pin:'left'},{id:'c1',label:&quot;解决人&quot;,size:140,minSize:64,required:false,defaultHidden:false,flex:false,selection:false,pin:false},{id:'c2',label:&quot;上线意图&quot;,size:140,minSize:64,required:false,defaultHidden:false,flex:false,selection:false,pin:false}]" table-key="research-pages-Inbox-1" :row-key="(record,rowIndex)=>typeof (&quot;__rowKey&quot;)==='function'?(&quot;__rowKey&quot;)(record):record[&quot;__rowKey&quot;]??rowIndex"><template #header-c0>关联工作项</template><template #cell-c0="{record,rowIndex}"><div class="min-w-0 break-words" :class="{truncate:!!(false)}"><template v-for="{record:w,rowIndex:rowIndex} in [{record,rowIndex}]"><div><label v-if="selected.kind==='manual'&&!recordTask&&canDecide"><Checkbox v-model="selectedIds" :value="w.id" :disabled="confirmedIds.has(w.id)" :aria-label="'确认 '+w.title"/></label><RouterLink :to="{path:'/work/'+w.id,query:{return:'inbox'}}">{{w.external_id||w.id}} · {{w.title}}</RouterLink><small v-if="!recordTask&&confirmedIds.has(w.id)">本人已确认</small></div></template></div></template><template #header-c1>解决人</template><template #cell-c1="{record,rowIndex}"><div class="min-w-0 break-words" :class="{truncate:!!(true)}"><template v-for="{record:w,rowIndex:rowIndex} in [{record,rowIndex}]"><div>{{w.resolved_by||w.owner||'—'}}</div></template></div></template><template #header-c2>上线意图</template><template #cell-c2="{record,rowIndex}"><div class="min-w-0 break-words" :class="{truncate:!!(true)}"><template v-for="{record:w,rowIndex:rowIndex} in [{record,rowIndex}]"><div>{{w.release_required===false?'不需要上线':w.release_required===true?'需要上线':'待判定'}}</div></template></div></template><template #empty><Empty><EmptyHeader><EmptyDescription>暂无数据</EmptyDescription></EmptyHeader></Empty></template></DataTable></div></div></div><DeliveryReviewContent v-if="plan" :plan="plan"/><div v-if="selected.source_kind==='query'"><p>目标：{{run?.query?.environment}} · {{run?.query?.service||run?.query?.connection}} / {{run?.query?.resource_alias}}</p><pre class="command-preview">{{run?.query?.sql||run?.query?.commands}}</pre><p class="r-muted">审批不会执行查询，申请人需另行执行。</p></div><div v-if="bundle?.release"><p>应用 {{bundle.release.service}} · 制品 #{{bundle.release.artifact_id}} · {{bundle.release.version}}</p><p>目标 {{bundle.release.environment}} · {{bundle.release.hosts?.join('、')}} · {{bundle.release.strategy}} · 每批 {{bundle.release.batch_size}} 台</p><p>已成功 {{bundle.release.succeeded}} / {{bundle.release.total}} · 失败 {{bundle.release.failed}}</p><p class="r-muted">审批或继续批次可能立即启动部署，请在原发布单核对后执行。</p></div><template v-if="selected.kind==='code-conflict'">
<PreparationScopeNotice :preparation="codeTask" :load="api" :can-manage="run?.owner===me.username||me.role==='admin'" :busy="busy" @state="scopeState=$event" @cancel="cancelPreparation"/><h3 class="text-sm font-semibold">{{codeRecovery.title||'正在核对代码准备结果'}}</h3>
<p class="r-muted">{{codeRecovery.instruction}}</p><p>{{codeTask?.message}}</p>
<ul><li v-for="r in codeTask?.repositories||[]" :key="r.target_branch">{{(r.services||[r.service]).join('、')}} → {{r.target_branch}}<small class="r-muted">{{r.conflicts?.join('、')}}</small></li></ul>
<label v-if="codeTask?.protected" class="r-check"><Checkbox v-model="scopeConfirmed"/>已核对生产代码只包含本次选中的工作项</label>
<p v-if="codeRecovery.needsRecovery&&codeRecovery.reason" id="inbox-code-recovery-reason" class="r-muted">{{codeRecovery.reason}}</p>
<div v-if="codeRecovery.needsRecovery" class="r-footer"><Button @click="resume" variant="default" size="default" type="button" :disabled="!codeRecovery.enabled" :aria-describedby="codeRecovery.reason?'inbox-code-recovery-reason':undefined"><Spinner v-if="busy"/>{{codeRecovery.actionLabel}}</Button></div>
</template></section><section v-show="detailTab==='progress'" class="r-surface"><p class="r-muted">本人处理完成不等于上线；前置确认和实际发布分别按执行台账追踪。</p><ol v-if="run?.steps" class="r-steps"><li v-for="s in run.steps" :key="s.node_id"><b>{{s.name}}</b> · {{states[s.status]||s.status}}<small class="r-muted"> · {{plan?.nodes?.find(n=>n.id===s.node_id)?.approvers?.join('、')||selected.owner}}</small><p v-if="s.detail" class="r-muted">{{s.detail}}</p><div v-for="(a,i) in s.reviews||[]" :key="i" class="r-muted">{{a.actor}}：{{a.approved?'同意':'拒绝'}} · {{a.comment}} · {{stamp(a.at)}}</div></li></ol><div v-for="child in bundle?.releases||[]" :key="child.id" class="child-release">{{child.service}} · 子发布单 {{releaseOrderLabel(child)}} · {{states[child.status]||child.status}} · 成功 {{child.succeeded}} / {{child.total}} · 失败 {{child.failed}}</div><div v-if="bundle?.linked_deliveries?.length" class="r-table linked-deliveries"><div class="console-table min-w-0 max-w-full"><div class="min-w-0 max-w-full" :style="{maxHeight:({x:800}).y?({x:800}).y+'px':undefined}"><DataTable class="[&_[data-slot=table-container]]:rounded-lg [&_[data-slot=table-container]]:border" :rows="(((bundle.linked_deliveries)||[]).map((t,rowIndex)=>({...t,__rowKey:t.id}))||[])" :columns="[{id:'c0',label:&quot;关联交付（按冻结引用关联）&quot;,size:320,minSize:64,required:true,defaultHidden:false,flex:true,selection:false,pin:'left'},{id:'c1',label:&quot;环境 / 阶段&quot;,size:200,minSize:64,required:false,defaultHidden:false,flex:false,selection:false,pin:false},{id:'c2',label:&quot;处理人&quot;,size:140,minSize:64,required:false,defaultHidden:false,flex:false,selection:false,pin:false},{id:'c3',label:&quot;操作&quot;,size:152,minSize:64,required:true,defaultHidden:false,flex:false,selection:false,pin:'right'}]" table-key="research-pages-Inbox-2" :row-key="(record,rowIndex)=>typeof (&quot;__rowKey&quot;)==='function'?(&quot;__rowKey&quot;)(record):record[&quot;__rowKey&quot;]??rowIndex"><template #header-c0>关联交付（按冻结引用关联）</template><template #cell-c0="{record,rowIndex}"><div class="min-w-0 break-words" :class="{truncate:!!(true)}"><template v-for="{record:t,rowIndex:rowIndex} in [{record,rowIndex}]"><div>{{t.title}} · #{{t.run_id}}</div></template></div></template><template #header-c1>环境 / 阶段</template><template #cell-c1="{record,rowIndex}"><div class="min-w-0 break-words" :class="{truncate:!!(true)}"><template v-for="{record:t,rowIndex:rowIndex} in [{record,rowIndex}]"><div>{{t.environment}} · {{t.stage}}<small>{{state(t)}}</small></div></template></div></template><template #header-c2>处理人</template><template #cell-c2="{record,rowIndex}"><div class="min-w-0 break-words" :class="{truncate:!!(true)}"><template v-for="{record:t,rowIndex:rowIndex} in [{record,rowIndex}]"><div>{{t.assignees?.join('、')||'—'}}</div></template></div></template><template #header-c3>操作</template><template #cell-c3="{record,rowIndex}"><div class="min-w-0 break-words" :class="{truncate:!!(false)}"><template v-for="{record:t,rowIndex:rowIndex} in [{record,rowIndex}]"><div><Button @click="open(t)" variant="ghost" size="sm" type="button">继续追踪</Button></div></template></div></template><template #empty><Empty><EmptyHeader><EmptyDescription>暂无数据</EmptyDescription></EmptyHeader></Empty></template></DataTable></div></div></div><p v-else-if="plan?.template?.workbench==='research'&&run?.status==='success'" class="r-muted">尚无通过冻结引用关联的后续发布记录；前置完成不代表已上线。</p><p v-if="!run?.steps">{{state(summary)}} · {{summary.next_action}}</p></section></template><Dialog :open="attentionDialog" @update:open="value=>{if(!ignorePending)attentionDialog=value}"><DialogContent class="max-w-xl" :show-close-button="!ignorePending" @interact-outside="event=>{if(ignorePending)event.preventDefault()}" @escape-key-down="event=>{if(ignorePending)event.preventDefault()}"><DialogHeader><DialogTitle>{{actionLabels[attentionAction]}} · {{attentionFrozen.length}} 项</DialogTitle><DialogDescription>{{attentionAction==='ignore'?'关闭本人的异常待办，同时归档对应关注。':attentionAction==='archive'?'仅归档个人交付追踪；必须处理的审批、范围确认仍保留。':'重新显示原事件的个人关注或待办；继续执行请进入原交付流程。'}}原交付状态、审批及执行保护均保留。</DialogDescription></DialogHeader><Field><FieldLabel for="attention-reason">{{attentionAction==='restore'?'恢复原因':'处理原因'}}</FieldLabel><Textarea id="attention-reason" v-model="attentionReason" :disabled="ignorePending" placeholder="请填写至少6字的原因" :rows="3"/></Field><ul v-if="attentionErrors.length" role="alert" class="max-h-48 overflow-auto space-y-2 text-sm text-destructive"><li v-for="(error,i) in attentionErrors" :key="i">{{error}}</li></ul><DialogFooter><Button variant="outline" :disabled="ignorePending" @click="attentionDialog=false">返回列表</Button><Button :disabled="ignorePending||!attentionFrozen.length" @click="confirmAttention"><Spinner v-if="ignorePending"/>确认{{actionLabels[attentionAction]}}</Button></DialogFooter></DialogContent></Dialog>
</template>
<style scoped>.next-action{max-width:320px;overflow-wrap:anywhere}.handoff{display:flex;gap:8px 20px;flex-wrap:wrap;padding:12px 16px}.handoff span{font-size:.75rem;color:var(--muted-foreground)}.handoff p{width:100%;margin:0;font-size:.875rem;overflow-wrap:anywhere}.r-receipt{color:var(--ok)}.command-preview{white-space:pre-wrap;overflow-wrap:anywhere;max-height:300px;overflow:auto}.child-release{padding:10px 0;border-bottom:1px solid var(--border)}.r-field{margin-top:16px}.r-table+.review-content{margin-top:12px}</style>

<style scoped>.handoff-card{margin-bottom:20px}.handoff-instructions{margin-top:12px;font-size:1rem;line-height:24px;overflow-wrap:anywhere}.inbox-detail-navigation{margin-bottom:16px}</style>

<style scoped>
.inbox-list-surface{min-width:0}
.inbox-filters{display:grid;grid-template-columns:minmax(0,1fr) auto;align-items:center;gap:12px}
.inbox-filters-with-select{grid-template-columns:minmax(0,1fr) 160px auto}
@media(max-width:640px){.inbox-filters-with-select{grid-template-columns:minmax(0,1fr) auto}.inbox-filters-with-select>[data-slot=input-group]{grid-column:1/-1}}
</style>
