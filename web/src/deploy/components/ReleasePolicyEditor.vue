<script setup>
import {computed} from 'vue'
import Sel from '../../shared/components/Sel.vue'
import {ElInputNumber} from '../../shared/components/ui.js'
const props=defineProps({target:{type:Object,required:true},disabled:Boolean})
const cfg=computed(()=>props.target.deploy_config||(props.target.deploy_config={}))
const cloud=computed(()=>props.target.target_type==='aliyun-sae')
const mode=computed(()=>cfg.value.release_mode||'batch')
function set(key,value){cfg.value[key]=String(value)}
function choose(value){set('release_mode',value);if(value==='canary'){cfg.value.canary_count||= '1';cfg.value.observe_seconds||='300'}if(cloud.value){cfg.value.batch_count||='2';cfg.value.batch_mode||='manual'}}
</script>
<template><fieldset :disabled="disabled" class="release-policy">
 <label>发布方式<Sel :model-value="mode" :options="[{value:'batch',label:'分批发布'},{value:'canary',label:'金丝雀：少量实例先发布并观察'}]" width="100%" :disabled="disabled" @change="choose" /></label>
 <div v-if="mode==='canary'" class="policy-grid"><label>首批实例数<ElInputNumber :model-value="Number(cfg.canary_count||1)" :min="1" :max="10000" :disabled="disabled" @update:model-value="v=>{choose('canary');set('canary_count',v)}" /></label><label>最低观察时间（秒）<ElInputNumber :model-value="Number(cfg.observe_seconds??300)" :min="0" :max="3600" :disabled="disabled" @update:model-value="v=>{choose('canary');set('observe_seconds',v)}" /></label></div>
 <div v-if="cloud" class="policy-grid"><label>{{mode==='canary'?'剩余实例批数':'发布批数'}}<ElInputNumber :model-value="Number(cfg.batch_count||2)" :min="1" :max="10000" :disabled="disabled" @update:model-value="v=>{choose(mode);set('batch_count',v)}" /></label><label v-if="mode==='batch'">批次推进<Sel :model-value="cfg.batch_mode||'manual'" :options="[{value:'manual',label:'每批人工确认'},{value:'auto',label:'自动推进'}]" :disabled="disabled" width="100%" @change="v=>{choose(mode);set('batch_mode',v)}" /></label><label v-if="mode==='batch'&&cfg.batch_mode==='auto'">批次间隔（分钟）<ElInputNumber :model-value="Number(cfg.batch_wait_minutes||0)" :min="0" :max="30" :disabled="disabled" @update:model-value="v=>{choose(mode);set('batch_wait_minutes',v)}" /></label></div>
 <div v-else class="policy-grid"><label>{{mode==='canary'?'后续每批主机数':'每批主机数'}}<ElInputNumber v-model="target.batch_size" :min="1" :max="100" :disabled="disabled" /></label><label>批内最大并发<ElInputNumber v-model="target.max_parallel" :min="1" :max="100" :disabled="disabled" /></label><label>允许失败主机数<ElInputNumber v-model="target.failure_threshold" :min="0" :max="100" :disabled="disabled" /></label><label><input v-model="target.pause_between_batches" type="checkbox"/> 后续每批完成后人工确认</label></div>
 <small v-if="mode==='canary'">至少两个实例，首批不超过总数的 50%。首批不允许失败；健康检查后开始观察，时间到后仍须人工确认，不会自动扩大发布。{{cloud?'SAE 金丝雀及后续批次均人工确认。':'首批按当前主机组顺序选取，提交后固定目标快照。'}}</small>
 <small v-else-if="cloud&&!cfg.release_mode">已有 SAE 原始发布策略保持不变；选择发布方式后才使用本表单策略。</small>
</fieldset></template>
<style scoped>.release-policy{border:0;margin:0;padding:0;min-width:0}.release-policy label{display:flex;flex-direction:column;gap:6px;font-size:12px;margin-bottom:12px}.policy-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:12px}.release-policy small{display:block;color:var(--muted);line-height:1.6;font-size:12px}@media(max-width:650px){.policy-grid{grid-template-columns:1fr}}</style>
