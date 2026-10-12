<script setup>
import { ref, provide, watch, onMounted, onBeforeUnmount } from 'vue'
import Sel from '../shared/components/Sel.vue'
import PlatformNav from '../shared/components/PlatformNav.vue'
import WorkspaceSidebar from '../shared/components/WorkspaceSidebar.vue'
import PlatformPanel from '../shared/components/PlatformPanel.vue'
import UiIcon from '../shared/components/UiIcon.vue'

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
  ['/', 'monitor', '总览'], ['/alerts', 'bell', '报警'], ['/machines', 'resources', '机器'],
  ['/api', 'devtools', '接口'], ['/jvm', 'java', 'JVM'], ['/trace', 'orchestration', 'Trace'], ['/rules', 'plugins', '规则'],
]
</script>
<template>
  <div class="layout" :class="{ embedded }">
    <WorkspaceSidebar>
      <span class="logo"><UiIcon name="monitor"/></span>
      <nav aria-label="监控平台导航">
        <RouterLink v-for="n in NAV" :key="n[0]" :to="n[0]"><UiIcon :name="n[1]"/><i>{{ n[2] }}</i></RouterLink>
      </nav>
      <PlatformNav @open="platformPanel=$event" />
    </WorkspaceSidebar>
    <main><div v-if="!embedded" class="standalone-context"><label>应用<Sel v-model="curSvc" :options="services" placeholder="选择服务" width="180px"/></label><label>环境<Sel v-model="curEnvironment" :options="environments" all-label="全部环境（聚合）" width="180px" /></label></div><RouterView /></main>
    <PlatformPanel :view="platformPanel" plugin="monitor" @switch="platformPanel=$event" @close="platformPanel=''" />
  </div>
</template>
<style scoped>
.layout{position:relative;display:flex;height:100vh}
.layout>header{box-sizing:border-box;width:46px;flex:0 0 46px;display:flex;flex-direction:column;align-items:center;gap:8px;padding:10px 0;border-right:1px solid var(--border);background:var(--panel);overflow-y:auto}
.layout.embedded .logo{display:none}
nav{display:flex;flex-direction:column;gap:4px;width:100%}
nav a{display:flex;flex-direction:column;align-items:center;gap:3px;padding:8px 0;width:100%;border-radius:8px;color:var(--muted);text-decoration:none;font-size:10px}
nav a i{font-style:normal}
nav a.router-link-active{color:var(--text);background:var(--panel2)}
main{flex:1;min-width:0;overflow-y:auto}
.layout:not(.embedded)>header{width:160px;flex-basis:160px;align-items:flex-start;padding:14px 10px}
.layout:not(.embedded) nav a{flex-direction:row;justify-content:flex-start;width:100%;padding:10px;font-size:12px;gap:10px;white-space:nowrap}
.layout:not(.embedded) :deep(.platform-nav button){flex-direction:row;justify-content:flex-start;padding:10px;font-size:12px;gap:10px;white-space:nowrap}
</style>

<style scoped>
.layout>header{width:var(--sidebar-width);flex-basis:var(--sidebar-width);padding:12px 6px;gap:16px}nav a{min-height:54px;justify-content:center;gap:5px;padding:9px 0;border-radius:10px;font-size:11px}nav a:hover{background:var(--panel2);color:var(--text)}nav a.router-link-active{background:var(--brand-soft);color:var(--brand-ink);font-weight:600}.layout:not(.embedded)>header{width:176px;flex-basis:176px;padding:20px 12px}.layout:not(.embedded) nav a,.layout:not(.embedded) :deep(.platform-nav button){font-size:13px;min-height:44px}.standalone-context{display:flex;align-items:center;flex-wrap:wrap;gap:12px;padding:12px 20px;border-bottom:1px solid var(--border);background:var(--panel)}.standalone-context label{display:flex;align-items:center;gap:8px;font-size:12px;color:var(--muted)}@media(max-width:760px){.layout:not(.embedded)>header{width:var(--sidebar-width);flex-basis:var(--sidebar-width);padding:12px 6px}.layout:not(.embedded) .logo{display:none}.layout:not(.embedded) nav a,.layout:not(.embedded) :deep(.platform-nav button){flex-direction:column;justify-content:center;padding:9px 0;gap:5px;font-size:11px}}
</style>
