<script setup>
import Sel from '../../shared/components/Sel.vue';
import HelpTip from '../../shared/components/HelpTip.vue';
import {ElButton,ElInput} from '../../shared/components/ui.js';
defineProps({model:{type:Object,required:true},disabled:Boolean});
</script>
<template>
 <div class="nacos-fields">
  <div class="nacos-location">
   <label data-field="data_id">Data ID（默认，可选）<ElInput v-model="model.data_id" :disabled="disabled||!!model.restore" placeholder="优先使用目标环境绑定的 Data ID" /></label>
   <label data-field="group">默认 Group<ElInput v-model="model.group" :disabled="disabled||!!model.restore" /></label>
   <label data-field="format">格式<Sel v-model="model.format" :options="['yaml','properties','json']" :disabled="disabled||!!model.restore" width="100%" /></label>
  </div>
  <div class="target-note"><span>环境绑定可覆盖默认值，执行前预览最终目标。</span><HelpTip align="right" :text="model.format==='properties'?'保存不发布；就绪后选择环境预览，再审核执行。properties 值按原文保存，只修改已有文档的配置项。请勿粘贴生产密钥。':'保存不发布；就绪后选择环境预览，再审核执行。YAML/JSON 的点表示层级；true、123、null 是对应类型，字符串可用双引号。只支持已有文档、标量配置项；请勿粘贴生产密钥。'" /></div>
  <section class="nacos-entries" data-field="entries" aria-label="配置项编辑">
   <div class="entry-columns" aria-hidden="true"><span>配置项</span><span>操作</span><span>期望值</span><span></span></div>
   <div v-for="(entry,i) in model.entries" :key="i" class="config-entry" :data-entry-index="i" role="group" :aria-label="'配置项 '+(i+1)">
    <div data-field="key"><ElInput v-model="entry.key" placeholder="配置项，例如 feature.enabled" :aria-label="'第 '+(i+1)+' 个配置项名称'" :disabled="disabled||!!model.restore" /></div>
    <div data-field="action"><Sel v-model="entry.action" :aria-label="'第 '+(i+1)+' 个配置项操作'" :options="[{value:'set',label:'设置值'},{value:'delete',label:'删除项'}]" :disabled="disabled||!!model.restore" width="100%" /></div>
    <div data-field="value"><ElInput v-model="entry.value" type="textarea" :autosize="{minRows:1,maxRows:6}" placeholder="期望值" :aria-label="'第 '+(i+1)+' 个配置项期望值'" :disabled="disabled||!!model.restore||entry.action==='delete'" /></div>
    <ElButton v-if="!disabled&&!model.restore" text :aria-label="'删除第 '+(i+1)+' 个配置项'" @click="model.entries.splice(i,1)">删除项</ElButton>
   </div>
   <ElButton v-if="!disabled&&!model.restore" size="small" @click="model.entries.push({key:'',value:'',action:'set'})">＋ 配置项</ElButton>
  </section>
  <p class="restore-note" v-if="model.restore">恢复草稿引用历史完整快照，配置项只读；执行前重新预览审核，当前文档变化会阻止恢复。</p>
 </div>
</template>
<style scoped>
.nacos-fields{grid-column:1/-1;display:grid;gap:12px;min-width:0}.nacos-location{display:grid;grid-template-columns:minmax(0,1.6fr) minmax(0,1fr) minmax(100px,.6fr);gap:12px}.nacos-location label{display:grid;gap:4px;min-width:0;font-size:12px;color:var(--muted)}.target-note{display:flex;align-items:center;gap:8px;color:var(--muted);font-size:12px}.target-note>span{min-width:0}.entry-columns,.config-entry{display:grid;grid-template-columns:minmax(0,1fr) 112px minmax(0,1.3fr) 76px;gap:8px;align-items:start}.entry-columns{font-size:12px;color:var(--muted);margin-bottom:6px}.config-entry{margin-bottom:8px}.config-entry>div{min-width:0}.restore-note{font-size:12px;color:var(--muted)}@media(max-width:700px){.nacos-location{grid-template-columns:1fr 1fr}.nacos-location>label:first-child{grid-column:1/-1}.entry-columns{display:none}.config-entry{grid-template-columns:minmax(0,1fr) 112px}.config-entry>[data-field=value]{grid-column:1}.config-entry>.el-button{grid-column:2;justify-self:end}.config-entry{padding:10px 0;border-bottom:1px solid var(--border)}}
</style>
