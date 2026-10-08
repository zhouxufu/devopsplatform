<script setup>
import { ref, provide, onMounted, onBeforeUnmount } from 'vue'
import { isDark, toggleTheme } from '../shared/composables/theme.js'
import { serviceNames } from './services.js'
import PlatformNav from '../shared/components/PlatformNav.vue'
import PlatformPanel from '../shared/components/PlatformPanel.vue'

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
      <span class="logo">▤</span>
      <nav>
        <RouterLink to="/search">🔍<i>搜索</i></RouterLink>
        <RouterLink to="/live">●<i>Live</i></RouterLink>
        <RouterLink to="/subs">🔔<i>订阅</i></RouterLink>
        <RouterLink to="/status">◉<i>状态</i></RouterLink>
      </nav>
      <PlatformNav @open="platformPanel=$event" />
    </header>
    <main><RouterView /></main>
    <PlatformPanel :view="platformPanel" plugin="logs" @switch="platformPanel=$event" @close="platformPanel=''" />
  </div>
</template>
<style scoped>
.layout{position:relative;display:flex;height:100vh}
header{width:46px;display:flex;flex-direction:column;align-items:center;gap:8px;padding:10px 0;border-right:1px solid var(--border);background:var(--panel)}
.logo{font-size:15px}
.layout.embedded .logo{display:none}
nav{display:flex;flex-direction:column;gap:4px;width:100%}
nav a{display:flex;flex-direction:column;align-items:center;gap:3px;padding:8px 0;width:100%;border-radius:8px;color:var(--muted);text-decoration:none;font-size:10px}
nav a.router-link-active{color:var(--text);background:var(--panel2)}
main{flex:1;min-width:0;display:flex;flex-direction:column}
.layout:not(.embedded) header{width:150px;align-items:flex-start;padding:14px 10px}
.layout:not(.embedded) nav{flex-direction:row;flex-wrap:wrap}
.layout:not(.embedded) nav a{flex-direction:row;width:auto;padding:5px 10px;font-size:12px;gap:6px}
.themebtn{margin-top:auto;font-size:14px;cursor:pointer;color:var(--muted);padding:4px}
.themebtn:hover{color:var(--text)}
</style>
