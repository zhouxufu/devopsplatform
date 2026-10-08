<script setup>
import { ref, onMounted, onUnmounted, nextTick, inject, watch } from 'vue'
import { useWS } from '../../shared/composables/ws.js'
import { serviceNames } from '../services.js'
import Sel from '../../shared/components/Sel.vue'
import { ElButton, ElInput } from '../../shared/components/ui.js'
import HelpTip from '../../shared/components/HelpTip.vue'
import { toast } from '../../shared/composables/toast.js'
import { QUERY_HELP } from '../queryHelp.js'
import { LOG_LEVELS } from '../levels.js'

const svc = ref(''), environment = ref(''), lv = ref([]), kw = ref('')
const platformContext = inject('platformContext', ref(null))
const services = ref([])
const hits = ref([]), pending = ref(0), connected = ref(false)
const streamEl = ref(null)
let ws = null

const fmtTs = ts => { const d = new Date(ts); return d.toLocaleTimeString('zh-CN', { hour12: false }) + '.' + String(d.getMilliseconds()).padStart(3, '0') }
const atBottom = () => { const el = streamEl.value; return el.scrollHeight - el.scrollTop - el.clientHeight < 60 }

function push(h) {
  hits.value.push(h)
  if (hits.value.length > 2000) hits.value.splice(0, hits.value.length - 2000)
  if (atBottom()) nextTick(() => { streamEl.value.scrollTop = streamEl.value.scrollHeight })
  else pending.value++
}
function jump() { nextTick(() => { streamEl.value.scrollTop = streamEl.value.scrollHeight }); pending.value = 0 }
function onScroll() { if (atBottom()) pending.value = 0 }

function connect() {
  const p = new URLSearchParams()
  if (svc.value) p.set('service', svc.value)
  if (environment.value) p.set('environment', environment.value)
  if (lv.value.length) p.set('level', lv.value.join(','))
  if (kw.value.trim()) p.set('q', kw.value.trim())
  p.set('replay', '200')
  const base = location.pathname.replace(/\/ui.*$/, '')
  ws?.close()
  hits.value = []
  ws = useWS(`${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}${base}/api/tail?` + p, {
    onMessage: d => {
      const m = JSON.parse(d)
      if (m.type === 'error') { toast(m.error || '实时过滤语法错误', 'error'); return false }
      if (m.type === 'log' && m.hit) push(m.hit)
    },
    onOpen: () => (connected.value = true),
    onClose: () => (connected.value = false),
  })
}
onMounted(async () => {
  services.value = serviceNames(await fetch('api/services').then(r => r.json()).catch(() => ({})))
  if (platformContext.value) { svc.value = platformContext.value.service || ''; environment.value = platformContext.value.environment || '' }
  connect()
})
watch(platformContext, ctx => {
  if (!ctx) return
  const next = ctx.service || '', nextEnvironment = ctx.environment || ''
  if (next !== svc.value || nextEnvironment !== environment.value) { svc.value = next; environment.value = nextEnvironment; connect() }
})
onUnmounted(() => ws?.close())
</script>
<template>
  <div style="display:flex;flex-direction:column;height:100%">
    <div style="display:flex;gap:8px;align-items:center;padding:10px 14px;border-bottom:1px solid var(--border);background:var(--panel);flex-shrink:0;flex-wrap:wrap">
      <Sel v-model="svc" :options="services" all-label="全部服务" width="130px" @change="connect" />
      <span v-if="environment" style="color:var(--brand);font-size:11px">环境 {{ environment }}</span>
      <Sel v-model="lv" :options="LOG_LEVELS" multiple all-label="全部级别（可多选）" width="220px" @change="connect" />
      <ElInput v-model="kw" placeholder="实时过滤，例如 level:ERROR timeout*" style="flex:1;min-width:180px" clearable @keydown.enter="connect" />
      <HelpTip :text="QUERY_HELP" align="right" />
      <ElButton type="primary" @click="connect">应用</ElButton>
      <span :style="{ color: connected ? 'var(--ok)' : 'var(--dim)', fontSize: 11 }">{{ connected ? '● Live' : '○ 未连接' }}</span>
    </div>
    <div ref="streamEl" class="stream" @scroll="onScroll">
      <div v-for="(h, i) in hits" :key="h.ts + '-' + i" class="lrow">
        <span class="ts">{{ fmtTs(h.ts) }}</span>
        <span class="sv">{{ h.service }}<small v-if="h.environment"> · {{ h.environment }}</small></span>
        <span class="badge" :class="'lv-' + (h.level || 'INFO')">{{ h.level || '-' }}</span>
        <span class="msg">{{ h.message }}</span>
      </div>
      <div v-if="!hits.length" class="empty">等待日志…</div>
    </div>
    <button v-if="pending" class="jump" @click="jump()">↓ 回到最新 · {{ pending }} 条新日志</button>
    <div style="height:28px;border-top:1px solid var(--border);background:var(--panel);display:flex;align-items:center;padding:0 14px;color:var(--dim);font-size:11px">已接收 {{ hits.length }} 条 · 上滚暂停跟随</div>
  </div>
</template>
<style scoped>
.stream{flex:1;overflow-y:auto;font-family:var(--mono);font-size:12px;position:relative}
.lrow{display:grid;grid-template-columns:160px 90px 60px 1fr;gap:10px;padding:3px 14px}
.lrow:hover{background:var(--panel2)}
.ts{color:var(--muted)}.sv{color:var(--muted)}.sv small{color:var(--dim)}
.msg{white-space:nowrap;overflow:hidden;text-overflow:ellipsis;color:#c9d1d9}
.badge{text-align:center;border-radius:4px;font-size:10px;padding:1px 0;font-weight:600}
.lv-ERROR{background:rgba(248,81,73,.15);color:var(--error)}
.lv-WARN{background:rgba(210,153,34,.15);color:var(--warn)}
.lv-INFO{background:rgba(88,166,255,.12);color:var(--info)}
.jump{position:absolute;bottom:44px;right:20px;background:var(--brand);color:#fff;border:none;border-radius:16px;padding:6px 14px;font-size:12px;cursor:pointer;box-shadow:0 4px 12px rgba(0,0,0,.4)}
</style>
