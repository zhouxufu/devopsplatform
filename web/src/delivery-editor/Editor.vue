<script setup>
import {computed, inject, nextTick, onBeforeUnmount, ref, watch} from 'vue';
import {deliveryDrafts} from '../deploy/deliveryDrafts.js';



import {confirmAction} from '../shared/shadcnFeedback.js';

import BranchField from './BranchField.vue';
import FeatureBranch from './FeatureBranch.vue';
import NacosFields from './NacosFields.vue';
import SQLFields from './SQLFields.vue';

import OwnershipField from './OwnershipField.vue';
import {deliveryRows, expandDeliveryRows} from '../deploy/deliveryContent.js';
import {deliveryEntryProgress,deliveryResourceOptions,savedDeliverySelection} from '../deploy/deliveryEntry.js';
import {personalView} from '../shared/viewMemory.js';
import {applicationOption} from '../shared/applicationName.js';
import {deliveryEditorGroups,changedDeliveryRowKeys} from '../deploy/deliveryEditorList.js';
import {validateDeliveryChange, locateDeliveryServerError} from '../deploy/deliveryValidation.js';
import {toast} from '../shared/shadcnFeedback.js';
const props=defineProps({inline:Boolean,embeddedSubmit:Boolean,hideNextSteps:Boolean,show:Boolean,batch:Boolean,item:Object,services:Array,workItems:Array,canManage:Function,actor:String,workspace:String});
const emit=defineEmits(['close','saved','open-sync','state','configure','draft','next-step']);
const api=inject('api'),rows=ref([]),baseline=ref(''),initial=ref(''),loading=ref(false),saving=ref(false),error=ref(''),also=ref([]),resources=ref({}),linksOpen=ref(false);
const branchBusy=ref(false);
const fullscreen=ref(false);
let displayMemory;
watch(()=>[props.actor,props.workspace],()=>{if(!props.actor)return;try{displayMemory=personalView({view:'delivery-editor',actor:props.actor,workspace:props.workspace,local:window.localStorage,session:window.sessionStorage});fullscreen.value=displayMemory.read({fullscreen:false}).fullscreen}catch{}},{immediate:true});
watch(fullscreen,value=>displayMemory?.write({fullscreen:value},{fullscreen:false}));
let session=0,seq=0;const loadedDraftKey=ref(null);
const types=[{value:'code',label:'代码与应用',icon:Code2Icon},{value:'mysql',label:'SQL 变更',icon:DatabaseIcon},{value:'nacos',label:'Nacos 配置',icon:FileCogIcon},{value:'redis',label:'Redis 命令',icon:TerminalIcon},{value:'manual',label:'自定义记录',icon:NotebookPenIcon}];
const serialized=()=>JSON.stringify({rows:rows.value,also:also.value});
const dirty=computed(()=>!!initial.value&&initial.value!==serialized());
const progress=computed(()=>deliveryEntryProgress(rows.value,dirty.value));
const markingReady=ref(false);
watch([dirty,saving,loading,branchBusy],()=>emit('state',{dirty:dirty.value,busy:saving.value||loading.value||branchBusy.value}));
const activeKey=ref(''),rowSearch=ref(''),visitedKeys=ref(new Set());
const sqlScripts=ref({});
function sqlDraftCache(row){return sqlScripts.value[row.key]||(sqlScripts.value[row.key]={})}
const rowGroups=computed(()=>deliveryEditorGroups(rows.value,rowSearch.value));
const activeRow=computed(()=>rows.value.find(r=>r.key===activeKey.value)||rows.value[0]);
watch(()=>activeRow.value?.key,key=>{if(key)visitedKeys.value.add(key)},{immediate:true,flush:'sync'});
const mountedRows=computed(()=>rows.value.filter(row=>row.key===activeRow.value?.key||visitedKeys.value.has(row.key)));
const changedRows=computed(()=>changedDeliveryRowKeys(rows.value,initial.value?JSON.parse(initial.value).rows:[]));
const locked=computed(()=>saving.value||refreshing.value||loading.value||branchBusy.value);
const canReferenceSync=computed(()=>!props.batch&&!props.embeddedSubmit);
const rowTitle=(row,index)=>row.title|| (row.originals.length?'已有内容 #'+row.originals.map(c=>c.id).join('、'):(types.find(t=>t.value===row.kind)?.label||'变更')+' '+(index+1));
const rowTarget=row=>row.kind==='code'?(row.services||[]).join('、')||'待选择应用':[row.service,(row.sql||row.nacos||row.redis)?.resource_alias].filter(Boolean).join(' / ')||(row.kind==='manual'?'人工处理':'待补齐');
const rowKind=row=>types.find(t=>t.value===row.kind)?.label||({'artifact':'制品引用','data-sync':'同步任务'})[row.kind]||'变更';
const readonlyReason=row=>row.status==='ready'?'已就绪，仅可查看':row.delivery_ownership?.origin_work_item_id&&row.delivery_ownership.origin_work_item_id!==props.item?.id?'来源于其他工作项，仅可查看':(row.owner?'由 '+row.owner+' 维护，':'')+'仅可查看';
const links=computed(()=>(props.workItems||[]).filter(w=>w.id!==props.item?.id).map(w=>({value:w.id,label:'#'+w.id+' '+w.title})));
const drafts=ref(deliveryDrafts);
const draftKey=computed(()=>props.batch?'independent-batch':props.item?.id);
const refreshing=ref(false),editor=ref(null),invalid=ref(null),toolbar=ref(null);
function selectRow(key){if(!locked.value)activeKey.value=key}
function openSync(){if(locked.value)return;stash();emit('open-sync')}
async function locateAddedRow(key){activeKey.value=key;rowSearch.value='';await nextTick();const article=[...(editor.value?.querySelectorAll('[data-row-key]')||[])].find(e=>e.dataset.rowKey===key);if(!article)return;article.scrollIntoView({block:'start',behavior:matchMedia('(prefers-reduced-motion: reduce)').matches?'auto':'smooth'});article.querySelector('input:not(:disabled),textarea:not(:disabled)')?.focus({preventScroll:true})}
function clearMarkers(){editor.value?.querySelectorAll('.invalid-field').forEach(e=>e.classList.remove('invalid-field'));editor.value?.querySelectorAll('[aria-invalid]').forEach(e=>e.removeAttribute('aria-invalid'));invalid.value=null}
function fieldSnapshot(location){const row=rows.value.find(r=>r.key===location.rowKey);if(!row)return '';let value=row[location.field];if(Number.isInteger(location.entry))value=row.nacos?.entries?.[location.entry]?.[location.field];else if(location.field==='resource')value=(row.sql||row.nacos||row.redis)?.resource_alias;else if(location.field==='sql_environment_overrides')value=row.sql?.environment_overrides;else if(location.field==='sql')value=row.sql?.content;else if(location.field==='max_affected_rows')value=row.sql?.max_affected_rows;else if(['group','format','data_id','entries'].includes(location.field))value=row.nacos?.[location.field];else if(location.field==='commands')value=row.redis?.commands;return JSON.stringify(value)}
let invalidSnapshot=''
watch(serialized,()=>{if(invalid.value&&fieldSnapshot(invalid.value)!==invalidSnapshot)clearMarkers()})
watch(error,message=>{if(message)toast(message,'error',{duration:6000})});
function clearInvalid(event){const target=editor.value?.querySelector('.invalid-field');if(target?.contains(event.target)){target.classList.remove('invalid-field');target.querySelectorAll('[aria-invalid]').forEach(e=>e.removeAttribute('aria-invalid'));invalid.value=null}}
async function reportInvalid(location){
 invalid.value=location;invalidSnapshot=fieldSnapshot(location);const row=rows.value.find(r=>r.key===location.rowKey);if(!row){toast(location.message,'error',{duration:6000});return}
 activeKey.value=row.key;rowSearch.value='';await nextTick();
 const article=[...editor.value.querySelectorAll('[data-row-key]')].find(e=>e.dataset.rowKey===row.key);
 editor.value.querySelectorAll('.invalid-field').forEach(e=>e.classList.remove('invalid-field'));
 editor.value.querySelectorAll('[aria-invalid]').forEach(e=>e.removeAttribute('aria-invalid'));
 let scope=article;if(Number.isInteger(location.entry))scope=article?.querySelector('[data-entry-index="'+location.entry+'"]')||article;
 const target=scope?.querySelector('[data-field="'+location.field+'"]')||scope;
 if(target){target.classList.add('invalid-field');const input=target.querySelector('input:not(:disabled),textarea:not(:disabled),button[aria-haspopup="listbox"]:not(:disabled)')||target.querySelector('button:not(:disabled)');input?.setAttribute('aria-invalid','true');target.scrollIntoView({block:'center',behavior:matchMedia('(prefers-reduced-motion: reduce)').matches?'auto':'smooth'});input?.focus({preventScroll:true})}
 toast(location.message,'error',{duration:6000});
}
async function loadResources(name,row){if(!name)return;try{const r=await api(`api/services/${encodeURIComponent(name)}/resources`);resources.value={...resources.value,[name]:r};if(row&&row.service===name){const target=row.sql||row.nacos||row.redis,choices=(r.requirements||[]).filter(r=>r.kind===row.kind);if(target&&!target.resource_alias&&choices.length===1)target.resource_alias=choices[0].alias}}catch(e){toast(e.message,'error')}}
function serviceChanged(row){const target=row.sql||row.nacos||row.redis;if(target)target.resource_alias='';loadResources(row.service,row)}
function options(row,kind){return deliveryResourceOptions(resources.value[row.service],kind)}
function resourceTargets(row){const alias=(row.sql||row.nacos||row.redis)?.resource_alias;return (resources.value[row.service]?.bindings||[]).filter(b=>b.alias===alias)}
function nextStep(action){if(!locked.value&&progress.value.canContinue&&!props.batch&&!props.hideNextSteps)emit('next-step',action)}
function canReopen(row){return row.status==='ready'&&row.originals.length===1&&props.canManage(row.originals[0])&&!row.sql?.data_sync&&(!row.delivery_ownership||row.delivery_ownership.origin_work_item_id===props.item?.id)}
async function reopen(row){
 if(locked.value)return;if(dirty.value)return toast('请先保存当前未提交内容，再退回修改已就绪变更','warning');saving.value=true;
 try{await api('api/changes/'+row.originals[0].id+'/reopen',{method:'POST',headers:{'Content-Type':'application/json'},body:'{}'});await load();emit('saved');toast('已退回草稿，可在当前编辑器继续修改','success')}catch(e){toast(e.message,'error')}finally{saving.value=false}
}
function restoreDraft(draft){rows.value=draft.rows;also.value=draft.also;baseline.value=draft.baseline;initial.value=draft.initial;sqlScripts.value=draft.sqlScripts||{};activeKey.value=draft.activeKey||'';rowSearch.value=draft.rowSearch||'';drafts.value.delete(draftKey.value)}
async function load(){
 const current=++session,sameContext=loadedDraftKey.value===draftKey.value,selected=sameContext?activeRow.value:null;loadedDraftKey.value=draftKey.value;error.value='';const draft=drafts.value.get(draftKey.value);
 if(!sameContext){rows.value=[];also.value=[];initial.value='';baseline.value='';sqlScripts.value={}}
 if(props.batch){rows.value=[];also.value=[];baseline.value='';initial.value=serialized();loading.value=false;if(draft)restoreDraft(draft)}
 else{
  loading.value=true;
  try{const data=await api(`api/work-items/${props.item.id}/delivery-content`);if(current!==session||!props.show)return;rows.value=deliveryRows(data.changes,props.canManage,props.services,props.item?.id);baseline.value=data.digest;also.value=[];initial.value=serialized();
   if(draft){restoreDraft(draft);mergeSyncedChanges(data)}
   else if(selected)activeKey.value=rows.value.find(r=>r.key===selected.key)?.key||rows.value.find(r=>r.kind===selected.kind&&r.title===selected.title)?.key||'';
  }catch(e){if(current===session)error.value=e.message}finally{if(current===session)loading.value=false}
 }
 if(current===session)for(const name of new Set(rows.value.map(r=>r.service).filter(Boolean)))loadResources(name)
}
watch(()=>props.show,show=>{if(show){activeKey.value='';rowSearch.value='';visitedKeys.value=new Set();linksOpen.value=false;load()}else{stash();session++}},{immediate:true});
function add(kind='code'){if(locked.value)return;const previous=activeRow.value||rows.value.at(-1),service=previous?.service||previous?.services?.[0]||((props.services||[]).length===1?props.services[0].name:'');let key;do{key='new-'+(++seq)}while(rows.value.some(row=>row.key===key));rows.value.push({key,kind,services:service?[service]:[],originals:[],service,reference:'',summary:'',risk_level:'medium',rollback_plan:'',delivery_ownership:props.batch?undefined:{mode:props.item?.release_required===true?'self':'draft',origin_work_item_id:props.item?.id,work_item_id:props.item?.release_required===true?props.item.id:0},sql:kind==='mysql'?{resource_alias:'',content:'',max_affected_rows:100}:undefined,nacos:kind==='nacos'?{resource_alias:'',data_id:'',group:'DEFAULT_GROUP',format:'yaml',entries:[{key:'',value:'',action:'set'}]}:undefined,redis:kind==='redis'?{resource_alias:'',commands:''}:undefined});if(service)loadResources(service,rows.value.at(-1));locateAddedRow(key)}
watch(()=>rows.value.map(r=>r.kind),()=>{for(const r of rows.value)if(r.kind==='redis'&&!r.redis)r.redis={resource_alias:'',commands:''}});
function close(){if(saving.value||refreshing.value||branchBusy.value)return;stash();emit('close')}
function stash(){if(loadedDraftKey.value===null)return;if(dirty.value)drafts.value.set(loadedDraftKey.value,{rows:JSON.parse(JSON.stringify(rows.value)),also:[...also.value],baseline:baseline.value,initial:initial.value,sqlScripts:JSON.parse(JSON.stringify(sqlScripts.value)),activeKey:activeRow.value?.key,rowSearch:rowSearch.value});else if(!loading.value&&!error.value)drafts.value.delete(loadedDraftKey.value)}
function mergeSyncedChanges(data){
 const originals=rows.value.flatMap(r=>r.originals),existing=new Map(data.changes.map(c=>[c.id,c]));
 if(originals.some(c=>JSON.stringify(existing.get(c.id))!==JSON.stringify(c))){error.value='已有交付内容同时发生变化，未保存输入已保留；请核对后重新载入';return}
 const known=new Set(originals.map(c=>c.id)),added=deliveryRows(data.changes.filter(c=>!known.has(c.id)),props.canManage,props.services,props.item?.id);
 rows.value.push(...added);const before=JSON.parse(initial.value);before.rows.push(...added);initial.value=JSON.stringify(before);baseline.value=data.digest;
}
async function refreshAfterSync(){
 if(!props.show||props.batch||saving.value||refreshing.value)return;const current=session;refreshing.value=true;
 try{const data=await api(`api/work-items/${props.item.id}/delivery-content`);if(current!==session)return;mergeSyncedChanges(data);
 }catch(e){error.value=e.message}finally{refreshing.value=false}
}
defineExpose({snapshotDraft:()=>{stash();return drafts.value.get(draftKey.value)},refreshResources:()=>loadResources(activeRow.value?.service),hasDraft:()=>drafts.value.has(draftKey.value)||(props.show&&dirty.value),refreshAfterSync,saveDrafts:()=>save(true)});
async function discard(){if(locked.value)return;if(dirty.value&&!await confirmAction('放弃当前未保存内容并重新读取？','重新载入',{confirmButtonText:'放弃并载入',cancelButtonText:'继续编辑'}))return;drafts.value.delete(draftKey.value);rows.value=[];also.value=[];initial.value=serialized();load()}
function unload(e){if(props.show&&dirty.value){e.preventDefault();e.returnValue=''}}
window.addEventListener('beforeunload',unload);onBeforeUnmount(()=>{if(initial.value)stash();window.removeEventListener('beforeunload',unload)});
function resumeStandalone(){if(window.parent===window)refreshAfterSync()}
window.addEventListener('focus',resumeStandalone);onBeforeUnmount(()=>window.removeEventListener('focus',resumeStandalone));
async function save(quiet=false,markReady=false){
 if(locked.value)return false;if(quiet===true&&!dirty.value)return true;error.value='';clearMarkers();const origins=[];let items;
 try{items=expandDeliveryRows(rows.value,props.batch?'变更':props.item.title,origins,markReady)}catch(e){if(e.rowKey)await reportInvalid(e);else toast(e.message,'error',{duration:6000});return false}
 for(const [i,item] of items.entries()){const issue=validateDeliveryChange(item,origins[i]);if(issue){await reportInvalid(issue);return false}}
 if(!items.length){toast('没有需要保存的修改','warning');return false}saving.value=true;markingReady.value=markReady;const current=session;
 const selected=items[origins.indexOf(activeRow.value?.key)],knownIDs=rows.value.flatMap(row=>row.originals.map(c=>c.id));
 try{
  const saved=await api(props.batch?'api/changes/batch':`api/work-items/${props.item.id}/delivery-content`,{method:props.batch?'POST':'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({digest:baseline.value,changes:items,also_work_item_ids:also.value,mark_ready:markReady})});
  if(current!==session)return false;
  drafts.value.delete(draftKey.value);emit('saved');
  if(props.batch){rows.value=[];initial.value=serialized();emit('close')}
  else{await load();if(error.value){toast('变更已保存，但重新读取失败，请核对后再继续','warning');return false}const selectedID=savedDeliverySelection(selected,saved.changes,knownIDs);const row=rows.value.find(row=>row.originals.some(c=>c.id===selectedID));if(row)activeKey.value=row.key}
  if(quiet!==true)toast(markReady?'本次可编辑变更已校验并就绪；发布前仍需环境预检与审批':props.batch?'变更草稿已保存':'变更草稿已保存，可继续补充或校验并就绪','success');
  return true
 }catch(e){if(current===session){saving.value=false;const location=locateDeliveryServerError(e.message,items,origins);if(location){const failed=rows.value.find(r=>r.key===location.rowKey);if(failed)location.message='「'+(failed.title||types.find(t=>t.value===failed.kind)?.label||'变更')+'」：'+location.message.replace(/^第\s*\d+\s*项[：:]\s*/, '')}if(location)await reportInvalid(location);else toast(e.message,'error',{duration:6000})}return false}finally{saving.value=false;markingReady.value=false}
}
function configure(row){stash();emit('configure',row.service)}
watch(()=>[serialized(),loading.value],()=>{if(initial.value&&!loading.value){stash();emit('draft',{key:draftKey.value,draft:drafts.value.get(draftKey.value)||null})}},{flush:'post'});
import {PlusIcon,ChevronDownIcon,MoreHorizontalIcon,LinkIcon,LockIcon,Code2Icon,DatabaseIcon,FileCogIcon,TerminalIcon,NotebookPenIcon,Maximize2Icon,Minimize2Icon,XIcon,SearchIcon,Trash2Icon} from '@lucide/vue'
import ContextHelp from '../shared/components/ContextHelp.vue'
import OptionPicker from '../shared/components/OptionPicker.vue'
function focusEditorTitle(event){event.preventDefault();nextTick(()=>document.querySelector('[data-slot="dialog-title"]')?.focus())}

import {Accordion,AccordionContent,AccordionItem,AccordionTrigger,Badge,Button,Dialog,DialogContent,DialogDescription,DialogFooter,DialogHeader,DialogTitle,DropdownMenu,DropdownMenuContent,DropdownMenuItem,DropdownMenuSeparator,DropdownMenuTrigger,Empty,EmptyContent,EmptyDescription,EmptyHeader,EmptyTitle,Field,FieldGroup,FieldLabel,Input,Spinner,Textarea} from '../shared/shadcn-ui.js'
</script>
<template>
<component :is="inline?'section':Dialog" :open="true" @update:open="value=>{if(!value)close()}">
 <component :is="inline?'section':DialogContent" :show-close-button="false" :class="inline?'min-w-0':fullscreen?'fixed top-0 left-0 flex h-dvh w-screen max-w-none translate-x-0 translate-y-0 flex-col gap-0 overflow-hidden rounded-none p-0 sm:max-w-none':'flex h-[min(860px,92dvh)] w-[94vw] max-w-[1200px] flex-col gap-0 overflow-hidden p-0 sm:max-w-[1200px] max-md:top-0 max-md:left-0 max-md:h-dvh max-md:w-screen max-md:translate-x-0 max-md:translate-y-0 max-md:rounded-none'" @open-auto-focus="focusEditorTitle" @escape-key-down="event=>{event.preventDefault();if(!saving&&!refreshing&&!branchBusy)close()}" @interact-outside="event=>{event.preventDefault()}">
  <DialogHeader v-if="!inline" class="flex-row items-center justify-between gap-3 border-b px-5 py-4">
   <div class="min-w-0"><DialogTitle tabindex="-1" class="truncate pr-2 text-base outline-none">{{batch?'批量新建变更':'维护交付内容 · '+(item?.title||'')}}</DialogTitle><DialogDescription class="sr-only">维护交付草稿，保存不会触发构建或执行</DialogDescription></div>
   <div class="flex shrink-0 gap-1"><Button variant="ghost" size="icon" :disabled="locked" :aria-label="fullscreen?'退出全屏编辑':'全屏编辑'" @click="fullscreen=!fullscreen"><Minimize2Icon v-if="fullscreen" class="size-4"/><Maximize2Icon v-else class="size-4"/></Button><Button variant="ghost" size="icon" :disabled="saving||refreshing||branchBusy" aria-label="关闭交付内容编辑" @click="close"><XIcon class="size-4"/></Button></div>
  </DialogHeader>
  <div ref="editor" :class="inline?'delivery-editor min-w-0':'delivery-editor flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden'" @input="clearInvalid" @change="clearInvalid">
   <div v-if="loading" class="flex min-h-72 items-center justify-center gap-2 text-sm text-muted-foreground"><Spinner/>正在读取交付内容</div>
   <template v-else>
    <div ref="toolbar" class="flex shrink-0 flex-wrap items-center justify-between gap-3 border-b bg-background px-5 py-3"><div class="flex items-center gap-2"><strong class="text-sm">交付清单</strong><span class="text-xs text-muted-foreground">{{rows.length}} 项</span><ContextHelp text="清单用于维护交付内容；实际执行顺序由发布编排决定。"/></div><div class="flex items-center gap-1">
     <Button variant="ghost" size="sm" :disabled="locked" @click="linksOpen=!linksOpen"><LinkIcon class="size-4"/><span class="hidden sm:inline">关联工作项</span><Badge variant="secondary">{{batch?also.length:also.length+1}}</Badge></Button>
     <DropdownMenu v-if="rows.length"><DropdownMenuTrigger as-child><Button size="sm" :disabled="locked"><PlusIcon class="size-4"/>添加变更<ChevronDownIcon class="size-3.5"/></Button></DropdownMenuTrigger><DropdownMenuContent align="end"><DropdownMenuItem v-for="type in types" :key="type.value" @select="add(type.value)"><component :is="type.icon" class="size-4"/>{{type.label}}</DropdownMenuItem><DropdownMenuSeparator v-if="canReferenceSync"/><DropdownMenuItem v-if="canReferenceSync" @select="openSync"><LinkIcon class="size-4"/>引用同步任务</DropdownMenuItem></DropdownMenuContent></DropdownMenu>
     <DropdownMenu><DropdownMenuTrigger as-child><Button variant="ghost" size="icon-sm" :disabled="locked" aria-label="交付内容更多操作"><MoreHorizontalIcon class="size-4"/></Button></DropdownMenuTrigger><DropdownMenuContent align="end"><DropdownMenuItem @select="discard">{{dirty?'放弃修改并重新载入':'重新载入'}}</DropdownMenuItem></DropdownMenuContent></DropdownMenu>
    </div></div>
    <div v-show="linksOpen" class="border-b px-5 py-4"><Field><FieldLabel for="delivery-related">同时关联的需求 / Bug</FieldLabel><OptionPicker id="delivery-related" v-model="also" :options="links" multiple :disabled="locked" placeholder="搜索并选择工作项"/></Field></div>
    <Empty v-if="!rows.length" class="min-h-80 px-5 py-12"><EmptyHeader><EmptyTitle>这次涉及哪些交付变更？</EmptyTitle><EmptyDescription>只添加本次涉及的内容，可以关联多个应用。</EmptyDescription></EmptyHeader><EmptyContent class="max-w-3xl"><div class="grid w-full grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5"><Button v-for="type in types" :key="type.value" variant="outline" class="h-auto min-h-24 flex-col gap-3 px-3 py-5" :disabled="locked" @click="add(type.value)"><component :is="type.icon" class="size-5"/>{{type.label}}</Button></div><Button v-if="canReferenceSync" variant="ghost" :disabled="locked" @click="openSync"><LinkIcon class="size-4"/>引用同步任务</Button></EmptyContent></Empty>
    <div v-else class="grid min-h-0 flex-1 grid-cols-1 grid-rows-[auto_minmax(0,1fr)] md:grid-cols-[230px_minmax(0,1fr)] md:grid-rows-1">
     <aside class="max-h-48 space-y-4 overflow-auto border-b bg-muted/20 p-3 md:max-h-none md:border-r md:border-b-0" aria-label="交付内容条目"><div v-if="rows.length>8" class="relative"><SearchIcon class="absolute top-2.5 left-2.5 size-4 text-muted-foreground"/><Input v-model="rowSearch" aria-label="查找交付条目" placeholder="查找条目或应用" class="pl-8"/></div><section v-for="group in rowGroups" :key="group.key" class="space-y-1"><h3 class="truncate px-2 py-1.5 text-xs font-medium text-muted-foreground" :title="group.label">{{group.label}}</h3><Button v-for="row in group.rows" :key="row.key" :variant="row.key===activeRow?.key?'secondary':'ghost'" :disabled="locked" class="h-auto min-h-20 w-full justify-start gap-3 px-3 py-3 text-left" :aria-pressed="row.key===activeRow?.key" @click="selectRow(row.key)"><component :is="types.find(t=>t.value===row.kind)?.icon||NotebookPenIcon" class="size-4 shrink-0 text-muted-foreground"/><span class="min-w-0 flex-1 space-y-1.5"><span class="block truncate text-sm font-medium" :title="rowTitle(row,rows.indexOf(row))">{{rowTitle(row,rows.indexOf(row))}}</span><span class="flex items-center justify-between gap-1 text-xs text-muted-foreground"><span>{{rowKind(row)}}</span><Badge variant="outline" class="shrink-0 text-[10px]"><LockIcon v-if="row.readonly" class="size-2.5"/>{{row.readonly?(row.status==='ready'?'已就绪':'只读'):changedRows.has(row.key)?'未保存':'草稿'}}</Badge></span></span></Button></section><p v-if="!rowGroups.length" class="p-3 text-sm text-muted-foreground">没有匹配条目；编辑内容仍保留。</p></aside>
     <div class="min-h-0 min-w-0 overflow-auto"><article v-for="row in mountedRows" v-show="row.key===activeRow?.key" :key="row.key" :data-row-key="row.key" class="space-y-5 p-5">
      <div class="flex items-center justify-between gap-3"><h2 class="text-base font-semibold">{{rowKind(row)}}<span v-if="row.originals.length" class="ml-2 text-xs font-normal text-muted-foreground">#{{row.originals.map(c=>c.id).join('、')}}</span></h2><Button v-if="!row.originals.length" variant="ghost" size="sm" class="text-destructive" :disabled="locked" @click="rows=rows.filter(r=>r.key!==row.key)"><Trash2Icon class="size-3.5"/>移除</Button></div>
      <div v-if="row.readonly" class="flex flex-wrap items-center gap-2 rounded-md bg-muted px-3 py-2 text-xs text-muted-foreground"><LockIcon class="size-3.5"/><span>{{readonlyReason(row)}}</span><Button v-if="canReopen(row)" variant="link" size="sm" class="ml-auto h-6 px-0 text-xs" :disabled="locked||dirty" @click="reopen(row)">退回草稿修改</Button></div>
      <FieldGroup class="grid items-start gap-x-5 gap-y-5 md:grid-cols-2">
       <Field class="md:col-span-2" data-field="title"><FieldLabel :for="row.key+'-title'">变更标题</FieldLabel><Input :id="row.key+'-title'" v-model="row.title" :disabled="row.readonly||locked" placeholder="留空按类型和应用命名"/></Field>
       <template v-if="row.kind==='code'"><Field data-field="services"><FieldLabel :for="row.key+'-services'">影响应用</FieldLabel><OptionPicker :id="row.key+'-services'" v-model="row.services" :options="services.map(applicationOption)" multiple :disabled="row.readonly||locked"/></Field><Field data-field="reference"><FieldLabel>关联分支</FieldLabel><BranchField v-model="row.reference" :repositories="row.services.map(service=>({service}))" :disabled="row.readonly||locked"/></Field><FeatureBranch v-if="!batch&&!row.readonly" :work-item-id="item?.id" :services="row.services" :disabled="saving" @busy="branchBusy=$event" @created="row.reference=$event.branch"/></template>
       <template v-else>
        <Field v-if="['manual','artifact'].includes(row.kind)&&!row.originals.length"><FieldLabel :for="row.key+'-kind'">操作类型</FieldLabel><OptionPicker :id="row.key+'-kind'" v-model="row.kind" :options="[{value:'manual',label:'自定义记录（人工处理）'},{value:'artifact',label:'应用制品引用'}]" :disabled="locked"/></Field>
        <Field data-field="service"><FieldLabel :for="row.key+'-service'">{{row.kind==='manual'?'应用（选填）':'应用'}}</FieldLabel><OptionPicker :id="row.key+'-service'" v-model="row.service" :options="services.map(applicationOption)" :disabled="row.readonly||locked" :all-label="row.kind==='manual'?'公共 / 人工事项':undefined" @change="serviceChanged(row)"/></Field>
        <Field v-if="row.sql||row.nacos||row.redis" data-field="resource"><div class="flex min-h-5 items-center justify-between gap-2"><FieldLabel :for="row.key+'-resource'">资源</FieldLabel><Button v-if="row.service&&!row.readonly" variant="link" size="sm" class="h-5 px-0 text-xs" :disabled="locked" @click="configure(row)">{{row.nacos?'环境绑定':'配置资源'}}</Button></div><OptionPicker :id="row.key+'-resource'" v-model="(row.sql||row.nacos||row.redis).resource_alias" :options="options(row,row.kind)" :disabled="row.readonly||locked"/></Field>
        <Accordion v-if="row.sql||row.nacos||row.redis" type="single" collapsible class="md:col-span-2"><AccordionItem value="resource-targets"><AccordionTrigger>查看资源环境映射{{resourceTargets(row).length?' · '+resourceTargets(row).length+' 个环境':''}}</AccordionTrigger><AccordionContent><div v-for="target in resourceTargets(row)" :key="target.environment" class="flex flex-wrap gap-x-3 gap-y-1 border-b py-2 text-xs"><strong>{{target.environment}}</strong><span>{{target.connection}}</span><span>{{target.config?.database||target.config?.data_id||'沿用资源设置'}}</span><span v-if="target.config?.group">{{target.config.group}}</span></div><p v-if="!resourceTargets(row).length" class="text-xs text-muted-foreground">{{(row.sql||row.nacos||row.redis).resource_alias?'此资源尚未绑定环境，可保存内容；发布前需完成环境配置。':'选择资源后查看各环境目标。'}}</p></AccordionContent></AccordionItem></Accordion>
        <SQLFields v-if="row.sql" :model="row.sql" :draft-scripts="sqlDraftCache(row)" :disabled="row.readonly||locked" :force-open="invalid?.rowKey===row.key&&invalid?.field==='sql_environment_overrides'"/>
        <NacosFields v-else-if="row.nacos" :model="row.nacos" :disabled="row.readonly||locked" :force-open="invalid?.rowKey===row.key&&['data_id','group'].includes(invalid?.field)"/>
        <Field v-else-if="row.redis" class="md:col-span-2" data-field="commands"><div class="flex items-center gap-1"><FieldLabel :for="row.key+'-redis'">Redis 命令</FieldLabel><ContextHelp text="每行一条。只允许资源前缀中的精确 Key，不扩展通配符；非事务且无自动恢复。保存不执行，执行时按环境与入口策略审核。请勿填写密钥。"/></div><Textarea :id="row.key+'-redis'" v-model="row.redis.commands" rows="7" class="min-h-44 font-mono text-xs leading-6" :disabled="row.readonly||locked" placeholder="TYPE menu:tree&#10;TTL menu:tree"/></Field>
        <Field v-else class="md:col-span-2" data-field="reference"><FieldLabel :for="row.key+'-reference'">{{row.kind==='manual'?'处理记录':'操作 / 引用'}}</FieldLabel><Textarea :id="row.key+'-reference'" v-model="row.reference" :rows="row.kind==='manual'?6:3" :disabled="row.readonly||locked" placeholder="记录操作步骤和验收标准"/></Field>
       </template>
       <Field class="md:col-span-2"><FieldLabel :for="row.key+'-summary'">说明（选填）</FieldLabel><Textarea :id="row.key+'-summary'" v-model="row.summary" rows="3" class="min-h-24" :disabled="row.readonly||locked"/></Field>
       <Accordion v-if="!batch" type="single" collapsible class="md:col-span-2"><AccordionItem value="ownership"><AccordionTrigger>交付归属 · {{row.delivery_ownership?.mode==='follow'?'随其他工作项':row.delivery_ownership?.mode==='draft'?'生产归属待确认':'当前工作项'}}</AccordionTrigger><AccordionContent><OwnershipField :model="row" :item="item" :disabled="row.readonly||locked||!!row.delivery_ownership?.origin_work_item_id&&row.delivery_ownership.origin_work_item_id!==item.id"/></AccordionContent></AccordionItem></Accordion>
       <Accordion type="single" collapsible class="md:col-span-2"><AccordionItem value="risk"><AccordionTrigger>风险与回退说明</AccordionTrigger><AccordionContent class="grid items-start gap-4 md:grid-cols-2"><Field><FieldLabel :for="row.key+'-risk'">风险等级</FieldLabel><OptionPicker :id="row.key+'-risk'" v-model="row.risk_level" :options="[{value:'low',label:'低'},{value:'medium',label:'中'},{value:'high',label:'高'}]" :disabled="row.readonly||locked"/></Field><Field><FieldLabel :for="row.key+'-rollback'">回退说明</FieldLabel><Textarea :id="row.key+'-rollback'" v-model="row.rollback_plan" rows="3" :disabled="row.readonly||locked"/></Field></AccordionContent></AccordionItem></Accordion>
      </FieldGroup>
     </article></div>
    </div>
   </template>
  </div>
  <DialogFooter v-if="!embeddedSubmit" class="flex-row flex-wrap items-center gap-2 border-t bg-background px-5 py-3 sm:justify-between">
   <div class="mr-auto min-w-0 basis-full space-y-1 text-xs sm:basis-auto" role="status"><div>{{refreshing?'正在合并新增内容':dirty?'有未保存修改 · '+changedRows.size+' 项':'已就绪 '+progress.ready+' / '+progress.total+' 项'}}</div><div class="text-muted-foreground">{{progress.hint}}</div></div>
   <ContextHelp text="校验并就绪只确认录入内容，不进行环境预检或执行；发布仍须按环境预检与审批。"/>
   <Button variant="ghost" :disabled="locked" @click="close">关闭</Button>
   <Button v-if="dirty" variant="outline" :disabled="locked" @click="save(false)"><Spinner v-if="saving&&!markingReady"/>保存草稿</Button>
   <Button v-if="progress.editable" :disabled="locked" @click="save(false,true)"><Spinner v-if="markingReady"/>校验并就绪{{progress.editable?' · '+progress.editable+' 项':''}}</Button>
   <template v-if="!batch&&!hideNextSteps&&progress.canContinue"><Button variant="outline" :disabled="locked" @click="nextStep('version')">纳入版本</Button><Button :disabled="locked" @click="nextStep('delivery')">发起交付</Button></template>
  </DialogFooter>
 </component>
</component>
</template>
