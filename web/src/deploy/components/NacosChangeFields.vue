<script setup>
import Sel from '../../shared/components/Sel.vue';
import {ElButton,ElInput} from '../../shared/components/ui.js';
defineProps({model:{type:Object,required:true},disabled:Boolean});
</script>
<template>
  <label>Data ID<ElInput v-model="model.data_id" :disabled="disabled||!!model.restore" /></label>
  <label>Group<ElInput v-model="model.group" :disabled="disabled||!!model.restore" /></label>
  <label>格式<Sel v-model="model.format" :options="['yaml','properties','json']" :disabled="disabled||!!model.restore" width="100%" /></label>
  <div class="span">
    <div v-for="(entry,i) in model.entries" :key="i" class="config-entry">
      <ElInput v-model="entry.key" placeholder="配置项，例如 feature.enabled" :disabled="disabled||!!model.restore" />
      <Sel v-model="entry.action" :options="[{value:'set',label:'设置值'},{value:'delete',label:'删除项'}]" :disabled="disabled||!!model.restore" width="100px" />
      <ElInput v-model="entry.value" placeholder="期望值" :disabled="disabled||!!model.restore||entry.action==='delete'" />
      <ElButton v-if="!disabled&&!model.restore" @click="model.entries.splice(i,1)">移除</ElButton>
    </div>
    <ElButton v-if="!disabled&&!model.restore" @click="model.entries.push({key:'',value:'',action:'set'})">＋ 配置项</ElButton>
  </div>
  <p class="hint span" v-if="model.restore">恢复草稿引用历史完整文档快照，配置项只读；标记就绪后重新预览、审核并执行。当前文档变化会阻止恢复。</p>
  <p class="hint span" v-else>保存不会发布。标记就绪后，选择目标环境预览差异，再通过部署编排审核执行。{{model.format==='properties'?'properties 值按原文保存。':'YAML/JSON 的点表示层级；true、123、null 为对应类型，字符串可用双引号。'}}只支持已有配置、标量配置项；请勿粘贴生产密钥。</p>
</template>
<style scoped>
.config-entry{display:grid;grid-template-columns:minmax(0,1fr) 100px minmax(0,1fr) auto;gap:8px;margin-bottom:8px}.hint{color:var(--muted);font-size:12px;line-height:1.6}@media(max-width:700px){.config-entry{grid-template-columns:minmax(0,1fr) 100px}.config-entry>:nth-child(3){grid-column:1}}
</style>
