<script setup>
import Sel from '../../shared/components/Sel.vue'
import { ElButton, ElInput } from '../../shared/components/ui.js'
defineProps({ nodes: { type: Array, default: () => [] }, apps: { type: Array, default: () => [] }, changes: { type: Array, default: () => [] }, disabled: Boolean })
const kinds = [{value:'deploy',label:'发布应用'},{value:'manual',label:'人工确认'},{value:'mysql',label:'SQL / 数据同步变更'},{value:'nacos',label:'Nacos 配置'},{value:'redis',label:'Redis 命令'}]
function move(nodes, i, delta) { const j=i+delta; if(j>=0&&j<nodes.length) [nodes[i],nodes[j]]=[nodes[j],nodes[i]] }
function add(nodes) { nodes.push({id:crypto.randomUUID(),name:'发布应用',kind:'deploy',service:'',instructions:''}) }
</script>
<template>
  <fieldset :disabled="disabled" class="node-editor">
    <details v-for="(n,i) in nodes" :key="n.id" class="node-edit">
      <summary>{{i+1}}. {{n.name||'未命名节点'}} · {{kinds.find(k=>k.value===n.kind)?.label||n.kind}} <span>{{n.service||''}}</span></summary>
      <div class="node-fields"><label>名称<ElInput v-model="n.name" :disabled="disabled" /></label><label>节点类型<Sel v-model="n.kind" :disabled="disabled" :options="kinds" width="100%" /></label></div>
      <label v-if="n.kind==='deploy'">应用<Sel v-model="n.service" :disabled="disabled" :options="apps" all-label="范围内全部应用（每个应用仅发布一次）" filterable width="100%" /></label>
      <label v-else>操作 / 确认说明<ElInput v-model="n.instructions" :disabled="disabled" type="textarea" :rows="2" /></label>
      <label v-if="['mysql','nacos','redis'].includes(n.kind)">关联变更<Sel v-model="n.change_ids" multiple filterable :disabled="disabled" :options="changes.filter(c=>c.kind===n.kind).map(c=>({value:c.id,label:'#'+c.id+' '+c.title}))" all-label="所选版本内该类型全部变更" width="100%" /></label>
      <p v-if="n.kind==='data-sync'" role="alert">独立数据同步节点尚不可执行。请在数据同步任务中对比、核对并冻结为 SQL 变更，再使用 SQL 节点。</p>
      <div class="node-tools"><ElButton size="small" :disabled="disabled||i===0" @click="move(nodes,i,-1)">上移</ElButton><ElButton size="small" :disabled="disabled||i===nodes.length-1" @click="move(nodes,i,1)">下移</ElButton><ElButton size="small" :disabled="disabled" @click="nodes.splice(i,1)">删除</ElButton></div>
    </details>
    <ElButton :disabled="disabled" @click="add(nodes)">＋ 添加节点</ElButton>
  </fieldset>
</template>
<style scoped>
.node-editor{border:0;padding:0;margin:0;min-width:0}.node-edit{border:1px solid var(--border);border-radius:8px;padding:12px;margin-bottom:10px}.node-edit summary{cursor:pointer;font-weight:600}.node-edit summary span{font-weight:400;color:var(--muted)}.node-fields{display:grid;grid-template-columns:1fr 1fr;gap:16px}.node-edit label{display:grid;gap:6px;margin-top:12px}.node-tools{display:flex;gap:8px;margin-top:12px}@media(max-width:700px){.node-fields{grid-template-columns:1fr}}
</style>
