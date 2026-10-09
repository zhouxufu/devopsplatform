<script setup>
import { ref, provide, onMounted, onBeforeUnmount } from 'vue'
import { isDark, toggleTheme } from '../shared/composables/theme.js'
import { serviceNames } from './services.js'
import PlatformNav from '../shared/components/PlatformNav.vue'
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
</script>
<template>
  <div class="layout" :class="{ embedded }">
    <header>
      <span class="logo"><UiIcon name="logs"/></span>
      <nav aria-label="日志平台导航">
        <RouterLink to="/search"><UiIcon name="search"/><i>搜索</i></RouterLink>
        <RouterLink to="/live"><UiIcon name="live"/><i>Live</i></RouterLink>
        <RouterLink to="/subs"><UiIcon name="bell"/><i>订阅</i></RouterLink>
        <RouterLink to="/status"><UiIcon name="status"/><i>状态</i></RouterLink>
      </nav>
      <PlatformNav @open="platformPanel=$event" />
    </header>
    <main><RouterView /></main>
    <PlatformPanel :view="platformPanel" plugin="logs" @switch="platformPanel=$event" @close="platformPanel=''" />
  </div>
</template>
<style scoped>
.layout{position:relative;display:flex;height:100vh}
.layout>header{box-sizing:border-box;width:46px;flex:0 0 46px;display:flex;flex-direction:column;align-items:center;gap:8px;padding:10px 0;border-right:1px solid var(--border);background:var(--panel);overflow-y:auto}
.logo{font-size:15px}
.layout.embedded .logo{display:none}
nav{display:flex;flex-direction:column;gap:4px;width:100%}
nav a{display:flex;flex-direction:column;align-items:center;gap:3px;padding:8px 0;width:100%;border-radius:8px;color:var(--muted);text-decoration:none;font-size:10px}
nav a.router-link-active{color:var(--text);background:var(--panel2)}
main{flex:1;min-width:0;display:flex;flex-direction:column}
.layout:not(.embedded)>header{width:160px;flex-basis:160px;align-items:flex-start;padding:14px 10px}
.layout:not(.embedded) nav a{flex-direction:row;justify-content:flex-start;width:100%;padding:10px;font-size:12px;gap:10px;white-space:nowrap}
.layout:not(.embedded) :deep(.platform-nav button){flex-direction:row;justify-content:flex-start;padding:10px;font-size:12px;gap:10px;white-space:nowrap}
.themebtn{margin-top:auto;font-size:14px;cursor:pointer;color:var(--muted);padding:4px}
.themebtn:hover{color:var(--text)}
.layout>header{width:var(--sidebar-width);flex-basis:var(--sidebar-width);padding:12px 6px;gap:16px}nav a{min-height:54px;justify-content:center;gap:5px;padding:9px 0;border-radius:10px;font-size:11px}nav a i{font-style:normal}nav a:hover{background:var(--panel2);color:var(--text)}nav a.router-link-active{background:var(--brand-soft);color:var(--brand-ink);font-weight:600}.layout:not(.embedded)>header{width:176px;flex-basis:176px;padding:20px 12px}.layout:not(.embedded) nav a,.layout:not(.embedded) :deep(.platform-nav button){font-size:13px;min-height:44px}
@media(max-width:760px){.layout:not(.embedded)>header{width:var(--sidebar-width);flex-basis:var(--sidebar-width);padding:12px 6px}.layout:not(.embedded) .logo{display:none}.layout:not(.embedded) nav a,.layout:not(.embedded) :deep(.platform-nav button){flex-direction:column;justify-content:center;padding:9px 0;gap:5px;font-size:11px}}
</style>
