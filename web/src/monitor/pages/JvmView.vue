<script setup>
import PageHeader from '../../shared/components/PageHeader.vue'
import { ref, inject, watch, onMounted } from 'vue'
import Sel from '../../shared/components/Sel.vue'
import Chart from '../../shared/components/Chart.vue'

const curSvc = inject('curSvc')
const embedded = inject('embedded', false)
const curEnvironment = inject('curEnvironment', ref(''))
const contextRange = inject('contextRange', ref([Date.now() - 3600_000, Date.now()]))
const otlpSvcs = ref([]), svc = ref(''), instances = ref([]), instance = ref(''), names = ref([]), name = ref(''), pts = ref([])

async function loadSvcs() {
  otlpSvcs.value = (await fetch('api/otlp/services').then(r => r.json())) ?? []
  svc.value = curSvc.value && otlpSvcs.value.includes(curSvc.value) ? curSvc.value : embedded ? '' : otlpSvcs.value[0] || ''
  if (svc.value) await loadInstances()
}
async function loadInstances() {
  instances.value = (await fetch('api/instances?' + new URLSearchParams({ service: svc.value, environment: curEnvironment.value || '' })).then(r => r.json()).catch(() => [])) ?? []
  if (instance.value && !instances.value.includes(instance.value)) instance.value = ''
  await loadNames()
}
async function loadNames() {
  names.value = (await fetch('api/otlp/names?' + new URLSearchParams({ service: svc.value, environment: curEnvironment.value || '', instance: instance.value })).then(r => r.json())) ?? []
  if (!name.value || !names.value.includes(name.value)) name.value = names.value[0] || ''
  await loadSeries()
}
async function loadSeries() {
  if (!name.value) return
  const [start, end] = contextRange.value
  const p = new URLSearchParams({ service: svc.value, name: name.value, start, end })
  if (curEnvironment.value) p.set('environment', curEnvironment.value)
  if (instance.value) p.set('instance', instance.value)
  pts.value = (await fetch('api/otlp/series?' + p).then(r => r.json())) ?? []
}
watch(curSvc, loadSvcs, { immediate: true })
watch(curEnvironment, loadSvcs)
watch(contextRange, loadSeries)
watch(svc, () => { name.value = ''; loadInstances() })
const isCnt = computed(() => pts.value.some(p => p.cnt > 0))
import { computed } from 'vue'
</script>
<template>
  <div style="padding:16px">
    <div v-if="embedded && !curSvc" class="empty">请先在顶部“全局上下文”选择服务</div>
    <template v-else>
    <PageHeader title="服务指标" help="OTLP 指标来自 Java Agent 或 Micrometer，无需侵入业务代码。"><template #navigation>
      <Sel v-model="svc" :options="otlpSvcs" width="160px" />
      <Sel v-model="instance" :options="instances" all-label="全部实例" width="160px" @change="loadNames" />
    </template></PageHeader>
    <div v-if="!otlpSvcs.length" class="empty">暂无 OTLP 数据——给服务加 OTel javaagent 或 Micrometer OTLP 后出现</div>
    <template v-else>
      <div style="margin-bottom:8px"><Sel v-model="name" :options="names" filterable width="230px" @change="loadSeries" /></div>
      <div v-if="pts.length" class="card">
        <Chart :times="pts.map(p => p.minute)" :series="[
          { name: '值', color: 'var(--brand)', vals: pts.map(p => p.val) },
          ...(isCnt ? [{ name: '次数', color: 'var(--ok)', vals: pts.map(p => p.cnt) }] : []),
        ]" />
      </div>
      <div v-else class="empty">该指标暂无数据</div>
    </template>
    </template>
  </div>
</template>
