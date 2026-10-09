<script setup>
import { computed, inject, onMounted, ref, watch } from "vue";
import { onBeforeRouteLeave, useRoute, useRouter } from "vue-router";
import WorkDeliveryEditor from '../components/WorkDeliveryEditor.vue';
import WorkItemsTable from '../components/WorkItemsTable.vue';
import GitBranchSelect from '../components/GitBranchSelect.vue';
import DataSyncTasks from '../components/DataSyncTasks.vue';
import Modal from "../../shared/components/Modal.vue";
import HelpTip from "../../shared/components/HelpTip.vue";
import Sel from "../../shared/components/Sel.vue";
import ApplicationResourcesEditor from '../components/ApplicationResourcesEditor.vue';
import ResourcesView from './ResourcesView.vue';
import RichDescription from '../components/RichDescription.vue';
import NacosPlanPreview from '../components/NacosPlanPreview.vue';
import NacosChangeFields from '../components/NacosChangeFields.vue';
import RedisChangeFields from '../components/RedisChangeFields.vue';
import RedisPlanPreview from '../components/RedisPlanPreview.vue';
import { descriptionSummary } from '../richDescription.js';
import { ElButton, ElInput } from "../../shared/components/ui.js";
import { toast } from "../../shared/composables/toast.js";
import { scopeChanges, scopePayload, frozenChangeIDs } from '../versionScope.js';
const api = inject("api"),
  route = useRoute(),
  router = useRouter();
const tab = computed(() =>
  ["work", "changes", "versions"].includes(route.query.tab)
    ? route.query.tab
    : "work",
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
const bugStatuses=[{value:'closed',label:'已关闭'},{value:'active',label:'激活（未解决）'},{value:'resolved',label:'已解决（未关闭）'},{value:'unclosed',label:'未关闭（激活 + 已解决）'},{value:'all',label:'全部状态'}];
const syncDefaultStatus=computed(()=>connections.value.find(c=>c.name===syncForm.value.connection)?.config?.bug_status||'closed');
const syncStatusOptions=computed(()=>[{value:'__default',label:`沿用连接默认：${bugStatuses.find(v=>v.value===syncDefaultStatus.value)?.label||'已关闭'}`},...bugStatuses]);
const syncStatusModel=computed({get:()=>syncForm.value.status||'__default',set:value=>{syncForm.value.status=value==='__default'?'':value}});
watch(showSync,opened=>{if(opened&&!connections.value.some(c=>c.kind==='zentao'&&c.name===syncForm.value.connection)){syncForm.value.connection=connections.value.find(c=>c.kind==='zentao')?.name||'';syncForm.value.status=''}});
function openResources(){if(window.parent!==window)window.parent.postMessage({type:'platform.open',view:'resources'},location.origin);else showResources.value=true}
async function syncIssues(){
  if(!syncForm.value.connection)return toast('请选择禅道连接','warning');saving.value=true
  try{await api(`api/issue-sync/${encodeURIComponent(syncForm.value.connection)}`,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({baseline:syncForm.value.baseline,status:syncForm.value.status})});await load();toast('禅道同步完成','success')}catch(e){toast(e.message,'error')}finally{saving.value=false;await load()}
}
async function checkSQL(item){previewItem.value=item;previewEnvironment.value=environments.value[0]?.name||'';sqlPreview.value=null;showSQLPreview.value=true}
async function runSQLPreview(){
  saving.value=true;sqlPreview.value=null
  try{sqlPreview.value=await api(`api/changes/${previewItem.value.id}/sql-preview`,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({environment:previewEnvironment.value})})}catch(e){toast(e.message,'error')}finally{saving.value=false}
}
const query = ref(""),
  filter = ref(""),
  dialog = ref(""),
  form = ref({}),
  saving = ref(false);
const associationFilter=ref('');
const showDeliveryEditor=ref(false),deliveryItem=ref(null),deliveryEditor=ref(null),addedVersion=ref(null);
const showSyncTasks=ref(false);
function openSyncTasks(){showSyncTasks.value=true}
function syncGenerated(){load();deliveryEditor.value?.refreshAfterSync()}
function editDelivery(item){deliveryItem.value=item;showDeliveryEditor.value=true}
onBeforeRouteLeave(()=>!deliveryEditor.value?.hasDraft()||confirm('离开将放弃尚未保存的交付草稿，确定离开？'));
watch(()=>form.value.kind,kind=>{if(dialog.value==='changes'&&kind==='mysql'&&!form.value.sql)form.value.sql={resource_alias:'',content:'',max_affected_rows:100}})
const kinds = [
  { value: "code", label: "代码分支" },
  { value: "artifact", label: "应用制品" },
  { value: "nacos", label: "Nacos 配置" },
  { value: "mysql", label: "SQL 变更" },
  { value: "data-sync", label: "数据对比同步" },
  { value: "redis", label: "Redis 命令" },
  { value: "manual", label: "人工操作" },
];
const labels = {
  open: "进行中",
  closed: "已关闭",
  draft: "草稿",
  ready: "已就绪",
  completed: "已完成",
  canceled: "已取消",
};
const kindLabel = (value) =>
  kinds.find((v) => v.value === value)?.label || value;
