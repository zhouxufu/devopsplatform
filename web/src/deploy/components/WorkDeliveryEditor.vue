<script setup>
import {computed,inject,onBeforeUnmount,ref,watch} from 'vue';
import Modal from '../../shared/components/Modal.vue';
import Sel from '../../shared/components/Sel.vue';
import {ElButton,ElInput} from '../../shared/components/ui.js';
import ApplicationResourcesEditor from './ApplicationResourcesEditor.vue';
import GitBranchSelect from './GitBranchSelect.vue';
import NacosChangeFields from './NacosChangeFields.vue';
import RedisChangeFields from './RedisChangeFields.vue';
import {deliveryRows,expandDeliveryRows} from '../deliveryContent.js';
import {toast} from '../../shared/composables/toast.js';
const props=defineProps({show:Boolean,batch:Boolean,item:Object,services:Array,workItems:Array,canManage:Function});
const emit=defineEmits(['close','saved','open-sync']);
const api=inject('api'),rows=ref([]),baseline=ref(''),initial=ref(''),loading=ref(false),saving=ref(false),error=ref(''),also=ref([]),resources=ref({}),bindingService=ref(''),showBindings=ref(false),section=ref('code');
let session=0,seq=0;
const types=[{value:'code',label:'代码与应用'},{value:'mysql',label:'SQL 变更'},{value:'nacos',label:'Nacos 配置'},{value:'redis',label:'Redis 命令'},{value:'manual',label:'其他操作'}];
const serialized=()=>JSON.stringify({rows:rows.value,also:also.value});
const dirty=computed(()=>initial.value!==serialized());
const visibleRows=computed(()=>rows.value.filter(r=>section.value==='manual'?['manual','artifact','data-sync'].includes(r.kind):r.kind===section.value));
const links=computed(()=>(props.workItems||[]).filter(w=>w.id!==props.item?.id).map(w=>({value:w.id,label:'#'+w.id+' '+w.title})));
const draftKey=computed(()=>props.batch?'independent-batch':props.item?.id);
const refreshing=ref(false);
async function loadResources(name){if(!name)return;try{const r=await api(`api/services/${encodeURIComponent(name)}/resources`);resources.value={...resources.value,[name]:r.requirements||[]}}catch(e){toast(e.message,'error')}}
function options(row,kind){return (resources.value[row.service]||[]).filter(r=>r.kind===kind).map(r=>({value:r.alias,label:r.alias+' · '+(r.description||kind)}))}
async function load(){if(props.batch){++session;rows.value=[];also.value=[];error.value='';initial.value=serialized();loading.value=false;const draft=drafts.value.get(draftKey.value);if(draft){rows.value=draft.rows;also.value=draft.also;initial.value=draft.initial;drafts.value.delete(draftKey.value)}else add('code');return}const current=++session;loading.value=true;error.value='';try{const data=await api(`api/work-items/${props.item.id}/delivery-content`);if(current!==session||!props.show)return;rows.value=deliveryRows(data.changes,props.canManage,props.services);baseline.value=data.digest;also.value=[];initial.value=serialized();for(const name of new Set(rows.value.map(r=>r.service).filter(Boolean)))loadResources(name)}catch(e){if(current===session)error.value=e.message}finally{if(current===session)loading.value=false}}
watch(()=>props.show,show=>{if(show){section.value='code';load()}else session++});
function add(kind=section.value){rows.value.push({key:'new-'+(++seq),kind,services:[],originals:[],service:'',reference:'',summary:'',risk_level:'medium',rollback_plan:'',sql:kind==='mysql'?{resource_alias:'',content:'',max_affected_rows:100}:undefined,nacos:kind==='nacos'?{resource_alias:'',data_id:'',group:'DEFAULT_GROUP',format:'yaml',entries:[{key:'',value:'',action:'set'}]}:undefined})}
watch(()=>rows.value.map(r=>r.kind),()=>{for(const r of rows.value)if(r.kind==='redis'&&!r.redis)r.redis={resource_alias:'',commands:''}});
function close(){if(saving.value)return;if(dirty.value&&!confirm('保留本次未保存草稿并关闭？下次打开可继续编辑。'))return;if(dirty.value)drafts.value.set(draftKey.value,{rows:JSON.parse(JSON.stringify(rows.value)),also:[...also.value],baseline:baseline.value,initial:initial.value});else drafts.value.delete(draftKey.value);emit('close')}
const drafts=ref(new Map());
async function refreshAfterSync(){
 if(!props.show||props.batch||saving.value||refreshing.value)return;const current=session;refreshing.value=true;
 try{const data=await api(`api/work-items/${props.item.id}/delivery-content`);if(current!==session)return;
 const originals=rows.value.flatMap(r=>r.originals),existing=new Map(data.changes.map(c=>[c.id,c]));
 if(originals.some(c=>JSON.stringify(existing.get(c.id))!==JSON.stringify(c))){error.value='已有交付内容同时发生变化，未保存输入已保留；请核对后重新载入';return}
 const known=new Set(originals.map(c=>c.id)),added=deliveryRows(data.changes.filter(c=>!known.has(c.id)),props.canManage,props.services);
 rows.value.push(...added);const before=JSON.parse(initial.value);before.rows.push(...added);initial.value=JSON.stringify(before);baseline.value=data.digest;
 }catch(e){error.value=e.message}finally{refreshing.value=false}
}
defineExpose({hasDraft:()=>drafts.value.size>0||(props.show&&dirty.value),refreshAfterSync});
watch(()=>loading.value,v=>{const draft=drafts.value.get(props.item?.id);if(!v&&props.show&&draft&&!error.value){rows.value=draft.rows;also.value=draft.also;baseline.value=draft.baseline;initial.value=draft.initial;drafts.value.delete(props.item.id);refreshAfterSync()}});
function discard(){if(!confirm('放弃未保存内容并重新读取？'))return;drafts.value.delete(draftKey.value);load()}
function unload(e){if(props.show&&dirty.value){e.preventDefault();e.returnValue=''}}
window.addEventListener('beforeunload',unload);onBeforeUnmount(()=>window.removeEventListener('beforeunload',unload));
function resumeStandalone(){if(window.parent===window)refreshAfterSync()}
window.addEventListener('focus',resumeStandalone);onBeforeUnmount(()=>window.removeEventListener('focus',resumeStandalone));
async function save(){if(saving.value)return;error.value='';let items;try{items=expandDeliveryRows(rows.value,props.batch?'变更':props.item.title)}catch(e){error.value=e.message;return}if(!items.length)return toast('没有需要保存的修改','warning');saving.value=true;const current=session;try{await api(props.batch?'api/changes/batch':`api/work-items/${props.item.id}/delivery-content`,{method:props.batch?'POST':'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({digest:baseline.value,changes:items,also_work_item_ids:also.value})});if(current!==session)return;drafts.value.delete(draftKey.value);emit('saved');if(props.batch){rows.value=[];initial.value=serialized();emit('close')}else await load();toast(props.batch?'全部变更草稿已保存，未执行任何操作':'全部交付内容已保存，仍停留在当前需求','success')}catch(e){error.value=e.message}finally{saving.value=false}}
function configure(row){bindingService.value=row.service;showBindings.value=true}
</script>
<template>
<Modal :show="show" :title="batch?'批量新建变更':'交付内容 · '+(item?.title||'')" extra-wide @close="close">
 <div class="delivery-editor">
 <p class="hint">一次维护、多应用交付。保存只建立草稿，不构建、不执行 SQL、不发布配置。</p>
 <p v-if="error" class="error" role="alert">{{error}}</p><p v-if="loading">正在读取交付内容…</p>
 <template v-else>
 <nav><ElButton v-for="t in types" :key="t.value" :type="section===t.value?'primary':''" @click="section=t.value">{{t.label}} · {{rows.filter(r=>t.value==='manual'?['manual','artifact','data-sync'].includes(r.kind):r.kind===t.value).length}}</ElButton><ElButton v-if="!batch" @click="emit('open-sync')">引用同步任务</ElButton></nav>
 <div class="tools"><span>{{batch?'连续添加不同类型和多个应用的变更，保存后分别生成记录。':'已就绪或他人内容只读；新内容自动关联当前需求。'}}</span><ElButton @click="add()">＋ 添加{{types.find(t=>t.value===section)?.label}}</ElButton></div>
 <p v-if="!visibleRows.length" class="empty">本类暂无交付内容，无需填写不涉及的变更。</p>
 <article v-for="row in visibleRows" :key="row.key" class="content-row">
 <div class="row-head"><strong>{{row.originals.length?'已有内容 #'+row.originals.map(c=>c.id).join('、'):'新增内容'}}</strong><span v-if="row.readonly">{{row.status==='ready'?'已就绪 · 只读':'他人内容 · 只读'}}</span><ElButton v-if="!row.originals.length" size="small" @click="rows=rows.filter(r=>r.key!==row.key)">移除</ElButton></div>
 <div class="grid"><label v-if="batch" class="span">变更标题<ElInput v-model="row.title" :disabled="saving" placeholder="选填；留空按类型和应用自动命名" /></label>
 <template v-if="row.kind==='code'"><label>影响应用<Sel v-model="row.services" :disabled="row.readonly||saving" :options="services.map(s=>s.name)" multiple filterable width="100%" /></label><label>关联分支<GitBranchSelect v-model="row.reference" :disabled="row.readonly||saving" :repositories="row.services.map(service=>({service}))" /></label><p class="hint span" v-if="new Set(services.filter(s=>row.services.includes(s.name)).map(s=>s.git_url)).size>1">当前包含不同仓库，同步后仅列出所有应用的共同分支；不同分支应分开添加。</p></template>
 <template v-else>
 <label v-if="section==='manual'&&!row.originals.length">操作类型<Sel v-model="row.kind" :disabled="saving" :options="[{value:'manual',label:'人工操作'},{value:'artifact',label:'应用制品引用'}]" width="100%" /></label>
 <label>应用<Sel v-model="row.service" :disabled="row.readonly||saving" :options="services.map(s=>s.name)" filterable width="100%" @change="loadResources(row.service)" /></label>
 <label v-if="row.sql||row.nacos||row.redis">资源<Sel v-model="(row.sql||row.nacos||row.redis).resource_alias" :disabled="row.readonly||saving" :options="options(row,row.kind)" width="100%" /></label>
 <ElButton v-if="(row.sql||row.nacos||row.redis)&&row.service&&!row.readonly" @click="configure(row)">配置该应用资源</ElButton>
 <template v-if="row.sql"><label>允许影响行数<ElInput v-model.number="row.sql.max_affected_rows" type="number" :disabled="row.readonly||saving" /></label><label class="span">SQL 脚本<ElInput v-model="row.sql.content" type="textarea" :rows="7" :disabled="row.readonly||saving" placeholder="整段粘贴 SQL；保存校验语法，执行前再读取结构和影响范围" /></label></template>
 <NacosChangeFields v-else-if="row.nacos" :model="row.nacos" :disabled="row.readonly||saving" />
 <RedisChangeFields v-else-if="row.kind==='redis'&&row.redis" :model="row.redis" :disabled="row.readonly||saving" />
 <label v-else class="span">操作 / 引用<ElInput v-model="row.reference" type="textarea" :rows="3" :disabled="row.readonly||saving" /></label>
 </template>
 <label class="span">说明（选填）<ElInput v-model="row.summary" :disabled="row.readonly||saving" /></label>
 <details class="span"><summary>风险与回退说明</summary><label>风险等级<Sel v-model="row.risk_level" :options="[{value:'low',label:'低'},{value:'medium',label:'中'},{value:'high',label:'高'}]" :disabled="row.readonly||saving" /></label><ElInput v-model="row.rollback_plan" placeholder="回退说明" :disabled="row.readonly||saving" /></details>
 </div></article>
 <label>{{batch?'关联需求 / Bug（选填）':'新增内容同时关联其他需求 / Bug（选填）'}}<Sel v-model="also" :options="links" multiple filterable width="100%" /></label>
 <div class="footer"><span>{{refreshing?'正在合并新增交付内容':dirty?'有未保存修改':'已保存'}} · {{rows.length}} 项</span><ElButton :disabled="saving||refreshing" @click="discard">重新载入</ElButton><ElButton :disabled="saving||refreshing" @click="close">关闭</ElButton><ElButton type="primary" :loading="saving" :disabled="refreshing" @click="save">{{batch?'保存全部变更':'保存全部内容'}}</ElButton></div>
 </template></div>
</Modal>
<ApplicationResourcesEditor :show="showBindings" :services="services" :initial-service="bindingService" @close="showBindings=false;loadResources(bindingService)" />
</template>
<style scoped>
.delivery-editor{display:grid;gap:14px}.hint,.tools{color:var(--muted);font-size:13px}.error{color:var(--red,#d33);background:var(--surface);padding:12px;border:1px solid}.delivery-editor nav,.tools,.row-head,.footer{display:flex;align-items:center;gap:10px;flex-wrap:wrap}.tools,.row-head{justify-content:space-between}.content-row{border:1px solid var(--border);padding:16px;border-radius:8px}.grid{display:grid;grid-template-columns:1fr 1fr;gap:12px;margin-top:12px}.span{grid-column:1/-1}label{display:grid;gap:7px;min-width:0}.config-entry{display:flex;gap:8px;margin-bottom:8px}.config-entry>.el-input{flex:1;min-width:0}.empty{padding:25px;text-align:center;color:var(--muted)}.footer{position:sticky;bottom:0;background:var(--surface);padding:14px 0;border-top:1px solid var(--border);justify-content:flex-end}.footer>span{margin-right:auto}details label{margin:10px 0}@media(max-width:700px){.grid{grid-template-columns:1fr}.config-entry{flex-wrap:wrap}.config-entry>.el-input{flex-basis:100%}}
</style>
