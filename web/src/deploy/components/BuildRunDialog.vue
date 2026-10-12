<script setup>
import { ref, watch } from 'vue'
import Modal from '../../shared/components/Modal.vue'
import Sel from '../../shared/components/Sel.vue'
import {ElButton,ElInput} from '../../shared/components/ui.js'
import GitBranchSelect from './GitBranchSelect.vue'
const props=defineProps({show:Boolean,service:Object,delivery:Object,targets:{type:Array,default:()=>[]},mode:{type:String,default:'build'},environment:{type:String,default:''}})
const emit=defineEmits(['close','started'])
const form=ref({}),busy=ref(false),error=ref(''),advanced=ref(false)
watch(()=>props.show,opened=>{if(opened){const s=props.service||{};form.value={service:s.name,branch:s.branch||'',deploy_environment:props.mode==='deploy'?props.environment:'',reason:'',override_maven:false,override_gradle:false,maven_profiles:s.maven_profiles||'',maven_properties:s.maven_properties||'',gradle_tasks:s.gradle_tasks||'clean build',gradle_excluded_tasks:s.gradle_excluded_tasks||'',gradle_project_properties:s.gradle_project_properties||'',gradle_system_properties:s.gradle_system_properties||''};error.value='';advanced.value=false}})
function overrides(value){advanced.value=value;form.value.override_maven=value&&props.service?.build_tool==='maven';form.value.override_gradle=value&&props.service?.build_tool==='gradle'}
async function start(){if(busy.value)return;busy.value=true;error.value='';try{
 const body={...form.value};if(props.mode!=='deploy'){body.deploy_environment='';body.reason=''}
 const r=await fetch('api/pipeline/runs',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)}),v=await r.json()
 if(!r.ok||!v.id)throw Error(v.error||'构建未能发起');emit('started',v.id)
}catch(e){error.value=e.message}finally{busy.value=false}}
const target=()=>props.targets.find(t=>t.environment===form.value.deploy_environment)
const targetReady=()=>{const t=target();return t&&(t.target_type==='aliyun-sae'?t.deploy_config?.connection&&t.deploy_config?.app_id&&t.deploy_config?.namespace_id&&t.deploy_config?.region:t.host_group&&t.hosts?.length)}
</script>
<template>
 <Modal :show="show" :busy="busy" :title="(mode==='deploy'?'构建并发布 ':'构建 ')+(service?.name||'')" wide @close="emit('close')">
  <div class="build-launch">
   <label>本次构建分支<GitBranchSelect v-model="form.branch" :repositories="service?.git_url?[{service:service.name}]:[]" :disabled="busy"/></label>
   <small>只覆盖本次运行，不修改默认配置；源码解析后记录实际 Commit。</small>
   <div class="build-launch-summary"><span>工具 {{service?.build_tool||service?.artifact_type}}</span><span>Runner {{delivery?.runner||'local'}}</span><span>{{delivery?.build_environment?.mode==='container'?'隔离容器 · '+(delivery.build_environment.preset||'自定义镜像'):'Runner 本机工具'}}</span></div>
   <template v-if="mode==='deploy'"><label>目标环境<Sel v-model="form.deploy_environment" :options="targets.map(t=>({value:t.environment,label:t.display_name||t.environment}))" :disabled="busy" width="100%"/></label><label>发布原因<ElInput v-model="form.reason" :disabled="busy" placeholder="说明本次发布目的"/></label><p class="launch-warning">{{target()?.protected?'构建成功后提交审批，不直接发布。':'构建成功后将自动创建发布单；失败不会发布。'}} 使用本次生成的制品。</p></template>
   <button v-if="['maven','gradle'].includes(service?.build_tool)" class="text-link" :disabled="busy" @click="overrides(!advanced)">{{advanced?'收起':'覆盖本次'}}构建参数</button>
   <div v-if="advanced&&service?.build_tool==='maven'" class="launch-advanced"><label>Profiles<ElInput v-model="form.maven_profiles" :disabled="busy"/></label><label>-D 属性（每行 key=value）<ElInput v-model="form.maven_properties" type="textarea" :rows="3" :disabled="busy"/></label></div>
   <div v-if="advanced&&service?.build_tool==='gradle'" class="launch-advanced"><label>Tasks<ElInput v-model="form.gradle_tasks" :disabled="busy"/></label><label>排除任务<ElInput v-model="form.gradle_excluded_tasks" :disabled="busy"/></label><label>-P 项目属性<ElInput v-model="form.gradle_project_properties" type="textarea" :rows="2" :disabled="busy"/></label><label>-D 系统属性<ElInput v-model="form.gradle_system_properties" type="textarea" :rows="2" :disabled="busy"/></label></div>
   <p v-if="error" class="launch-error" role="alert">{{error}}</p>
   <p v-if="mode==='deploy'&&form.deploy_environment&&!targetReady()" class="launch-warning">该环境发布资源不完整，请先配置环境。</p>
   <footer><ElButton :disabled="busy" @click="emit('close')">取消</ElButton><ElButton type="primary" :loading="busy" :disabled="mode==='deploy'&&(!targetReady()||(target()?.require_reason&&!form.reason.trim())||(target()?.protected&&!target()?.approvers))" @click="start">{{mode==='deploy'?'确认构建并发布':'开始构建'}}</ElButton></footer>
  </div>
 </Modal>
</template>
<style scoped>
.build-launch,.launch-advanced{display:grid;gap:14px}.build-launch label{display:grid;gap:6px;font-size:13px}.build-launch small{font-size:12px;color:var(--muted)}.build-launch-summary{display:flex;gap:12px;flex-wrap:wrap;font-size:12px;color:var(--muted);padding:8px;background:var(--panel2);border-radius:6px}.build-launch footer{display:flex;justify-content:flex-end;gap:8px;border-top:1px solid var(--border);padding-top:12px}.text-link{border:0;background:none;color:var(--brand);text-align:left;cursor:pointer;padding:0}.launch-error{color:var(--error);font-size:13px}.launch-warning{margin:0;color:var(--warn);font-size:12px}
</style>
