<script setup>
import { provide, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { toast } from '../shared/composables/toast.js'
import PlatformNav from '../shared/components/PlatformNav.vue'
import WorkspaceSidebar from '../shared/components/WorkspaceSidebar.vue'
import PlatformPanel from '../shared/components/PlatformPanel.vue'
import UiIcon from '../shared/components/UiIcon.vue'
const embedded = window.parent !== window.window
const directAPI = !location.pathname.startsWith('/api/plugins/')
const route = useRoute(), router = useRouter()
watch(()=>route.path,()=>{if(embedded)window.parent.postMessage({type:'context.visibility',service:!!route.params.name,environment:!!route.params.name},location.origin)},{immediate:true})
const globalEnvironment = ref(sessionStorage.getItem('lp-deploy-environment') || '')
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
    sessionStorage.setItem('lp-deploy-environment', patch.environment)
  }
  if (embedded) window.parent.postMessage({ type: 'context.update', ...patch }, location.origin)
}
provide('setPlatformContext', setPlatformContext)
provide('api', (p, opt) => fetch(p, opt).then(async r => {
  if (!r.ok) throw new Error((await r.json().catch(() => ({}))).error || r.status)
  return r.json()
}))
async function syncServiceContext(service) {
  globalService.value = service
  if (!service) {
    if (route.params.name) await router.push('/')
    return
  }
  // 日志和监控能发现未接入部署平台的服务。只有真正存在的部署应用
  // 才允许全局上下文驱动详情页，避免显示“幽灵应用”。
  const services = await fetch('api/services').then(r => r.ok ? r.json() : []).catch(() => [])
  if (!services.some(item => item.name === service)) {
    if (route.params.name) await router.push('/')
    return
  }
  // Multi-application delivery pages are global workspaces: context sync must
  // not interrupt editing a version or an orchestration plan.
  if (route.path.startsWith('/changes') || route.path.startsWith('/orchestration')) return
  if (String(route.params.name || '') !== service) await router.push(`/svc/${encodeURIComponent(service)}/overview`)
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
    sessionStorage.setItem('lp-deploy-environment', e.data.environment)
  }
})
try { if (embedded) window.parent.postMessage({ type: 'context.request' }, location.origin) } catch {}
</script>
<template>
  <div class="layout" :class="{ embedded }">
    <WorkspaceSidebar>
      <span class="logo"><UiIcon name="deploy"/> 部署平台</span>
      <nav aria-label="部署平台导航">
        <RouterLink to="/" title="基础构建"><UiIcon name="build"/><i>构建</i></RouterLink>
        <RouterLink to="/orchestration" title="部署编排"><UiIcon name="orchestration"/><i>编排</i></RouterLink>
        <RouterLink to="/changes" title="变更管控"><UiIcon name="changes"/><i>变更</i></RouterLink>
        <RouterLink to="/plugins" title="插件扩展"><UiIcon name="plugins"/><i>扩展</i></RouterLink>
      </nav>
      <PlatformNav @open="platformPanel=$event" />
    </WorkspaceSidebar>
    <main><RouterView /></main>
    <PlatformPanel :view="platformPanel" plugin="deploy" :directAPI="directAPI" @switch="platformPanel=$event" @close="platformPanel=''" />
  </div>
</template>
<style scoped>
.layout{position:relative;display:flex;height:100vh}
.layout>header{box-sizing:border-box;width:46px;flex:0 0 46px;display:flex;flex-direction:column;align-items:center;gap:10px;padding:10px 0;border-right:1px solid var(--border);background:var(--panel);overflow-y:auto}
.logo{font-size:18px}
.layout.embedded .logo{display:none}
nav{display:flex;flex-direction:column;gap:4px;width:100%}
nav a{display:flex;flex-direction:column;align-items:center;gap:3px;padding:8px 0;width:100%;border-radius:8px;color:var(--muted);text-decoration:none;font-size:10px}
nav a i{font-style:normal}
nav a.router-link-active{color:var(--text);background:var(--panel2)}
.layout:not(.embedded)>header{width:160px;flex-basis:160px;align-items:flex-start;padding:14px 10px}
.layout:not(.embedded) .logo{font-size:13px;font-weight:600}
.layout:not(.embedded) nav a{flex-direction:row;justify-content:flex-start;width:100%;padding:10px;font-size:12px;gap:10px;white-space:nowrap}
.layout:not(.embedded) :deep(.platform-nav button){flex-direction:row;justify-content:flex-start;padding:10px;font-size:12px;gap:10px;white-space:nowrap}
.layout>main{flex:1;min-width:0;overflow:auto;padding:14px 16px}
.layout>header{width:var(--sidebar-width);flex-basis:var(--sidebar-width);padding:12px 6px;gap:16px}
nav a{min-height:54px;justify-content:center;padding:9px 0;gap:5px;border-radius:10px;font-size:11px;transition:background .15s,color .15s}
nav a:hover{background:var(--panel2);color:var(--text)}nav a.router-link-active{background:var(--brand-soft);color:var(--brand-ink);font-weight:600}
.layout:not(.embedded)>header{width:176px;flex-basis:176px;padding:20px 12px}.layout:not(.embedded) .logo{display:flex;align-items:center;gap:8px;margin-bottom:8px}.layout:not(.embedded) nav a{font-size:13px;min-height:44px}.layout:not(.embedded) :deep(.platform-nav button){font-size:13px;min-height:44px}
.layout>main{padding:24px 28px}
@media(max-width:760px){.layout>main{padding:18px 16px}.layout:not(.embedded)>header{width:var(--sidebar-width);flex-basis:var(--sidebar-width);padding:12px 6px}.layout:not(.embedded) .logo{display:none}.layout:not(.embedded) nav a,.layout:not(.embedded) :deep(.platform-nav button){flex-direction:column;justify-content:center;padding:9px 0;font-size:11px;gap:5px}}
</style>
