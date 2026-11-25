<script setup>
import {computed,onBeforeUnmount,ref,watch} from 'vue'
import {PlayIcon,PauseIcon,RefreshCwIcon,PlusIcon} from '@lucide/vue'
import {api,post,operationId,stamp} from '../api.js'
import {loopStages,loopStateLabels,loopCanAdvance,loopCurrent,loopScopeRows,loopRelatedApplications,loopApplicationRows} from '../workLoop.js'
import {changeKinds} from '../workChangeOverview.js'
import {copyText} from '../../shared/clipboard.js'
import OptionPicker from '../../shared/components/OptionPicker.vue'
import {Accordion,AccordionContent,AccordionItem,AccordionTrigger,Badge,Button,Checkbox,Field,FieldLabel,Input,Spinner,Textarea,Table,TableHeader,TableHead,TableBody,TableRow,TableCell} from '../../shared/shadcn-ui.js'
const props=defineProps({item:{type:Object,required:true},acceptanceText:{type:String,default:""},taskId:{type:String,default:''},services:{type:Array,default:()=>[]},changes:{type:Array,default:()=>[]},changesLoading:Boolean})
const emit=defineEmits(['edit','updated','drafts-saved','state'])
const tasks=ref([]),selected=ref(''),creating=ref(false),busy=ref(false),error=ref(''),environments=ref([]),preview=ref(null),requestID=ref(''),summary=ref(''),selectedChanges=ref([]),resolutions=ref([]),builds=ref([]),sourceUsers=ref([]),expandedRun=ref(0),renewing=ref(false),authorizationPreview=ref(null)
const emptyForm=()=>({environment:'',applications:[],acceptance:'',complete_scope:false,resolution:'',resolved_build:'',assigned_to:''})
const form=ref(emptyForm()),addingApplication=ref(false),applicationsInitialized=ref(false)
const draftForm=ref(false)
const relatedApplications=computed(()=>draftForm.value?[...new Set(props.changes.filter(c=>c.service&&['draft','ready'].includes(c.status)).map(c=>c.service))]:loopRelatedApplications(props.changes))
const applicationRows=computed(()=>loopApplicationRows(props.changes,props.services,form.value.applications,relatedApplications.value))
const additionalApplications=computed(()=>props.services.filter(s=>!applicationRows.value.some(row=>row.name===s.name)).map(s=>({value:s.name,label:(s.display_name||s.name)+(s.display_name&&s.display_name!==s.name?' · '+s.name:'')})))
function addApplication(name){if(!name||form.value.applications.includes(name))return;form.value.applications=[...form.value.applications,name];addingApplication.value=false}
const task=computed(()=>tasks.value.find(t=>t.id===selected.value)||tasks.value[0])
const draftTask=computed(()=>task.value?.grant.spec.draft_only===true)
const iteration=computed(()=>loopCurrent(task.value))
async function follow(){await run(async seq=>{
 const value=await post('work-loops/follow',{work_item_id:props.item.id,applications:loopRelatedApplications(props.changes)})
 if(seq===generation)adopt(value)
})}
function stateLabel(row){
 if(row?.grant.spec.draft_only&&row.state==='developing')return row.draft_receipt?'登记草稿中':'已授权，尚无登记'
 return loopStateLabels[row?.state]||row?.state
}
const receiptRows=computed(()=>{
 const receipt=task.value?.draft_receipt
 if(receipt?.items?.length)return receipt.items
 return (receipt?.change_ids||[]).map(id=>({id,title:props.changes.find(c=>c.id===id)?.title}))
})
const showAllReceipts=ref(false)
watch([()=>task.value?.id,()=>task.value?.draft_receipt?.request_revision],()=>{showAllReceipts.value=false})
function copyHandoff(){
 const {id,grant}=task.value
 const kind=props.item.kind==='bug'?'Bug':'需求'
 const text=`请用 devops-change-report 持续登记${kind} #${props.item.id} 的开发变更。登记任务 ${id}，应用 ${(grant.spec.applications || []).join('、') || '待按实际变更关联'}。请先核对本人授权，随实际改动维护草稿和发布前置关系。`
 return copyText(text,{successMessage:'已复制，可粘贴到开发会话'})
}
const differenceRows=computed(()=>{const rows=[];for(const d of authorizationPreview.value?.differences||[]){if(d.field==='spec'){const labels={environment:'环境',applications:'允许应用',acceptance:'验收条件',complete_scope:'完整验收范围',draft_only:'仅登记草稿',resolution:'解决方案',resolved_build:'解决版本',assigned_to:'接收人'};for(const key of Object.keys(labels)){if(JSON.stringify(d.before?.[key])!==JSON.stringify(d.after?.[key])){const format=v=>Array.isArray(v)?v.join('、'):typeof v==='boolean'?(v?'是':'否'):String(v??'未设置');rows.push({label:labels[key],before:format(d.before?.[key]),after:format(d.after?.[key])})}}}else{const labels={policies:'代码与构建策略',regression:'绑定回归',source:'来源回写',template_id:'交付流程',workflow_digest:'流程版本'};rows.push({label:labels[d.field]||d.field,before:JSON.stringify(d.before),after:JSON.stringify(d.after)})}}return rows})
const scopeRows=computed(()=>loopScopeRows(preview.value||task.value?.grant))
const eligibleChanges=computed(()=>props.changes.filter(c=>['code','artifact'].includes(c.kind)&&c.status==='ready'&&task.value?.grant.spec.applications?.includes(c.service)))
let generation=0,timer,stopped=false
const dirty=computed(()=>!!(form.value.environment||form.value.applications.length||form.value.acceptance)||!!summary.value)
watch([dirty,busy,task],()=>emit('state',{dirty:dirty.value,busy:busy.value,active:!!task.value&&!draftTask.value&&!['completed','canceled'].includes(task.value.state)}),{immediate:true})
function adopt(value){const i=tasks.value.findIndex(t=>t.id===value.id);if(i<0)tasks.value.unshift(value);else tasks.value[i]=value;selected.value=value.id}
async function load(clearError=false){const id=props.item.id,seq=generation;try{const list=await api('work-loops?work_item_id='+id);if(seq!==generation)return;
 const refreshed=list.some(row=>{const previous=tasks.value.find(t=>t.id===row.id);return row.grant.spec.draft_only&&row.draft_receipt&&previous?.draft_receipt?.request_revision!==row.draft_receipt.request_revision});
 tasks.value=list;if(refreshed)emit('drafts-saved');if(!selected.value&&list.length)selected.value=list.some(t=>t.id===props.taskId)?props.taskId:list[0].id;if(clearError===true)error.value=''}catch(e){if(seq===generation)error.value=e.message}}
