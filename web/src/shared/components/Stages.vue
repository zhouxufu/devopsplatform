<script setup>
// 流水线 stage 条：状态/耗时一目了然
const props = defineProps({ stages: { type: Array, default: () => [] } })
const ICON = { success:'✓', failed:'✗', running:'●', waiting:'⏸', pending:'○', skipped:'—', warning:'!' }
const COLOR = { success:'var(--ok)', failed:'var(--error)', running:'var(--warn)', waiting:'var(--warn)', pending:'var(--dim)', skipped:'var(--dim)', warning:'var(--warn)' }
</script>
<template>
  <div class="bar">
    <template v-for="(st, i) in stages" :key="i">
      <span class="stage" :style="{ color: COLOR[st.status] || 'var(--dim)' }"
            :title="`${st.name} ${st.status}${st.dur_ms ? ' ' + (st.dur_ms/1000).toFixed(0) + 's' : ''}`">
        {{ ICON[st.status] || '○' }} {{ st.name }}
        <em v-if="st.dur_ms">{{ (st.dur_ms / 1000).toFixed(0) }}s</em>
      </span>
      <span v-if="i < stages.length - 1" class="arrow">→</span>
    </template>
  </div>
</template>
<style scoped>
.bar{display:flex;gap:6px;align-items:center;flex-wrap:wrap}
.stage{padding:3px 10px;border-radius:6px;background:var(--panel2);border:1px solid var(--border);font-size:.75rem}
.stage em{color:var(--dim);font-style:normal}
.arrow{color:var(--dim)}
</style>
