<script setup>
import { ref, provide, watch, onMounted, onBeforeUnmount } from 'vue'
import Sel from '../shared/components/Sel.vue'
import PlatformNav from '../shared/components/PlatformNav.vue'
import PlatformPanel from '../shared/components/PlatformPanel.vue'

const embedded = window.parent !== window
const platformPanel = ref('')
// 全局服务（嵌入模式由壳广播；独立模式顶栏选择）
const curSvc = ref('')
const curEnvironment = ref('')
const services = ref([])
const environments = ref([])
const contextRange = ref([Date.now() - 15 * 60000, Date.now()])
provide('curSvc', curSvc)
provide('curEnvironment', curEnvironment)
provide('environments', environments)
provide('contextRange', contextRange)
provide('embedded', embedded)
function onContext(e) {
  if (e.source !== window.parent || e.origin !== location.origin) return
  const d = e.data
  if (d?.type === 'context.sync' && typeof d.service === 'string') {
    curSvc.value = d.service
    curEnvironment.value = typeof d.environment === 'string' ? d.environment : ''
    if (Number(d.rangeStart) && Number(d.rangeEnd) > Number(d.rangeStart)) {
      contextRange.value = [Number(d.rangeStart), Number(d.rangeEnd)]
    }
  }
}
window.addEventListener('message', onContext)
try { if (embedded) window.parent.postMessage({ type: 'context.request' }, location.origin) } catch {}
async function loadEnvironments() {
  const [observed, configured] = await Promise.all([
    fetch('api/environments?' + new URLSearchParams({ service: curSvc.value || '' })).then(r => r.json()).catch(() => []),
    fetch('/api/plugins/deploy/api/environments').then(r => r.json()).catch(() => []),
  ])
  environments.value = [...new Set([...(configured || []).map(e => e.name), ...(observed || [])].filter(Boolean))]
  if (!embedded && curEnvironment.value && !environments.value.includes(curEnvironment.value)) curEnvironment.value = ''
  if (!embedded && !curEnvironment.value && environments.value.length) {
    curEnvironment.value = environments.value.includes('test') ? 'test' : environments.value[0]
  }
}
onMounted(async () => {
  const [derived, otlp] = await Promise.all([
    fetch('api/services').then(r => r.json()).catch(() => []),
    fetch('api/otlp/services').then(r => r.json()).catch(() => []),
  ])
  services.value = [...new Set([...(derived || []), ...(otlp || [])])].sort()
  if (!embedded && !curSvc.value) curSvc.value = services.value[0] || ''
  loadEnvironments()
})
watch(curSvc, loadEnvironments)
onBeforeUnmount(() => window.removeEventListener('message', onContext))
const NAV = [
  ['/', '▣', '总览'], ['/alerts', '🔔', '报警'], ['/machines', '🖥', '机器'],
  ['/api', '⇄', '接口'], ['/jvm', '☕', 'JVM'], ['/trace', '⇢', 'Trace'], ['/rules', '⚙', '规则'],
]
</script>
<template>
  <div class="layout" :class="{ embedded }">
    <header>
      <span class="logo">📈</span>
      <nav>
        <RouterLink v-for="n in NAV" :key="n[0]" :to="n[0]">{{ n[1] }}<i>{{ n[2] }}</i></RouterLink>
      </nav>
      <PlatformNav @open="platformPanel=$event" />
      <Sel v-if="!embedded" v-model="curSvc" :options="services" placeholder="选择服务" width="120px" style="margin-top:10px"/>
      <Sel v-if="!embedded" v-model="curEnvironment" :options="environments" all-label="全部环境（聚合）" width="130px" />
    </header>
    <main><RouterView /></main>
    <PlatformPanel :view="platformPanel" plugin="monitor" @switch="platformPanel=$event" @close="platformPanel=''" />
  </div>
</template>
<style scoped>
.layout{position:relative;display:flex;height:100vh}
header{width:46px;display:flex;flex-direction:column;align-items:center;gap:8px;padding:10px 0;border-right:1px solid var(--border);background:var(--panel)}
.layout.embedded .logo{display:none}
nav{display:flex;flex-direction:column;gap:4px;width:100%}
nav a{display:flex;flex-direction:column;align-items:center;gap:3px;padding:8px 0;width:100%;border-radius:8px;color:var(--muted);text-decoration:none;font-size:10px}
nav a i{font-style:normal}
nav a.router-link-active{color:var(--text);background:var(--panel2)}
main{flex:1;min-width:0;overflow-y:auto}
.layout:not(.embedded) header{width:150px;align-items:flex-start;padding:14px 10px}
.layout:not(.embedded) nav{flex-direction:row;flex-wrap:wrap}
.layout:not(.embedded) nav a{flex-direction:row;width:auto;padding:5px 10px;font-size:12px;gap:6px}
</style>
