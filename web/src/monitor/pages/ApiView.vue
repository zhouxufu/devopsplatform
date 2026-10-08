<script setup>
import { ref, inject, watch } from 'vue'
import Sel from '../../shared/components/Sel.vue'
import Chart from '../../shared/components/Chart.vue'

const curSvc = inject('curSvc')
const embedded = inject('embedded', false)
const curEnvironment = inject('curEnvironment', ref(''))
const contextRange = inject('contextRange', ref([Date.now() - 3600_000, Date.now()]))
const otlpSvcs = ref([]), ifaceSvcs = ref(''), instances = ref([]), instance = ref('')
const names = ref([]), metric = ref(''), ifaces = ref([]), iface = ref('')
const meth = ref(''), status = ref('')
const pts = ref([]), avail = ref([])

async function loadSvcs() {
  otlpSvcs.value = (await fetch('api/otlp/services').then(r => r.json())) ?? []
  ifaceSvcs.value = curSvc.value && otlpSvcs.value.includes(curSvc.value) ? curSvc.value : embedded ? '' : otlpSvcs.value[0] || ''
  if (ifaceSvcs.value) await loadInstances()
}
async function loadInstances() {
  instances.value = (await fetch('api/instances?' + new URLSearchParams({ service: ifaceSvcs.value, environment: curEnvironment.value || '' })).then(r => r.json()).catch(() => [])) ?? []
  if (instance.value && !instances.value.includes(instance.value)) instance.value = ''
  await loadNames()
}
async function loadNames() {
  names.value = (await fetch('api/otlp/names?' + new URLSearchParams({ service: ifaceSvcs.value, environment: curEnvironment.value || '', instance: instance.value })).then(r => r.json())) ?? []
  const hist = names.value.filter(n => /duration|latency|time$|code|result|outcome/.test(n))
  metric.value = hist[0] || names.value[0] || ''
  if (metric.value) await loadIfaces()
}
async function loadIfaces() {
  ifaces.value = (await fetch('api/otlp/interfaces?' + new URLSearchParams({ service: ifaceSvcs.value, environment: curEnvironment.value || '', instance: instance.value, name: metric.value })).then(r => r.json())) ?? []
  iface.value = ifaces.value.length ? ifaces.value[0].iface : ''
  meth.value = ''; status.value = ''
  await loadQ()
}
async function loadQ() {
  const [start, end] = contextRange.value
  const p = new URLSearchParams({ service: ifaceSvcs.value, name: metric.value, iface: iface.value, meth: meth.value, status: status.value, start, end })
  if (curEnvironment.value) p.set('environment', curEnvironment.value)
  if (instance.value) p.set('instance', instance.value)
  ;[pts.value, avail.value] = await Promise.all([
    fetch('api/otlp/quantiles?' + p).then(r => r.json()).catch(() => []),
    fetch('api/otlp/availability?' + p).then(r => r.json()).catch(() => []),
  ])
}
watch(curSvc, () => { loadSvcs() }, { immediate: true })
watch(curEnvironment, loadSvcs)
watch(contextRange, loadQ)

