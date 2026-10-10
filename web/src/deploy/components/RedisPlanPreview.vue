<script setup>
const props=defineProps({plans:{type:Array,default:()=>[]},executions:{type:Array,default:()=>[]},requiresReview:{type:Boolean,default:true}});
const execution=(node,id,i)=>props.executions.find(e=>e.node_id===node&&e.change_id===id&&e.command_index===i);
const labels={success:'已执行',uncertain:'结果不确定，必须核对',executing:'执行中或中断待核对',reserved:'尚未开始（已保留执行权）'};
</script>
<template>
  <section v-if="plans?.length" class="redis-preview">
    <h4>Redis 执行计划（{{requiresReview?'需审核':'仍须确认执行'}}）</h4>
    <p>预览仅测试连接，不执行以下命令，不读取缓存值。目标与命令冻结，Key 的实时内容不冻结。非事务、无自动恢复；错误或超时停止，不自动重试。</p>
    <template v-for="plan in plans" :key="plan.node_id">
      <article v-for="item in plan.changes" :key="item.change_id">
        <b>#{{item.change_id}} · {{item.service}} / {{item.alias}}</b>
        <p>{{item.connection}} · {{item.endpoint}} · DB {{item.db}} · 前缀 {{item.prefix}}</p>
        <div v-for="(args,i) in item.commands" :key="i" class="redis-command">
          <pre>{{args.map(a=>/\s/.test(a)||!a?JSON.stringify(a):a).join(' ')}}</pre>
          <span v-if="execution(plan.node_id,item.change_id,i)">{{labels[execution(plan.node_id,item.change_id,i).status]||execution(plan.node_id,item.change_id,i).status}} · {{execution(plan.node_id,item.change_id,i).result}}</span>
        </div>
      </article>
    </template>
  </section>
</template>
<style scoped>.redis-preview{min-width:0}.redis-preview article{border:1px solid var(--border);border-radius:8px;padding:12px;margin:10px 0;min-width:0}.redis-preview p,.redis-preview span{color:var(--muted);font-size:12px;overflow-wrap:anywhere}.redis-preview b{overflow-wrap:anywhere}.redis-command{margin-top:8px}.redis-command pre{white-space:pre-wrap;overflow-wrap:anywhere;background:var(--panel2);padding:8px;margin:0;font-size:12px}</style>
