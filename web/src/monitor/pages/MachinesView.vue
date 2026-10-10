<script setup>
import PageHeader from '../../shared/components/PageHeader.vue'
import { ref, onMounted, onUnmounted, inject, watch } from 'vue'
import Sel from '../../shared/components/Sel.vue'
import Chart from '../../shared/components/Chart.vue'

const machines = ref([]), series = ref([]), agent = ref('')
const contextRange = inject('contextRange', ref([Date.now() - 3600_000, Date.now()]))
const curEnvironment = inject('curEnvironment', ref(''))
let timer = null
const load = async () => {
  machines.value = (await fetch('api/machines?' + new URLSearchParams({ environment: curEnvironment.value || '' })).then(r => r.json())) ?? []
  if (agent.value && !machines.value.some(m => m.agent_id === agent.value)) agent.value = ''
  if (!agent.value && machines.value.length) agent.value = machines.value[0].agent_id
  if (agent.value) {
    const [start, end] = contextRange.value
    const p = new URLSearchParams({ agent: agent.value, environment: curEnvironment.value || '', start, end })
    const d = (await fetch('api/machine-series?' + p).then(r => r.json())) ?? []
    series.value = d
  }
}
onMounted(() => { load(); timer = setInterval(load, 30000) })
onUnmounted(() => clearInterval(timer))
watch(contextRange, load)
watch(curEnvironment, load)
</script>
<template>
  <div style="padding:16px">
    <PageHeader title="机器指标" help="上方为实时快照；Agent 每 15 秒采样，页面每 30 秒刷新。"><template #navigation>
      <Sel v-model="agent" :options="machines.map(m => ({ value: m.agent_id, label: m.agent_id + (curEnvironment ? '' : ' · ' + (m.environment || '未标记')) }))" width="180px" @change="load" />
    </template></PageHeader>
    <div v-if="machines.length" class="machine-grid">
      <div class="card"><div class="t">CPU</div><div class="v" :style="{ color: (machines.find(m => m.agent_id === agent) || {}).cpu > 80 ? 'var(--error)' : 'var(--ok)' }">{{ (machines.find(m => m.agent_id === agent) || {}).cpu }}%</div><div class="s">{{ agent }}</div></div>
      <div class="card"><div class="t">内存</div><div class="v">{{ (machines.find(m => m.agent_id === agent) || {}).mem_used }}<small style="font-size:12px;color:var(--dim)"> / {{ (machines.find(m => m.agent_id === agent) || {}).mem_total }}G</small></div></div>
      <div class="card"><div class="t">磁盘 /</div><div class="v">{{ (machines.find(m => m.agent_id === agent) || {}).disk_used }}<small style="font-size:12px;color:var(--dim)"> / {{ (machines.find(m => m.agent_id === agent) || {}).disk_total }}G</small></div></div>
      <div class="card"><div class="t">网络 ↓↑</div><div class="v" style="font-size:18px">{{ (machines.find(m => m.agent_id === agent) || {}).net_rx_kbs }} / {{ (machines.find(m => m.agent_id === agent) || {}).net_tx_kbs }}<small style="font-size:12px;color:var(--dim)"> KB/s</small></div></div>
    </div>
    <div v-else class="empty">暂无机器数据（agent 配置 monitor_url 后出现）</div>
    <div v-if="series.length" class="card"><h4>CPU %</h4><Chart :times="series.map(p => p.minute)" :series="[{ name: 'CPU', color: 'var(--brand)', vals: series.map(p => p.cpu) }]" :y-max="100" unit="%" /></div>
    <div v-if="series.length" class="card"><h4>内存 GB</h4><Chart :times="series.map(p => p.minute)" :series="[{ name: '内存', color: 'var(--warn)', vals: series.map(p => p.mem_used) }]" unit=" G" /></div>
    <div v-if="machines.length && !series.length" class="empty">所选全局时间区间内没有趋势数据；可在顶部切换“最近15分钟”。实时快照不受时间区间影响。</div>
  </div>
</template>
<style scoped>
.card .t{color:var(--muted);font-size:12px}
.card .v{font-size:22px;font-weight:600;font-family:var(--mono);margin-top:2px}
.card .s{color:var(--dim);font-size:11px}
.machine-grid{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:12px;margin-bottom:12px}
@media(max-width:760px){.machine-grid{grid-template-columns:repeat(2,minmax(0,1fr))}}
@media(max-width:420px){.machine-grid{grid-template-columns:1fr}}
</style>
