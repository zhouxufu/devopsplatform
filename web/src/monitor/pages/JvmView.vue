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

const svc=ref(''), instance=ref(''), name=ref('')
const otlpSvcs=computed(()=>data.value?.services||[]), instances=computed(()=>data.value?.instances||[]), names=computed(()=>data.value?.names||[]), pts=computed(()=>data.value?.points||[])
async function load() {
 const selected={service:svc.value,instance:instance.value,name:name.value}, environment=curEnvironment.value||'', range=[...contextRange.value], contextService=curSvc.value
 const ok=await read(async signal=>{
  const services=await requestList('api/otlp/services',{signal})
  const service=embedded?(contextService||''):(services.includes(selected.service)?selected.service:services.includes(contextService)?contextService:services[0]||'')
  if(!service)return {services,instances:[],names:[],points:[],service:'',instance:'',name:''}
  const instances=await list('api/instances',{service,environment},signal)
  const instance=instances.includes(selected.instance)?selected.instance:''
  const names=await list('api/otlp/names',{service,environment,instance},signal)
  const name=names.includes(selected.name)?selected.name:names[0]||''
  const points=name?await list('api/otlp/series',{service,environment,instance,name,start:range[0],end:range[1]},signal):[]
  return {services,service,instances,instance,names,name,points}
 })
 if(ok){svc.value=data.value.service;instance.value=data.value.instance;name.value=data.value.name}
}
watch([curSvc,curEnvironment],()=>{svc.value=curSvc.value;instance.value='';name.value='';load()},{immediate:true})
watch(contextRange,load)
const isCnt=computed(()=>pts.value.some(p=>p.cnt>0))

import OptionPicker from '../../shared/components/OptionPicker.vue'



import {Button} from '../../shared/shadcn-ui.js'
</script>
<template><div class="console-content"><PageHeader title="JVM 监控" help="OTLP 指标来自 Java Agent 或 Micrometer，无需侵入业务代码。"><template v-if="!embedded||curSvc" #filters><OptionPicker v-model="instance" :options="instances" all-label="全部实例" :style="{width:'160px',maxWidth:'100%'}" @change="name='';load()" :disabled="loading" aria-label="实例"/></template></PageHeader><StatePanel v-if="embedded && !curSvc" title="请选择要查看的应用" message="在顶部选择应用和环境后查看JVM 监控。"/><template v-else><StatePanel v-if="loading||error" :loading="loading" :error="!!error" :title="error?'指标加载失败':'正在加载指标…'" :message="error"><Button v-if="error" @click="load" variant="outline" size="default" type="button">重试</Button></StatePanel><div v-else-if="!names.length" class="empty">当前应用和环境暂无指标，请检查 Agent 采集配置</div><template v-else><div style="margin-bottom:8px"><OptionPicker v-model="name" :options="names"  :style="{width:'230px',maxWidth:'100%'}" @change="load" :disabled="loading" aria-label="指标"/></div><Card v-if="pts.length" class="card gap-0 py-0 mb-3"><CardContent class="p-4"><Chart :times="pts.map(p => p.minute)" :series="[
          { name: '值', color: 'var(--brand)', vals: pts.map(p => p.val) },
          ...(isCnt ? [{ name: '次数', color: 'var(--ok)', vals: pts.map(p => p.cnt) }] : []),
        ]"/></CardContent></Card><div v-else class="empty">该指标暂无数据</div></template></template></div></template>
