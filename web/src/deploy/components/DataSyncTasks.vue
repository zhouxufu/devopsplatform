<script setup>
import {computed,inject,ref,watch} from 'vue';
import Modal from '../../shared/components/Modal.vue';
import Sel from '../../shared/components/Sel.vue';
import {ElButton,ElInput} from '../../shared/components/ui.js';
import ApplicationResourcesEditor from './ApplicationResourcesEditor.vue';
import {toast} from '../../shared/composables/toast.js';
const props=defineProps({show:Boolean,inline:Boolean,services:Array,environments:Array,item:Object,approvedComparison:Object});
const emit=defineEmits(['close','generated','preview','query-approval']);
const approvalID=ref(0);
const lastGenerated=ref(null);
const api=inject('api'),tasks=ref([]),form=ref(null),original=ref(''),resources=ref([]),source=ref(''),target=ref(''),comparison=ref(null),selected=ref([]),edited=ref({}),saving=ref(false),error=ref(''),showBindings=ref(false);
let session=0;
const dirty=computed(()=>form.value&&JSON.stringify(form.value)!==original.value);
const envOptions=computed(()=>props.environments.map(e=>({value:e.name,label:e.display_name||e.name})));
const resourceOptions=computed(()=>resources.value.filter(r=>r.kind==='mysql').map(r=>({value:r.alias,label:r.alias})));
async function load(){try{tasks.value=await api('api/data-sync/tasks')}catch(e){error.value=e.message}}
watch(()=>props.show,v=>{session++;if(v){form.value=null;comparison.value=null;lastGenerated.value=null;error.value='';load()}},{immediate:true});
watch(()=>props.approvedComparison,async data=>{if(!data)return;form.value=JSON.parse(JSON.stringify(data.result.task));original.value=JSON.stringify(form.value);source.value=data.result.source_environment;target.value=data.result.target_environment;await resourcesFor();comparison.value=data.result;approvalID.value=data.ticket.id;selected.value=[];edited.value=Object.fromEntries(data.result.differences.map(d=>[d.key,JSON.parse(JSON.stringify(d.after))]));});
async function resourcesFor(){resources.value=[];if(!form.value?.service)return;const name=form.value.service;try{const r=await api(`api/services/${encodeURIComponent(name)}/resources`);if(form.value?.service===name)resources.value=r.requirements||[]}catch(e){error.value=e.message}}
function invalidate(){comparison.value=null;selected.value=[];edited.value={};approvalID.value=0}
watch([source,target,()=>JSON.stringify(form.value)],invalidate);
function open(task){if(saving.value)return;if(dirty.value&&!confirm('放弃当前未保存的任务修改？'))return;form.value=task?JSON.parse(JSON.stringify(task)):{name:'',service:'',resource_alias:'',source_sql:'',target_sql:'',keys:[]};original.value=JSON.stringify(form.value);invalidate();source.value='';target.value='';lastGenerated.value=null;error.value='';resourcesFor()}
function close(){if(saving.value)return;if(dirty.value&&!confirm('放弃任务的未保存修改并返回原页面？'))return;emit('close')}
async function save(){saving.value=true;error.value='';const current=session;try{const result=await api('api/data-sync/tasks'+(form.value.id?'/'+form.value.id:''),{method:form.value.id?'PUT':'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(form.value)});if(current!==session)return;form.value=result;original.value=JSON.stringify(result);invalidate();await load();toast('任务已保存，选择本次来源和目标环境即可对比','success')}catch(e){error.value=e.message}finally{saving.value=false}}
async function compare(){if(dirty.value)return error.value='请先保存任务修改，再对比';if(!source.value||!target.value)return error.value='请选择本次来源和目标环境';if(source.value===target.value)return error.value='来源和目标环境不能相同';saving.value=true;error.value='';invalidate();const current=session;try{const scope={source_environment:source.value,target_environment:target.value};if(props.inline){const policies=await Promise.all([source.value,target.value].map(env=>api(`api/policy?environment=${encodeURIComponent(env)}`)));if(current!==session)return;if(policies.some(p=>p.query_approval)){emit('query-approval',{task_id:form.value.id,scope});return}}const data=await api(`api/data-sync/tasks/${form.value.id}/compare`,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(scope)});if(current!==session)return;comparison.value=data;selected.value=[];edited.value=Object.fromEntries(data.differences.map(d=>[d.key,JSON.parse(JSON.stringify(d.after))]))}catch(e){error.value=e.message}finally{saving.value=false}}
async function freeze(){if(dirty.value)return error.value='任务已有修改，请保存并重新对比';if(!comparison.value||!selected.value.length)return error.value='请勾选要同步的差异';saving.value=true;error.value='';const current=session;try{const work=props.item?await api(`api/work-items/${props.item.id}/delivery-content`):null;const data=await api(`api/data-sync/tasks/${form.value.id}/freeze`,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({source_environment:comparison.value.source_environment,target_environment:comparison.value.target_environment,digest:comparison.value.digest,query_approval_id:approvalID.value,work_item_id:props.item?.id||0,work_digest:work?.digest,rows:selected.value.map(key=>({key,after:edited.value[key]}))})});if(current!==session)return;lastGenerated.value=data;emit('generated',data);toast('已生成同步 SQL 草稿，未执行；请到变更提交预检目标库','success');invalidate()}catch(e){error.value=e.message}finally{saving.value=false}}
function keys(text){form.value.keys=text.split(',').map(k=>k.trim()).filter(Boolean)}
</script>
<template>
<component :is="inline?'section':Modal" :show="show" title="数据同步任务" extra-wide @close="close"><div class="sync-tasks">
 <h3 v-if="inline">数据对比同步</h3>
 <p>任务只保存查询与匹配规则，不绑定环境；每次对比时选择来源和目标环境。目标独有数据保留，不自动删除。</p>
 <p v-if="error" class="error" role="alert">{{error}}</p>
 <div class="toolbar"><Sel :model-value="form?.id||''" :options="tasks.map(t=>({value:t.id,label:t.name+' · '+t.service}))" :disabled="saving" filterable placeholder="选择已有任务" width="100%" @change="open(tasks.find(t=>t.id===$event))" /><ElButton :disabled="saving" @click="open()">＋ 新建任务</ElButton></div>
 <p v-if="!form">{{tasks.length?'选择任务开始对比，也可新建。':'还没有任务；创建一次后可在需求、Bug 交付内容中反复使用。'}}</p>
 <template v-else><div class="grid">
 <label>任务名称<ElInput v-model="form.name" :disabled="saving" /></label><label>应用<Sel v-model="form.service" :options="services.map(s=>s.name)" :disabled="saving" width="100%" @change="resourcesFor" /></label>
 <label class="full">逻辑数据库资源<Sel v-model="form.resource_alias" :options="resourceOptions" :disabled="saving" width="100%" /><small>只保存资源别名，实际数据库按本次选择的环境绑定解析。</small></label>
 <label>来源查询 SQL<ElInput v-model="form.source_sql" type="textarea" :rows="5" :disabled="saving" /></label><label>目标查询 SQL<ElInput v-model="form.target_sql" type="textarea" :rows="5" :disabled="saving" /></label>
 <label>匹配键（多个用逗号分隔）<ElInput :model-value="form.keys.join(',')" :disabled="saving" @update:model-value="keys" /></label><p class="hint">仅支持单表显式字段 SELECT + WHERE；查询最多 1000 行。匹配键须对应目标主键或唯一索引，来源与目标字段一致。</p>
 </div><div class="toolbar"><ElButton :disabled="saving||!form.service" @click="showBindings=true">配置应用资源</ElButton><ElButton :loading="saving" @click="save">保存任务</ElButton><span>{{form.id?(dirty?'任务有未保存修改':'任务已保存'):'尚未保存任务'}}</span></div>
 <section v-if="form.id" class="comparison-scope"><h3>本次对比</h3><p class="hint">环境选择不写入任务；开发 → 测试、测试 → 生产可复用同一任务。两侧须已绑定该应用的逻辑数据库资源。</p><div class="toolbar"><label>本次来源环境<Sel v-model="source" :options="envOptions" width="180px" :disabled="saving" /></label><span class="direction">→</span><label>本次目标环境<Sel v-model="target" :options="envOptions" width="180px" :disabled="saving" /></label><ElButton type="primary" :loading="saving" @click="compare">对比数据（只读）</ElButton></div></section>
 <template v-if="comparison"><p>{{comparison.source_environment}} → {{comparison.target_environment}} · 差异 {{comparison.differences.length}} 行 · 一致 {{comparison.unchanged}} 行 · 目标独有 {{comparison.target_only}} 行（保留）</p>
 <div class="diff-list"><article v-for="d in comparison.differences" :key="d.key"><label class="diff-title"><input v-model="selected" type="checkbox" :value="d.key" :disabled="saving" />{{d.kind==='insert'?'新增':'更新'}} · {{d.key}}</label><div class="diff-row" v-for="col in comparison.columns" :key="col"><b>{{col}}{{form.keys.includes(col)?'（匹配键）':''}}</b><span class="before">{{d.before?d.before[col]===null?'NULL':d.before[col]:'不存在'}}</span><ElInput v-model="edited[d.key][col]" :disabled="saving||form.keys.includes(col)||edited[d.key][col]===null" /><label v-if="!form.keys.includes(col)"><input type="checkbox" :checked="edited[d.key][col]===null" :disabled="saving" @change="edited[d.key][col]=$event.target.checked?null:''" />NULL</label></div></article></div>
 <p v-if="!comparison.differences.length">无需同步，没有可写入差异。</p><p class="hint">修改右侧值后勾选差异，再生成草稿。生成前会重新核对任务和数据；之后执行仍须审批及目标数据校验。</p>
 <ElButton type="primary" :loading="saving" :disabled="!selected.length" @click="freeze">生成同步草稿{{item?'并关联当前需求':''}}（{{selected.length}} 行）</ElButton></template>
 </template><section v-if="lastGenerated" class="result"><b>已生成同步草稿 #{{lastGenerated.id}} · {{lastGenerated.sql.data_sync.source_environment}} → {{lastGenerated.sql.data_sync.target_environment}}</b><p>{{item?'已关联当前需求，在需求详情核对就绪后随版本交付。':'已加入变更列表，核对就绪后可纳入版本交付。'}}未执行数据库写入。</p><pre style="white-space:pre-wrap;overflow-wrap:anywhere">{{lastGenerated.sql.content}}</pre><ElButton @click="emit('preview',lastGenerated)">继续预检目标库（只读）</ElButton></section><div class="footer"><ElButton :disabled="saving" @click="close">关闭并返回原页面</ElButton></div>
 </div></component>
 <ApplicationResourcesEditor :show="showBindings" :services="services" :initial-service="form?.service||''" @close="showBindings=false;resourcesFor()" />
</template>
<style scoped>
.full{grid-column:1/-1}.comparison-scope{border:1px solid var(--border);border-radius:8px;padding:16px}.comparison-scope h3{margin:0}.comparison-scope .toolbar{align-items:end}.direction{padding-bottom:8px}small{color:var(--muted)}
.sync-tasks{display:grid;gap:14px}.toolbar,.footer{display:flex;align-items:center;gap:10px;flex-wrap:wrap}.toolbar>.el-select{flex:1}.grid{display:grid;grid-template-columns:1fr 1fr;gap:14px}label{display:grid;gap:7px;min-width:0}.error{color:var(--red,#d33);padding:10px;border:1px solid}.hint{color:var(--muted);font-size:13px}.diff-list article{padding:14px;border:1px solid var(--border);border-radius:8px;margin:10px 0}.diff-title{display:flex;align-items:center}.diff-row{display:grid;grid-template-columns:130px 1fr 1fr 70px;align-items:center;gap:12px;margin-top:10px}.before{overflow-wrap:anywhere;white-space:pre-wrap;color:var(--muted)}.footer{justify-content:flex-end;border-top:1px solid var(--border);padding-top:12px}@media(max-width:700px){.grid{grid-template-columns:1fr}.diff-row{grid-template-columns:1fr 1fr}.diff-row b{grid-column:1/-1}}
</style>
