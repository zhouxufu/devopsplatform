<script setup>
import PageSurface from '../shared/components/PageSurface.vue'
import {useRoute as useMenuRoute,useRouter as useMenuRouter} from 'vue-router'

import {ref, provide, onMounted, onBeforeUnmount} from 'vue'
import {isDark, toggleTheme} from '../shared/composables/theme.js'
import {serviceNames} from './services.js'
import PlatformNav from '../shared/components/PlatformNav.vue'
import WorkspaceSidebar from '../shared/components/WorkspaceSidebar.vue'
import PlatformPanel from '../shared/components/PlatformPanel.vue'
import UiIcon from '../shared/components/UiIcon.vue'

const dark = ref(isDark())
const platformPanel = ref('')
function switchTheme() { dark.value = toggleTheme() }
window.addEventListener('message', e => {
  if (e.source !== window.parent || e.origin !== location.origin) return
  if (e.data?.type === 'theme.sync') dark.value = !!e.data.dark
})

const embedded = window.parent !== window
const services = ref([])
const platformContext = ref(null)
const externalLogSource=ref('')
provide('externalLogSource',externalLogSource)
provide('logServices', services)
provide('platformContext', platformContext)
// 全局上下文（嵌入模式由壳驱动）
function onContext(e) {
  if (e.source !== window.parent || e.origin !== location.origin) return
  const d = e.data
  if (d?.type === 'context.sync' && typeof d.service === 'string') {
    platformContext.value = {
      service: d.service,
      environment: typeof d.environment === 'string' ? d.environment : '',
      rangeStart: Number(d.rangeStart) || 0,
      rangeEnd: Number(d.rangeEnd) || 0,
      rangeMinutes: Number(d.rangeMinutes) || 0,
    }
  }
}
window.addEventListener('message', onContext)
try { if (embedded) window.parent.postMessage({ type: 'context.request' }, location.origin) } catch {}
onMounted(async () => {
  services.value = serviceNames(await fetch('api/services').then(r => r.json()).catch(() => ({})))
})
onBeforeUnmount(() => window.removeEventListener('message', onContext))
const menuRoute=useMenuRoute(),menuRouter=useMenuRouter()



import {SidebarMenu,SidebarMenuButton,SidebarMenuItem} from '../shared/shadcn-ui.js'
</script>
<template><div :class="{ embedded }" class="layout"><WorkspaceSidebar v-slot="{collapsed}"><span class="logo"><UiIcon name="logs"/></span><SidebarMenu class="platform-navigation"><SidebarMenuItem v-for="n in [['/search','search','搜索'],['/live','live','实时日志'],['/subs','bell',externalLogSource?'本地订阅':'订阅'],['/status','status',externalLogSource?'本地采集':'状态']]" :aria-label="n[2]"><SidebarMenuButton class="h-10 text-sm" :is-active="([menuRoute.path]).includes(n[0])" @click="(key=>menuRouter.push(key))(n[0])"><UiIcon :name="n[1]"/><span><span>{{n[2]}}</span></span></SidebarMenuButton></SidebarMenuItem></SidebarMenu><PlatformNav @open="platformPanel=$event"/></WorkspaceSidebar><main :class="{'log-query-host':menuRoute.path==='/search'||menuRoute.path==='/'}"><PageSurface :fill="['/','/search','/live'].includes(menuRoute.path)" :flush="['/','/search','/live'].includes(menuRoute.path)"><RouterView/></PageSurface></main><PlatformPanel :view="platformPanel" plugin="logs" @switch="platformPanel=$event" @close="platformPanel=''"/></div></template>


<style scoped>main{flex:1;min-width:0;min-height:0;overflow:auto}.logo{display:flex;align-items:center;gap:.5rem;padding:.75rem;font-weight:600}.standalone-context{display:flex;flex-wrap:wrap;align-items:end;gap:1rem;margin-bottom:1.5rem}</style>