async function run(action){if(busy.value)return;busy.value=true;error.value='';const seq=generation;try{await action(seq)}catch(e){if(seq===generation)error.value=e.message}finally{if(seq===generation)busy.value=false}}
async function setup(renew=false,draft=renew&&draftTask.value){
 const nextDraft=draft
 if(nextDraft!==draftForm.value){form.value=emptyForm();applicationsInitialized.value=false;requestID.value=''}
 draftForm.value=nextDraft
 addingApplication.value=false;renewing.value=renew===true;if(!renewing.value&&!applicationsInitialized.value){form.value.applications=[...relatedApplications.value];applicationsInitialized.value=true;}authorizationPreview.value=null;if(renewing.value){const spec=task.value.grant.spec;form.value={...spec,applications:[...(spec.applications || [])],acceptance:spec.acceptance||props.acceptanceText||'本轮变更通过所选应用的绑定回归'}}form.value.acceptance ||= props.acceptanceText||'本轮变更通过所选应用的绑定回归';creating.value=true;preview.value=null;requestID.value=requestID.value||operationId();if(draftForm.value)return;await run(async(seq)=>{const envs=(await api('environments')).filter(e=>!e.protected&&['development','test'].includes(e.purpose));if(seq!==generation)return;environments.value=envs;if(!form.value.environment&&envs.length===1)form.value.environment=envs[0].name;})}
