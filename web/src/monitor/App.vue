<script setup>
import PageSurface from '../shared/components/PageSurface.vue'
import {useRoute as useMenuRoute,useRouter as useMenuRouter} from 'vue-router'

import {ref, provide, watch, onMounted, onBeforeUnmount} from 'vue'

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
  ['/api', 'devtools', '接口'], ['/jvm', 'java', 'JVM'], ['/arthas', 'arthas', 'Arthas 诊断'], ['/trace', 'orchestration', 'Trace'], ['/rules', 'plugins', '规则'],
]
const menuRoute=useMenuRoute(),menuRouter=useMenuRouter()

import OptionPicker from '../shared/components/OptionPicker.vue'



import {Field,FieldLabel,SidebarMenu,SidebarMenuButton,SidebarMenuItem} from '../shared/shadcn-ui.js'
</script>
<template><div :class="{ embedded }" class="layout"><WorkspaceSidebar v-slot="{collapsed}"><span class="logo"><UiIcon name="monitor"/></span><SidebarMenu class="platform-navigation"><SidebarMenuItem v-for="n in NAV" :aria-label="n[2]"><SidebarMenuButton class="h-10 text-sm" :is-active="([menuRoute.path]).includes(n[0])" @click="(key=>menuRouter.push(key))(n[0])"><UiIcon :name="n[1]"/><span><span>{{n[2]}}</span></span></SidebarMenuButton></SidebarMenuItem></SidebarMenu><PlatformNav @open="platformPanel=$event"/></WorkspaceSidebar><main><PageSurface><div v-if="!embedded" class="standalone-context"><Field ><FieldLabel as="div">应用</FieldLabel><OptionPicker v-model="curSvc" :options="services" placeholder="选择服务" :style="{width:'180px',maxWidth:'100%'}"/></Field><Field ><FieldLabel as="div">环境</FieldLabel><OptionPicker v-model="curEnvironment" :options="environments" all-label="全部环境（聚合）" :style="{width:'180px',maxWidth:'100%'}"/></Field></div><RouterView/></PageSurface></main><PlatformPanel :view="platformPanel" plugin="monitor" @switch="platformPanel=$event" @close="platformPanel=''"/></div></template>




<style scoped>main{flex:1;min-width:0;min-height:0;overflow:auto}.logo{display:flex;align-items:center;gap:.5rem;padding:.75rem;font-weight:600}.standalone-context{display:flex;flex-wrap:wrap;align-items:end;gap:1rem;margin-bottom:1rem}.standalone-context>[data-slot=field]{width:180px;flex:0 0 180px}</style>