const isSec = computed(() => /duration|latency/.test(metric.value))
const ms = v => v * (isSec.value ? 1000 : 1)
const last = computed(() => pts.value[pts.value.length - 1])
const availTotal = computed(() => {
  if (!avail.value.length) return null
  const t = avail.value.reduce((s, p) => s + p.total, 0), o = avail.value.reduce((s, p) => s + p.ok, 0)
  return { rate: t ? o / t * 100 : 100, ok: o, total: t }
})
import { computed } from 'vue'
const attrs = ref([])
watch(iface, async () => {
  meth.value = ''; status.value = ''
  attrs.value = (await fetch('api/otlp/iface-attrs?' + new URLSearchParams({ service: ifaceSvcs.value, environment: curEnvironment.value || '', instance: instance.value, name: metric.value, iface: iface.value })).then(r => r.json())) ?? []
  await loadQ()
})
</script>
<template>
  <div style="padding:16px">
    <div v-if="embedded && !curSvc" class="empty">请先在顶部“全局上下文”选择服务</div>
    <template v-else>
    <div style="display:flex;gap:8px;align-items:center;flex-wrap:wrap;margin-bottom:12px">
      <h3 style="font-size:14px">接口分位</h3>
      <Sel v-model="ifaceSvcs" :options="otlpSvcs" width="150px" @change="loadInstances" />
      <Sel v-model="instance" :options="instances" all-label="全部实例" width="160px" @change="loadNames" />
      <Sel v-model="metric" :options="names" width="190px" @change="loadIfaces" />
      <Sel v-model="iface" :options="ifaces.map(i => ({ value: i.iface, label: (i.iface || '（无接口维度）') + ' · ' + Math.round(i.cnt) + '/h' }))" width="260px" />
      <Sel v-model="meth" :options="[...new Set(attrs.map(a => a.meth).filter(Boolean))]" all-label="全部方法" width="120px" @change="loadQ" />
      <Sel v-model="status" :options="[...new Set(attrs.map(a => a.status).filter(Boolean))]" all-label="全部状态" width="110px" @change="loadQ" />
    </div>
    <div v-if="last" style="display:grid;grid-template-columns:repeat(5,1fr);gap:12px;margin-bottom:12px">
      <div class="card"><div class="t">请求量</div><div class="v">{{ Math.round(last.cnt) }}<small style="font-size:12px;color:var(--dim)"> /分</small></div></div>
      <div class="card"><div class="t">平均耗时</div><div class="v">{{ ms(last.avg).toFixed(1) }}<small style="font-size:12px;color:var(--dim)"> ms</small></div><div class="s">{{ isSec ? '源单位:秒' : '源单位:毫秒' }}</div></div>
      <div class="card"><div class="t">TP95</div><div class="v" style="color:var(--warn)">{{ ms(last.p95).toFixed(1) }}<small style="font-size:12px;color:var(--dim)"> ms</small></div></div>
      <div class="card"><div class="t">TP99</div><div class="v" style="color:var(--error)">{{ ms(last.p99).toFixed(1) }}<small style="font-size:12px;color:var(--dim)"> ms</small></div></div>
      <div class="card"><div class="t">可用率</div>
        <div v-if="availTotal" class="v" :style="{ color: availTotal.rate >= 99.5 ? 'var(--ok)' : availTotal.rate >= 99 ? 'var(--warn)' : 'var(--error)' }">{{ availTotal.rate.toFixed(2) }}<small style="font-size:12px;color:var(--dim)"> %</small></div>
        <div v-else class="v">—</div><div class="s">{{ availTotal ? Math.round(availTotal.ok) + '/' + Math.round(availTotal.total) : '无状态维度' }}</div>
      </div>
    </div>
    <div v-if="pts.length" class="card"><h4>耗时分位（ms）<span style="margin-left:12px;font-size:10px;color:var(--dim)">源单位:{{ isSec ? '秒' : '毫秒' }}</span></h4>
      <Chart :times="pts.map(p => p.minute)" :series="[
        { name: 'P50', color: 'var(--info)', vals: pts.map(p => ms(p.p50)) },
        { name: 'P95', color: 'var(--warn)', vals: pts.map(p => ms(p.p95)) },
        { name: 'P99', color: 'var(--error)', vals: pts.map(p => ms(p.p99)) },
        { name: 'max', color: '#8b949e', vals: pts.map(p => ms(p.max)) },
      ]" unit=" ms" :height="210" />
    </div>
    <div v-else class="empty">暂无直方图数据</div>
    </template>
  </div>
</template>
<style scoped>
.card .t{color:var(--muted);font-size:12px}
.card .v{font-size:20px;font-weight:600;font-family:var(--mono);margin-top:2px}
.card .s{color:var(--dim);font-size:11px}
</style>
