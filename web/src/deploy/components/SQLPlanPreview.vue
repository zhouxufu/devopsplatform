<script setup>
import SQLCode from '../../shared/components/SQLCode.vue'
defineProps({plans:Array,selectionsOnly:Boolean,baselines:{type:Array,default:()=>[]}})
</script>
<template>
  <section v-for="plan in plans||[]" :key="plan.node_id" class="sql-plan">
    <h4 v-if="!selectionsOnly">SQL 节点 {{plan.node_id}} · {{plan.connection}} / {{plan.database}}</h4>
    <article v-for="selection in plan.script_selections||[]" :key="'selection-'+selection.change_id" class="sql-script-selection"><b>#{{selection.change_id}} · {{selection.environment}} · {{selection.skip?'该环境无需执行':'使用环境专用 SQL'}}</b><p>原因：{{selection.reason}}</p><template v-if="!selection.skip"><div class="sql-script-diff"><div><strong>默认 SQL</strong><SQLCode :content="selection.default_content" label="默认 SQL"/></div><div><strong>本环境实际执行 SQL</strong><SQLCode :content="selection.content" label="本环境实际执行 SQL"/></div></div><p>此环境脚本单独预检、审批；其他环境的执行结果不能代替本脚本的验证。</p></template></article>
    <template v-if="!selectionsOnly">
    <p v-for="item in plan.applied||[]" :key="item.change_id">#{{item.change_id}} · 已成功执行，自动跳过 · 原执行单 #{{item.run_id}}（不重复执行 SQL）</p>
    <p v-if="plan.previous_nodes?.length">前序同库阶段：{{plan.previous_nodes.join(' → ')}}；全部成功后执行本阶段。</p><p v-for="baseline in baselines.filter(b=>b.node_id===plan.node_id)" :key="baseline.plan_digest">阶段数据已核验：{{baseline.scopes.map(s=>'第 '+(s.statement+1)+' 条影响 '+s.rows+' 行').join('；')||'按冻结 INSERT 执行'}}。回执与原审批快照分别保留。</p><p v-if="plan.schema_digest">{{plan.previous_nodes?.length?'前序执行后的预期结构':'表结构基线'}}：<code>{{plan.schema_digest}}</code></p>
    <p v-for="warning in plan.warnings" :key="warning">{{warning}}</p>
    <article v-for="(op,i) in plan.statements" :key="i"><b>#{{op.change_id}} · {{op.skip?'结构一致跳过':'执行'}} · {{op.reason||op.action}}<span v-if="plan.mode==='dml'"> · {{op.expected_rows<0?'阶段开始时核验':'预检 '+op.expected_rows+' 行'}} / 上限 {{op.max_affected_rows}} 行</span></b><SQLCode :content="op.sql" label="计划 SQL"/></article>
    </template>
  </section>
</template>
<style scoped>
.sql-plan{margin:16px 0;padding:14px;border:1px solid var(--border);border-radius:8px;min-width:0}.sql-plan p{color:var(--muted-foreground);overflow-wrap:anywhere}.sql-plan article{border-top:1px solid var(--border);padding:10px 0}
.sql-script-diff{display:grid;grid-template-columns:minmax(0,1fr) minmax(0,1fr);gap:12px}.sql-script-diff>div{min-width:0}.sql-script-selection>b{color:var(--brand)}@media(max-width:800px){.sql-script-diff{grid-template-columns:1fr}}
</style>
