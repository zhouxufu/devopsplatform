<script setup>
import {Card,CardContent} from "../../shared/shadcn-ui.js"

import PageHeader from '../../shared/components/PageHeader.vue'
import StatePanel from '../../shared/components/StatePanel.vue'
import {ref, computed, inject, watch} from 'vue'

import {useAsyncData, requestList} from '../../shared/composables/asyncData.js'

import Chart from '../../shared/components/Chart.vue'
const curSvc = inject('curSvc', ref(''))
const embedded = inject('embedded', false)
const curEnvironment = inject('curEnvironment', ref(''))
const contextRange = inject('contextRange', ref([Date.now() - 3600_000, Date.now()]))
const {data, loading, error, load:read} = useAsyncData()
const list = (url, params, signal) => requestList(url + '?' + new URLSearchParams(params), {signal})

const ifaceSvcs=ref(''), instance=ref(''), metric=ref(''), iface=ref(''), meth=ref(''), status=ref('')
const otlpSvcs=computed(()=>data.value?.services||[]), instances=computed(()=>data.value?.instances||[]), names=computed(()=>data.value?.names||[]), ifaces=computed(()=>data.value?.ifaces||[]), attrs=computed(()=>data.value?.attrs||[]), pts=computed(()=>data.value?.points||[]), avail=computed(()=>data.value?.availability||[])
async function load() {
 const selected={service:ifaceSvcs.value,instance:instance.value,name:metric.value,iface:iface.value,meth:meth.value,status:status.value}, environment=curEnvironment.value||'', range=[...contextRange.value], contextService=curSvc.value
 const ok=await read(async signal=>{
  const services=await requestList('api/otlp/services',{signal})
  const service=embedded?(contextService||''):(services.includes(selected.service)?selected.service:services.includes(contextService)?contextService:services[0]||'')
  if(!service)return {services,service:'',instances:[],instance:'',names:[],name:'',ifaces:[],iface:'',attrs:[],points:[],availability:[]}
  const instances=await list('api/instances',{service,environment},signal), instance=instances.includes(selected.instance)?selected.instance:''
  const names=await list('api/otlp/names',{service,environment,instance},signal)
  const name=names.includes(selected.name)?selected.name:names.find(n=>/duration|latency|time$|code|result|outcome/.test(n))||names[0]||''
  const ifaces=name?await list('api/otlp/interfaces',{service,environment,instance,name},signal):[]
  const iface=ifaces.some(i=>i.iface===selected.iface)?selected.iface:ifaces[0]?.iface||''
  const attrs=name?await list('api/otlp/iface-attrs',{service,environment,instance,name,iface},signal):[]
  const p={service,environment,instance,name,iface,meth:selected.meth,status:selected.status,start:range[0],end:range[1]}
  const [points,availability]=name?await Promise.all([list('api/otlp/quantiles',p,signal),list('api/otlp/availability',p,signal)]):[[],[]]
  return {services,service,instances,instance,names,name,ifaces,iface,attrs,points,availability}
 })
 if(ok){ifaceSvcs.value=data.value.service;instance.value=data.value.instance;metric.value=data.value.name;iface.value=data.value.iface}
}
function reset(stage){if(stage==='service')instance.value='';if(['service','instance'].includes(stage))metric.value='';if(['service','instance','metric'].includes(stage))iface.value='';meth.value='';status.value='';load()}
watch([curSvc,curEnvironment],()=>{ifaceSvcs.value=curSvc.value;reset('service')},{immediate:true})
watch(contextRange,load)
const isSec=computed(()=>/duration|latency/.test(metric.value)), ms=v=>v*(isSec.value?1000:1), last=computed(()=>pts.value.at(-1))
const availTotal=computed(()=>{const total=avail.value.reduce((s,p)=>s+p.total,0),ok=avail.value.reduce((s,p)=>s+p.ok,0);return total>0?{rate:ok/total*100,ok,total}:null})

import OptionPicker from '../../shared/components/OptionPicker.vue'