watch(()=>[creating.value,draftForm.value,form.value.complete_scope,props.item.id],async(_,prior,onCleanup)=>{
 const item=props.item
 if(!creating.value||draftForm.value||!form.value.complete_scope||item.kind!=='bug'||!item.source?.startsWith('zentao:'))return
 let active=true;onCleanup(()=>{active=false});resolutions.value=[];builds.value=[];sourceUsers.value=[]
 try {const options=await api('research/work-items/'+item.id+'/action-options?action=resolve');if(!active)return
 const choices=v=>Object.entries(v||{}).map(([value,label])=>({value,label}));resolutions.value=choices(options.resolutions||options.resolutionList); builds.value=choices(options.builds);sourceUsers.value=choices(options.users)
 }catch(e){if(active)error.value=e.message}
})
async function prepare(){await run(async(seq)=>{const spec=draftForm.value?{draft_only:true,work_item_id:props.item.id,applications:form.value.applications}:{work_item_id:props.item.id,environment:form.value.environment,applications:form.value.applications,acceptance:form.value.acceptance,complete_scope:Boolean(form.value.complete_scope),...(form.value.complete_scope?{resolution:form.value.resolution,resolved_build:form.value.resolved_build,assigned_to:form.value.assigned_to}:{})};const value=await post(renewing.value?'work-loops/'+task.value.id+'/authorization/preview':'work-loops/preview',renewing.value?{revision:task.value.revision,spec}:spec);if(seq===generation){authorizationPreview.value=renewing.value?value:null;preview.value=renewing.value?value.grant:value}})}
async function authorize(){await run(async(seq)=>{const value=await post(renewing.value?'work-loops/'+task.value.id+'/authorization':'work-loops',renewing.value?{revision:authorizationPreview.value.revision,spec:preview.value.spec,expected_digest:authorizationPreview.value.digest}:{id:requestID.value,spec:preview.value.spec,expected_digest:preview.value.digest});if(seq!==generation)return;adopt(value);creating.value=false;preview.value=null;requestID.value='';form.value=emptyForm();applicationsInitialized.value=false})}
async function control(action){await run(async(seq)=>{const value=await post('work-loops/'+task.value.id+'/control',{revision:task.value.revision,action,...(action==='claim'?{claimant:'本人页面接手'}:{})});if(seq===generation)adopt(value)})}
async function start(){await run(async(seq)=>{const value=await post('work-loops/'+task.value.id+'/start',{revision:task.value.revision,change_ids:selectedChanges.value,summary:summary.value});if(seq!==generation)return;adopt(value);summary.value='';selectedChanges.value=[]})}
async function advance(){await run(async(seq)=>{const value=await post('work-loops/'+task.value.id+'/advance',{revision:task.value.revision});if(seq===generation){adopt(value);if(value.stage==='acceptance')emit('updated')}})}
async function tick(){if(stopped)return;try{if(!busy.value&&!creating.value&&!document.hidden){if(!task.value?.authority&&(loopCanAdvance(task.value)||task.value?.state==='awaiting_acceptance')&&!error.value)await advance();else await load()}}finally{if(!stopped)timer=setTimeout(tick,4000)}}
watch(()=>props.item.id,async()=>{generation++;clearTimeout(timer);busy.value=false;error.value='';tasks.value=[];selected.value='';creating.value=false;addingApplication.value=false;applicationsInitialized.value=false;preview.value=null;summary.value='';selectedChanges.value=[];expandedRun.value=0;form.value=emptyForm();await load();if(!stopped)timer=setTimeout(tick,4000)},{immediate:true})
watch(()=>props.taskId,value=>{if(value&&!dirty.value&&!busy.value&&tasks.value.some(t=>t.id===value))selected.value=value})
watch(()=>task.value?.id+':'+task.value?.state,(value,old)=>{if(old&&value!==old&&task.value?.stage==='acceptance')emit('updated')})
watch(selected,(value,old)=>{if(old&&value!==old){summary.value='';selectedChanges.value=[];expandedRun.value=0}})
watch(form,()=>{preview.value=null},{deep:true})
onBeforeUnmount(()=>{stopped=true;generation++;clearTimeout(timer)})
const selectApp=(app,on)=>{form.value.applications=on?[...form.value.applications,app]:form.value.applications.filter(x=>x!==app)}
const selectChange=(id,on)=>{selectedChanges.value=on?[...selectedChanges.value,id]:selectedChanges.value.filter(x=>x!==id)}
</script>
<template>
<section class="work-loop-panel space-y-4 border-b pb-5 mb-5" aria-label="需求与 Bug 跟进任务" :aria-busy="busy">
 <header class="flex items-center justify-between gap-4"><div class="flex items-center gap-3"><h3 class="text-base font-semibold">跟进进度</h3><Badge v-if="task" :variant="task.state==='blocked'?'destructive':'secondary'">{{stateLabel(task)}}</Badge><Spinner v-if="busy"/></div><div class="flex gap-2"><Button v-if="task&&!creating" variant="ghost" size="sm" :disabled="busy" @click="load(true)"><RefreshCwIcon/>刷新</Button><Button v-if="!creating&&!task" variant="ghost" size="sm" :disabled="busy||changesLoading" @click="follow">接手开发</Button><Button v-if="!creating&&(!task||draftTask&&!task.revoked&&!task.paused)" variant="outline" size="sm" :disabled="busy||changesLoading" @click="setup(!!task,false)">交付并回归</Button></div></header>
 <p v-if="!task&&!creating" class="text-sm text-muted-foreground">尚无开发记录。</p>
 <p v-if="error" role="alert" class="text-sm text-destructive break-words">{{error}} <Button variant="ghost" size="sm" :disabled="busy" @click="load(true)">核对最新记录</Button></p>
 <form v-if="creating&&!preview" class="space-y-4 max-w-5xl" @submit.prevent="prepare">
  <Field v-if="!draftForm" class="max-w-sm"><FieldLabel>目标环境</FieldLabel><OptionPicker v-model="form.environment" :options="environments.map(e=>({value:e.name,label:e.display_name||e.name}))" placeholder="选择开发／测试环境" :disabled="busy"/></Field>
  <Field>
   <div class="flex items-center justify-between gap-4"><FieldLabel>{{draftForm?'允许登记草稿的应用':'本轮交付应用'}}</FieldLabel><span class="text-xs text-muted-foreground" aria-live="polite">已选 {{form.applications.length}} 项</span></div>
   <p v-if="applicationRows.length" class="text-sm text-muted-foreground">{{draftForm?'选择 Agent 可以为此工作项维护草稿的应用。':renewing?'保留原授权范围；新增关联应用需勾选后确认差异。':'已根据当前工作项的代码／制品变更选择应用，可取消本轮不涉及的应用。'}}</p>
   <div v-if="applicationRows.length" class="grid grid-cols-2 gap-x-6 gap-y-2" role="group" aria-label="本轮交付应用">
    <label v-for="app in applicationRows" :key="app.name" class="flex min-w-0 items-start gap-3 rounded-md border px-3 py-2 text-sm">
     <Checkbox class="mt-0.5" :aria-label="app.label+'（'+app.name+'）'" :model-value="form.applications.includes(app.name)" :disabled="busy" @update:model-value="selectApp(app.name,$event===true)"/>
     <span class="min-w-0"><span class="block break-words font-medium">{{app.label}}</span><span v-if="app.label!==app.name" class="block break-all text-xs text-muted-foreground">{{app.name}}</span></span>
     <span v-if="!app.related" class="ml-auto shrink-0 text-xs text-muted-foreground">{{renewing&&task.grant.spec.applications?.includes(app.name)?'原授权':'补充应用'}}</span>
    </label>
   </div>
   <p v-else class="text-sm text-muted-foreground">尚未识别到可交付的应用，可补充本次涉及的应用。</p>
   <p v-if="form.applications.length>10" role="alert" class="text-sm text-destructive">每次最多授权 10 个应用，请取消本轮不涉及的应用。</p>
   <div v-if="addingApplication" class="flex max-w-lg items-center gap-2"><OptionPicker :model-value="''" :options="additionalApplications" :retain-missing="false" aria-label="搜索补充应用" placeholder="搜索应用名称或标识" :disabled="busy" @update:model-value="addApplication"/><Button type="button" variant="ghost" :disabled="busy" @click="addingApplication=false">取消</Button></div>
   <Button v-else-if="additionalApplications.length" type="button" variant="ghost" size="sm" class="self-start" :disabled="busy||form.applications.length>=10" @click="addingApplication=true"><PlusIcon/>补充应用</Button>
  </Field>
  <Field v-if="!draftForm"><FieldLabel for="loop-acceptance">本次验收条件</FieldLabel><Textarea id="loop-acceptance" v-model="form.acceptance" :disabled="busy" placeholder="描述本轮完成范围及必须通过的回归要求" :maxlength="4000"/></Field>
  <div v-if="!draftForm&&form.complete_scope&&resolutions.length" class="grid grid-cols-3 gap-4"><Field><FieldLabel>来源解决方案</FieldLabel><OptionPicker v-model="form.resolution" :options="resolutions" :disabled="busy"/></Field><Field><FieldLabel>来源解决版本</FieldLabel><OptionPicker v-model="form.resolved_build" :options="builds" :disabled="busy"/></Field><Field><FieldLabel>回归接收人</FieldLabel><OptionPicker v-model="form.assigned_to" :options="sourceUsers" :disabled="busy"/></Field></div>
  <label v-if="!draftForm" class="flex items-center gap-2 text-sm"><Checkbox v-model="form.complete_scope" :disabled="busy"/>回归通过后将整个工作项更新为{{item.kind==='bug'?'已解决':'开发完成／待验收'}}</label>
  <div class="flex gap-2"><Button type="submit" :disabled="busy||changesLoading||!form.applications.length||form.applications.length>10">预览范围</Button><Button type="button" variant="ghost" :disabled="busy" @click="creating=false">收起</Button></div>
 </form>
 <div v-if="creating&&preview" class="space-y-4">
  <p v-if="draftForm" class="text-sm">仅登记本人草稿 · {{ (preview.spec.applications || []).join('、') || '尚未关联应用' }}</p><p v-else class="text-sm">{{preview.spec.environment}} · 回写目标：{{preview.source.label}}</p>
  <Table v-if="!draftForm" class="table-fixed"><TableHeader><TableRow><TableHead>应用</TableHead><TableHead>合并目标</TableHead><TableHead>构建方案</TableHead></TableRow></TableHeader><TableBody><TableRow v-for="row in scopeRows" :key="row.app"><TableCell class="break-words whitespace-normal">{{row.app}}</TableCell><TableCell class="break-words whitespace-normal">{{row.mode}} · {{row.target}}</TableCell><TableCell class="break-words whitespace-normal">{{row.variant}}</TableCell></TableRow></TableBody></Table>
  <p v-if="!draftForm" class="text-sm text-muted-foreground">授权保存本人变更、准备代码、构建、开发／测试交付、指定回归和来源回写；关闭页面后平台继续推进，可随时暂停或撤销后续授权。涉及其他人的审批、资源写入或未知结果时暂停。</p>
  <div v-if="authorizationPreview" class="space-y-3"><p v-if="!draftForm" class="text-sm">授权渠道：{{authorizationPreview.previous_authority==='mcp'?'原 MCP Key':authorizationPreview.previous_authority==='page'?'本人页面':'旧任务（未启用后台）'}} → 本人页面；后续自动推进由当前启用账号授权。</p><p class="text-sm font-medium">{{authorizationPreview.new_iteration?'范围变化将开始新轮次，原执行记录保留':'续授权保留当前轮次及原执行快照'}}</p><Table class="table-fixed"><TableHeader><TableRow><TableHead class="w-40">变更项</TableHead><TableHead>原授权</TableHead><TableHead>本次授权</TableHead></TableRow></TableHeader><TableBody><TableRow v-for="row in differenceRows" :key="row.label"><TableCell>{{row.label}}</TableCell><TableCell class="whitespace-pre-wrap break-all text-sm">{{row.before}}</TableCell><TableCell class="whitespace-pre-wrap break-all text-sm">{{row.after}}</TableCell></TableRow></TableBody></Table></div>
  <p v-if="!draftForm" class="text-sm">验收条件：{{preview.spec.acceptance}}</p>
  <div class="flex gap-2"><Button :disabled="busy" @click="authorize">{{renewing?'确认范围变更':draftForm?'授权持续登记':'确认交付范围'}}</Button><Button variant="outline" :disabled="busy" @click="preview=null">修改范围</Button></div>
 </div>
 <template v-if="task&&!creating">
  <OptionPicker v-if="tasks.length>1" v-model="selected" :disabled="busy||dirty" class="max-w-md" :options="tasks.map(t=>({value:t.id,label:(t.grant.spec.draft_only?'草稿登记':t.grant.spec.environment)+' · '+stamp(t.updated_at)+' · '+stateLabel(t)}))"/>
  <ol v-if="!draftTask" class="grid grid-cols-6 gap-2 border-b pb-3 text-sm" aria-label="跟进阶段"><li v-for="[key,label] in loopStages" :key="key" :aria-current="task.stage===key?'step':undefined" :class="task.stage===key?'font-semibold text-foreground':'text-muted-foreground'">{{label}}</li></ol>
  <div class="flex justify-between items-start gap-6"><div class="min-w-0 space-y-2"><p class="text-sm font-medium break-words">{{draftTask?(task.revoked?'已撤销草稿登记':task.paused?'已暂停草稿登记':'Agent 可在开发过程中新增或修订本人草稿'):task.message}}</p><p class="text-xs text-muted-foreground">{{draftTask?(task.grant.spec.applications || []).join('、') || '尚未关联应用':`第 ${iteration.number} 轮 · ${task.grant.spec.environment} · ${task.claimant||'等待人员或 Agent 接手'}`}} · {{stamp(task.updated_at)}}</p></div><div class="flex shrink-0 gap-2"><Button v-if="!task.revoked" variant="outline" size="sm" :disabled="busy||!!summary.trim()" @click="setup(true)">调整范围</Button><Button v-if="task.state==='running'||(draftTask&&task.state==='developing')" variant="outline" size="sm" :disabled="busy" @click="control('pause')"><PauseIcon/>暂停</Button><Button v-if="task.state==='paused'||task.state==='blocked'||task.state==='waiting_human'" size="sm" :disabled="busy||task.revoked" @click="control('resume')"><PlayIcon/>核对并继续</Button><Button v-if="['awaiting_acceptance','completed','blocked'].includes(task.state)" size="sm" :disabled="busy" @click="control('next_iteration')">继续开发／补充修复</Button></div></div>
  <div v-if="['blocked','waiting_human'].includes(task.state)&&(iteration.delivery_id||iteration.preparation_id)" class="flex items-center gap-3"><Button variant="outline" size="sm" @click="expandedRun=iteration.delivery_id||iteration.preparation_id">查看当前节点与恢复操作</Button><span class="text-sm text-muted-foreground">在下方处理原执行单，完成后核对并继续。</span></div>
  <div v-if="draftTask" class="space-y-2 text-sm">
   <div class="flex flex-wrap items-center gap-2"><Button v-if="!task.revoked&&!task.paused" size="sm" variant="outline" @click="copyHandoff">复制给开发 Agent</Button></div>
   <template v-if="receiptRows.length"><p>最近登记 {{receiptRows.length}} 项<span v-if="task.draft_receipt.saved_at"> · {{stamp(task.draft_receipt.saved_at)}}</span></p>
    <ul><li v-for="row in (showAllReceipts?receiptRows:receiptRows.slice(0,5))" :key="row.id"><Button variant="link" size="sm" class="h-auto whitespace-normal text-left" @click="emit('edit',row.id)">{{row.created===undefined?'':row.created?'新增':'更新'}} {{changeKinds[row.kind]||row.kind}} #{{row.id}} {{row.title}}</Button></li></ul>
    <Button v-if="receiptRows.length>5" size="sm" variant="ghost" :aria-expanded="showAllReceipts" @click="showAllReceipts=!showAllReceipts">{{showAllReceipts?'收起':'查看全部 '+receiptRows.length+' 项'}}</Button>
   </template>
  </div>
  <div v-if="!draftTask&&task.stage==='development'&&!task.revoked" class="space-y-4">
   <div class="flex gap-2"><Button variant="outline" :disabled="busy" @click="control('claim')">本人接手</Button><span class="self-center text-xs text-muted-foreground">Agent 可使用任务编号接续；页面操作不需要 MCP Key。</span></div>
   <Field><FieldLabel>本轮就绪变更</FieldLabel><p v-if="!eligibleChanges.length" class="text-sm text-muted-foreground">尚无可交付的就绪代码变更。先登记并校验就绪，再选择本轮内容。</p><label v-for="change in eligibleChanges" :key="change.id" class="flex items-center gap-2 text-sm"><Checkbox :model-value="selectedChanges.includes(change.id)" :disabled="busy" @update:model-value="selectChange(change.id,$event===true)"/>#{{change.id}} · {{change.service}} · {{change.title}} · {{change.reference}}</label></Field>
   <Field><FieldLabel :for="'loop-summary-'+task.id">开发与本地验证说明</FieldLabel><Textarea :id="'loop-summary-'+task.id" v-model="summary" :disabled="busy" placeholder="填写本轮改动、提交及已执行的本地验证；平台仍会运行绑定制品的回归" :maxlength="4000"/></Field>
   <Button :disabled="busy||!selectedChanges.length||!summary.trim()" @click="start"><PlayIcon/>启动自动交付</Button>
  </div>
  <Accordion type="single" collapsible><AccordionItem value="records"><AccordionTrigger>{{draftTask?'草稿登记授权记录':`授权与执行记录 · ${task.iterations.length} 轮`}}</AccordionTrigger><AccordionContent><div class="space-y-4"><p v-if="!draftTask" class="text-sm text-muted-foreground">{{task.authority?'平台在授权有效期间自动衔接阶段，关闭页面后仍会继续；暂停或撤销会停止后续派发。':'此任务尚未授权后台推进，请通过调整范围开启。'}}</p><p class="text-xs break-all">任务编号：{{task.id}} · 授权人：{{task.owner}}</p><div v-for="it in (draftTask?[]:task.iterations)" :key="it.number" class="flex flex-wrap items-center gap-3 text-sm"><span>第 {{it.number}} 轮 · {{it.summary||'开发中'}}</span><Button v-if="it.preparation_id" size="sm" variant="outline" @click="expandedRun=expandedRun===it.preparation_id?0:it.preparation_id">准备单 #{{it.preparation_id}}</Button><Button v-if="it.delivery_id" size="sm" variant="outline" @click="expandedRun=expandedRun===it.delivery_id?0:it.delivery_id">执行单 #{{it.delivery_id}}</Button><span v-for="report in it.reports||[]" :key="report.service+':'+report.run_id">{{report.service}} · 回归 #{{report.run_id}}</span></div><ol class="space-y-2 text-sm"><li v-for="(event,index) in task.events.slice(-12)" :key="index"><span class="text-muted-foreground mr-3">{{stamp(event.at)}}</span>{{event.message}}</li></ol><Button variant="outline" size="sm" :disabled="busy||task.revoked" @click="control('revoke')">撤销后续自动化授权</Button></div></AccordionContent></AccordionItem></Accordion>
  <iframe v-if="expandedRun" :key="expandedRun" :src="'/api/plugins/deploy/ui?embedded=1#/orchestration?run='+expandedRun" class="w-full min-h-[640px] border rounded-lg" title="原交付执行单及恢复操作"/>
 </template>
</section>
</template>
