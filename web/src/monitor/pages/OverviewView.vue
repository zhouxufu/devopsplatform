<script setup>
import PageHeader from "../../shared/components/PageHeader.vue"
import {Card,CardContent} from "../../shared/shadcn-ui.js"


import {ref, inject, watch, onMounted, computed} from 'vue'
import {useRouter} from 'vue-router'
import Chart from '../../shared/components/Chart.vue'
import StatePanel from '../../shared/components/StatePanel.vue'

const router = useRouter()
const curSvc = inject('curSvc')
const curEnvironment = inject('curEnvironment', ref(''))
const contextRange = inject('contextRange', ref([Date.now() - 3600_000, Date.now()]))
const services = ref([]), pts = ref([]), alerts = ref([]), alertSt = ref({})
const machines = ref([]), otlpNames = ref([])
const otlpLatest = ref({})
const loadError=ref(''),loading=ref(false)
let requestSequence=0
async function readJSON(url){const response=await fetch(url);if(!response.ok)throw Error('监控数据读取失败（'+response.status+'）');return response.json()}

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
  const sequence=++requestSequence;loading.value=true;loadError.value=''
  try{
  const [start, end] = contextRange.value
  const p = new URLSearchParams({ service: curSvc.value, start, end })
  if (curEnvironment.value) p.set('environment', curEnvironment.value)
  const [metric, alertsL, ast, mach, names] = await Promise.all([
    readJSON('api/metrics?' + p),
    readJSON('api/alerts?' + new URLSearchParams({ environment: curEnvironment.value || '' })),
    readJSON('api/alerts/status?' + new URLSearchParams({ environment: curEnvironment.value || '' })),
    readJSON('api/machines?' + new URLSearchParams({ environment: curEnvironment.value || '' })),
    readJSON('api/otlp/names?' + new URLSearchParams({ service: curSvc.value, environment: curEnvironment.value || '' })),
  ])
  if(sequence!==requestSequence)return
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
    const d = await readJSON('api/otlp/series?' + p)
    return d?.length ? d[d.length - 1] : null
  }
  const cpuN = pick('process_cpu_utilization', 'jvm.cpu.recent_utilization')
  const heapN = pick('jvm_memory_used_bytes_heap', 'jvm.memory.used')
  const qpsN = pick('http_server_request_duration_seconds', 'http.server.request.duration', 'http.client.request.duration')
  const [cpu, heap, qps] = await Promise.all([latest(cpuN), latest(heapN), latest(qpsN)])
  if(sequence===requestSequence)otlpLatest.value = { cpu, heap, qps }
  }catch(e){if(sequence===requestSequence){loadError.value=e.message;pts.value=[];otlpLatest.value={};machines.value=[];alerts.value=[];alertSt.value={}}}finally{if(sequence===requestSequence)loading.value=false}
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



