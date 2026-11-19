<script setup>
import {Card,CardContent} from "../../shared/shadcn-ui.js"

import PageHeader from '../../shared/components/PageHeader.vue'
import StatePanel from '../../shared/components/StatePanel.vue'
import {formatMetric,metricNumber} from '../../shared/observedMetrics.js'
import {ref, computed, inject, watch, onMounted, onUnmounted} from 'vue'

import {useAsyncData, requestList} from '../../shared/composables/asyncData.js'

import Chart from '../../shared/components/Chart.vue'
const agent=ref(''), contextRange=inject('contextRange',ref([Date.now()-3600_000,Date.now()])), curEnvironment=inject('curEnvironment',ref(''))
const {data,loading,error,load:read}=useAsyncData()
const machines=computed(()=>data.value?.machines||[]), series=computed(()=>data.value?.series||[])
const selectedMachine=computed(()=>machines.value.find(m=>m.agent_id===agent.value)||{})
const cpuTone=computed(()=>metricNumber(selectedMachine.value.cpu)===null?'var(--muted-foreground)':Number(selectedMachine.value.cpu)>80?'var(--error)':'var(--ok)')
let timer
async function load(){
 const environment=curEnvironment.value||'', range=[...contextRange.value], selected=agent.value
 const ok=await read(async signal=>{
  const machines=await requestList('api/machines?'+new URLSearchParams({environment}),{signal})
  const agent=machines.some(m=>m.agent_id===selected)?selected:machines[0]?.agent_id||''
  const series=agent?await requestList('api/machine-series?'+new URLSearchParams({agent,environment,start:range[0],end:range[1]}),{signal}):[]
  return {machines,agent,series}
 })
 if(ok)agent.value=data.value.agent
}
watch([curEnvironment,contextRange],load,{immediate:true})
onMounted(()=>{timer=setInterval(()=>{if(!loading.value)load()},30000)})
onUnmounted(()=>clearInterval(timer))

import OptionPicker from '../../shared/components/OptionPicker.vue'



import {Button} from '../../shared/shadcn-ui.js'
</script>
<template><div class="console-content"><PageHeader title="主机监控" help="上方为实时快照；Agent 每 15 秒采样，页面每 30 秒刷新。"><template #filters><OptionPicker v-model="agent" :options="machines.map(m => ({ value: m.agent_id, label: m.agent_id + (curEnvironment ? '' : ' · ' + (m.environment || '未标记')) }))" :style="{width:'180px',maxWidth:'100%'}" @change="load" :disabled="loading" aria-label="主机"/></template></PageHeader><StatePanel v-if="loading||error" :loading="loading" :error="!!error" :title="error?'主机指标加载失败':'正在加载指标…'" :message="error"><Button v-if="error" @click="load" variant="outline" size="default" type="button">重试</Button></StatePanel><div v-else-if="machines.length" class="machine-grid"><Card class="card gap-0 py-0 mb-4"><CardContent class="p-4"><div class="t">CPU</div><div :style="{ color: cpuTone }" class="v">{{ formatMetric(selectedMachine.cpu,'%') }}</div><div class="s">{{ agent }}</div></CardContent></Card><Card class="card gap-0 py-0 mb-4"><CardContent class="p-4"><div class="t">内存</div><div class="v">{{ formatMetric(selectedMachine.mem_used) }}<small style="font-size:.75rem;color:var(--dim)"> / {{ formatMetric(selectedMachine.mem_total) }}G</small></div></CardContent></Card><Card class="card gap-0 py-0 mb-4"><CardContent class="p-4"><div class="t">磁盘 /</div><div class="v">{{ formatMetric(selectedMachine.disk_used) }}<small style="font-size:.75rem;color:var(--dim)"> / {{ formatMetric(selectedMachine.disk_total) }}G</small></div></CardContent></Card><Card class="card gap-0 py-0 mb-4"><CardContent class="p-4"><div class="t">网络 ↓↑</div><div style="font-size:1.125rem" class="v">{{ formatMetric(selectedMachine.net_rx_kbs) }} / {{ formatMetric(selectedMachine.net_tx_kbs) }}<small style="font-size:.75rem;color:var(--dim)"> KB/s</small></div></CardContent></Card></div><div v-else class="empty">暂无机器数据（agent 配置 monitor_url 后出现）</div><Card v-if="series.length" class="card gap-0 py-0 mb-4"><CardContent class="p-4"><h4>CPU %</h4><Chart :times="series.map(p => p.minute)" :series="[{ name: 'CPU', color: 'var(--brand)', vals: series.map(p => p.cpu) }]" :y-max="100" unit="%"/></CardContent></Card><Card v-if="series.length" class="card gap-0 py-0 mb-4"><CardContent class="p-4"><h4>内存 GB</h4><Chart :times="series.map(p => p.minute)" :series="[{ name: '内存', color: 'var(--warn)', vals: series.map(p => p.mem_used) }]" unit=" G"/></CardContent></Card><div v-if="machines.length && !series.length" class="empty">所选全局时间区间内没有趋势数据；可在顶部切换“最近15分钟”。实时快照不受时间区间影响。</div></div></template>
<style scoped>.machine-grid>.card,.summary-grid>.card{margin-bottom:0}.card .t{color:var(--muted-foreground);font-size:.75rem}
.card .v{font-size:1.25rem;font-weight:600;font-family:var(--mono);margin-top:2px}
.card .s{color:var(--dim);font-size:.75rem}
.machine-grid{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:16px;margin-bottom:24px}
@media(max-width:760px){.machine-grid{grid-template-columns:repeat(2,minmax(0,1fr))}}
</style>