import {Button} from '../../shared/shadcn-ui.js'
</script>
<template><div class="console-content"><PageHeader title="接口监控"><template v-if="!embedded||curSvc" #filters><OptionPicker v-model="instance" :options="instances" all-label="全部实例" :style="{width:'160px',maxWidth:'100%'}" @change="reset('instance')" :disabled="loading" aria-label="实例"/><OptionPicker v-model="metric" :options="names" :style="{width:'190px',maxWidth:'100%'}" @change="reset('metric')" :disabled="loading" aria-label="指标"/><OptionPicker v-model="iface" :options="ifaces.map(i => ({ value: i.iface, label: (i.iface || '（无接口维度）') + ' · ' + Math.round(i.cnt) + '/h' }))" :style="{width:'260px',maxWidth:'100%'}" @change="reset('iface')" :disabled="loading" aria-label="接口"/><OptionPicker v-model="meth" :options="[...new Set(attrs.map(a => a.meth).filter(Boolean))]" all-label="全部方法" :style="{width:'120px',maxWidth:'100%'}" @change="load" :disabled="loading"/><OptionPicker v-model="status" :options="[...new Set(attrs.map(a => a.status).filter(Boolean))]" all-label="全部状态" :style="{width:'110px',maxWidth:'100%'}" @change="load" :disabled="loading"/></template></PageHeader><StatePanel v-if="embedded && !curSvc" title="请选择要查看的应用" message="在顶部选择应用和环境后查看接口监控。"/><template v-else><StatePanel v-if="loading||error" :loading="loading" :error="!!error" :title="error?'接口指标加载失败':'正在加载指标…'" :message="error"><Button v-if="error" @click="load" variant="outline" size="default" type="button">重试</Button></StatePanel><template v-else><div v-if="last" class="metric-grid"><Card class="card gap-0 py-0 mb-4"><CardContent class="p-4"><div class="t">请求量</div><div class="v">{{ Math.round(last.cnt) }}<small style="font-size:.75rem;color:var(--dim)"> /分</small></div></CardContent></Card><Card class="card gap-0 py-0 mb-4"><CardContent class="p-4"><div class="t">平均耗时</div><div class="v">{{ ms(last.avg).toFixed(1) }}<small style="font-size:.75rem;color:var(--dim)"> ms</small></div><div class="s">{{ isSec ? '源单位:秒' : '源单位:毫秒' }}</div></CardContent></Card><Card class="card gap-0 py-0 mb-4"><CardContent class="p-4"><div class="t">TP95</div><div style="color:var(--warn)" class="v">{{ ms(last.p95).toFixed(1) }}<small style="font-size:.75rem;color:var(--dim)"> ms</small></div></CardContent></Card><Card class="card gap-0 py-0 mb-4"><CardContent class="p-4"><div class="t">TP99</div><div style="color:var(--error)" class="v">{{ ms(last.p99).toFixed(1) }}<small style="font-size:.75rem;color:var(--dim)"> ms</small></div></CardContent></Card><Card class="card gap-0 py-0 mb-4"><CardContent class="p-4"><div class="t">可用率</div><div v-if="availTotal" :style="{ color: availTotal.rate >= 99.5 ? 'var(--ok)' : availTotal.rate >= 99 ? 'var(--warn)' : 'var(--error)' }" class="v">{{ availTotal.rate.toFixed(2) }}<small style="font-size:.75rem;color:var(--dim)"> %</small></div><div v-else class="v">—</div><div class="s">{{ availTotal ? Math.round(availTotal.ok) + '/' + Math.round(availTotal.total) : '无状态维度' }}</div></CardContent></Card></div><Card v-if="pts.length" class="card gap-0 py-0 mb-4"><CardContent class="p-4"><h4>耗时分位（ms）<span style="margin-left:12px;font-size:.75rem;color:var(--dim)">源单位:{{ isSec ? '秒' : '毫秒' }}</span></h4><Chart :times="pts.map(p => p.minute)" :series="[
        { name: 'P50', color: 'var(--info)', vals: pts.map(p => ms(p.p50)) },
        { name: 'P95', color: 'var(--warn)', vals: pts.map(p => ms(p.p95)) },
        { name: 'P99', color: 'var(--error)', vals: pts.map(p => ms(p.p99)) },
        { name: 'max', color: '#8b949e', vals: pts.map(p => ms(p.max)) },
      ]" unit=" ms" :height="210"/></CardContent></Card><div v-else class="empty">当前应用、环境和时间范围内暂无直方图数据</div></template></template></div></template>
<style scoped>.metric-grid>.card,.summary-grid>.card{margin-bottom:0}.metric-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(150px,1fr));gap:16px;margin-bottom:24px}
.card .t{color:var(--muted-foreground);font-size:.75rem}
.card .v{font-size:1.25rem;font-weight:600;font-family:var(--mono);margin-top:2px}
.card .s{color:var(--dim);font-size:.75rem}</style>