import {Button} from '../../shared/shadcn-ui.js'
</script>
<template><div class="console-content"><PageHeader title="监控概览"/><StatePanel v-if="!curSvc" title="请选择要查看的应用" message="选择应用和环境后展示对应日志、机器与运行指标。"/><template v-else><StatePanel v-if="loadError" :error="true" title="监控数据读取失败" :message="loadError"><Button @click="load" variant="outline" size="default" type="button">重试读取</Button></StatePanel><div class="metric-grid"><Card style="cursor:pointer" @click="openLogs" class="card gap-0 py-0 mb-4"><CardContent class="p-4"><div class="t">ERROR ↗</div><div style="color:var(--error)" class="v">{{ loading||loadError||!pts.length?'—':tot('ERROR') }}</div><div class="s">查看区间错误日志</div></CardContent></Card><Card style="cursor:pointer" @click="router.push('/alerts')" class="card gap-0 py-0 mb-4"><CardContent class="p-4"><div class="t">报警中 ↗</div><div :style="{ color: firing ? 'var(--error)' : 'var(--ok)' }" class="v">{{loading||loadError?'—':firing}}</div><div class="s">{{loading?'正在读取':loadError?'数据读取失败':firing?'点击处理':!pts.length&&!otlpNames.length?'暂无采集数据，待核对':!alerts.length?'未配置报警规则':'暂无触发记录'}}</div></CardContent></Card><Card style="cursor:pointer" @click="router.push('/jvm')" class="card gap-0 py-0 mb-4"><CardContent class="p-4"><div class="t">进程 CPU ↗</div><div class="v">{{ otlpLatest.cpu ? (otlpLatest.cpu.val * 100).toFixed(1) + '%' : '—' }}</div><div class="s">OTLP</div></CardContent></Card><Card style="cursor:pointer" @click="router.push('/jvm')" class="card gap-0 py-0 mb-4"><CardContent class="p-4"><div class="t">堆/内存 ↗</div><div class="v">{{ otlpLatest.heap ? (otlpLatest.heap.val / 1e9).toFixed(2) + 'G' : '—' }}</div><div class="s">OTLP</div></CardContent></Card></div><Card class="card gap-0 py-0 mb-4"><CardContent class="p-4"><h4>每分钟日志量 <code style="font-size:.75rem;color:var(--brand)">{{ curEnvironment || '全部环境（聚合）' }}</code><span style="margin-left:12px;font-size:.75rem">■ERROR ■WARN ■INFO ■其他</span><span style="float:right;color:var(--brand);cursor:pointer;font-size:.75rem" @click="router.push('/api')">接口分位 →</span></h4><Chart v-if="pts.length" :times="pts.map(p => p.minute)" :series="series" stacked unit=" 条"/><StatePanel v-else :loading="loading" :title="loading?'正在读取监控数据…':'当前时间范围无日志指标'" message="无数据不代表运行正常。请核对应用、环境与采集接入，或调整时间范围。"><Button v-if="!loading" @click="openLogs" variant="outline" size="default" type="button">查看日志与采集状态</Button></StatePanel></CardContent></Card><div class="summary-grid"><Card class="card gap-0 py-0 mb-4"><CardContent class="p-4"><h4>机器 <span style="float:right;color:var(--brand);cursor:pointer;font-size:.75rem" @click="router.push('/machines')">详情 →</span></h4><template v-if="me"><div style="display:grid;grid-template-columns:repeat(3,1fr);gap:8px"><div><div class="s">CPU</div><div :style="{ color: me.cpu > 80 ? 'var(--error)' : 'var(--ok)' }" class="v2">{{ me.cpu }}%</div></div><div><div class="s">内存</div><div class="v2">{{ Math.round(me.mem_used / me.mem_total * 100) }}%</div></div><div><div class="s">磁盘</div><div class="v2">{{ Math.round(me.disk_used / me.disk_total * 100) }}%</div></div></div></template><div v-else class="empty">暂无机器数据</div></CardContent></Card><Card class="card gap-0 py-0 mb-4"><CardContent class="p-4"><h4>请求量（OTLP）</h4><div v-if="otlpLatest.qps" style="font-size:1.125rem" class="v2">{{ Math.round(otlpLatest.qps.cnt) }} <span class="s">次/分钟（最近）</span></div><div v-else class="empty">暂无 OTLP 数据（接入 javaagent / Micrometer 后出现）</div></CardContent></Card></div></template></div></template>
<style scoped>.metric-grid>.card,.summary-grid>.card{margin-bottom:0}.card .t{color:var(--muted-foreground);font-size:.75rem}
.card .v{font-size:1.25rem;font-weight:600;font-family:var(--mono);margin-top:2px}
.card .s{color:var(--dim);font-size:.75rem}
.v2{font-size:1.125rem;font-family:var(--mono);font-weight:600}
.metric-grid{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:16px;margin-bottom:24px}.summary-grid{display:grid;grid-template-columns:minmax(0,1fr) minmax(0,1fr);gap:16px}
@media(max-width:760px){.metric-grid{grid-template-columns:repeat(2,minmax(0,1fr))}.summary-grid{grid-template-columns:1fr}}
</style>
