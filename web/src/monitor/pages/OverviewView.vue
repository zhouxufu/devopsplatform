<script setup>
import { ref, inject, watch, onMounted, computed } from 'vue'
import { useRouter } from 'vue-router'
import Chart from '../../shared/components/Chart.vue'

const router = useRouter()
const curSvc = inject('curSvc')
const curEnvironment = inject('curEnvironment', ref(''))
const contextRange = inject('contextRange', ref([Date.now() - 3600_000, Date.now()]))
const services = ref([]), pts = ref([]), alerts = ref([]), alertSt = ref({})
const machines = ref([]), otlpNames = ref([])
const otlpLatest = ref({})

const firing = computed(() => alerts.value.filter(a => a.enabled && (!curEnvironment.value || a.environment === curEnvironment.value || a.environment === '*') && alertSt.value[a.id]?.firing).length)

function openLogs() {
  if (window.parent !== window) {
    window.parent.postMessage({ type: 'workspace.open', plugin: 'logs', hash: '#/search' }, location.origin)
  } else {
    window.open(location.origin + '/api/plugins/logs/ui#/search', '_blank')
  }
}

async function load() {
  if (!curSvc.value) return
  const [start, end] = contextRange.value
  const p = new URLSearchParams({ service: curSvc.value, start, end })
  if (curEnvironment.value) p.set('environment', curEnvironment.value)
  const [metric, alertsL, ast, mach, names] = await Promise.all([
    fetch('api/metrics?' + p).then(r => r.json()).catch(() => ({})),
    fetch('api/alerts?' + new URLSearchParams({ environment: curEnvironment.value || '' })).then(r => r.json()).catch(() => []),
    fetch('api/alerts/status?' + new URLSearchParams({ environment: curEnvironment.value || '' })).then(r => r.json()).catch(() => ({})),
    fetch('api/machines?' + new URLSearchParams({ environment: curEnvironment.value || '' })).then(r => r.json()).catch(() => []),
    fetch('api/otlp/names?' + new URLSearchParams({ service: curSvc.value, environment: curEnvironment.value || '' })).then(r => r.json()).catch(() => []),
  ])
  pts.value = metric.points || []
  alerts.value = alertsL || []
  alertSt.value = ast
  machines.value = mach || []
  otlpNames.value = names || []
  // OTLP 关键值
  const pick = (...c) => c.find(n => otlpNames.value.includes(n))
  const latest = async n => {
    if (!n) return null
    const p = new URLSearchParams({ service: curSvc.value, name: n, start, end })
    if (curEnvironment.value) p.set('environment', curEnvironment.value)
    const d = await fetch('api/otlp/series?' + p).then(r => r.json()).catch(() => [])
    return d?.length ? d[d.length - 1] : null
  }
  const cpuN = pick('process_cpu_utilization', 'jvm.cpu.recent_utilization')
  const heapN = pick('jvm_memory_used_bytes_heap', 'jvm.memory.used')
  const qpsN = pick('http_server_request_duration_seconds', 'http.server.request.duration', 'http.client.request.duration')
  const [cpu, heap, qps] = await Promise.all([latest(cpuN), latest(heapN), latest(qpsN)])
  otlpLatest.value = { cpu, heap, qps }
}
onMounted(async () => {
  const list = (await fetch('api/services').then(r => r.json())) ?? []
  services.value = list
  if (window.parent === window && !curSvc.value) curSvc.value = list[0] || ''
})
watch(curSvc, load, { immediate: true })
watch(curEnvironment, load)
watch(contextRange, load)

