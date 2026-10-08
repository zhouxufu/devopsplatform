<script setup>
defineProps({plans:Array})
</script>
<template>
  <section v-for="plan in plans||[]" :key="plan.node_id" class="sql-plan">
    <h4>SQL 节点 {{plan.node_id}} · {{plan.connection}} / {{plan.database}}</h4>
    <p>表结构基线：<code>{{plan.schema_digest}}</code></p>
    <p v-for="warning in plan.warnings" :key="warning">{{warning}}</p>
    <article v-for="(op,i) in plan.statements" :key="i"><b>#{{op.change_id}} · {{op.skip?'结构一致跳过':'执行'}} · {{op.reason||op.action}}<span v-if="plan.mode==='dml'"> · 预检 {{op.expected_rows}} 行 / 上限 {{op.max_affected_rows}} 行</span></b><pre>{{op.sql}}</pre></article>
  </section>
</template>
<style scoped>
.sql-plan{margin:16px 0;padding:14px;border:1px solid var(--border);border-radius:8px;min-width:0}.sql-plan p{color:var(--muted);overflow-wrap:anywhere}.sql-plan pre{white-space:pre-wrap;overflow-wrap:anywhere;font-size:13px}.sql-plan article{border-top:1px solid var(--border);padding:10px 0}
</style>