const list = computed(() =>
  (tab.value === "work"
    ? work.value
    : tab.value === "versions"
      ? versions.value
      : changes.value
  ).filter(
    (v) =>
      (!filter.value || v.status === filter.value) &&
      (!associationFilter.value || (tab.value==='work' ? v.kind===associationFilter.value : tab.value==='changes' ? (associationFilter.value==='independent' ? !v.work_items?.length : !!v.work_items?.length) : true)) &&
      (!query.value ||
        `${v.title} ${v.version || ""} ${v.external_id || ""} ${v.service || ""} ${v.reference || ""}`
          .toLowerCase()
          .includes(query.value.toLowerCase())),
  ),
);
const workTable=ref(null),workDetail=ref(null);
const detail = computed(() => route.query.item ? (tab.value==='work'&&workDetail.value?.id===Number(route.query.item)?workDetail.value:(tab.value === 'work' ? work.value : tab.value === 'versions' ? versions.value : []).find(v=>v.id===Number(route.query.item))) : null);
let detailSequence=0;
async function loadWorkDetail(){
 const request=++detailSequence;
 if(tab.value!=='work'||!route.query.item)return;
 try{const item=await api(`api/work-items/${encodeURIComponent(route.query.item)}`);if(request===detailSequence)workDetail.value=item}catch(e){if(request===detailSequence){workDetail.value=null;toast(e.message,'error')}}
}
watch(()=>[tab.value,route.query.item],loadWorkDetail);
const summaries = computed(() => new Map(list.value.map(item => [item.id, descriptionSummary(item.summary || item.description, 180, item.url || '')])));
const detailChanges = computed(()=> tab.value==='work' ? changes.value.filter(c=>(c.work_items||[]).some(w=>w.id===detail.value?.id)) : detail.value?.changes || []);
const detailVersions = computed(()=>versions.value.filter(v=>(v.work_items||[]).some(w=>w.id===detail.value?.id)||(v.changes||[]).some(c=>(c.work_items||[]).some(w=>w.id===detail.value?.id))));
const detailRuns = computed(()=>deliveries.value.filter(r=>tab.value==='versions' ? r.plan.version?.id===detail.value?.id : detailVersions.value.some(v=>v.id===r.plan.version?.id)));
const selectedChanges = computed(()=>scopeChanges(changes.value,form.value.work_item_ids,form.value.manual_change_ids));
const frozenIDs=computed(()=>frozenChangeIDs(versions.value));
const isFrozenChange=c=>frozenIDs.value.has(c.id);
const showAddVersion=ref(false),addVersionID=ref(''),addItem=ref(null),addType=ref('work');
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
function view(type,item){if(type==='work')workDetail.value=item;router.push({path:'/changes',query:{tab:type,item:String(item.id)}})}
function back(){router.push({path:'/changes',query:{tab:tab.value}})}
function addChange(item){open('changes');form.value.work_item_ids=[item.id];const apps=[...new Set(changes.value.filter(c=>(c.work_items||[]).some(w=>w.id===item.id)).map(c=>c.service).filter(Boolean))];if(apps.length===1)form.value.service=apps[0]}
function chooseVersion(type,item){addType.value=type;addItem.value=item;addVersionID.value='';showAddVersion.value=true}
async function addToVersion(){
  const v=versions.value.find(v=>v.id===addVersionID.value);if(!v)return toast('请选择草稿版本','warning');saving.value=true;
  try{const w=(v.work_items||[]).map(w=>w.id),m=[...(v.manual_change_ids||[])];(addType.value==='work'?w:m).push(addItem.value.id);
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
      connections.value,environments.value,syncStates.value,
    ] = await Promise.all([
      api("api/work-items?limit=500"),
      api("api/changes?limit=500"),
      api("api/release-versions?limit=500"),
      api("api/services"),
      api("api/delivery/runs"),
      api('api/connections'),api('api/environments'),api('api/issue-sync'),
    ]);
    if(dataLoaded)workTable.value?.refresh();
    dataLoaded=true;
    await loadWorkDetail();
  } catch (e) {
    toast(e.message, "error");
  }
}
function open(type, item) {
  dialog.value = type;
  if (type === "work")
    form.value = item
      ? { ...item }
      : {
          title: "",
          kind: "requirement",
          source: "manual",
          external_id: "",
          description: "",
          status: "open",
        };
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
}
async function save() {
  if (!form.value.title?.trim()) return toast("请填写标题", "warning");
  if (
    dialog.value === "versions" &&
    !form.value.version?.trim()
  )
    return toast("请填写版本号", "warning");
  if(dialog.value==='changes'&&form.value.kind==='mysql'&&(!form.value.service||!sqlResources.value.some(r=>r.alias===form.value.sql?.resource_alias)))return toast('请选择应用及已配置的数据库资源','warning');
  if(dialog.value==='changes'&&form.value.kind==='nacos'&&(!form.value.service||!sqlResources.value.some(r=>r.alias===form.value.nacos?.resource_alias)))return toast('请选择应用及已配置的 Nacos 资源','warning');
  if(dialog.value==='changes'&&form.value.kind==='redis'&&(!form.value.service||!sqlResources.value.some(r=>r.alias===form.value.redis?.resource_alias)))return toast('请选择应用及已配置的 Redis 资源','warning');
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
    toast(e.message, "error");
  } finally {
    saving.value = false;
  }
}
async function action(type, item, op) {
  if (
    !confirm(
      `${op === "delete" ? "删除" : op === "ready" ? type==='versions'?'冻结版本范围（之后不可修改）':'标记就绪' : "退回草稿"}「${item.title}」？`,
    )
  )
    return;
  try {
    const endpoint = type === "versions" ? "release-versions" : "changes";
    await api(`api/${endpoint}/${item.id}${op === "delete" ? "" : "/" + op}`, {
      method: op === "delete" ? "DELETE" : "POST",
    });
    await load();
  } catch (e) {
    toast(e.message, "error");
  }
}
watch(tab, () => {
  query.value = "";
  filter.value = "";
  associationFilter.value='';
});
onMounted(async () => {
  me.value = await api("/api/me").catch(() => ({
    username: "local",
    role: "admin",
  }));
  await load();
});
</script>
<template>
  <div class="change-center">
    <div class="heading">
      <div class="title">
        <h2>变更管控</h2>
        <HelpTip
          text="需求与变更独立存在；发布版本汇总本次交付范围，不绑定环境。执行时由部署编排选择环境，审核的是具体执行快照。"
        />
      </div>
      <div v-if="!route.query.item" class="actions"><ElButton v-if="tab==='work'" @click="showSync=true">同步禅道 BUG</ElButton>
      <ElButton type="primary" @click="open(tab)"
        >＋
        {{
          tab === "work"
            ? "新建需求"
            : tab === "versions"
              ? "创建发布版本"
              : "新建变更"
        }}</ElButton
      ></div>
    </div>
    <nav class="tabs">
      <button
        v-for="item in [
          { key: 'work', label: '需求与 BUG' },
          { key: 'changes', label: '变更列表' },
          { key: 'versions', label: '发布版本' },
        ]"
        :key="item.key"
        :class="{ active: tab === item.key }"
        @click="router.replace({ path: '/changes', query: { tab: item.key } })"
      >
        {{ item.label }}
      </button>
      <button @click="deliveryItem=null;openSyncTasks()">数据同步任务</button>
    </nav>
    <p v-if="addedVersion&&!route.query.item" class="notice">已纳入 {{addedVersion.version}}。<ElButton @click="view('versions',addedVersion);addedVersion=null">进入版本 →</ElButton></p>
    <template v-if="route.query.item">
      <ElButton @click="back">← 返回列表</ElButton>
      <section v-if="detail" class="workspace">
        <div class="heading"><div><h2>{{tab==='versions'?detail.version+' · ':''}}{{detail.title}}</h2><p>{{labels[detail.status]}} · 平台创建人：{{detail.owner || '—'}}<template v-if="detail.external_id"> · {{detail.source}} / {{detail.external_id}}</template> <a v-if="safeURL(detail.url)" :href="safeURL(detail.url)" target="_blank" rel="noopener noreferrer">外部记录</a></p></div>
          <div class="actions"><ElButton v-if="canManage(detail)&&(tab==='work'||detail.status==='draft')" @click="open(tab,detail)">{{tab==='versions'?'编辑范围':'编辑需求'}}</ElButton>
            <template v-if="tab==='work'"><ElButton @click="chooseVersion('work',detail)">纳入版本</ElButton><ElButton type="primary" @click="editDelivery(detail)">编辑交付内容</ElButton></template>
            <ElButton v-else-if="detail.status==='draft'&&canManage(detail)" type="primary" @click="action('versions',detail,'ready')">冻结并标记就绪</ElButton>
            <ElButton v-else-if="['ready','completed'].includes(detail.status)" type="primary" @click="router.push({path:'/orchestration',query:{version:String(detail.id)}})">发起交付</ElButton>
          </div></div>
        <div v-if="tab==='work'&&detail.source?.startsWith('zentao:')" class="chips"><span>指派给：{{detail.assigned_to==='closed'?'已关闭':detail.assigned_to||'—'}}</span><span>由谁创建：{{detail.opened_by||'—'}}</span><span>由谁解决：{{detail.resolved_by||'—'}}</span><span>由谁关闭：{{detail.closed_by||'—'}}</span></div>
        <RichDescription :key="`${tab}-${detail.id}`" :content="detail.description || ''" :source-url="detail.url || ''" :title="tab==='versions'?'版本说明':detail.kind==='bug'?'Bug 说明':'需求说明'" />
        <div v-if="tab==='versions'&&detail.status==='draft'&&versionBlockers(detail).length" class="notice"><b>发布前待处理</b><p v-for="reason in versionBlockers(detail)" :key="reason">{{reason}}</p></div>
        <ElButton v-if="tab==='versions'&&detail.status==='draft'&&canManage(detail)" :loading="saving" @click="refreshScope(detail)">刷新关联范围</ElButton>
        <div v-if="tab==='versions'" class="record"><h3>需求与 Bug · {{detail.work_items?.length||0}}</h3><div class="chips"><ElButton v-for="w in detail.work_items||[]" :key="w.id" @click="view('work',w)">#{{w.id}} {{w.title}}</ElButton></div><p v-if="!detail.work_items?.length">可先建立空草稿，也可以直接选择独立变更。</p></div>
        <p v-if="addedVersion" class="notice">已纳入 {{addedVersion.version}}。<ElButton @click="view('versions',addedVersion);addedVersion=null">进入版本 →</ElButton></p>
        <h3>{{tab==='versions'?'实施范围':'交付内容'}} · {{detailChanges.length}}</h3>
        <div v-if="!detailChanges.length" class="empty compact">尚无内容。{{tab==='work'?'点击“编辑交付内容”，连续填写涉及的代码、SQL 和配置。':'点击“编辑范围”，选择需求或手工补入变更。'}}</div>
        <article v-for="c in detailChanges" :key="c.id" class="record"><div class="record-head"><strong>{{kindLabel(c.kind)}} · {{c.title}}</strong><span class="status">{{labels[c.status]}}</span></div><p>{{c.service||'无关联应用'}} · {{c.reference||c.sql?.resource_alias||c.summary||'暂无说明'}}</p>
          <p v-if="c.sql?.data_sync" class="meta">数据同步任务 #{{c.sql.data_sync.task_id}} · 修订 {{c.sql.data_sync.task_revision}} · 本次目标 {{c.sql.data_sync.target_environment}}；差异已冻结，重新修改请再次对比。</p>
          <p v-if="c.redis">缓存资源：{{c.redis.resource_alias}} <ElButton size="small" @click="checkRedis(c)">Redis 命令预检</ElButton></p>
          <p v-if="c.nacos">配置资源：{{c.nacos.resource_alias}} · {{c.nacos.data_id}} <ElButton size="small" @click="checkNacos(c)">Nacos 差异预览</ElButton></p>
          <div class="actions"><ElButton v-if="c.service&&['code','artifact'].includes(c.kind)" @click="router.push(`/svc/${encodeURIComponent(c.service)}/build`)">查看构建与制品</ElButton><ElButton v-if="c.sql" @click="checkSQL(c)">SQL 预检</ElButton><template v-if="canManage(c)"><ElButton v-if="c.status==='draft'&&!c.sql?.data_sync" @click="open('changes',c)">编辑变更</ElButton><ElButton v-if="c.status==='draft'" @click="action('changes',c,'ready')">标记就绪</ElButton><ElButton v-else-if="!isFrozenChange(c)" @click="action('changes',c,'reopen')">退回草稿</ElButton></template><span v-if="isFrozenChange(c)" class="meta">已随版本冻结；后续修改请新建变更</span></div>
        </article>
        <p v-if="tab==='versions'" class="notice">版本规划不自动合并代码，也不会自动选择最新制品。发起交付时明确选择每个应用的构建制品，同一应用只发布一次。</p>
        <template v-if="tab==='work'"><h3>所在版本</h3><div class="chips"><ElButton v-for="v in detailVersions" :key="v.id" @click="view('versions',v)">{{v.version}} · {{labels[v.status]}}</ElButton><span v-if="!detailVersions.length">未纳入版本，可继续独立开发与测试。</span></div></template>
        <h3>交付记录</h3><div class="chips"><ElButton v-for="r in detailRuns" :key="r.id" @click="router.push({path:'/orchestration',query:{run:String(r.id)}})">#{{r.id}} · {{r.plan.environment.display_name}} · {{runLabel(r.status)}}</ElButton><span v-if="!detailRuns.length">尚无交付记录；需求关闭不代表已经上线。</span></div>
      </section><div v-else class="empty">记录不存在或正在加载。<ElButton @click="back">返回列表</ElButton></div>
    </template>
    <div v-show="!route.query.item">
    <WorkItemsTable v-if="tab==='work'" ref="workTable" :me="me" :can-manage="canManage" @view="view('work',$event)" @edit="open('work',$event)" @delivery="editDelivery" @version="chooseVersion('work',$event)" @create="open('work')" />
    <div v-if="tab!=='work'" class="toolbar">
      <ElInput
        v-model="query"
        placeholder="搜索标题、版本、外部编号或应用"
        clearable
        style="max-width: 420px"
      /><Sel
        v-model="filter"
        :options="
          (tab === 'work'
            ? ['open', 'closed']
            : tab === 'versions'
              ? ['draft', 'ready', 'completed']
              : ['draft', 'ready']
          ).map((v) => ({ value: v, label: labels[v] }))
        "
        all-label="全部状态"
      /><Sel v-if="tab!=='versions'" v-model="associationFilter" :options="tab==='work'?[{value:'requirement',label:'需求'},{value:'bug',label:'BUG'}]:[{value:'linked',label:'关联需求的变更'},{value:'independent',label:'独立变更'}]" all-label="全部类型" width="180px" /><span>{{ list.length }} 条</span><ElButton @click="load">刷新</ElButton>
    </div>
    <div v-if="tab!=='work'&&!list.length" class="empty">
      <h3>
        尚无{{
          tab === "work"
            ? "需求与缺陷"
            : tab === "versions"
              ? "发布版本"
              : "变更"
        }}
      </h3>
      <p>
        {{
          tab === "versions"
            ? "先创建版本草稿，再逐步选择需求与变更；就绪校验在冻结版本时进行。"
            : "可以独立创建，不需要先建立发布版本。"
        }}
      </p>
      <ElButton @click="open(tab)">新建</ElButton>
    </div>
    <article v-for="item in tab==='work'?[]:list" :key="item.id" class="record">
      <div class="record-head">
        <div>
          <span class="tag">{{
            tab === "versions"
              ? item.version
              : tab === "work"
                ? item.kind === "bug"
                  ? "BUG"
                  : "需求"
                : kindLabel(item.kind)
          }}</span
          ><button v-if="tab!=='changes'" class="record-link" @click="view(tab,item)">{{item.title}}</button><strong v-else>{{ item.title }}</strong>
        </div>
        <span class="status">{{ labels[item.status] || item.status }}</span>
      </div>
      <p class="record-summary">{{ summaries.get(item.id) || "暂无说明" }}</p>
      <div class="meta">
        <span>#{{ item.id }} · {{ item.owner }}</span
        ><span v-if="item.service">应用 {{ item.service }}</span
        ><span v-if="item.external_id"
          >{{ item.source }} / {{ item.external_id }}</span
        ><a
          v-if="safeURL(item.url)"
          :href="safeURL(item.url)"
          target="_blank"
          rel="noopener noreferrer"
          >外部记录</a
        >
      </div>
      <template v-if="tab === 'changes'"
        ><code v-if="item.reference">{{ item.reference }}</code>
        <p v-if="item.sql">数据库资源：{{item.sql.resource_alias}} · 最多 {{item.sql.max_affected_rows}} 行 <ElButton size="small" @click="checkSQL(item)">SQL 预检</ElButton></p>
        <p v-if="item.redis">缓存资源：{{item.redis.resource_alias}} <ElButton size="small" @click="checkRedis(item)">Redis 命令预检</ElButton></p>
        <p v-if="item.nacos">配置资源：{{item.nacos.resource_alias}} · {{item.nacos.data_id}} <ElButton size="small" @click="checkNacos(item)">Nacos 差异预览</ElButton></p>
        <div class="meta">
          关联需求：{{
            (item.work_items || []).map((v) => v.title).join("、") ||
            "无（独立变更）"
          }}
          ·
          {{
            item.risk_level === "high"
              ? "高"
              : item.risk_level === "low"
                ? "低"
                : "中"
          }}风险
        </div>
      </template>
      <template v-if="tab === 'versions'"
        ><p>需求 / Bug {{item.work_items?.length||0}} 项 · 变更 {{item.changes?.length||0}} 项 · 待就绪 {{(item.changes||[]).filter(c=>c.status!=='ready').length}} 项</p><div class="chips">
          <span v-for="c in item.changes" :key="c.id"
            >{{ kindLabel(c.kind) }} · {{ c.title }}
            <small v-if="c.service">/ {{ c.service }}</small></span
          >
        </div>
        <div class="meta">
          部署范围：{{
            [
              ...new Set(
                (item.changes || [])
                  .filter((c) => ["code", "artifact"].includes(c.kind))
                  .map((c) => c.service)
                  .filter(Boolean),
              ),
            ].join("、") || "仅非代码变更，不自动发布服务"
          }}
          · 不绑定环境
        </div></template
      >
      <div v-if="tab === 'versions'" class="chips">
        <button
          v-for="run in deliveries.filter(
            (r) => r.plan.version?.id === item.id,
          )"
          :key="run.id"
          @click="
            router.push({
              path: '/orchestration',
              query: { run: String(run.id) },
            })
          "
        >
          {{ run.plan.environment.display_name }} · #{{ run.id }} ·
          {{
            {
              pending_approval: "待审核",
              approved: "待执行",
              running: "执行中",
              success: "成功",
              failed: "失败",
              rejected: "已驳回",
              canceled: "已取消",
            }[run.status] || run.status
          }}
        </button>
      </div>
      <div class="actions">
        <template v-if="tab==='work'"><ElButton type="primary" size="small" @click="view('work',item)">查看需求</ElButton><ElButton size="small" @click="editDelivery(item)">编辑交付内容</ElButton><ElButton size="small" @click="chooseVersion('work',item)">纳入版本</ElButton></template>
        <ElButton v-if="tab==='changes'" size="small" @click="chooseVersion('changes',item)">纳入版本</ElButton><span v-if="item.sql?.data_sync" class="meta">同步任务 #{{item.sql.data_sync.task_id}} · {{item.sql.data_sync.target_environment}}（冻结差异）</span>
        <ElButton v-if="tab==='versions'" size="small" type="primary" @click="view('versions',item)">进入版本</ElButton>
        <template v-if="canManage(item)"
          ><ElButton
            v-if="tab === 'work' || (item.status === 'draft'&&!item.sql?.data_sync)"
            size="small"
            @click="open(tab, item)"
            >编辑</ElButton
          ><ElButton
            v-if="tab === 'changes' && item.status === 'draft'"
            size="small"
            type="primary"
            @click="action(tab, item, 'ready')"
            >标记就绪</ElButton
          ><ElButton
            v-if="tab === 'changes' && item.status === 'ready' && !isFrozenChange(item)"
            size="small"
            @click="action(tab, item, 'reopen')"
            >退回草稿</ElButton
          ><ElButton
            v-if="tab !== 'work' && item.status === 'draft'"
            size="small"
            @click="action(tab, item, 'delete')"
            >删除</ElButton
          ></template
        ><ElButton
          v-if="
            tab === 'versions' && ['ready', 'completed'].includes(item.status)
          "
          size="small"
          type="primary"
          @click="
            router.push({
              path: '/orchestration',
              query: { version: String(item.id) },
            })
          "
          >按版本发起编排</ElButton
        >
      </div>
    </article>
    </div>
    <Modal :show="showRedisPreview" title="Redis 命令预检（只读）" extra-wide @close="closeRedisPreview"><div class="form-grid"><p class="span">{{redisPreviewItem?.title}} · 仅测试连接和命令范围，不执行命令、不读取缓存值。</p><label>目标环境<Sel v-model="redisEnvironment" :disabled="saving" :options="environments.map(e=>({value:e.name,label:e.display_name||e.name}))" width="100%" /></label><ElButton type="primary" :loading="saving" :disabled="!redisEnvironment" @click="runRedisPreview">检查连接与命令范围</ElButton><div class="span"><RedisPlanPreview :plans="redisPreview?[redisPreview]:[]" /></div></div></Modal>
    <Modal :show="showNacosPreview" title="Nacos 配置差异（只读预览）" extra-wide @close="closeNacosPreview"><div class="form-grid"><p class="span">{{nacosPreviewItem?.title}} · 预览不发布配置；执行单提交时会重新读取并冻结基线。</p><label>目标环境<Sel v-model="nacosEnvironment" :disabled="saving" :options="environments.map(e=>({value:e.name,label:e.display_name||e.name}))" width="100%" /></label><ElButton type="primary" :loading="saving" :disabled="!nacosEnvironment" @click="runNacosPreview">读取目标配置并对比</ElButton><div class="span"><NacosPlanPreview :plans="nacosPreview?[nacosPreview]:[]" /></div></div></Modal>
    <WorkDeliveryEditor ref="deliveryEditor" :show="showDeliveryEditor" :item="deliveryItem" :services="services" :work-items="work" :can-manage="canManage" @close="showDeliveryEditor=false" @saved="load" @open-sync="openSyncTasks" />
    <DataSyncTasks :show="showSyncTasks" :services="services" :environments="environments" :item="deliveryItem" @close="showSyncTasks=false" @generated="syncGenerated" @preview="checkSQL" />
    <Modal :show="showAddVersion" title="纳入发布版本" wide @close="showAddVersion=false"><div class="form-grid"><p class="span">{{addItem?.title}} · 仅调整草稿范围，不触发构建或执行</p><label class="span">草稿版本<Sel v-model="addVersionID" :options="versions.filter(v=>v.status==='draft'&&canManage(v)).map(v=>({value:v.id,label:v.version+' · '+v.title}))" width="100%" /></label><div class="footer span"><ElButton @click="showAddVersion=false;open('versions');form.work_item_ids=addType==='work'?[addItem.id]:[];form.manual_change_ids=addType==='changes'?[addItem.id]:[]">创建新版本</ElButton><ElButton @click="showAddVersion=false">取消</ElButton><ElButton type="primary" :loading="saving" @click="addToVersion">确认纳入</ElButton></div></div></Modal>
    <Modal :show="showResources" title="资源中心" extra-wide @close="showResources=false;load()"><ResourcesView directAPI /></Modal>
    <ApplicationResourcesEditor :show="showBindings" :services="services" :initial-service="form.service" @close="showBindings=false;loadSQLResources()" />
    <Modal :show="showSync" title="同步禅道 BUG（开源 16.0）" wide @close="showSync=false">
      <div class="form-grid"><label class="span">禅道连接<Sel v-model="syncForm.connection" :options="connections.filter(c=>c.kind==='zentao').map(c=>c.name)" width="100%" /></label><label class="span">本次 Bug 状态<Sel v-model="syncStatusModel" :options="syncStatusOptions" width="100%" :disabled="saving" /></label><label class="span">更新或关闭日期基线（留空沿用连接设置）<ElInput v-model="syncForm.baseline" placeholder="YYYY-MM-DD；包含当天" /></label><p class="notice span">默认同步已关闭 Bug，可按本次需要切换状态；筛选不删除此前已同步的记录。按更新、关闭时间倒序读取，日期基线前的数据不纳入本次范围。已关闭不等于未上线；历史上线范围仍需核对。</p><p v-for="state in syncStates.filter(s=>s.connection===syncForm.connection)" :key="state.connection" class="span">上次同步：{{state.status==='success'?'成功':'失败'}} · {{state.detail}}</p><div class="actions span"><ElButton @click="openResources">配置禅道连接</ElButton><ElButton type="primary" :loading="saving" @click="syncIssues">立即同步</ElButton></div></div>
    </Modal>
    <Modal :show="showSQLPreview" title="SQL 结构与影响范围预检（不执行）" extra-wide @close="showSQLPreview=false">
      <div class="form-grid"><label>目标环境<Sel v-model="previewEnvironment" :options="environments.map(e=>({value:e.name,label:e.display_name}))" width="100%" /></label><ElButton :loading="saving" @click="runSQLPreview">读取目标库并预检</ElButton><template v-if="sqlPreview"><p class="span">{{sqlPreview.connection}} / {{sqlPreview.database}} · {{sqlPreview.mode.toUpperCase()}}</p><p v-for="warning in sqlPreview.warnings" :key="warning" class="notice span">{{warning}}</p><article v-for="(op,index) in sqlPreview.statements" :key="index" class="span"><b>{{op.skip?'跳过（结构一致）':'计划执行'}} · {{op.reason||op.action}} · {{op.expected_rows||0}} 行</b><pre>{{op.sql}}</pre></article><p class="notice span">预检不执行 SQL。标记就绪后，在部署编排中选择 SQL 节点，提交冻结计划并按环境审批。</p></template></div>
    </Modal>
    <Modal
      :show="!!dialog"
      :title="
        dialog === 'work'
          ? '需求 / BUG'
          : dialog === 'versions'
            ? '发布版本'
            : '变更'
      "
      extra-wide
      @close="dialog = ''"
    >
      <form class="form-grid" @submit.prevent="save">
        <label class="span">标题<ElInput v-model="form.title" /></label>
        <template v-if="dialog === 'work'"
          ><label
            >类型<Sel
              v-model="form.kind"
              :options="[
                { value: 'requirement', label: '需求' },
                { value: 'bug', label: 'BUG' },
              ]"
              width="100%" /></label
          ><label
            >来源<Sel
              v-model="form.source"
              :options="[
                { value: 'manual', label: '自定义' },
                { value: 'zentao', label: '禅道（手工关联）' },
                { value: 'external', label: '其他外部平台' },
              ]"
              width="100%" /></label
          ><label v-if="form.source !== 'manual'"
            >外部编号<ElInput v-model="form.external_id" /></label
          ><label v-if="form.source !== 'manual'"
            >记录链接<ElInput v-model="form.url" /></label
          ><label v-if="form.id"
            >状态<Sel
              v-model="form.status"
              :options="[
                { value: 'open', label: '进行中' },
                { value: 'closed', label: '已关闭' },
              ]"
              width="100%" /></label
          ><label class="span"
            >说明<ElInput v-model="form.description" type="textarea" :rows="3"
          /></label>
          <p class="notice span">
            禅道 16.0 可通过“同步禅道 BUG”导入；此处用于自定义需求或手工关联。上线状态回写尚未接通。
          </p></template
        >
        <template v-if="dialog === 'changes'"
          ><label
            >变更类型<Sel
              v-model="form.kind"
              :options="kinds"
              width="100%" /></label
          ><label
            >关联应用<Sel
              v-model="form.service"
              :options="services.map((s) => ({ value: s.name, label: s.name }))"
              all-label="不关联应用"
              filterable
              width="100%" /></label
          ><label v-if="form.kind==='code'" class="span">代码分支<GitBranchSelect v-model="form.reference" :repositories="form.service?[{service:form.service}]:[]" /></label><template v-if="form.kind==='nacos'"><label>配置资源<Sel v-model="form.nacos.resource_alias" :disabled="!!form.nacos.restore" :options="sqlResources.map(r=>({value:r.alias,label:r.alias}))" width="100%" /><span v-if="sqlResourcesLoading">正在读取…</span><span v-else-if="sqlResourcesError">{{sqlResourcesError}}</span><ElButton v-if="form.service&&(me.role==='admin'||me.username==='local')" size="small" @click="showBindings=true">配置应用资源</ElButton></label><NacosChangeFields :model="form.nacos" :disabled="saving" /></template><template v-if="form.kind==='redis'"><label>Redis 资源<Sel v-model="form.redis.resource_alias" :options="sqlResources.map(r=>({value:r.alias,label:r.alias}))" width="100%" /><span v-if="sqlResourcesLoading">正在读取…</span><span v-else-if="sqlResourcesError">{{sqlResourcesError}}</span><ElButton v-if="form.service&&(me.role==='admin'||me.username==='local')" size="small" @click="showBindings=true">配置应用资源</ElButton></label><RedisChangeFields :model="form.redis" :disabled="saving" /></template><label v-if="!['mysql','code','nacos','redis'].includes(form.kind)" class="span"
            >{{
              form.kind === "code"
                ? "代码分支"
                : form.kind === "artifact"
                  ? "制品说明 / 标识"
                  : form.kind === "nacos"
                    ? "配置项与期望值（本版记录）"
                    : form.kind === "mysql"
                      ? "SQL 内容（本版记录）"
                      : "实施内容（本版记录）"
            }}<ElInput
              v-model="form.reference"
              type="textarea"
              :rows="3" /></label
          ><template v-if="form.kind==='mysql'"><label>数据库资源<Sel v-model="form.sql.resource_alias" :options="sqlResources.map(r=>({value:r.alias,label:r.alias}))" width="100%" /><span v-if="sqlResourcesLoading">正在读取…</span><span v-else-if="sqlResourcesError">{{sqlResourcesError}}</span><span v-else-if="!sqlResources.length">{{form.service?'该应用尚未声明数据库资源':'先选择关联应用'}}</span><ElButton v-if="form.service&&(me.role==='admin'||me.username==='local')" size="small" @click="showBindings=true">配置应用资源</ElButton></label><label>允许影响行数（1–10000）<ElInput v-model.number="form.sql.max_affected_rows" type="number" /></label><label class="span">执行 SQL<ElInput v-model="form.sql.content" type="textarea" :rows="7" placeholder="ALTER TABLE menus ADD COLUMN enabled int NOT NULL DEFAULT 1;" /></label><p class="notice span">支持建表、单字段新增、普通/唯一索引及单表 INSERT/UPDATE/DELETE；不支持删除结构、函数、子查询或跨库。DDL 与 DML 分节点、分次交付。预检检查目标结构，执行前再次核对审批基线。</p></template><label
            >关联需求 / BUG（可选）<Sel
              v-model="form.work_item_ids"
              multiple
              :options="
                work.map((w) => ({ value: w.id, label: `#${w.id} ${w.title}` }))
              "
              filterable
              width="100%" /></label
          ><label
            >风险等级<Sel
              v-model="form.risk_level"
              :options="[
                { value: 'low', label: '低' },
                { value: 'medium', label: '中' },
                { value: 'high', label: '高' },
              ]"
              width="100%" /></label
          ><label
            >变更说明<ElInput
              v-model="form.summary"
              type="textarea"
              :rows="3" /></label
          ><label
            >回退方案<ElInput
              v-model="form.rollback_plan"
              type="textarea"
              :rows="3"
          /></label>
          <p
            v-if="!['code', 'artifact', 'manual','mysql','nacos','redis'].includes(form.kind)"
            class="notice span"
          >
            该类变更目前可登记、关联和进入版本；执行器未接通，编排会阻止提交，不会自动执行或标记成功。
          </p></template
        >
        <template v-if="dialog === 'versions'"
          ><label
            >版本号<ElInput
              v-model="form.version"
              placeholder="例如 2026.10.1" /></label
          ><label
            >选择需求 / BUG<Sel
              v-model="form.work_item_ids"
              multiple
              :options="work.map((w) => ({ value: w.id, label: w.title }))"
              filterable
              width="100%"
              /></label
          ><label class="span"
            >手工补入变更（可含草稿）<Sel
              v-model="form.manual_change_ids"
              multiple
              :options="
                changes.map((c) => ({
                  value: c.id,
                  label: `${kindLabel(c.kind)} · ${c.title}${c.service ? ' / ' + c.service : ''}`,
                }))
              "
              filterable
              width="100%"
          /></label>
          <div class="selection span">
            <p v-for="c in selectedChanges" :key="c.id">
              {{c.title}} · {{labels[c.status]}} · {{(form.manual_change_ids||[]).includes(c.id)?'手工选择':'由需求关联'}}
            </p>
          </div>
          <label class="span"
            >版本说明<ElInput
              v-model="form.description"
              type="textarea"
              :rows="3"
          /></label>
          <p class="notice span">
            需求关联的变更自动汇总并去重；取消需求会移除其独有变更，共享或手工选择的变更保留。草稿可暂为空；冻结时要求变更全部就绪且每个需求至少有一项实施变更。创建版本不会执行变更。
          </p></template
        >
        <div class="footer span">
          <ElButton native-type="button" @click="dialog = ''">取消</ElButton
          ><ElButton type="primary" native-type="submit" :loading="saving"
            >保存</ElButton
          >
        </div>
      </form>
    </Modal>
  </div>
