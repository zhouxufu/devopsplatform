<script setup>
import { inject, ref, watch } from 'vue'
import Modal from '../../shared/components/Modal.vue'
import ResourcesView from '../pages/ResourcesView.vue'
import Sel from '../../shared/components/Sel.vue'
import { ElButton, ElInput } from '../../shared/components/ui.js'
import { toast } from '../../shared/composables/toast.js'
const props=defineProps({show:Boolean,services:Array,initialService:String})
const emit=defineEmits(['close'])
const api=inject('api'),service=ref(''),resources=ref({requirements:[],bindings:[]}),connections=ref([]),environments=ref([]),busy=ref(false),loading=ref(false)
const resourcePlugin=inject('resourcePlugin','deploy')
const directAPI=!location.pathname.startsWith('/api/plugins/')
async function loadService(){
  if(!service.value){resources.value={requirements:[],bindings:[]};return}
  loading.value=true
  const requestedService=service.value
  try{const value=await api(`api/services/${encodeURIComponent(requestedService)}/resources`);if(service.value===requestedService)resources.value={...value,bindings:(value.bindings||[]).map(b=>({...b,config:b.config||{}}))}}catch(e){toast(e.message,'error')}finally{if(service.value===requestedService)loading.value=false}
}
watch(service,loadService)
watch(()=>props.show,async show=>{if(!show)return;try{[connections.value,environments.value]=await Promise.all([api('api/connections'),api('api/environments')]);service.value=props.initialService||props.services?.[0]?.name||'';await loadService()}catch(e){toast(e.message,'error')}})
function addBinding(alias){resources.value.bindings.push({alias,environment:'',connection:'',config:{}})}
const showConnections=ref(false)
async function refreshConnections(){try{connections.value=await api('api/connections')}catch(e){toast(e.message,'error')}}
function openConnections(){if(window.parent!==window)window.parent.postMessage({type:'platform.open',view:'resources'},location.origin);else showConnections.value=true}
async function save(){
  busy.value=true
  try{await api(`api/services/${encodeURIComponent(service.value)}/resources`,{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify(resources.value)});toast('应用环境资源绑定已保存','success');emit('close')}catch(e){toast(e.message,'error')}finally{busy.value=false}
}
</script>
<template>
  <Modal :show="show" title="应用环境资源绑定" extra-wide @close="emit('close')">
    <div class="binding-editor">
      <label>应用<Sel v-model="service" :disabled="busy||loading" :options="(services||[]).map(s=>s.name)" width="100%" /></label>
      <p>变更选择逻辑资源别名；执行时按环境解析实际连接。</p>
      <div><ElButton size="small" @click="openConnections">管理资源连接</ElButton> <ElButton size="small" @click="refreshConnections">刷新连接</ElButton></div>
      <p v-if="loading">读取资源绑定中…</p>
      <template v-else>
        <article v-for="(r,i) in resources.requirements" :key="i">
          <div class="binding-row"><ElInput v-model="r.alias" :disabled="!!r.service" placeholder="资源别名，如 business-db" /><Sel v-model="r.kind" :disabled="!!r.service" :options="[{value:'mysql',label:'MySQL'},{value:'nacos',label:'Nacos'},{value:'redis',label:'Redis'}]" /><ElButton @click="resources.requirements.splice(i,1);resources.bindings=resources.bindings.filter(b=>b.alias!==r.alias)">删除资源</ElButton></div>
          <div v-for="(b,j) in resources.bindings" :key="j">
            <div v-if="b.alias===r.alias" class="binding-row" :class="{'nacos-binding':r.kind==='nacos'}"><Sel v-model="b.environment" :options="environments.map(e=>({value:e.name,label:e.display_name}))" all-label="选择环境" /><Sel v-model="b.connection" :options="connections.filter(c=>c.kind===r.kind).map(c=>({value:c.name,label:`${c.name} · ${c.config?.database||c.endpoint}`}))" all-label="选择连接" filterable /><template v-if="r.kind==='nacos'"><label>Data ID<ElInput v-model="b.config.data_id" :disabled="busy" placeholder="该环境 Data ID；留空沿用变更默认值" /></label><label>Group<ElInput v-model="b.config.group" :disabled="busy" placeholder="留空沿用变更默认 Group" /></label></template><ElButton @click="resources.bindings.splice(j,1)">移除绑定</ElButton></div>
          </div>
          <ElButton size="small" :disabled="!r.alias" @click="addBinding(r.alias)">＋ 环境绑定</ElButton>
        </article>
        <ElButton :disabled="!service" @click="resources.requirements.push({alias:'',kind:'mysql',required:false,description:''})">＋ 声明资源</ElButton>
      </template>
      <div class="binding-footer"><ElButton @click="emit('close')">取消</ElButton><ElButton type="primary" :loading="busy" :disabled="loading||!service" @click="save">保存绑定</ElButton></div>
    </div>
  </Modal>
  <Modal :show="showConnections" title="资源中心" extra-wide @close="showConnections=false;refreshConnections()"><ResourcesView :plugin="resourcePlugin" :directAPI="directAPI" /></Modal>
</template>
<style scoped>
.binding-editor{display:grid;gap:14px}.binding-editor p{color:var(--muted);margin:0}.binding-editor article{border:1px solid var(--border);border-radius:8px;padding:14px;display:grid;gap:10px}.binding-row{display:grid;grid-template-columns:minmax(160px,1fr) minmax(160px,1.4fr) auto;gap:12px;align-items:center}.nacos-binding{grid-template-columns:minmax(100px,.7fr) minmax(160px,1fr) minmax(180px,1.3fr) minmax(130px,.8fr) auto}.binding-row>.el-select{width:100%!important}.nacos-binding label{display:grid;gap:4px;font-size:12px;color:var(--muted);min-width:0}.binding-footer{display:flex;justify-content:flex-end;gap:12px}@media(max-width:900px){.nacos-binding{grid-template-columns:1fr 1fr}.nacos-binding>.el-button{justify-self:start}}@media(max-width:700px){.binding-row{grid-template-columns:1fr}}
</style>
