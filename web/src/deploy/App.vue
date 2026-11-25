<script setup>
import PageSurface from '../shared/components/PageSurface.vue'

import {availableGroups, navigationLeaves, navigationPath, selectedNavigation} from '../shell/navigation.js'
import {provide, ref, watch, computed} from 'vue'
import {useRoute, useRouter} from 'vue-router'
import {toast} from '../shared/composables/toast.js'
import PlatformNav from '../shared/components/PlatformNav.vue'
import WorkspaceSidebar from '../shared/components/WorkspaceSidebar.vue'
import PlatformPanel from '../shared/components/PlatformPanel.vue'
import UiIcon from '../shared/components/UiIcon.vue'
const embedded = window.parent !== window.window
const directAPI = !location.pathname.startsWith('/api/plugins/')
const route = useRoute(), router = useRouter()
const navigation=availableGroups([{name:'deploy'}])[0].items
const navigationKey=computed(()=>route.path==='/plugins'?'plugins':selectedNavigation('deploy','#'+route.fullPath))
const navigationOpen=ref([])
watch(navigationKey,key=>navigationOpen.value=navigationPath(key,navigation).slice(0,-1),{immediate:true})
function navigateMenu(key){if(key==='plugins')return router.push('/plugins');const item=navigationLeaves(navigation).find(i=>i.id===key);if(item)router.push(item.hash.slice(1))}
watch(()=>route.fullPath,()=>{if(embedded){window.parent.postMessage({type:'context.visibility',service:false,environment:false},location.origin);window.parent.postMessage({type:'workspace.label',label:route.params.name|| (route.params.id?'构建 #'+route.params.id:route.path==='/changes'?(route.query.tab==='work'?'需求与 Bug · 待上线':route.query.item?'变更 #'+route.query.item:'变更记录'):route.path==='/orchestration'?(route.query.run?'执行单 #'+route.query.run:route.query.preparation?'准备单 #'+route.query.preparation:route.query.area==='templates'?'编排模板':'发布记录'):route.path==='/plugins'?'插件扩展':'应用与构建')},location.origin)}},{immediate:true})
const environmentKey=embedded?'lp-deploy-environment:'+window.name:'lp-deploy-environment'
const globalEnvironment = ref(sessionStorage.getItem(environmentKey) || '')
const globalService = ref('')
const platformPanel = ref('')
provide('globalEnvironment', globalEnvironment)
provide('globalService', globalService)
function setPlatformContext(patch) {
  if (Object.prototype.hasOwnProperty.call(patch, 'service') && typeof patch.service === 'string') {
    globalService.value = patch.service
  }
  if (Object.prototype.hasOwnProperty.call(patch, 'environment') && typeof patch.environment === 'string') {
    globalEnvironment.value = patch.environment
    sessionStorage.setItem(environmentKey, patch.environment)
  }
  if (embedded) window.parent.postMessage({ type: 'context.update', ...patch }, location.origin)
}
provide('setPlatformContext', setPlatformContext)
provide('api', (p, opt) => fetch(p, opt).then(async r => {
  if (!r.ok) throw new Error((await r.json().catch(() => ({}))).error || r.status)
  return r.json()
}))
function syncServiceContext(service) {
 // Context is a filter, not a navigation command. Initial/late context sync must
 // never replace an explicit detail/run URL or turn a list into an application.
 globalService.value = String(route.params.name || service || '')
}
// 平台壳上下文
window.addEventListener('message', e => {
  if (e.source !== window.parent || e.origin !== location.origin) return
  if (e.data?.type === 'context.sync' && typeof e.data.service === 'string') {
    // 只有应用真的变化才跳转，环境同步不能把用户从发布页踢回概览。
    syncServiceContext(e.data.service)
  }
  if (typeof e.data?.environment === 'string') {
    globalEnvironment.value = e.data.environment
    sessionStorage.setItem(environmentKey, e.data.environment)
  }
})
try { if (embedded) window.parent.postMessage({ type: 'context.request' }, location.origin) } catch {}



import {Collapsible,CollapsibleContent,CollapsibleTrigger,SidebarMenu,SidebarMenuButton,SidebarMenuItem,SidebarMenuSub} from '../shared/shadcn-ui.js'
</script>
<template><div :class="{ embedded }" class="layout"><WorkspaceSidebar v-slot="{collapsed}"><span class="logo"><UiIcon name="deploy"/> 部署平台</span><SidebarMenu aria-label="部署平台导航" class="delivery-navigation platform-navigation"><template v-for="item in navigation" :key="item.id"><Collapsible v-if="item.items" :aria-label="item.label" :open="(navigationOpen).includes(item.id)" @update:open="v=>{navigationOpen=v?[...navigationOpen,item.id]:navigationOpen.filter(k=>k!==item.id)}"><SidebarMenuItem><CollapsibleTrigger as-child><SidebarMenuButton class="h-10 text-base"><UiIcon name="settings"/><span>{{item.label}}</span></SidebarMenuButton></CollapsibleTrigger><CollapsibleContent><SidebarMenuSub><SidebarMenuItem v-for="child in item.items"><SidebarMenuButton class="h-10 text-sm" :is-active="([navigationKey]).includes(child.id)" @click="navigateMenu(child.id)"><span>{{child.label}}</span></SidebarMenuButton></SidebarMenuItem></SidebarMenuSub></CollapsibleContent></SidebarMenuItem></Collapsible><SidebarMenuItem v-else :aria-label="item.label"><SidebarMenuButton class="h-10 text-sm" :is-active="([navigationKey]).includes(item.id)" @click="navigateMenu(item.id)"><UiIcon name="build"/><span>{{item.label}}</span></SidebarMenuButton></SidebarMenuItem></template><SidebarMenuItem aria-label="插件扩展"><SidebarMenuButton class="h-10 text-sm" :is-active="([navigationKey]).includes(&quot;plugins&quot;)" @click="navigateMenu(&quot;plugins&quot;)"><UiIcon name="plugins"/><span>插件扩展</span></SidebarMenuButton></SidebarMenuItem></SidebarMenu><PlatformNav @open="platformPanel=$event"/></WorkspaceSidebar><main><PageSurface><RouterView/></PageSurface></main><PlatformPanel :view="platformPanel" plugin="deploy" :directAPI="directAPI" @switch="platformPanel=$event" @close="platformPanel=''"/></div></template>


<style scoped>main{flex:1;min-width:0;min-height:0;overflow:auto}.logo{display:flex;align-items:center;gap:.5rem;padding:.75rem;font-weight:600}.standalone-context{display:flex;flex-wrap:wrap;align-items:end;gap:1rem;margin-bottom:1.5rem}</style>
