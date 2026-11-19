<script setup>

const props=defineProps({plans:{type:Array,default:()=>[]},executions:{type:Array,default:()=>[]},canRestore:Boolean})
defineEmits(['restore'])
const execution=id=>props.executions.find(e=>e.change_id===id);
const status=s=>({success:'发布成功并回读校验',uncertain:'结果不确定，必须核对远端',running:'执行权已保留，完成前或中断后需核对'}[s]||s);



import {Button} from '../../shared/shadcn-ui.js'
</script>
<template><section v-if="plans?.length" class="nacos-preview"><h4>Nacos 配置差异</h4><p>仅展示所选配置项；完整文档快照加密保存。执行前检查基线，CAS 发布后再次核对摘要。多文档不是一个事务。</p><template v-for="plan in plans" :key="plan.node_id"><article v-for="config in plan.configs" :key="config.change_id"><b>#{{config.change_id}} · {{config.service}} / {{config.alias}} · {{config.data_id}}</b><p v-if="config.skip">已成功执行且当前配置项一致，自动跳过 · 原执行单 #{{config.prior_run_id}}</p><p>{{config.connection}} · {{config.namespace||'public'}} · {{config.group}} · {{config.format}}</p><code>{{config.before_md5}} → {{config.after_md5}}</code><p v-if="config.restore">恢复操作：恢复该次发布前的完整文档快照（含格式和注释）；若当前内容已变化，会阻止执行。</p><div v-for="diff in config.diff" :key="diff.key" class="nacos-diff"><b>{{diff.key}}</b><pre>{{diff.before===null?'（不存在）':diff.before}}</pre><span>→</span><pre>{{diff.after===null?'（删除）':diff.after}}</pre></div><p v-if="!config.diff.length">所选配置项已一致，本次无需发布。</p><p v-if="execution(config.change_id)">{{status(execution(config.change_id).status)}} · {{execution(config.change_id).detail}}</p><Button v-if="canRestore&&execution(config.change_id)?.status==='success'" @click="$emit('restore',config.change_id)" variant="outline" size="default" type="button">生成恢复草稿（不立即执行）</Button></article></template></section></template>
<style scoped>.nacos-preview{min-width:0}.nacos-preview p{color:var(--muted-foreground);font-size:.75rem;overflow-wrap:anywhere}.nacos-preview article{border:1px solid var(--border);border-radius:var(--radius);padding:12px;margin:10px 0;min-width:0}.nacos-preview b,.nacos-preview code{overflow-wrap:anywhere}.nacos-preview code{font-size:.75rem}.nacos-diff{display:grid;grid-template-columns:minmax(100px,1fr) minmax(0,2fr) auto minmax(0,2fr);gap:8px;align-items:start;margin-top:10px}.nacos-diff pre{white-space:pre-wrap;overflow-wrap:anywhere;max-height:160px;overflow:auto;margin:0;padding:8px;background:var(--panel2);font-size:.75rem}@media(max-width:700px){.nacos-diff{grid-template-columns:1fr}}</style>