</template>
<style scoped>
.change-center {
  max-width: 1500px;
  margin: auto;
}
.workspace { margin-top:20px; }
.workspace h2 { margin:0; overflow-wrap:anywhere; }
.workspace .notice { margin-bottom:16px; }
.record-link { color:var(--brand); border:0; background:none; font:inherit; font-weight:600; cursor:pointer; text-align:left; overflow-wrap:anywhere; }
.empty.compact { padding:30px; margin-bottom:16px; }
.heading,
.title,
.toolbar,
.record-head,
.actions,
.meta,
.chips {
  display: flex;
  align-items: center;
  gap: 12px;
}
.heading,
.record-head {
  justify-content: space-between;
  flex-wrap: wrap;
}
.title { min-width: 0; }
.actions { flex-wrap: wrap; }
.title h2 {
  margin: 0;
}
.tabs {
  display: flex;
  gap: 4px;
  border-bottom: 1px solid var(--border);
  margin: 20px 0;
}
.tabs button {
  border: 0;
  border-bottom: 2px solid transparent;
  background: none;
  color: var(--muted);
  padding: 12px 16px;
  font-size: 13px;
  border-radius:8px 8px 0 0;
  cursor: pointer;
}
.tabs button.active {
  color: var(--brand-ink);
  border-color: var(--brand);
  background:var(--brand-soft);
  font-weight:600;
}
.tabs button:hover:not(.active){background:var(--panel2);color:var(--text)}
.toolbar {
  margin-bottom: 16px;
  flex-wrap: wrap;
  padding:16px;
  border:1px solid var(--border);
  border-radius:12px;
  background:var(--panel);
}
.toolbar span,
.meta,
p {
  color: var(--muted);
  font-size: 12px;
}
.record {
  padding: 20px;
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: 12px;
  margin-bottom: 12px;
  box-shadow:var(--shadow-sm);
}
.record-head strong,.record-link{font-size:14px}.record-summary{margin-top:12px}.record>.actions{padding-top:14px;border-top:1px solid var(--border)}
.record p {overflow-wrap:anywhere}
.record-summary { display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; line-height: 1.7; max-height: 3.4em; }
.record-head > div {
  display: flex;
  gap: 12px;
  align-items: center;
}
.tag,
.status,
.chips span {
  padding: 4px 8px;
  border-radius: 5px;
  background: var(--panel2);
  font-size: 12px;
}
.meta,
.chips {
  flex-wrap: wrap;
  margin: 12px 0;
}
.record code {
  display: block;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  background: var(--panel2);
  padding: 12px;
  font-size: 12px;
  max-height: 150px;
  overflow: auto;
}
.actions {
  margin-top: 12px;
}
.empty {
  text-align: center;
  padding: 70px;
  border: 1px solid var(--border);
  background:var(--panel);
  border-radius: 12px;
}
.form-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 16px;
}
.form-grid label {
  display: grid;
  gap: 8px;
  font-size: 13px;
  min-width: 0;
}
.span {
  grid-column: 1/-1;
}
.notice {
  background: var(--panel2);
  padding: 12px;
  border-radius: 8px;
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
.selection button {
  background: none;
  border: 0;
  color: var(--brand);
  cursor: pointer;
}
@media (max-width: 700px) {
  .tabs{gap:0;overflow-x:auto;white-space:nowrap}.tabs button{flex-shrink:0;padding:12px 10px}.toolbar{padding:12px}.empty{padding:40px 16px}
  .form-grid {
    grid-template-columns: 1fr;
  }
  .heading {
    align-items: flex-start;
    gap: 12px;
  }
  .record-head {
    align-items: flex-start;
  }
  .record-head > div {
    flex-wrap: wrap;
  }
}
</style>
