<script setup>
import {applicationOption} from '../../shared/applicationName.js'



import DateFilter from '../../shared/components/DateFilter.vue'
import PageHeader from '../../shared/components/PageHeader.vue'
import {computed, inject, onMounted, onBeforeUnmount, nextTick, ref, watch} from "vue";
import {onBeforeRouteLeave, useRoute, useRouter} from "vue-router";
import WorkDeliveryEditor from '../components/WorkDeliveryEditor.vue';
import EditorDialogTitle from '../../shared/components/EditorDialogTitle.vue';
import WorkItemsTable from '../components/WorkItemsTable.vue';
import WorkItemPicker from '../components/WorkItemPicker.vue';
import DeliveryRecordsTable from '../components/DeliveryRecordsTable.vue';
import GitBranchSelect from '../components/GitBranchSelect.vue';
import DataSyncTasks from '../components/DataSyncTasks.vue';
import HelpTip from "../../shared/components/HelpTip.vue";

import ApplicationResourcesEditor from '../components/ApplicationResourcesEditor.vue';
import ResourcesView from './ResourcesView.vue';
import RichDescription from '../components/RichDescription.vue';
import SQLCode from '../../shared/components/SQLCode.vue'
import MarkdownContent from '../../shared/components/MarkdownContent.vue'
import MarkdownEditor from '../components/MarkdownEditor.vue';
import ReleaseNotice from '../components/ReleaseNotice.vue';
import ReleaseReadinessPanel from '../components/ReleaseReadinessPanel.vue';
import NacosPlanPreview from '../components/NacosPlanPreview.vue';
import NacosChangeFields from '../components/NacosChangeFields.vue';
import SQLScriptFields from '../components/SQLScriptFields.vue';
import SQLPlanPreview from '../components/SQLPlanPreview.vue';
import RedisChangeFields from '../components/RedisChangeFields.vue';
import RedisPlanPreview from '../components/RedisPlanPreview.vue';

import {toast} from "../../shared/composables/toast.js";
import {deliveryChangePayload} from '../deliveryEntry.js';
import {validateDeliveryChange,locateDeliveryServerError} from '../deliveryValidation.js';
import {scopeChanges, scopePayload, frozenChangeIDs} from '../versionScope.js';
import {openWorkspace} from '../../research/api.js';
const directResourcesAPI=!location.pathname.startsWith('/api/plugins/')
const props=defineProps({scope:{type:String,default:'changes'}})
const legacyVersions=computed(()=>!versionWorkspace.value&&route.query.tab==='versions')
const versionWorkspace=computed(()=>props.scope==='versions')
function openResearchWork(){const hash='#/work';if(window.parent!==window)window.parent.postMessage({type:'workspace.open',plugin:'research',hash},location.origin);else window.open('/api/plugins/research/ui'+hash,'_blank','noopener')}
function resolveReadiness(check){
 if(check.action==='change')return view('changes',{id:check.change_id});
 if(check.action==='scope'){if(detail.value.status==='draft'&&canManage(detail.value))open('versions',detail.value);else{detailTab.value='scope';toast('版本范围已冻结；新增内容需建立新版本','info')};return}
 if(check.action==='environment')return router.push(`/svc/${encodeURIComponent(check.service)}/environment`);
 if(check.action==='artifact')return router.push(`/svc/${encodeURIComponent(check.service)}/artifact`);
 router.push({path:'/orchestration',query:{version:String(detail.value.id)}})
}
const api = inject("api"),
  route = useRoute(),
  nativeRouter = useRouter();
function navigate(to,replace=false){
 const destination=typeof to==='string'?{path:to}:to
 if(versionWorkspace.value){
  if(destination.path==='/changes'){
   const {tab:kind,...query}=destination.query||{}
   if(kind==='work')return nativeRouter.push('/work/'+query.item)
   if(kind==='versions')return nativeRouter[replace?'replace':'push']({path:'/versions',query})
  }
  if(destination.path?.startsWith('/svc/')||destination.path==='/orchestration'||destination.path==='/changes')return openWorkspace('deploy','#'+destination.path+(destination.query?'?'+new URLSearchParams(destination.query):''))
 }
 if(window.parent!==window&&(destination.path?.startsWith('/svc/')||destination.path==='/orchestration'))return openWorkspace('deploy','#'+destination.path+(destination.query?'?'+new URLSearchParams(destination.query):''))
 return nativeRouter[replace?'replace':'push'](to)
}
const router={push:to=>navigate(to),replace:to=>navigate(to,true)}
const tab = computed(() =>
  versionWorkspace.value ? 'versions' :
  ["work", "changes", "versions"].includes(route.query.tab)
    ? route.query.tab
    : "changes",
);
const work = ref([]),
  changes = ref([]),
  versions = ref([]),
  deliveries = ref([]),
  services = ref([]),
  me = ref({});
const connections=ref([]),environments=ref([]),syncStates=ref([]),showSync=ref(false),syncForm=ref({connection:'',baseline:'',status:''}),showBindings=ref(false),showResources=ref(false),showSQLPreview=ref(false),sqlPreview=ref(null),previewItem=ref(null),previewEnvironment=ref('');
const showNacosPreview=ref(false),nacosPreview=ref(null),nacosPreviewItem=ref(null),nacosEnvironment=ref('');
let nacosPreviewSequence=0;
function checkNacos(item){nacosPreviewSequence++;nacosPreviewItem.value=item;nacosEnvironment.value=environments.value[0]?.name||'';nacosPreview.value=null;showNacosPreview.value=true}
function closeNacosPreview(){nacosPreviewSequence++;showNacosPreview.value=false}
async function runNacosPreview(){
  if(saving.value||!nacosEnvironment.value)return;
  const sequence=++nacosPreviewSequence,changeID=nacosPreviewItem.value.id,environment=nacosEnvironment.value;
  saving.value=true;nacosPreview.value=null;
  try{const result=await api(`api/changes/${changeID}/nacos-preview`,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({environment})});if(sequence===nacosPreviewSequence&&showNacosPreview.value)nacosPreview.value=result}catch(e){if(sequence===nacosPreviewSequence&&showNacosPreview.value)toast(e.message,'error')}finally{saving.value=false}
}
watch(nacosEnvironment,()=>{nacosPreviewSequence++;nacosPreview.value=null});
const showRedisPreview=ref(false),redisPreview=ref(null),redisPreviewItem=ref(null),redisEnvironment=ref('');
let redisPreviewSequence=0;
function checkRedis(item){redisPreviewSequence++;redisPreviewItem.value=item;redisEnvironment.value=environments.value[0]?.name||'';redisPreview.value=null;showRedisPreview.value=true}
function closeRedisPreview(){redisPreviewSequence++;showRedisPreview.value=false}
async function runRedisPreview(){
 if(saving.value||!redisEnvironment.value)return;const sequence=++redisPreviewSequence,changeID=redisPreviewItem.value.id,environment=redisEnvironment.value;saving.value=true;redisPreview.value=null;
 try{const result=await api(`api/changes/${changeID}/redis-preview`,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({environment})});if(sequence===redisPreviewSequence&&showRedisPreview.value)redisPreview.value=result}catch(e){if(sequence===redisPreviewSequence&&showRedisPreview.value)toast(e.message,'error')}finally{saving.value=false}
}
watch(redisEnvironment,()=>{redisPreviewSequence++;redisPreview.value=null});
const sourceCapabilities=ref([]);
const syncConnections=computed(()=>connections.value.filter(c=>sourceCapabilities.value.some(p=>p.connection_kind===c.kind)));
const syncConnection=computed(()=>syncConnections.value.find(c=>c.name===syncForm.value.connection));
const syncCapability=computed(()=>sourceCapabilities.value.find(p=>p.connection_kind===syncConnection.value?.kind));
const bugStatuses=computed(()=>syncCapability.value?.statuses||[]);
const sourceOptions=computed(()=>[{value:'manual',label:'自定义'},{value:'external',label:'外部平台'},...(!['manual','external'].includes(form.value.source)&&form.value.source?[{value:form.value.source,label:'外部关联（历史来源）'}]:[])]);
const syncDefaultStatus=computed(()=>syncConnection.value?.config?.[syncCapability.value?.status_config_key]||syncCapability.value?.default_status||'');
const syncStatusOptions=computed(()=>[{value:'__default',label:`沿用连接默认：${bugStatuses.value.find(v=>v.value===syncDefaultStatus.value)?.label||'来源默认'}`},...bugStatuses.value]);
const syncStatusModel=computed({get:()=>syncForm.value.status||'__default',set:value=>{syncForm.value.status=value==='__default'?'':value}});
watch([showSync,syncConnections],([opened])=>{if(opened&&!syncConnections.value.some(c=>c.name===syncForm.value.connection)){syncForm.value.connection=syncConnections.value[0]?.name||'';syncForm.value.status=''}});
watch(()=>syncCapability.value?.connection_kind,()=>{syncForm.value.status=''});
function openResources(){if(window.parent!==window)window.parent.postMessage({type:'platform.open',view:'resources'},location.origin);else showResources.value=true}
async function syncIssues(){
  if(!syncForm.value.connection)return toast('请选择来源连接','warning');saving.value=true
  try{await api(`api/work-item-sync/${encodeURIComponent(syncForm.value.connection)}`,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({baseline:syncForm.value.baseline,status:syncForm.value.status})});await load();toast('工作项同步完成','success')}catch(e){toast(e.message,'error')}finally{saving.value=false;await load()}
}
let sqlPreviewSequence=0;
function closeSQLPreview(){sqlPreviewSequence++;showSQLPreview.value=false}
watch(previewEnvironment,()=>{sqlPreviewSequence++;sqlPreview.value=null});
async function checkSQL(item){sqlPreviewSequence++;previewItem.value=item;previewEnvironment.value=environments.value[0]?.name||'';sqlPreview.value=null;showSQLPreview.value=true}
async function runSQLPreview(){
 if(saving.value||!previewEnvironment.value)return;
 const sequence=++sqlPreviewSequence,changeID=previewItem.value.id,environment=previewEnvironment.value;
 saving.value=true;sqlPreview.value=null;
 try{const result=await api(`api/changes/${changeID}/sql-preview`,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({environment})});if(sequence===sqlPreviewSequence&&showSQLPreview.value)sqlPreview.value=result}catch(e){if(sequence===sqlPreviewSequence&&showSQLPreview.value)toast(e.message,'error')}finally{saving.value=false}
}

