<script setup>
import {computed,onMounted,onBeforeUnmount,provide,reactive,ref,watch} from 'vue'
import {ElButton,ElInput} from '../shared/components/ui.js'
import Sel from '../shared/components/Sel.vue'
import Modal from '../shared/components/Modal.vue'
import PlatformNav from '../shared/components/PlatformNav.vue'
import PlatformPanel from '../shared/components/PlatformPanel.vue'
import ApplicationResourcesEditor from '../deploy/components/ApplicationResourcesEditor.vue'
import DataSyncTasks from '../deploy/components/DataSyncTasks.vue'
import RedisChangeFields from '../deploy/components/RedisChangeFields.vue'
import RedisPlanPreview from '../deploy/components/RedisPlanPreview.vue'
import SQLPlanPreview from '../deploy/components/SQLPlanPreview.vue'

const kind=location.pathname.includes('/redis/')||location.pathname.endsWith('/ui/redis')?'redis':'dbms'
const title=kind==='redis'?'Redis 工作台':'数据库工作台',changeKind=kind==='redis'?'redis':'mysql'
const embedded=window.parent!==window,directAPI=!location.pathname.startsWith('/api/plugins/')
const panel=ref(''),tab=ref('operations'),busy=ref(false),loading=ref(false),error=ref(''),keyword=ref('')
const services=ref([]),environments=ref([]),changes=ref([]),runs=ref([]),me=ref({}),aliases=ref([])
const form=ref(null),original=ref(''),selected=ref([]),environment=ref(''),reason=ref(''),plan=ref(null),templateID=ref(0),run=ref(null),queryResult=ref(null)
const showBindings=ref(false),showSync=ref(false),bindingService=ref('')
const query=reactive({service:'',resource_alias:'',sql:'SELECT id, name FROM sys_menu WHERE id > 0'})
const stateLabel={draft:'草稿',ready:'就绪',pending:'待执行',executing:'正在执行',success:'成功',pending_approval:'待审核',approved:'已审核',running:'执行中',completed:'完成',failed:'失败',rejected:'已拒绝',canceled:'已取消'}
const envOptions=computed(()=>environments.value.map(e=>({value:e.name,label:e.display_name||e.name})))
const filtered=computed(()=>changes.value.filter(c=>!keyword.value||`${c.title} ${c.service}`.toLowerCase().includes(keyword.value.toLowerCase())))
const dirty=computed(()=>form.value&&JSON.stringify(form.value)!==original.value)
const currentEnvironment=computed(()=>environments.value.find(e=>e.name===environment.value))
const canReview=computed(()=>run.value&&me.value.username!==run.value.owner&&(run.value.plan.environment.approvers||'').split(',').map(s=>s.trim()).includes(me.value.username))
const canManage=computed(()=>run.value&&(me.value.role==='admin'||me.value.username===run.value.owner))
const statusText=v=>stateLabel[v]||v
async function api(path,opt){
  const url=path.startsWith('api/')?`api/workbench/${kind}/${path.slice(4)}`:path
  const r=await fetch(url,opt),value=await r.json().catch(()=>({}))
  if(!r.ok)throw new Error(value.error||`请求失败（${r.status}）`)
  return value
}
provide('api',api)
provide('resourcePlugin',kind)
const post=(path,body)=>api(path,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)})
const list=v=>Array.isArray(v)?v:[]
async function action(fn){if(busy.value)return;busy.value=true;error.value='';try{await fn()}catch(e){error.value=e.message}finally{busy.value=false}}
async function load(){loading.value=true;try{
  const [apps,envs,ops,history,catalog,identity]=await Promise.all([api('api/services'),api('api/environments'),api('api/changes'),api('api/delivery/runs'),fetch('/api/registry/applications').then(r=>r.ok?r.json():[]).catch(()=>[]),fetch('/api/me').then(r=>r.ok?r.json():{}).catch(()=>({}))])
  services.value=[...new Map([...list(apps),...list(catalog)].map(a=>[a.name,{name:a.name}])).values()]
  environments.value=list(envs);changes.value=list(ops);runs.value=list(history);me.value=identity
}catch(e){error.value=e.message}finally{loading.value=false}}
let resourceRequest=0
async function resourcesFor(service){const request=++resourceRequest;aliases.value=[];if(!service)return;try{const data=await api(`api/services/${encodeURIComponent(service)}/resources`);if(request===resourceRequest)aliases.value=list(data.requirements).filter(r=>r.kind===changeKind).map(r=>({value:r.alias,label:r.alias}))}catch(e){if(request===resourceRequest)error.value=e.message}}
function invalidate(){plan.value=null;templateID.value=0}
watch([environment,reason,()=>JSON.stringify(selected.value)],invalidate)
watch(()=>JSON.stringify(form.value),()=>{if(dirty.value)invalidate()})
function open(c){
  if(busy.value)return
  if(dirty.value&&!confirm('放弃未保存的编辑？'))return
  form.value=c?JSON.parse(JSON.stringify(c)):{kind:changeKind,title:'',service:'',risk_level:'medium',rollback_plan:'',sql:kind==='dbms'?{resource_alias:'',content:'',max_affected_rows:100}:undefined,redis:kind==='redis'?{resource_alias:'',commands:''}:undefined}
  original.value=JSON.stringify(form.value);selected.value=c?.status==='ready'?[c.id]:[];invalidate();error.value='';resourcesFor(form.value.service)
}
async function save(markReady=false){
  if(!form.value?.title.trim()||!form.value.service)throw new Error('请填写标题、应用和操作内容')
  const f=form.value
  const saved=await api('api/changes'+(f.id?'/'+f.id:''),{method:f.id?'PUT':'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(f)})
  form.value=saved;original.value=JSON.stringify(saved)
  if(markReady&&saved.status!=='ready'){form.value=await post(`api/changes/${saved.id}/ready`,{});original.value=JSON.stringify(form.value)}
  selected.value=[saved.id];invalidate();await load()
}
async function preview(){
	 invalidate()
  if(form.value){if(dirty.value||!form.value.id||form.value.status!=='ready')await save(true);selected.value=[form.value.id]}
  if(!selected.value.length||!environment.value)throw new Error('请选择就绪操作和本次目标环境')
  const tpl=await post('api/delivery/templates',{name:`${title}操作`,workbench:kind,mode:'fixed',services:[],nodes:[{id:'operation',kind:changeKind,name:title,change_ids:selected.value}]})
  templateID.value=tpl.id
  plan.value=await post('api/delivery/preview',{template_id:tpl.id,environment:environment.value,reason:reason.value,nodes:[{id:'operation',kind:changeKind,name:title,change_ids:selected.value}]})
}
async function submit(){
  if(!plan.value||!templateID.value||plan.value.blockers?.length)throw new Error('请先完成预检并解决阻断项')
  run.value=await post('api/delivery/runs',{template_id:templateID.value,environment:environment.value,reason:reason.value,nodes:plan.value.nodes})
  invalidate();await load()
}
async function runAction(name){
  if(name==='advance'&&!confirm('执行该已审核快照中的资源操作？请核对目标环境。'))return
  run.value=await post(`api/delivery/runs/${run.value.id}/${name}`,{digest:run.value.digest});await load()
}
async function bind(){
  bindingService.value=tab.value==='query'?query.service:form.value?.service
  if(!bindingService.value)throw new Error('请先选择应用')
  await post('api/applications',{name:bindingService.value});showBindings.value=true
}
async function readQuery(){
  queryResult.value=null
  if(!query.service||!query.resource_alias||!environment.value)throw new Error('请选择应用、资源和本次目标环境')
  queryResult.value=await post('api/query',{...query,environment:environment.value})
}
watch([environment,()=>query.service,()=>query.resource_alias,()=>query.sql],()=>queryResult.value=null)
function closeEditor(){if(dirty.value&&!confirm('放弃未保存的编辑？'))return;form.value=null;aliases.value=[];invalidate()}
function closeRun(){if(!busy.value)run.value=null}
function contextMessage(e){if(e.source===window.parent&&e.origin===location.origin&&e.data?.type==='context.sync'&&!environment.value&&environments.value.some(v=>v.name===e.data.environment))environment.value=e.data.environment}
onMounted(()=>{load();window.addEventListener('message',contextMessage);if(embedded)window.parent.postMessage({type:'context.request'},location.origin)})
onBeforeUnmount(()=>window.removeEventListener('message',contextMessage))
</script>

<template>
<div class="layout" :class="{embedded}">
 <header><b>{{kind==='redis'?'⚡ Redis':'🗄 DBMS'}}</b><nav aria-label="工作台导航"><button :class="{active:tab==='operations'}" @click="tab='operations'">操作</button><button :class="{active:tab==='history'}" @click="tab='history'">审核记录</button><button v-if="kind==='dbms'" :class="{active:tab==='query'}" @click="tab='query';resourcesFor(query.service)">只读查询</button></nav><PlatformNav @open="panel=$event"/></header>
 <main>
  <div class="page-head"><h2>{{title}}</h2><div><ElButton v-if="kind==='dbms'" :disabled="busy" @click="showSync=true">数据对比同步</ElButton><ElButton :disabled="busy||loading" @click="load">刷新</ElButton><ElButton v-if="tab==='operations'" type="primary" :disabled="busy" @click="open()">＋ 新建操作</ElButton></div></div>
  <p class="hint">{{kind==='redis'?'精确 Key 的受限命令；保存、预检不执行命令，不支持通配符或危险命令。':'MySQL 只读查询、SQL 变更和数据同步；保存、预检不会写入数据库。'}}写操作在所有环境均需另一名审核人批准。不依赖构建、制品或发布版本。</p>
  <p v-if="me.auth_enabled===false" class="notice">当前未启用登录，不能作为双人审核保障；实际使用请启用平台登录并配置环境审核人。</p>
  <p v-if="error" class="error" role="alert">{{error}}</p><p v-if="loading" role="status">正在加载…</p>
  <div class="scope"><label>本次目标环境<Sel v-model="environment" :options="envOptions" :disabled="busy" filterable width="210px" /></label><label class="reason">操作原因<ElInput v-model="reason" :disabled="busy" placeholder="随执行单记录，不写入可复用变更"/></label><small v-if="currentEnvironment">审核人：{{currentEnvironment.approvers||'未配置，请到资源中心 → 环境设置'}}</small></div>
  <template v-if="tab==='operations'">
   <div class="workspace">
    <section class="operation-list"><ElInput v-model="keyword" placeholder="筛选标题或应用" clearable/><p v-if="!filtered.length" class="empty">{{keyword?'没有匹配的操作':'还没有操作。新建一项后，可在不同环境复用，也可随版本编排。'}}</p><article v-for="c in filtered" :key="c.id" :class="{chosen:form?.id===c.id}"><label><input v-model="selected" type="checkbox" :value="c.id" :disabled="busy||c.status!=='ready'||!!form"/><b>#{{c.id}} {{c.title}}</b></label><small>{{c.service}} · {{statusText(c.status)}}</small><ElButton size="small" :disabled="busy" @click="open(c)">继续操作</ElButton></article><ElButton v-if="!form&&selected.length" :loading="busy" @click="action(preview)">预检选中操作（{{selected.length}}）</ElButton></section>
    <section class="editor">
     <template v-if="form"><div class="editor-head"><h3>{{form.id?'操作 #'+form.id:'新建操作'}}</h3><ElButton :disabled="busy" @click="closeEditor">关闭编辑</ElButton></div><div class="grid">
      <label class="full">标题<ElInput v-model="form.title" :disabled="busy||form.status==='ready'"/></label><label>应用<Sel v-model="form.service" :options="services.map(s=>s.name)" :disabled="busy||form.status==='ready'" filterable width="100%" @change="form[changeKind==='mysql'?'sql':'redis'].resource_alias='';resourcesFor(form.service)"/></label>
      <label>逻辑资源别名<Sel v-model="form[changeKind==='mysql'?'sql':'redis'].resource_alias" :options="aliases" :disabled="busy||form.status==='ready'" width="100%"/></label>
      <template v-if="kind==='dbms'"><label class="full">SQL 变更<ElInput v-model="form.sql.content" type="textarea" :rows="8" :disabled="busy||form.status==='ready'||!!form.sql.data_sync" placeholder="CREATE TABLE / ALTER TABLE / 带 WHERE 的 INSERT、UPDATE、DELETE"/></label><label>最多影响行数<ElInput v-model.number="form.sql.max_affected_rows" type="number" :disabled="busy||form.status==='ready'"/></label></template>
      <RedisChangeFields v-else :model="form.redis" :disabled="busy||form.status==='ready'"/><label class="full">恢复方案<ElInput v-model="form.rollback_plan" type="textarea" :rows="2" :disabled="busy||form.status==='ready'" placeholder="说明失败后如何人工核对与恢复，不承诺自动回滚"/></label>
     </div><div class="actions"><ElButton :disabled="busy||!form.service||me.role!=='admin'" @click="action(bind)">配置资源绑定</ElButton><ElButton v-if="form.status==='ready'" :disabled="busy" @click="action(async()=>{form=await post(`api/changes/${form.id}/draft`,{});original=JSON.stringify(form);invalidate()})">退回草稿修改</ElButton><ElButton v-else :loading="busy" @click="action(()=>save())">保存草稿</ElButton><ElButton type="primary" :loading="busy" @click="action(preview)">预检（保存并就绪）</ElButton></div></template>
     <div v-else class="empty">选择已有操作继续，或新建操作。低频连接与环境设置仍在资源中心，关闭后保留当前编辑位置。</div>
     <section v-if="plan" class="preview"><h3>本次只读预检 · {{plan.environment.display_name||plan.environment.name}}</h3><p v-for="b in plan.blockers||[]" :key="b" class="error">{{b}}</p><p v-for="w in plan.warnings||[]" :key="w" class="hint">{{w}}</p><SQLPlanPreview :plans="plan.sql_plans"/><RedisPlanPreview :plans="plan.redis_plans"/><ElButton type="primary" :loading="busy" :disabled="!!plan.blockers?.length" @click="action(submit)">提交审核（不执行）</ElButton></section>
    </section>
   </div>
  </template>
  <section v-else-if="tab==='history'" class="history"><p v-if="!runs.length" class="empty">暂无审核执行单。</p><article v-for="v in runs" :key="v.id"><div><b>#{{v.id}} · {{v.plan.environment.display_name||v.plan.environment.name}}</b><small>{{statusText(v.status)}} · 发起人 {{v.owner}} · {{new Date(v.created_at*1000).toLocaleString('zh-CN')}}</small></div><ElButton :disabled="busy" @click="run=v">查看 / 审核</ElButton></article></section>
  <section v-else class="query"><h3>受限只读查询</h3><p class="hint">显式字段、单表 SELECT，可带 WHERE；不支持 *、JOIN、函数或多语句。最多 1000 行 / 2 MiB，30 秒超时。查询结果不进入审计日志；审计仅记录目标与行数。不会绕过数据库账号权限。</p><div class="grid"><label>应用<Sel v-model="query.service" :options="services.map(s=>s.name)" :disabled="busy" width="100%" filterable @change="query.resource_alias='';resourcesFor(query.service)"/></label><label>逻辑数据库资源<Sel v-model="query.resource_alias" :options="aliases" :disabled="busy" width="100%"/></label><label class="full">查询 SQL<ElInput v-model="query.sql" type="textarea" :rows="6" :disabled="busy"/></label></div><div class="actions"><ElButton :disabled="busy||!query.service||me.role!=='admin'" @click="action(bind)">配置资源绑定</ElButton><ElButton type="primary" :loading="busy" @click="action(readQuery)">执行只读查询</ElButton></div><div v-if="queryResult" class="table-scroll"><p>{{queryResult.rows.length}} 行</p><table><thead><tr><th v-for="c in queryResult.columns" :key="c">{{c}}</th></tr></thead><tbody><tr v-for="(row,i) in queryResult.rows" :key="i"><td v-for="c in queryResult.columns" :key="c">{{row[c]===null?'NULL':row[c]}}</td></tr></tbody></table></div></section>
 </main>
 <PlatformPanel :view="panel" :plugin="kind" :directAPI="directAPI" @switch="panel=$event" @close="panel='';load()"/>
 <ApplicationResourcesEditor :show="showBindings" :services="services" :initial-service="bindingService" @close="showBindings=false;resourcesFor(bindingService)"/>
 <DataSyncTasks v-if="kind==='dbms'" :show="showSync" :services="services" :environments="environments" @close="showSync=false;load()" @generated="c=>{showSync=false;tab='operations';open(c);environment=c.sql.data_sync.target_environment;load()}" @preview="c=>{showSync=false;open(c);environment=c.sql.data_sync.target_environment}"/>
 <Modal :show="!!run" :title="'审核执行单 #'+(run?.id||'')" extra-wide @close="closeRun"><template v-if="run"><p>{{run.plan.environment.display_name||run.plan.environment.name}} · {{statusText(run.status)}} · 发起 {{run.owner}} · 审核 {{run.approver||'待审核'}}</p><p>操作原因：{{run.plan.reason||'未填写'}}</p><code class="digest">{{run.digest}}</code><SQLPlanPreview :plans="run.plan.sql_plans"/><RedisPlanPreview :plans="run.plan.redis_plans"/><article v-for="s in run.steps||[]" :key="s.node_id"><b>{{s.name}} · {{statusText(s.status)}}</b><pre>{{s.detail||'等待执行'}}</pre></article><p v-if="error" class="error" role="alert">{{error}}</p><div class="actions"><template v-if="run.status==='pending_approval'&&canReview"><ElButton :loading="busy" @click="action(()=>runAction('reject'))">拒绝</ElButton><ElButton type="primary" :loading="busy" @click="action(()=>runAction('approve'))">批准此快照</ElButton></template><ElButton v-if="['approved','running'].includes(run.status)&&canManage" type="primary" :loading="busy" @click="action(()=>runAction('advance'))">执行下一步 / 确认结束</ElButton><ElButton v-if="['approved','pending_approval'].includes(run.status)&&canManage" :loading="busy" @click="action(()=>runAction('cancel'))">取消执行单</ElButton><ElButton :disabled="busy" @click="run=null">关闭并保留页面</ElButton></div></template></Modal>
</div>
</template>

<style scoped>
.editor :deep(.span){grid-column:1/-1}
.layout{height:100vh;display:flex;position:relative}.layout>header{box-sizing:border-box;width:160px;flex:0 0 160px;padding:16px 10px;border-right:1px solid var(--border);background:var(--panel)}header>b{display:block;padding:8px;font-size:14px}nav{display:grid;gap:8px;margin:14px 0}nav button{min-height:44px;border:0;background:transparent;color:var(--muted);border-radius:8px;cursor:pointer}nav button:hover,nav button.active{background:var(--panel2);color:var(--text)}main{flex:1;min-width:0;overflow:auto;padding:24px}.page-head,.page-head>div,.actions,.editor-head{display:flex;gap:12px;align-items:center;flex-wrap:wrap}.page-head,.editor-head{justify-content:space-between}h2{font-size:22px;margin:0}h3{font-size:16px}.hint,small{color:var(--muted);line-height:1.65}.notice{padding:12px;border:1px solid var(--border);border-radius:8px}.scope{display:flex;align-items:end;gap:16px;flex-wrap:wrap;padding:16px 0;margin-bottom:12px;border-bottom:1px solid var(--border)}label{display:grid;gap:8px;min-width:0}.reason{flex:1;min-width:200px}.workspace{display:grid;grid-template-columns:minmax(260px,320px) minmax(0,1fr);gap:24px}.operation-list article,.history article{border:1px solid var(--border);background:var(--panel);border-radius:8px;padding:16px;margin:12px 0;display:grid;gap:10px}.operation-list article.chosen{border-color:var(--brand)}.operation-list article label{display:flex;align-items:flex-start}.operation-list b{overflow-wrap:anywhere}.history article{display:flex;justify-content:space-between;align-items:center}.history small{display:block;margin-top:6px}.editor,.query{border:1px solid var(--border);border-radius:10px;padding:20px;background:var(--panel);min-width:0}.grid{display:grid;grid-template-columns:1fr 1fr;gap:16px}.full{grid-column:1/-1}.actions{margin-top:20px}.preview{margin-top:24px;border-top:1px solid var(--border);padding-top:12px}.error{color:var(--red,#d33);white-space:pre-wrap;overflow-wrap:anywhere;padding:10px;border:1px solid currentColor;border-radius:8px}.empty{padding:40px 10px;color:var(--muted);line-height:1.8;text-align:center}.digest,pre{white-space:pre-wrap;overflow-wrap:anywhere}.table-scroll{overflow:auto;max-height:500px}table{border-collapse:collapse;min-width:100%;font-size:13px}th,td{padding:10px;border:1px solid var(--border);text-align:left;max-width:340px;overflow-wrap:anywhere}button:focus-visible{outline:2px solid var(--brand);outline-offset:3px}.layout.embedded>header{width:60px;flex-basis:60px;padding:12px 4px}.embedded header>b{font-size:10px;padding:2px}.embedded nav button{font-size:10px}.layout :deep(.platform-nav button){min-height:44px}@media(max-width:1000px){.workspace{grid-template-columns:1fr}.operation-list{max-height:300px;overflow:auto}}@media(max-width:700px){.layout>header{width:60px;flex-basis:60px;padding:8px 4px}.layout header>b{font-size:10px}main{padding:14px}.grid{grid-template-columns:1fr}.scope{align-items:stretch}.scope>label{width:100%}.editor,.query{padding:14px}.page-head>div{width:100%}.history article{align-items:start;flex-direction:column}}
</style>