const tot = lv => pts.value.reduce((s, p) => s + (p.counts[lv] || 0), 0)
const series = computed(() => {
  const order = ['OTHER', 'INFO', 'WARN', 'ERROR']
  const colors = { OTHER: '#8b949e', INFO: 'var(--info)', WARN: 'var(--warn)', ERROR: 'var(--error)' }
  return order.map(lv => ({ name: lv, color: colors[lv], vals: pts.value.map(p => p.counts[lv] || 0) }))
})
const me = computed(() => machines.value[0])
</script>
<template>
  <div style="padding:14px 16px">
    <div v-if="!curSvc" class="empty">暂无服务数据</div>
    <template v-else>
      <div class="metric-grid">
        <div class="card" style="cursor:pointer" @click="openLogs"><div class="t">ERROR ↗</div><div class="v" style="color:var(--error)">{{ tot('ERROR') }}</div><div class="s">查看区间错误日志</div></div>
        <div class="card" style="cursor:pointer" @click="router.push('/alerts')"><div class="t">报警中 ↗</div><div class="v" :style="{ color: firing ? 'var(--error)' : 'var(--ok)' }">{{ firing }}</div><div class="s">{{ firing ? '点击处理' : '规则正常' }}</div></div>
        <div class="card" style="cursor:pointer" @click="router.push('/jvm')"><div class="t">进程 CPU ↗</div><div class="v">{{ otlpLatest.cpu ? (otlpLatest.cpu.val * 100).toFixed(1) + '%' : '—' }}</div><div class="s">OTLP</div></div>
        <div class="card" style="cursor:pointer" @click="router.push('/jvm')"><div class="t">堆/内存 ↗</div><div class="v">{{ otlpLatest.heap ? (otlpLatest.heap.val / 1e9).toFixed(2) + 'G' : '—' }}</div><div class="s">OTLP</div></div>
      </div>
      <div class="card">
        <h4>每分钟日志量 <code style="font-size:10px;color:var(--brand)">{{ curEnvironment || '全部环境（聚合）' }}</code>
          <span style="margin-left:12px;font-size:10px">■ERROR ■WARN ■INFO ■其他</span>
          <span style="float:right;color:var(--brand);cursor:pointer;font-size:11px" @click="router.push('/api')">接口分位 →</span>
        </h4>
        <Chart v-if="pts.length" :times="pts.map(p => p.minute)" :series="series" stacked unit=" 条" />
        <div v-else class="empty">区间内无数据</div>
      </div>
      <div class="summary-grid">
        <div class="card"><h4>机器 <span style="float:right;color:var(--brand);cursor:pointer;font-size:11px" @click="router.push('/machines')">详情 →</span></h4>
          <template v-if="me">
            <div style="display:grid;grid-template-columns:repeat(3,1fr);gap:8px">
              <div><div class="s">CPU</div><div class="v2" :style="{ color: me.cpu > 80 ? 'var(--error)' : 'var(--ok)' }">{{ me.cpu }}%</div></div>
              <div><div class="s">内存</div><div class="v2">{{ Math.round(me.mem_used / me.mem_total * 100) }}%</div></div>
              <div><div class="s">磁盘</div><div class="v2">{{ Math.round(me.disk_used / me.disk_total * 100) }}%</div></div>
            </div>
          </template><div v-else class="empty">暂无机器数据</div>
        </div>
        <div class="card"><h4>请求量（OTLP）</h4>
          <div v-if="otlpLatest.qps" class="v2" style="font-size:18px">{{ Math.round(otlpLatest.qps.cnt) }} <span class="s">次/分钟（最近）</span></div>
          <div v-else class="empty">暂无 OTLP 数据（接入 javaagent / Micrometer 后出现）</div>
        </div>
      </div>
    </template>
  </div>
</template>
<style scoped>
.card .t{color:var(--muted);font-size:12px}
.card .v{font-size:22px;font-weight:600;font-family:var(--mono);margin-top:2px}
.card .s{color:var(--dim);font-size:11px}
.v2{font-size:18px;font-family:var(--mono);font-weight:600}
.metric-grid{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:12px;margin-bottom:12px}.summary-grid{display:grid;grid-template-columns:1fr 1fr;gap:12px}
@media(max-width:760px){.metric-grid{grid-template-columns:repeat(2,minmax(0,1fr))}.summary-grid{grid-template-columns:1fr}}
@media(max-width:420px){.metric-grid{grid-template-columns:1fr}}
</style>