const dialogFullscreen=ref(false);
const dialog = ref(""),
  form = ref({}),
  saving = ref(false);
watch(dialog,()=>{dialogFullscreen.value=false});
const showWorkPicker=ref(false),pickedWork=ref([]),scopePreview=ref([]),scopeLoading=ref(false),scopeError=ref('');
let scopeSequence=0,scopeTimer;
function confirmWorkSelection(items){pickedWork.value=items;form.value.work_item_ids=items.map(w=>w.id);showWorkPicker.value=false}
async function previewVersionScope(){
 const sequence=++scopeSequence;scopeError.value='';scopeLoading.value=true;scopePreview.value=[]
 const scope={work_item_ids:[...(form.value.work_item_ids||[])],manual_change_ids:[...(form.value.manual_change_ids||[])]}
 try{
  const data=scope.work_item_ids.length||scope.manual_change_ids.length?await api('api/release-versions/scope-preview',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(scope)}):[]
  if(sequence===scopeSequence&&dialog.value==='versions'){if(!Array.isArray(data))throw Error('范围预览响应无效');scopePreview.value=data}
 }catch(e){if(sequence===scopeSequence)scopeError.value=e.message}finally{if(sequence===scopeSequence)scopeLoading.value=false}
}
watch(()=>[dialog.value,form.value.id,JSON.stringify(form.value.work_item_ids||[]),JSON.stringify(form.value.manual_change_ids||[])],()=>{
 scopeSequence++;clearTimeout(scopeTimer);scopePreview.value=[];scopeError.value=''
 if(dialog.value==='versions'){scopeLoading.value=true;scopeTimer=setTimeout(previewVersionScope,150)}else{scopeLoading.value=false;showWorkPicker.value=false}
})
onBeforeUnmount(()=>{scopeSequence++;clearTimeout(scopeTimer)})
const showBatchEditor=ref(false),batchEditor=ref(null),imageUploading=ref(false);
const newWorkDraft=ref(null),formBaseline=ref('');
const formDirty=computed(()=>!!dialog.value&&JSON.stringify(form.value)!==formBaseline.value);
const workspaceDirty=computed(()=>formDirty.value||!!newWorkDraft.value||!!deliveryEditor.value?.hasDraft()||!!batchEditor.value?.hasDraft());


const showDeliveryEditor=ref(false),deliveryItem=ref(null),deliveryEditor=ref(null),addedVersion=ref(null);
const showSyncTasks=ref(false);
watch([workspaceDirty,saving,imageUploading],()=>{if(window.parent!==window)window.parent.postMessage({type:'workspace.dirty',dirty:workspaceDirty.value||saving.value||imageUploading.value},location.origin)});
async function openSyncTasks(){
 const plugins=await fetch('/api/plugins').then(r=>r.ok?r.json():[]).catch(()=>[]);
 if(plugins.some(p=>p.name==='dbms')){const hash='#/sync'+(deliveryItem.value?'?work_item='+deliveryItem.value.id:'');if(window.parent!==window)window.parent.postMessage({type:'workspace.open',plugin:'dbms',hash},location.origin);else window.open('/api/plugins/dbms/ui'+hash,'_blank','noopener');return}
 showSyncTasks.value=true
}
function syncGenerated(){load();deliveryEditor.value?.refreshAfterSync()}
function linkedContentChanged(e){if(e.source!==window.parent||e.origin!==location.origin||e.data?.type!=='work-item.changed')return;if(e.data.id===deliveryItem.value?.id)syncGenerated()}
window.addEventListener('message',linkedContentChanged);onBeforeUnmount(()=>window.removeEventListener('message',linkedContentChanged));
function editDelivery(item){deliveryItem.value=item;showDeliveryEditor.value=true}
onBeforeRouteLeave(async()=>{if(saving.value||imageUploading.value)return false;return !workspaceDirty.value||await confirmAction('离开将放弃尚未保存的草稿，确定离开？')});
function warnUnsaved(e){if(workspaceDirty.value||saving.value||imageUploading.value){e.preventDefault();e.returnValue=''}}
window.addEventListener('beforeunload',warnUnsaved);onBeforeUnmount(()=>window.removeEventListener('beforeunload',warnUnsaved));
watch(()=>form.value.kind,kind=>{if(dialog.value==='changes'&&kind==='mysql'&&!form.value.sql)form.value.sql={resource_alias:'',content:'',max_affected_rows:100}})
const kinds = [
  { value: "code", label: "代码分支" },
  { value: "artifact", label: "应用制品" },
  { value: "nacos", label: "Nacos 配置" },
  { value: "mysql", label: "SQL 变更" },
  { value: "data-sync", label: "数据对比同步" },
  { value: "redis", label: "Redis 命令" },
  { value: "manual", label: "自定义记录" },
];
const labels = {
  open: "进行中",
  closed: "已关闭",
  draft: "草稿",
  ready: "已就绪",
  archived: "已归档",
  completed: "已完成",
  canceled: "已取消",
};
const kindLabel = (value) =>
  kinds.find((v) => v.value === value)?.label || value;
const workTable=ref(null),changeTable=ref(null),versionTable=ref(null),recordDetail=ref(null);
const detailTab=ref('overview')
watch(()=>[route.query.item,tab.value],()=>{detailTab.value='overview'})
const detail = computed(()=>route.query.item&&recordDetail.value?.type===tab.value&&recordDetail.value.item?.id===Number(route.query.item)?recordDetail.value.item:null);
let detailSequence=0;
async function loadRecordDetail(){
 const request=++detailSequence,type=tab.value,id=route.query.item;
 if(!id){recordDetail.value=null;await nextTick();(type==='changes'?changeTable:type==='versions'?versionTable:workTable).value?.restorePosition?.();return}
 const endpoint={work:'work-items',changes:'changes',versions:'release-versions'}[type];
 try{const item=await api(`api/${endpoint}/${encodeURIComponent(id)}`);if(request===detailSequence)recordDetail.value={type,item}}catch(e){if(request===detailSequence){recordDetail.value=null;toast(e.message,'error')}}
}
watch(()=>[tab.value,route.query.item],loadRecordDetail);
const detailChanges = computed(()=> tab.value==='work' ? changes.value.filter(c=>(c.work_items||[]).some(w=>w.id===detail.value?.id)) : tab.value==='changes'&&detail.value?[detail.value]:detail.value?.changes||[]);
const detailVersions = computed(()=>versions.value.filter(v=>(v.work_items||[]).some(w=>w.id===detail.value?.id)||(v.changes||[]).some(c=>(c.work_items||[]).some(w=>w.id===detail.value?.id))));
const detailRuns = computed(()=>deliveries.value.filter(r=>tab.value==='versions' ? r.plan.version?.id===detail.value?.id : (r.plan.scope?.work_items||r.plan.version?.work_items||[]).some(w=>w.id===detail.value?.id)));
const selectedChanges = computed(()=>dialog.value==='versions'?scopePreview.value:scopeChanges(changes.value,form.value.work_item_ids,form.value.manual_change_ids));
const frozenIDs=computed(()=>frozenChangeIDs(versions.value));
const isFrozenChange=c=>c.frozen||frozenIDs.value.has(c.id);
const showAddVersion=ref(false),addVersionID=ref(''),addItems=ref([]),addType=ref('work');
const draftVersions=ref([]),draftQuery=ref(''),draftPage=ref(1),draftTotal=ref(0),draftLoading=ref(false),draftError=ref('');
let draftSequence=0,draftTimer;
async function loadDraftVersions(page=1){
 const request=++draftSequence;draftLoading.value=true;draftError.value='';if(page===1)draftVersions.value=[];
 const owner=me.value.role==='admin'||me.value.username==='local'?'':me.value.username||'';
 try{const result=await api(`api/release-versions/page?${new URLSearchParams({status:'draft',owner,q:draftQuery.value,page:String(page),page_size:'100'})}`);if(request!==draftSequence)return;if(!Array.isArray(result?.items))throw new Error('草稿版本列表响应错误');draftVersions.value=page===1?result.items:[...draftVersions.value,...result.items];draftPage.value=result.page;draftTotal.value=result.total}catch(e){if(request===draftSequence)draftError.value=e.message}finally{if(request===draftSequence)draftLoading.value=false}
}
watch(draftQuery,()=>{if(!showAddVersion.value)return;draftSequence++;clearTimeout(draftTimer);draftVersions.value=[];draftLoading.value=true;addVersionID.value='';draftTimer=setTimeout(()=>loadDraftVersions(),300)});
watch(showAddVersion,opened=>{if(!opened){draftSequence++;clearTimeout(draftTimer)}});
onBeforeUnmount(()=>{draftSequence++;clearTimeout(draftTimer)});
const sqlResources=ref([]),sqlResourcesLoading=ref(false),sqlResourcesError=ref('');
let resourceRequest=0;
async function loadSQLResources(){
  const seq=++resourceRequest;sqlResources.value=[];sqlResourcesError.value='';
  if(dialog.value!=='changes'||!['mysql','nacos','redis'].includes(form.value.kind)||!form.value.service)return;
  sqlResourcesLoading.value=true;
  try{const data=await api(`api/services/${encodeURIComponent(form.value.service)}/resources`);if(seq===resourceRequest)sqlResources.value=(data.requirements||[]).filter(r=>r.kind===form.value.kind)}
  catch(e){if(seq===resourceRequest)sqlResourcesError.value=e.message}finally{if(seq===resourceRequest)sqlResourcesLoading.value=false}
}
watch(()=>[dialog.value,form.value.kind,form.value.service],()=>{sqlResourcesLoading.value=false;loadSQLResources()});
function view(type,item){if(type==='versions'&&!versionWorkspace.value)return openWorkspace('research','#/versions?item='+item.id);if(!route.query.item)(tab.value==='changes'?changeTable:tab.value==='versions'?versionTable:workTable).value?.rememberPosition?.();router.push({path:'/changes',query:{tab:type,item:String(item.id)}})}
async function editRecord(type,item){try{const full=await api(`api/${type==='versions'?'release-versions':'changes'}/${item.id}`);open(type,full)}catch(e){toast(e.message,'error')}}
async function previewRecord(item){try{const full=await api(`api/changes/${item.id}`);if(full.kind==='mysql')checkSQL(full);else if(full.kind==='nacos')checkNacos(full);else if(full.kind==='redis')checkRedis(full)}catch(e){toast(e.message,'error')}}
function back(){router.push({path:'/changes',query:{tab:tab.value}})}
function addChange(item){open('changes');form.value.work_item_ids=[item.id];const apps=[...new Set(changes.value.filter(c=>(c.work_items||[]).some(w=>w.id===item.id)).map(c=>c.service).filter(Boolean))];if(apps.length===1)form.value.service=apps[0]}
function chooseVersion(type,item){addType.value=type;addItems.value=Array.isArray(item)?item:[item];addVersionID.value='';draftQuery.value='';showAddVersion.value=true;loadDraftVersions()}
const versionEntryLoaded=ref(false)
async function consumeVersionEntry(){
 const id=Number(route.query.add_work_item)
 if(!versionWorkspace.value||!Number.isSafeInteger(id)||id<1)return
 try{const item=await api('api/work-items/'+id);if(Number(route.query.add_work_item)!==id)return;const query={...route.query};delete query.add_work_item;await nativeRouter.replace({path:route.path,query});chooseVersion('work',item)}catch(e){toast(e.message,'error')}
}
watch(()=>route.query.add_work_item,()=>{if(versionEntryLoaded.value)consumeVersionEntry()})
async function addToVersion(){
  if(saving.value)return;if(!addVersionID.value)return toast('请选择草稿版本','warning');saving.value=true;
  try{const v=await api(`api/release-versions/${addVersionID.value}`);if(v.status!=='draft')throw new Error('版本已冻结，请选择其他草稿版本');const w=(v.work_items||[]).map(w=>w.id),m=[...(v.manual_change_ids||[])];(addType.value==='work'?w:m).push(...addItems.value.map(item=>item.id));
    await api(`api/release-versions/${v.id}`,{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify(scopePayload(v,w,m))});
    showAddVersion.value=false;await load();addedVersion.value=v;toast('已纳入版本范围，当前页面保持不变','success')
  }catch(e){toast(e.message,'error')}finally{saving.value=false}
}
function versionBlockers(v){
  const out=[];if(!v.changes?.length)out.push('尚未选择实施变更');
  for(const c of v.changes||[])if(c.status!=='ready')out.push(`待就绪：${c.title}`);
  for(const w of v.work_items||[])if(!(v.changes||[]).some(c=>(c.work_items||[]).some(x=>x.id===w.id)))out.push(`待补充变更：${w.title}`);
  const expected=scopeChanges(changes.value,(v.work_items||[]).map(w=>w.id),v.manual_change_ids);
  if(expected.some(c=>!(v.changes||[]).some(x=>x.id===c.id)))out.push('需求有新增变更，请刷新关联范围');
  return out;
}
async function refreshScope(v){saving.value=true;try{await api(`api/release-versions/${v.id}`,{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify(scopePayload(v,(v.work_items||[]).map(w=>w.id),v.manual_change_ids))});await load();toast('已刷新关联范围','success')}catch(e){toast(e.message,'error')}finally{saving.value=false}}
const runLabel=s=>({pending_approval:'待审核',approved:'待执行',running:'执行中',success:'成功',failed:'失败',rejected:'已驳回',canceled:'已取消'}[s]||s);
const canManage = (item) =>
  me.value.username === "local" ||
  me.value.role === "admin" ||
  me.value.username === item.owner;
const safeURL = (value) =>
  /^https?:\/\//i.test(value || "") ? value : undefined;
let dataLoaded=false;
async function load() {
  try {
    [
      work.value,
      changes.value,
      versions.value,
      services.value,
      deliveries.value,
      connections.value,environments.value,syncStates.value,sourceCapabilities.value,
    ] = await Promise.all([
      api("api/work-items?limit=500"),
      api("api/changes?limit=500"),
      api("api/release-versions?limit=500"),
      api("api/services"),
      api("api/delivery/runs"),
      api(location.pathname.startsWith('/api/plugins/')?'/api/resources/connections':'api/connections'),api('api/environments'),versionWorkspace.value?[]:api('api/work-item-sync'),versionWorkspace.value?[]:api('api/work-item-sources'),
    ]);
    if(dataLoaded){workTable.value?.refresh();changeTable.value?.refresh();versionTable.value?.refresh()}
    dataLoaded=true;
    await loadRecordDetail();
  } catch (e) {
    toast(e.message, "error");
  }
}
function closeDialog(){if(imageUploading.value||saving.value)return;if(dialog.value==='work'&&!form.value.id){newWorkDraft.value=form.value.title||form.value.description?JSON.parse(JSON.stringify(form.value)):null;if(newWorkDraft.value)toast('未保存的需求草稿已保留，下次新建可继续编辑','success')}dialog.value=''}
async function discardWorkDraft(){if(imageUploading.value||saving.value||!await confirmAction('放弃当前未保存的需求草稿？'))return;newWorkDraft.value=null;dialog.value='';form.value={}}
function open(type, item) {
  if(type==='changes'&&!item){showBatchEditor.value=true;return}
  dialog.value = type;
  if (type === "work")
    form.value = item
      ? { ...item }
      : newWorkDraft.value ? JSON.parse(JSON.stringify(newWorkDraft.value)) : {
          title: "",
          kind: "requirement",
          source: "manual",
          external_id: "",
          description: "",
          status: "open",
          description_format: "markdown",
        };
  if(type==='work'&&!form.value.description_format)form.value.description_format=/<\/?[a-z][^>]*>/i.test(form.value.description||'')?'html':'markdown';
  if (type === "changes")
    form.value = item
      ? { ...item, work_item_ids: (item.work_items || []).map((v) => v.id) }
      : {
          title: "",
          kind: "code",
          service: "",
          reference: "",
          summary: "",
          risk_level: "medium",
          rollback_plan: "",
          work_item_ids: [],
        };
  if(type==='changes')form.value.sql=item?.sql?{...item.sql}:{resource_alias:'',content:item?.kind==='mysql'?item.reference||'':'',max_affected_rows:100};
  if(type==='changes')form.value.redis=item?.redis?{...item.redis}:{resource_alias:'',commands:''};
  if(type==='changes')form.value.nacos=item?.nacos?JSON.parse(JSON.stringify(item.nacos)):{resource_alias:'',data_id:'',group:'DEFAULT_GROUP',format:'yaml',entries:[{key:'',value:'',action:'set'}]};
  if (type === "versions")
    form.value = item
      ? {
          ...item,
          change_ids: (item.changes || []).map((v) => v.id),
          work_item_ids: (item.work_items || []).map(v=>v.id),
          manual_change_ids: [...(item.manual_change_ids || [])],
        }
      : {
          version: "",
          title: "",
          description: "",
          change_ids: [],
          work_item_ids: [],
          manual_change_ids: [],
        };
  formBaseline.value=JSON.stringify(form.value);
}
const changeInvalid=ref(null)
function clearChangeValidation(){document.querySelectorAll('#change-editor-form [aria-invalid]').forEach(e=>e.removeAttribute('aria-invalid'))}
async function focusChangeIssue(issue){
 changeInvalid.value=issue;await nextTick();clearChangeValidation();let root=document.querySelector('#change-editor-form');
 if(Number.isInteger(issue.entry))root=root?.querySelector('[data-entry-index="'+issue.entry+'"]')||root;
 const field=root?.querySelector('[data-field="'+(issue.field==='services'?'service':issue.field)+'"]')||root;
 const input=field?.querySelector('textarea:not(:disabled),input:not(:disabled),button[aria-haspopup="listbox"]:not(:disabled)');
 input?.setAttribute('aria-invalid','true');field?.scrollIntoView({block:'center',behavior:'auto'});input?.focus({preventScroll:true});
}
async function save() {
  if(saving.value||imageUploading.value)return;
  if(dialog.value==='versions'&&(scopeLoading.value||scopeError.value))return toast('请先完成范围预览或处理错误','warning');
  if (!form.value.title?.trim()) return toast("请填写标题", "warning");
  if (
    dialog.value === "versions" &&
    !form.value.version?.trim()
  )
    return toast("请填写版本号", "warning");
  if(dialog.value==='changes'&&form.value.kind==='mysql'&&(!form.value.service||!sqlResources.value.some(r=>r.alias===form.value.sql?.resource_alias)))return toast('请选择应用及已配置的数据库资源','warning');
  if(dialog.value==='changes'&&form.value.kind==='nacos'&&(!form.value.service||!sqlResources.value.some(r=>r.alias===form.value.nacos?.resource_alias)))return toast('请选择应用及已配置的 Nacos 资源','warning');
  if(dialog.value==='changes'&&form.value.kind==='redis'&&(!form.value.service||!sqlResources.value.some(r=>r.alias===form.value.redis?.resource_alias)))return toast('请选择应用及已配置的 Redis 资源','warning');
  if(dialog.value==='changes'){const issue=validateDeliveryChange(deliveryChangePayload(form.value),'standalone');if(issue){await focusChangeIssue(issue);return toast(issue.message,'warning')}}
  saving.value = true;
  const endpoint = {
    work: "work-items",
    changes: "changes",
    versions: "release-versions",
  }[dialog.value];
  const origin=route.fullPath,creating=!form.value.id;
  try {
    const type=dialog.value;
    const result=await api(`api/${endpoint}${form.value.id ? "/" + form.value.id : ""}`, {
      method: form.value.id ? "PUT" : "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({...form.value,sql:dialog.value==='changes'&&form.value.kind==='mysql'?{...form.value.sql,max_affected_rows:Number(form.value.sql.max_affected_rows)}:undefined,nacos:dialog.value==='changes'&&form.value.kind==='nacos'?form.value.nacos:undefined,redis:dialog.value==='changes'&&form.value.kind==='redis'?form.value.redis:undefined}),
    });
    if(type==='versions'&&result.preparation_error)toast('版本已保存，准备流程待补齐：'+result.preparation_error,'warning',{duration:7000});
    if(type==='work'&&creating)newWorkDraft.value=null;
    const keepContext=dialog.value===type&&route.fullPath===origin;
    dialog.value = "";
    await load();
    if(creating&&keepContext&&route.fullPath===origin&&(type==='work'||type==='versions')){
      if(type==='versions'&&tab.value==='work')addedVersion.value=result;
      else view(type,result);
    }
    // Adding a change from a planned demand refreshes that draft's derived scope.
    // No ready/frozen version is ever modified here.
    if(type==='changes'&&tab.value==='work')toast('变更已保存；版本范围可通过“编辑范围”重新载入关联变更','success');
    toast("已保存", "success");
  } catch (e) {
    if(dialog.value==='changes'&&/SQL|WHERE|DDL|字段|事务|注释|配置项|资源别名|Data ID|Group|影响行数/.test(e.message)){saving.value=false;await focusChangeIssue(locateDeliveryServerError('第 1 项：'+e.message,[form.value],['standalone']))}
    toast(e.message, "error");
  } finally {
    saving.value = false;
  }
}
const confirmingAction=ref(false)
async function action(type,item,op){
 if(saving.value||confirmingAction.value)return
 confirmingAction.value=true
 let confirmed=false
 try{confirmed=await confirmAction(`${op==='delete'?'删除':op==='ready'?type==='versions'?'冻结版本范围（之后不可修改）':'标记就绪':'退回草稿'}「${item.title}」？`)}finally{confirmingAction.value=false}
 if(!confirmed)return
 saving.value=true
 try{const endpoint=type==='versions'?'release-versions':'changes';await api(`api/${endpoint}/${item.id}${op==='delete'?'':'/'+op}`,{method:op==='delete'?'DELETE':'POST'});await load()}
 catch(e){if(type==='changes'&&op==='ready'&&item.status==='draft'&&/SQL|WHERE|DDL|CREATE|字段|事务|注释|配置项|Data ID|Group|影响行数/.test(e.message)){saving.value=false;open('changes',item);await focusChangeIssue(locateDeliveryServerError('第 1 项：'+e.message,[item],['standalone']))}toast(e.message,'error')}
 finally{saving.value=false}
}
onMounted(async () => {
  if(!versionWorkspace.value&&route.query.tab==='versions'){openWorkspace('research','#/versions'+(route.query.item?'?item='+route.query.item:''));return}
  me.value = await api("/api/me").catch(() => ({
    username: "local",
    role: "admin",
  }));
  await load();
  versionEntryLoaded.value=true;await consumeVersionEntry();
});
const scopePanels=ref(['panel'])
watch(()=>selectedChanges.value.length<=8,open=>scopePanels.value=open?['panel']:[])

import OptionPicker from '../../shared/components/OptionPicker.vue'

import {confirmAction} from '../../shared/shadcnFeedback.js'

import {ref as _viewRef} from 'vue'
const _view1=_viewRef([])


import {Accordion,AccordionContent,AccordionItem,AccordionTrigger,Badge,Button,Card,CardAction,CardContent,CardFooter,CardHeader,CardTitle,Dialog,DialogContent,DialogDescription,DialogFooter,DialogHeader,DialogTitle,Empty,EmptyContent,EmptyDescription,EmptyHeader,Field,FieldLabel,Input,Spinner,Table,TableBody,TableCell,TableHead,TableHeader,TableRow,Tabs,TabsContent,TabsList,TabsTrigger,Textarea} from '../../shared/shadcn-ui.js'

import {InputGroup,InputGroupInput,InputGroupAddon,InputGroupButton} from '../../shared/shadcn-ui.js'

import {XIcon} from '@lucide/vue'
</script>
<template><div v-if="legacyVersions" class="change-center"><h2>发布版本<HelpTip text="发布版本已移至研发协作。"/></h2><Button @click="openWorkspace('research','#/versions'+(route.query.item?'?item='+route.query.item:''))" variant="default" size="default" type="button">打开发布版本</Button></div><section v-else :class="{'list-page-card':!route.query.item}" class="change-center "><PageHeader v-if="!route.query.item"><template #title><div class="title"><h2>{{versionWorkspace?'发布版本':tab==='work'?'交付工作项':'变更列表'}}</h2><HelpTip :text="tab==='work'?'仅展示有实际变更的交付工作项，默认生产范围。来源状态、变更准备和目标执行结果分别展示；子 Bug 聚合到主项，待归属不算生产候选。':'需求与变更独立存在；发布版本汇总本次交付范围，不绑定环境。执行时由部署编排选择环境，审核的是具体执行快照。'"/></div></template><template #actions><div v-if="!route.query.item" class="actions"><Button v-if="tab==='work'" @click="openResearchWork" variant="outline" size="default" type="button">查看来源工作项</Button><Button v-if="tab!=='work'" @click="open(tab)" variant="default" size="default" type="button">＋
        {{
          tab === "work"
            ? "新建需求"
            : tab === "versions"
              ? "创建发布版本"
              : "新建变更"
        }}</Button></div></template></PageHeader><p v-if="addedVersion&&!route.query.item" class="notice">已纳入 {{addedVersion.version}}。<Button @click="view('versions',addedVersion);addedVersion=null" variant="outline" size="default" type="button">进入版本 →</Button></p><template v-if="route.query.item"><section v-if="detail" class="record-detail"><PageHeader show-back @back="back"><template #title><h2>{{tab==='versions'?detail.version+' · ':''}}{{detail.title}}</h2></template><template #subtitle><Badge :variant="(detail.status==='draft'?'gray':detail.status==='ready'?'blue':'green')==='red'?'destructive':'secondary'">{{tab==='versions'&&detail.status==='ready'?'已冻结':labels[detail.status]||detail.status}}</Badge><span>创建人 {{detail.owner||'—'}}</span><span v-if="detail.external_id">{{detail.source}} / {{detail.external_id}}</span><a v-if="safeURL(detail.url)" :href="safeURL(detail.url)" target="_blank" rel="noopener noreferrer">外部记录</a></template><template #actions><Button v-if="canManage(detail)&&(tab==='work'||(detail.status==='draft'&&!(tab==='changes'&&(detail.sql?.data_sync||isFrozenChange(detail)))))" @click="open(tab,detail)" variant="outline" size="default" type="button">{{tab==='versions'?'编辑范围':tab==='changes'?'编辑变更':'编辑需求'}}</Button><template v-if="tab==='work'"><Button @click="chooseVersion('work',detail)" variant="outline" size="default" type="button">纳入版本</Button><Button @click="editDelivery(detail)" variant="outline" size="default" type="button">维护交付内容</Button><Button @click="router.push({path:'/orchestration',query:{work_item:String(detail.id)}})" variant="default" size="default" type="button">发起交付</Button></template><Button v-else-if="tab==='changes'&&detail.status==='draft'&&canManage(detail)" @click="action('changes',detail,'ready')" variant="default" size="default" type="button" :disabled="(saving)"><Spinner v-if="saving"/>标记就绪</Button><Button v-else-if="tab==='versions'&&detail.status==='draft'&&canManage(detail)" @click="action('versions',detail,'ready')" variant="default" size="default" type="button" :disabled="(saving)"><Spinner v-if="saving"/>冻结并标记就绪</Button><Button v-else-if="tab==='versions'&&['ready','completed'].includes(detail.status)" @click="router.push({path:'/orchestration',query:{version:String(detail.id)}})" variant="default" size="default" type="button">发起交付</Button></template><template v-if="tab==='versions'" #navigation><Tabs class="section-tabs" :model-value="detailTab" @update:model-value="detailTab=$event;"><div class="flex min-w-0 items-center gap-2"><TabsList class="h-auto! max-w-full flex-wrap gap-y-1 [&>[data-slot=tabs-trigger]]:h-7"><TabsTrigger :value="&quot;overview&quot;">版本概览</TabsTrigger><TabsTrigger :value="&quot;scope&quot;">{{'实施范围 · '+detailChanges.length}}</TabsTrigger><TabsTrigger v-if="detailRuns.length" :value="&quot;history&quot;">{{'交付记录 · '+detailRuns.length}}</TabsTrigger></TabsList></div><TabsContent :value="&quot;overview&quot;" force-mount v-show="!(detailTab!==&quot;overview&quot;)" class="min-w-0"></TabsContent><TabsContent :value="&quot;scope&quot;" force-mount v-show="!(detailTab!==&quot;scope&quot;)" class="min-w-0"></TabsContent><TabsContent v-if="detailRuns.length" :value="&quot;history&quot;" force-mount v-show="!(detailTab!==&quot;history&quot;)" class="min-w-0"></TabsContent></Tabs></template></PageHeader><div v-show="tab!=='versions'||detailTab==='overview'"><RichDescription v-if="detail.description||detail.summary||tab!=='versions'" :key="`${tab}-${detail.id}`" :content="detail.description||detail.summary||''" :format="detail.description_format||(tab==='changes'?'markdown':'')" :source-url="detail.url||''" :title="tab==='versions'?'版本说明':tab==='changes'?'变更说明':detail.kind==='bug'?'Bug 说明':'需求说明'" full thumbnails/><ReleaseReadinessPanel v-if="tab==='versions'" :key="detail.id" :version-id="detail.id" :revision="detail.updated_at" :actor="me.username" :environments="environments" @resolve="resolveReadiness"/><ReleaseNotice v-if="tab==='versions'" :key="detail.id" kind="version" :record-id="detail.id" :context-revision="detail.updated_at" :editable="canManage(detail)"/></div><div v-if="tab==='versions'" v-show="detailTab==='scope'" class="detail-sections"><Card><CardHeader><CardTitle>{{'需求与 Bug · '+(detail.work_items?.length||0)}}</CardTitle><CardAction><Button v-if="detail.status==='draft'&&canManage(detail)" @click="refreshScope(detail)" variant="ghost" size="default" type="button" :disabled="(saving)"><Spinner v-if="saving"/>刷新关联范围</Button></CardAction></CardHeader><CardContent class="min-w-0"><ul v-if="detail.work_items?.length" class="version-scope-work-list divide-y" tabindex="0" aria-label="版本关联工作项" role="region"><template v-for="{item:w} in (detail.work_items||[]).map(item=>({item}))"><li class="py-3"><Button @click="view('work',w)" class="h-auto min-h-8 w-full min-w-0 justify-start whitespace-normal break-words text-left" variant="ghost" size="default" type="button">#{{w.id}} · {{w.title}}</Button></li></template></ul><Empty v-else><EmptyHeader><EmptyDescription>{{"未关联需求或 Bug，可直接选择独立变更"}}</EmptyDescription></EmptyHeader></Empty></CardContent></Card><Card><CardHeader><CardTitle>{{'实施范围 · '+detailChanges.length}}</CardTitle><CardAction><Button v-if="detail.status==='draft'&&canManage(detail)" @click="open('versions',detail)" variant="ghost" size="default" type="button">编辑范围</Button></CardAction></CardHeader><CardContent class="min-w-0"><div class="console-table min-w-0 max-w-full"><div class="max-w-full overflow-auto rounded-lg border" :style="{maxHeight:({x:920}).y?({x:920}).y+'px':undefined}"><Table container-class="max-h-[420px] overflow-auto" :container-tabindex="0" container-label="版本实施范围" class="table-fixed min-w-[var(--table-min-width)]" :style="{'--table-min-width':(({x:920}).x||({x:920}).minWidth||920)+'px'}"><TableHeader class="sticky top-0 z-10 bg-background"><TableRow><TableHead :style="{width:(340)+'px'}">变更</TableHead><TableHead :style="{width:(220)+'px'}">应用</TableHead><TableHead :style="{width:(130)+'px'}">状态</TableHead><TableHead :style="{width:(230)+'px'}">操作</TableHead></TableRow></TableHeader><TableBody><template v-for="(record,rowIndex) in (detailChanges||[])" :key="typeof (&quot;id&quot;)==='function'?(&quot;id&quot;)(record):record[&quot;id&quot;]??rowIndex"><TableRow><TableCell class="align-top"><div class="min-w-0 break-words" :class="{truncate:!!(&quot;&quot;)}"><template v-for="{record:c} in [{record,rowIndex}]"><Button @click="view('changes',c)" class="h-auto min-h-8 max-w-full justify-start whitespace-normal break-words text-left" variant="ghost" size="sm" type="button">#{{c.id}} · {{c.title}}</Button><small>{{kindLabel(c.kind)}} · {{c.reference||c.sql?.resource_alias||c.summary||'暂无说明'}}</small></template></div></TableCell><TableCell class="align-top"><div class="min-w-0 break-words" :class="{truncate:!!(&quot;&quot;)}">{{record["service"]}}</div></TableCell><TableCell class="align-top"><div class="min-w-0 break-words" :class="{truncate:!!(false)}"><template v-for="{record:c} in [{record,rowIndex}]"><Badge :variant="(c.status==='draft'?'gray':'blue')==='red'?'destructive':'secondary'">{{labels[c.status]||c.status}}</Badge></template></div></TableCell><TableCell class="align-top"><div class="min-w-0 break-words" :class="{truncate:!!(false)}"><template v-for="{record:c} in [{record,rowIndex}]"><div class="cell-actions"><Button @click="view('changes',c)" variant="ghost" size="sm" type="button">查看变更</Button><Button v-if="c.service&&['code','artifact'].includes(c.kind)" @click="router.push(`/svc/${encodeURIComponent(c.service)}/build`)" variant="ghost" size="sm" type="button">构建与制品</Button></div></template></div></TableCell></TableRow></template><TableRow v-if="!(detailChanges||[]).length"><TableCell :colspan="4"><Empty><EmptyHeader><EmptyDescription>{{"尚未选择变更，请编辑版本范围"}}</EmptyDescription></EmptyHeader></Empty></TableCell></TableRow></TableBody></Table></div></div></CardContent></Card></div><template v-else><div v-if="tab==='work'&&detail.source?.startsWith('zentao:')" class="chips"><span>指派给：{{detail.assigned_to==='closed'?'已关闭':detail.assigned_to||'—'}}</span><span>由谁创建：{{detail.opened_by||'—'}}</span><span>由谁解决：{{detail.resolved_by||'—'}}</span><span>由谁关闭：{{detail.closed_by||'—'}}</span></div><Empty v-if="!detailChanges.length"><EmptyHeader><EmptyDescription>{{"尚无交付内容，请通过维护交付内容添加"}}</EmptyDescription></EmptyHeader></Empty><Card v-for="c in detailChanges" :key="c.id" class="detail-change-card"><CardHeader><CardTitle>{{tab==='changes'?kindLabel(c.kind)+'内容':kindLabel(c.kind)+' · '+c.title}}</CardTitle><CardAction><Badge :variant="(c.status==='draft'?'gray':'blue')==='red'?'destructive':'secondary'">{{labels[c.status]||c.status}}</Badge></CardAction></CardHeader><CardContent class="min-w-0"><dl class="grid min-w-0 gap-4 sm:grid-cols-2"><div class="min-w-0 space-y-1"><dt class="text-sm text-muted-foreground">应用</dt><dd class="min-w-0 break-words">{{c.service||'未关联应用'}}</dd></div><div class="min-w-0 space-y-1"><dt class="text-sm text-muted-foreground">参考信息</dt><dd class="min-w-0 break-words">{{c.reference||c.sql?.resource_alias||c.summary||'—'}}</dd></div></dl><template v-if="tab==='changes'"><div v-if="c.work_items?.length" class="chips"><Button v-for="w in c.work_items" :key="w.id" @click="view('work',w)" variant="ghost" size="default" type="button">#{{w.id}} {{w.title}}</Button></div><SQLCode v-if="c.sql?.content" :content="c.sql.content"/><Accordion v-if="c.sql?.environment_overrides?.length" class="platform-collapse" type="multiple" :model-value="(_view1||[]).map(String)" @update:model-value="_view1=$event"><AccordionItem :value="String(&quot;panel&quot;)"><AccordionTrigger>SQL 环境例外 · {{c.sql.environment_overrides.length}}</AccordionTrigger><AccordionContent force-mount v-show="!(!(_view1||[]).map(String).includes(String(&quot;panel&quot;)))"><div v-for="e in c.sql.environment_overrides" :key="e.environment"><b>{{e.environment}} · {{e.skip?'无需执行':'使用专用 SQL'}}</b><p>{{e.reason}}</p><SQLCode v-if="!e.skip" :content="e.content" label="环境专用 SQL"/></div></AccordionContent></AccordionItem></Accordion><pre v-if="c.redis?.commands" class="detail-code">{{c.redis.commands}}</pre><div v-if="c.nacos?.entries?.length"><p v-for="(entry,index) in c.nacos.entries" :key="index"><code>{{entry.key}}</code> · {{entry.action==='delete'?'删除':entry.value}}</p></div><div v-if="c.rollback_plan" class="space-y-2"><p>恢复方案</p><MarkdownContent :content="c.rollback_plan" label="恢复方案"/></div></template><p v-if="c.sql?.data_sync" class="meta">数据同步任务 #{{c.sql.data_sync.task_id}} · 修订 {{c.sql.data_sync.task_revision}} · 本次目标 {{c.sql.data_sync.target_environment}}；差异已冻结，重新修改请再次对比。</p><p v-if="c.redis" class="meta">缓存资源：{{c.redis.resource_alias}} <Button @click="checkRedis(c)" variant="ghost" size="default" type="button">Redis 命令预检</Button></p><p v-if="c.nacos" class="meta">配置资源：{{c.nacos.resource_alias}} · {{c.nacos.data_id}} <Button @click="checkNacos(c)" variant="ghost" size="default" type="button">Nacos 差异预览</Button></p></CardContent><CardFooter><Button v-if="tab==='changes'" @click="chooseVersion('changes',c)" variant="ghost" size="default" type="button">纳入版本</Button><Button v-if="c.service&&['code','artifact'].includes(c.kind)" @click="router.push(`/svc/${encodeURIComponent(c.service)}/build`)" variant="ghost" size="default" type="button">查看构建与制品</Button><Button v-if="c.sql" @click="checkSQL(c)" variant="ghost" size="default" type="button">SQL 预检</Button><template v-if="canManage(c)"><Button v-if="tab!=='changes'&&c.status==='draft'&&!c.sql?.data_sync" @click="open('changes',c)" variant="ghost" size="default" type="button">编辑变更</Button><Button v-if="tab!=='changes'&&c.status==='draft'" @click="action('changes',c,'ready')" variant="ghost" size="default" type="button" :disabled="(saving)"><Spinner v-if="saving"/>标记就绪</Button><Button v-else-if="c.status!=='draft'&&!isFrozenChange(c)" @click="action('changes',c,'reopen')" variant="ghost" size="default" type="button" :disabled="(saving)"><Spinner v-if="saving"/>退回草稿</Button></template><span v-if="isFrozenChange(c)" class="meta">已随版本冻结；后续修改请新建变更</span></CardFooter></Card><Card v-if="tab==='work'"><CardHeader><CardTitle>所在版本</CardTitle></CardHeader><CardContent class="min-w-0"><div class="chips"><Button v-for="v in detailVersions" :key="v.id" @click="view('versions',v)" variant="ghost" size="default" type="button">{{v.version}} · {{labels[v.status]}}</Button><span v-if="!detailVersions.length">未纳入版本，可继续独立开发与测试。</span></div></CardContent></Card></template><Card v-if="detailRuns.length" v-show="tab!=='versions'||detailTab==='history'"><CardHeader><CardTitle>交付记录</CardTitle></CardHeader><CardContent class="min-w-0"><ul class="divide-y"><template v-for="{item:r} in (detailRuns||[]).map(item=>({item}))"><li class="py-3"><div class="ml-auto flex gap-2"><Button @click="router.push({path:'/orchestration',query:{run:String(r.id)}})" variant="ghost" size="default" type="button">查看执行</Button></div><div class="flex flex-wrap items-center gap-2"><span>#{{r.id}}</span><span>{{r.plan.environment.display_name}}</span><Badge variant="secondary">{{runLabel(r.status)}}</Badge><span v-if="r.status==='success'&&r.plan.scope?.excluded_change_ids?.length">部分范围</span></div></li></template></ul></CardContent></Card><p v-if="addedVersion" class="notice">已纳入 {{addedVersion.version}}。<Button @click="view('versions',addedVersion);addedVersion=null" variant="ghost" size="default" type="button">进入版本 →</Button></p></section><Empty v-else><EmptyHeader><EmptyDescription>{{"正在读取记录或记录不存在"}}</EmptyDescription></EmptyHeader></Empty></template><div v-show="!route.query.item"><WorkItemsTable v-if="tab==='work'" ref="workTable" :me="me" :can-manage="canManage" :environments="environments" @view="openWorkspace('research','#/work/'+$event.id)" @preview="openWorkspace('research','#/work/'+$event.id+'?action=preview')" @run="router.push({path:'/orchestration',query:{run:String($event)}})" @delivery="editDelivery" @version="chooseVersion('work',$event)"/><DeliveryRecordsTable v-show="tab==='changes'" ref="changeTable" type="changes" :active="tab==='changes'" :me="me" :services="services" :can-manage="canManage" @view="view('changes',$event)" @edit="editRecord('changes',$event)" @version="chooseVersion('changes',$event)" @action="action('changes',$event.item,$event.op)" @preview="previewRecord" @deliver="router.push({path:'/orchestration',query:{changes:$event.ids.join(',')}})" @work="view('work',$event)" @create="open('changes')"/><DeliveryRecordsTable v-show="tab==='versions'" ref="versionTable" type="versions" :active="tab==='versions'" :me="me" :services="services" :can-manage="canManage" @view="view('versions',$event)" @edit="editRecord('versions',$event)" @action="action('versions',$event.item,$event.op)" @deliver="router.push({path:'/orchestration',query:{version:String($event.id)}})" @create="open('versions')"/></div><Dialog :open="showRedisPreview" @update:open="async v=>{if(!v){closeRedisPreview()}}"><DialogContent class="flex max-h-[92dvh] flex-col gap-0 overflow-hidden p-0 sm:max-w-[var(--editor-width)]" :class="false?'inset-0 h-dvh max-h-dvh w-screen max-w-none translate-x-0 translate-y-0 rounded-none sm:max-w-none':''" :style="{'--editor-width':typeof (&quot;min(1060px, 92vw)&quot;)==='number'?(&quot;min(1060px, 92vw)&quot;)+'px':(&quot;min(1060px, 92vw)&quot;)}" :show-close-button="true" @interact-outside="e=>{e.preventDefault()}"><DialogHeader class="shrink-0 border-b p-5 pr-12"><DialogTitle>Redis 命令预检（只读）</DialogTitle><DialogDescription class="sr-only">详情与操作</DialogDescription></DialogHeader><div class="min-h-0 flex-1 overflow-auto p-5"><div class="form-grid"><p class="span">{{redisPreviewItem?.title}} · 仅测试连接和命令范围，不执行命令、不读取缓存值。</p><Field ><FieldLabel as="div">目标环境</FieldLabel><OptionPicker v-model="redisEnvironment" :disabled="saving" :options="environments.map(e=>({value:e.name,label:e.display_name||e.name}))" :style="{width:'100%',maxWidth:'100%'}"/></Field><Button @click="runRedisPreview" variant="default" size="default" type="button" :disabled="(!redisEnvironment)||(saving)"><Spinner v-if="saving"/>检查连接与命令范围</Button><div class="span"><RedisPlanPreview :plans="redisPreview?[redisPreview]:[]"/></div></div></div></DialogContent></Dialog><Dialog :open="showNacosPreview" @update:open="async v=>{if(!v){closeNacosPreview()}}"><DialogContent class="flex max-h-[92dvh] flex-col gap-0 overflow-hidden p-0 sm:max-w-[var(--editor-width)]" :class="false?'inset-0 h-dvh max-h-dvh w-screen max-w-none translate-x-0 translate-y-0 rounded-none sm:max-w-none':''" :style="{'--editor-width':typeof (&quot;min(1060px, 92vw)&quot;)==='number'?(&quot;min(1060px, 92vw)&quot;)+'px':(&quot;min(1060px, 92vw)&quot;)}" :show-close-button="true" @interact-outside="e=>{e.preventDefault()}"><DialogHeader class="shrink-0 border-b p-5 pr-12"><DialogTitle>Nacos 配置差异（只读预览）</DialogTitle><DialogDescription class="sr-only">详情与操作</DialogDescription></DialogHeader><div class="min-h-0 flex-1 overflow-auto p-5"><div class="form-grid"><p class="span">{{nacosPreviewItem?.title}} · 预览不发布配置；执行单提交时会重新读取并冻结基线。</p><Field ><FieldLabel as="div">目标环境</FieldLabel><OptionPicker v-model="nacosEnvironment" :disabled="saving" :options="environments.map(e=>({value:e.name,label:e.display_name||e.name}))" :style="{width:'100%',maxWidth:'100%'}"/></Field><Button @click="runNacosPreview" variant="default" size="default" type="button" :disabled="(!nacosEnvironment)||(saving)"><Spinner v-if="saving"/>读取目标配置并对比</Button><div class="span"><NacosPlanPreview :plans="nacosPreview?[nacosPreview]:[]"/></div></div></div></DialogContent></Dialog><WorkDeliveryEditor ref="batchEditor" batch :show="showBatchEditor" :services="services" :work-items="work" :can-manage="canManage" @close="showBatchEditor=false" @saved="load"/><WorkDeliveryEditor ref="deliveryEditor" :show="showDeliveryEditor" :item="deliveryItem" :services="services" :work-items="work" :can-manage="canManage" @close="showDeliveryEditor=false" @saved="load" @open-sync="openSyncTasks"/><DataSyncTasks :show="showSyncTasks" :services="services" :environments="environments" :item="deliveryItem" @close="showSyncTasks=false" @generated="syncGenerated" @preview="checkSQL"/><Dialog :open="showAddVersion" @update:open="async v=>{if(!v){showAddVersion=false}}"><DialogContent class="flex max-h-[92dvh] flex-col gap-0 overflow-hidden p-0 sm:max-w-[var(--editor-width)]" :class="false?'inset-0 h-dvh max-h-dvh w-screen max-w-none translate-x-0 translate-y-0 rounded-none sm:max-w-none':''" :style="{'--editor-width':typeof (&quot;min(720px, 92vw)&quot;)==='number'?(&quot;min(720px, 92vw)&quot;)+'px':(&quot;min(720px, 92vw)&quot;)}" :show-close-button="true" @interact-outside="e=>{e.preventDefault()}"><DialogHeader class="shrink-0 border-b p-5 pr-12"><DialogTitle>纳入发布版本</DialogTitle><DialogDescription class="sr-only">详情与操作</DialogDescription></DialogHeader><div class="min-h-0 flex-1 overflow-auto p-5"><div class="form-grid"><p class="span">{{addItems.length===1?addItems[0]?.title:'已选择 '+addItems.length+' 项变更'}} · 仅调整草稿范围，不触发构建或执行</p><Field class="span"><FieldLabel as="div">搜索草稿版本</FieldLabel><InputGroup  class="min-w-0 "><InputGroupInput v-model="draftQuery" placeholder="按版本号、标题或说明搜索"/><InputGroupAddon align="inline-end"><InputGroupButton v-if="draftQuery" :disabled="false" size="icon-xs" aria-label="清空输入" @click="draftQuery='' "><XIcon/></InputGroupButton></InputGroupAddon></InputGroup></Field><Field class="span"><FieldLabel as="div">草稿版本</FieldLabel><OptionPicker v-model="addVersionID" :disabled="draftLoading" :options="draftVersions.filter(canManage).map(v=>({value:v.id,label:v.version+' · '+v.title}))" :style="{width:'100%',maxWidth:'100%'}"/></Field><p v-if="draftError" role="alert" class="span">{{draftError}} <Button @click="loadDraftVersions()" variant="outline" size="default" type="button">重试</Button></p><p v-else-if="!draftLoading&&!draftVersions.length" class="span">没有可编辑的草稿版本，可调整搜索条件或创建新版本。</p><Button v-if="draftVersions.length<draftTotal" @click="loadDraftVersions(draftPage+1)" class="span" variant="outline" size="default" type="button" :disabled="(draftLoading)"><Spinner v-if="draftLoading"/>加载更多草稿版本（已加载 {{draftVersions.length}} / {{draftTotal}}）</Button></div></div><DialogFooter><Button @click="showAddVersion=false;open('versions');form.work_item_ids=addType==='work'?addItems.map(item=>item.id):[];form.manual_change_ids=addType==='changes'?addItems.map(item=>item.id):[]" variant="outline" size="default" type="button">创建新版本</Button><Button @click="showAddVersion=false" variant="outline" size="default" type="button">取消</Button><Button @click="addToVersion" variant="default" size="default" type="button" :disabled="(draftLoading||!addVersionID)||(saving)"><Spinner v-if="saving"/>确认纳入</Button></DialogFooter></DialogContent></Dialog><Dialog :open="showResources" @update:open="async v=>{if(!v){showResources=false;load()}}"><DialogContent class="flex max-h-[92dvh] flex-col gap-0 overflow-hidden p-0 sm:max-w-[var(--editor-width)]" :class="false?'inset-0 h-dvh max-h-dvh w-screen max-w-none translate-x-0 translate-y-0 rounded-none sm:max-w-none':''" :style="{'--editor-width':typeof (&quot;min(1060px, 92vw)&quot;)==='number'?(&quot;min(1060px, 92vw)&quot;)+'px':(&quot;min(1060px, 92vw)&quot;)}" :show-close-button="true" @interact-outside="e=>{e.preventDefault()}"><DialogHeader class="shrink-0 border-b p-5 pr-12"><DialogTitle>资源中心</DialogTitle><DialogDescription class="sr-only">详情与操作</DialogDescription></DialogHeader><div class="min-h-0 flex-1 overflow-auto p-5"><ResourcesView v-if="showResources" :directAPI="directResourcesAPI"/></div></DialogContent></Dialog><ApplicationResourcesEditor :show="showBindings" :services="services" :initial-service="form.service" @close="showBindings=false;loadSQLResources()"/><Dialog :open="showSync" @update:open="async v=>{if(!v){showSync=false}}"><DialogContent class="flex max-h-[92dvh] flex-col gap-0 overflow-hidden p-0 sm:max-w-[var(--editor-width)]" :class="false?'inset-0 h-dvh max-h-dvh w-screen max-w-none translate-x-0 translate-y-0 rounded-none sm:max-w-none':''" :style="{'--editor-width':typeof (&quot;min(720px, 92vw)&quot;)==='number'?(&quot;min(720px, 92vw)&quot;)+'px':(&quot;min(720px, 92vw)&quot;)}" :show-close-button="true" @interact-outside="e=>{e.preventDefault()}"><DialogHeader class="shrink-0 border-b p-5 pr-12"><DialogTitle>同步工作项</DialogTitle><DialogDescription class="sr-only">详情与操作</DialogDescription></DialogHeader><div class="min-h-0 flex-1 overflow-auto p-5"><div class="form-grid"><Field class="span"><FieldLabel as="div">来源连接</FieldLabel><OptionPicker v-model="syncForm.connection" :options="syncConnections.map(c=>c.name)" :style="{width:'100%',maxWidth:'100%'}"/></Field><Field class="span"><FieldLabel as="div">本次状态</FieldLabel><OptionPicker v-model="syncStatusModel" :options="syncStatusOptions" :style="{width:'100%',maxWidth:'100%'}" :disabled="saving"/></Field><Field class="span"><FieldLabel as="div">更新或关闭日期基线（留空沿用连接设置）</FieldLabel><DateFilter v-model="syncForm.baseline" :disabled="saving" placeholder="沿用连接设置；包含当天"/></Field><p class="notice span">可同步类型：{{(syncCapability?.kinds||[]).map(k=>k==='bug'?'BUG':'需求').join('、')||'请选择来源连接'}}。状态默认沿用连接设置，可按本次需要调整；筛选不删除此前已同步的记录。按更新、关闭时间倒序读取，日期基线前的数据不纳入本次范围。已关闭不等于未上线；历史上线范围仍需核对。</p><p v-for="state in syncStates.filter(s=>s.connection===syncForm.connection)" :key="state.connection" class="span">上次同步：{{state.status==='success'?'成功':'失败'}} · {{state.detail}}</p></div></div><DialogFooter><Button @click="openResources" variant="outline" size="default" type="button">配置来源连接</Button><Button @click="syncIssues" variant="default" size="default" type="button" :disabled="(!syncForm.connection)||(saving)"><Spinner v-if="saving"/>立即同步</Button></DialogFooter></DialogContent></Dialog><Dialog :open="showSQLPreview" @update:open="async v=>{if(!v){closeSQLPreview()}}"><DialogContent class="flex max-h-[92dvh] flex-col gap-0 overflow-hidden p-0 sm:max-w-[var(--editor-width)]" :class="false?'inset-0 h-dvh max-h-dvh w-screen max-w-none translate-x-0 translate-y-0 rounded-none sm:max-w-none':''" :style="{'--editor-width':typeof (&quot;min(1060px, 92vw)&quot;)==='number'?(&quot;min(1060px, 92vw)&quot;)+'px':(&quot;min(1060px, 92vw)&quot;)}" :show-close-button="true" @interact-outside="e=>{e.preventDefault()}"><DialogHeader class="shrink-0 border-b p-5 pr-12"><DialogTitle>SQL 结构与影响范围预检（不执行）</DialogTitle><DialogDescription class="sr-only">详情与操作</DialogDescription></DialogHeader><div class="min-h-0 flex-1 overflow-auto p-5"><div class="form-grid"><div class="sql-preview-toolbar span"><Field ><FieldLabel as="div">目标环境</FieldLabel><OptionPicker v-model="previewEnvironment" :disabled="saving" :options="environments.map(e=>({value:e.name,label:e.display_name}))" :style="{width:'100%',maxWidth:'100%'}"/></Field><Button @click="runSQLPreview" variant="outline" size="default" type="button" :disabled="(!previewEnvironment)||(saving)"><Spinner v-if="saving"/>读取目标库并预检</Button></div><template v-if="sqlPreview"><div v-if="sqlPreview.script_selections?.length" class="span"><SQLPlanPreview :plans="[sqlPreview]" selections-only/></div><p v-if="sqlPreview.mode==='skipped'" class="span">本环境无需执行 SQL</p><p v-else class="span">{{sqlPreview.connection}} / {{sqlPreview.database}} · {{sqlPreview.mode.toUpperCase()}}</p><p v-for="warning in sqlPreview.warnings" :key="warning" class="notice span">{{warning}}</p><article v-for="(op,index) in sqlPreview.statements" :key="index" class="span"><b>{{op.skip?'跳过（结构一致）':'计划执行'}} · {{op.reason||op.action}} · {{op.expected_rows||0}} 行</b><SQLCode :content="op.sql" label="预检 SQL"/></article><p class="notice span">预检不执行 SQL。标记就绪后，在部署编排中选择 SQL 节点，提交冻结计划并按环境审批。</p></template></div></div></DialogContent></Dialog><Dialog :open="!!dialog&amp;&amp;!showWorkPicker" @update:open="async v=>{if(!v){closeDialog()}}"><DialogContent class="flex max-h-[92dvh] flex-col gap-0 overflow-hidden p-0 sm:max-w-[var(--editor-width)]" :class="dialogFullscreen?'inset-0 h-dvh max-h-dvh w-screen max-w-none translate-x-0 translate-y-0 rounded-none sm:max-w-none':''" :style="{'--editor-width':typeof (&quot;min(1060px, 92vw)&quot;)==='number'?(&quot;min(1060px, 92vw)&quot;)+'px':(&quot;min(1060px, 92vw)&quot;)}" :show-close-button="true" @interact-outside="e=>{e.preventDefault()}"><DialogHeader class="shrink-0 border-b p-5 pr-12"><DialogTitle><EditorDialogTitle :title="dialog==='work'?'需求 / BUG':dialog==='versions'?'发布版本':'变更'" :fullscreen="dialogFullscreen" @toggle="dialogFullscreen=!dialogFullscreen"/></DialogTitle><DialogDescription class="sr-only">详情与操作</DialogDescription></DialogHeader><div class="min-h-0 flex-1 overflow-auto p-5"><form id="change-editor-form" @input="clearChangeValidation" @change="clearChangeValidation" @submit.prevent="save" class="form-grid"><Field class="span"><FieldLabel as="div">标题</FieldLabel><Input v-model="form.title"/></Field><template v-if="dialog === 'work'"><Field ><FieldLabel as="div">类型</FieldLabel><OptionPicker v-model="form.kind" :options="[
                { value: 'requirement', label: '需求' },
                { value: 'bug', label: 'BUG' },
              ]" :style="{width:'100%',maxWidth:'100%'}"/></Field><Field ><FieldLabel as="div">来源</FieldLabel><OptionPicker v-model="form.source" :options="sourceOptions" :style="{width:'100%',maxWidth:'100%'}"/></Field><Field v-if="form.source !== 'manual'"><FieldLabel as="div">外部编号</FieldLabel><Input v-model="form.external_id"/></Field><Field v-if="form.source !== 'manual'"><FieldLabel as="div">记录链接</FieldLabel><Input v-model="form.url"/></Field><Field v-if="form.id"><FieldLabel as="div">状态</FieldLabel><OptionPicker v-model="form.status" :options="[
                { value: 'open', label: '进行中' },
                { value: 'closed', label: '已关闭' },
              ]" :style="{width:'100%',maxWidth:'100%'}"/></Field><label class="span">说明<MarkdownEditor :key="form.id||'new'" v-model="form.description" :format="form.description_format" :source-url="form.url" :disabled="saving" @uploading="imageUploading=$event"/></label><p class="notice span">
            外部 Bug 在研发工作台实时查询，选中后登记交付关联；此处用于自定义需求，不再全量同步来源数据。
          </p></template><template v-if="dialog === 'changes'"><Field ><FieldLabel as="div">变更类型</FieldLabel><OptionPicker v-model="form.kind" :options="kinds" :style="{width:'100%',maxWidth:'100%'}"/></Field><Field data-field="service"><FieldLabel as="div">关联应用</FieldLabel><OptionPicker v-model="form.service" :options="services.map(applicationOption)" all-label="不关联应用"  :style="{width:'100%',maxWidth:'100%'}"/></Field><Field v-if="form.kind==='code'" class="span" data-field="reference"><FieldLabel as="div">代码分支</FieldLabel><GitBranchSelect v-model="form.reference" :repositories="form.service?[{service:form.service}]:[]"/></Field><template v-if="form.kind==='nacos'"><Field data-field="resource"><FieldLabel as="div">配置资源</FieldLabel><OptionPicker v-model="form.nacos.resource_alias" :disabled="!!form.nacos.restore" :options="sqlResources.map(r=>({value:r.alias,label:r.description||r.alias}))"  :style="{width:'100%',maxWidth:'100%'}"/><span v-if="sqlResourcesLoading">正在读取…</span><span v-else-if="sqlResourcesError">{{sqlResourcesError}}</span><Button v-if="form.service&&(me.role==='admin'||me.username==='local')" @click="showBindings=true" variant="outline" size="sm" type="button">配置应用资源</Button></Field><NacosChangeFields :model="form.nacos" :disabled="saving"/></template><template v-if="form.kind==='redis'"><Field data-field="resource"><FieldLabel as="div">Redis 资源</FieldLabel><OptionPicker v-model="form.redis.resource_alias" :options="sqlResources.map(r=>({value:r.alias,label:r.alias}))" :style="{width:'100%',maxWidth:'100%'}"/><span v-if="sqlResourcesLoading">正在读取…</span><span v-else-if="sqlResourcesError">{{sqlResourcesError}}</span><Button v-if="form.service&&(me.role==='admin'||me.username==='local')" @click="showBindings=true" variant="outline" size="sm" type="button">配置应用资源</Button></Field><RedisChangeFields :model="form.redis" :disabled="saving"/></template><Field v-if="!['mysql','code','nacos','redis'].includes(form.kind)" class="span"><FieldLabel as="div">{{
              form.kind === "code"
                ? "代码分支"
                : form.kind === "artifact"
                  ? "制品说明 / 标识"
                  : form.kind === "nacos"
                    ? "配置项与期望值（本版记录）"
                    : form.kind === "mysql"
                      ? "SQL 内容（本版记录）"
                      : "实施内容（本版记录）"
            }}</FieldLabel><Textarea v-model="form.reference" :rows="({minRows:(3),maxRows:Math.max((3),16)}).minRows||4"/></Field><template v-if="form.kind==='mysql'"><Field data-field="resource"><FieldLabel as="div">数据库资源</FieldLabel><OptionPicker v-model="form.sql.resource_alias" :options="sqlResources.map(r=>({value:r.alias,label:r.alias}))" :style="{width:'100%',maxWidth:'100%'}"/><span v-if="sqlResourcesLoading">正在读取…</span><span v-else-if="sqlResourcesError">{{sqlResourcesError}}</span><span v-else-if="!sqlResources.length">{{form.service?'该应用尚未声明数据库资源':'先选择关联应用'}}</span><Button v-if="form.service&&(me.role==='admin'||me.username==='local')" @click="showBindings=true" variant="outline" size="sm" type="button">配置应用资源</Button></Field><SQLScriptFields :model="form.sql" :disabled="saving" :force-open="changeInvalid?.field==='sql_environment_overrides'"/><p class="notice span">保存草稿保留 SQL 原文；就绪时检查支持的语法，发布前再按目标环境预检与审批。</p></template><Field ><FieldLabel as="div">关联需求 / BUG（可选）</FieldLabel><OptionPicker v-model="form.work_item_ids" multiple :options="
                work.map((w) => ({ value: w.id, label: `#${w.id} ${w.title}` }))
              "  :style="{width:'100%',maxWidth:'100%'}"/></Field><Field ><FieldLabel as="div">风险等级</FieldLabel><OptionPicker v-model="form.risk_level" :options="[
                { value: 'low', label: '低' },
                { value: 'medium', label: '中' },
                { value: 'high', label: '高' },
              ]" :style="{width:'100%',maxWidth:'100%'}"/></Field><Field ><FieldLabel as="div">变更说明</FieldLabel><Textarea v-model="form.summary" :rows="({minRows:(3),maxRows:Math.max((3),16)}).minRows||4"/></Field><Field ><FieldLabel as="div">回退方案</FieldLabel><Textarea v-model="form.rollback_plan" :rows="({minRows:(3),maxRows:Math.max((3),16)}).minRows||4"/></Field><p v-if="!['code', 'artifact', 'manual','mysql','nacos','redis'].includes(form.kind)" class="notice span">
            该类变更目前可登记、关联和进入版本；执行器未接通，编排会阻止提交，不会自动执行或标记成功。
          </p></template><template v-if="dialog === 'versions'"><Field ><FieldLabel as="div">版本号</FieldLabel><Input v-model="form.version" placeholder="例如 2026.10.1"/></Field><div class="version-work-choice"><span>需求与 BUG · 已选 {{form.work_item_ids?.length||0}} 项</span><Button @click="showWorkPicker=true" variant="outline" size="default" type="button" :disabled="(saving)">选择需求与 BUG</Button><small>弹窗搜索、跨页勾选；确认后自动汇总关联变更。</small></div><Field class="span"><FieldLabel as="div">手工补入变更（可含草稿）</FieldLabel><OptionPicker v-model="form.manual_change_ids" multiple :options="
                changes.map((c) => ({
                  value: c.id,
                  label: `${kindLabel(c.kind)} · ${c.title}${c.service ? ' / ' + c.service : ''}`,
                }))
              "  :style="{width:'100%',maxWidth:'100%'}"/></Field><Accordion class="selection span platform-collapse" type="multiple" :model-value="(scopePanels||[]).map(String)" @update:model-value="scopePanels=$event;"><AccordionItem :value="String(&quot;panel&quot;)"><AccordionTrigger>实施范围 · {{scopeLoading?'正在汇总…':selectedChanges.length+' 项变更（已去重）'}}</AccordionTrigger><AccordionContent force-mount v-show="!(!(scopePanels||[]).map(String).includes(String(&quot;panel&quot;)))"><p v-if="scopeError" role="alert">{{scopeError}} <Button @click="previewVersionScope" variant="outline" size="sm" type="button">重试</Button></p><div class="scope-preview-scroll"><p v-for="c in selectedChanges" :key="c.id">{{c.title}} · {{labels[c.status]}} · {{(form.manual_change_ids||[]).includes(c.id)?'手工选择':'由需求关联'}}</p></div></AccordionContent></AccordionItem></Accordion><Field class="span"><FieldLabel as="div">版本说明</FieldLabel><Textarea v-model="form.description" :rows="({minRows:(3),maxRows:Math.max((3),16)}).minRows||4"/></Field><p class="notice span">
            需求关联的变更自动汇总并去重；取消需求会移除其独有变更，共享或手工选择的变更保留。草稿可暂为空；冻结时要求变更全部就绪且每个需求至少有一项实施变更。创建版本不会执行变更。
          </p></template></form></div><DialogFooter><Button v-if="dialog==='work'&&!form.id&&(form.title||form.description)" @click="discardWorkDraft" variant="outline" size="default" type="button" :disabled="(imageUploading||saving)">放弃草稿</Button><Button @click="closeDialog" variant="outline" size="default" type="button">{{dialog==='work'&&!form.id&&(form.title||form.description)?'关闭并保留':'取消'}}</Button><Button variant="default" size="default" type="submit" form="change-editor-form" :disabled="(imageUploading||(dialog==='versions'&amp;&amp;(scopeLoading||!!scopeError)))||(saving)"><Spinner v-if="saving"/>保存</Button></DialogFooter></DialogContent></Dialog><WorkItemPicker :show="showWorkPicker" :ids="form.work_item_ids||[]" :seed="[...work,...(form.work_items||[]),...pickedWork]" :me="me" :environments="environments" :version-i-d="Number(form.id||0)" @close="showWorkPicker=false" @confirm="confirmWorkSelection"/></section></template>
<style scoped>.change-center {
  width: 100%;
  min-width: 0;
}
.title,.actions,.meta,.chips{display:flex;align-items:center;gap:12px;flex-wrap:wrap}.title{min-width:0}.title h2{margin:0;font-size:1.25rem}.meta,.chips{margin:12px 0}.actions{margin-top:12px}.meta{color:var(--muted-foreground);font-size:.875rem}.chips span{color:var(--muted-foreground);font-size:.875rem}
.form-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 16px;
}
.form-grid label {
  display: grid;
  gap: 8px;
  font-size: .875rem;
  min-width: 0;
}
.span {
  grid-column: 1/-1;
}
.notice {
  background: var(--panel2);
  padding: 12px;
  border-radius:var(--radius);
  line-height: 1.6;
  margin: 0;
}
.footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
}
.selection p {
  margin: 5px 0;
  display: flex;
  justify-content: space-between;
}

@media(max-width:700px){.form-grid{grid-template-columns:1fr}}
.version-work-choice{display:grid;gap:8px;font-size:.875rem}.version-work-choice small{color:var(--muted-foreground)}.scope-preview-scroll{max-height:180px;overflow:auto}
.sql-preview-toolbar{display:flex;align-items:flex-end;gap:12px;min-width:0}.sql-preview-toolbar>label{flex:1 1 220px;max-width:260px;min-width:0}</style>

<style scoped>.record-detail{min-width:0}.detail-sections{display:grid;gap:20px}.detail-change-card{margin-bottom:20px;min-width:0}.detail-code{font:.875rem/1.6 var(--mono);white-space:pre-wrap;overflow-wrap:anywhere;max-height:360px;overflow:auto;padding:16px;background:var(--muted);margin:12px 0}.record-detail :deep(.description-panel){margin-top:0;margin-bottom:20px}</style>

<style scoped>.change-center.list-page-card{max-width:none}.list-page-card :deep(.platform-page-header){margin-bottom:16px}
@media(max-width:760px){}</style>

<style scoped>.version-scope-work-list{max-height:260px;overflow:auto;overscroll-behavior:contain}.version-scope-work-list:focus-visible{outline:2px solid var(--ring);outline-offset:2px}.version-scope-work-list>li{padding:8px 0}</style>
