<script setup>
import { ref, onMounted, computed, inject, watch } from 'vue'
import { useRouter } from 'vue-router'
import { toast } from '../../shared/composables/toast.js'
import Sel from '../../shared/components/Sel.vue'
import { ElButton, ElInput } from '../../shared/components/ui.js'
import RangePicker from '../../shared/components/RangePicker.vue'
import HelpTip from '../../shared/components/HelpTip.vue'
import { QUERY_HELP } from '../queryHelp.js'
import { LOG_LEVELS } from '../levels.js'
import { DEFAULT_RECENT_MINUTES, inferRecentMinutes, recentRange, resolveTimeRange } from '../../shared/timeRange.js'

const router = useRouter()
const embedded = window.parent !== window
const services = inject('logServices', ref([]))
const platformContext = inject('platformContext', ref(null))
const svc = ref(''), environment = ref(''), host = ref(''), lv = ref([]), kw = ref('')
const range = ref(recentRange()), recentMinutes = ref(DEFAULT_RECENT_MINUTES)
const hits = ref([]), openIdx = ref(-1), hosts = ref([]), executedQuery = ref(''), hasMore = ref(false)
let searchRequest = 0

async function loadHosts() { hosts.value = [...new Set(hits.value.map(h => h.host).filter(Boolean))] }

async function search() {
  const request = ++searchRequest
  const query = kw.value.trim()
  const p = new URLSearchParams()
  if (svc.value) p.set('service', svc.value)
  if (environment.value) p.set('environment', environment.value)
  if (host.value) p.set('host', host.value)
  if (lv.value.length) p.set('level', lv.value.join(','))
  if (query) p.set('q', query)
  const effectiveRange = resolveTimeRange(range.value, recentMinutes.value)
  if (effectiveRange?.length === 2) {
    range.value = effectiveRange
    p.set('start', effectiveRange[0])
    p.set('end', effectiveRange[1])
  } // 清空范围 = 全部时间
  try {
    let out = [], cursor = '', more = false
    for (let i = 0; i < 3; i++) {
      if (cursor) p.set('cursor', cursor)
      const response = await fetch('api/search?' + p)
      const d = await response.json().catch(() => ({}))
      if (request !== searchRequest) return
      if (!response.ok) throw new Error(d.error || '日志查询失败')
      out = out.concat(d.hits || [])
      cursor = d.next_cursor
      more = !!d.has_more
      if (!cursor || out.length >= 600) break
    }
    executedQuery.value = query
    hasMore.value = more
    hits.value = out
    hits.value.reverse()
    openIdx.value = -1
    loadHosts()
    const el = document.querySelector('.stream'); if (el) el.scrollTop = el.scrollHeight
  } catch (e) {
    if (request === searchRequest) toast(e.message || '日志查询失败', 'error')
  }
}
const fmtTs = ts => { const d = new Date(ts); return d.toLocaleTimeString('zh-CN', { hour12: false }) + '.' + String(d.getMilliseconds()).padStart(3, '0') }
const esc = s => s.replace(/&/g, '&amp;').replace(/</g, '&lt;')
function positiveTerms(q) {
  return [...q.matchAll(/"([^"]*)"|(\S+)/g)]
    .map(m => (m[1] ?? m[2] ?? '').trim())
    .filter(t => t && !t.startsWith('-'))
    .sort((a, b) => b.length - a.length)
}
function matchingExcerpt(message, terms) {
  const lines = String(message || '').split(/\r?\n/)
  if (!terms.length) return lines[0] || ''
  const lowerTerms = terms.map(t => t.toLocaleLowerCase())
  const line = lines.find(v => lowerTerms.some(t => v.toLocaleLowerCase().includes(t)))
  if (!line) return lines[0] || ''
  const lower = line.toLocaleLowerCase()
  const at = Math.min(...lowerTerms.map(t => lower.indexOf(t)).filter(i => i >= 0))
  const start = Math.max(0, at - 90), end = Math.min(line.length, at + 230)
  return (start ? '…' : '') + line.slice(start, end) + (end < line.length ? '…' : '')
}
function highlightHTML(value, terms) {
  if (!terms.length) return esc(value)
  const source = terms.map(t => t.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('|')
  const re = new RegExp(source, 'ig')
  let html = '', last = 0, match
  while ((match = re.exec(value)) !== null) {
    html += esc(value.slice(last, match.index)) + '<mark>' + esc(match[0]) + '</mark>'
    last = match.index + match[0].length
  }
  return html + esc(value.slice(last))
}
const msgHTML = computed(() => {
  const map = {}
  const terms = positiveTerms(executedQuery.value)
  hits.value.forEach(h => {
    map[h.ts + '|' + h.message] = highlightHTML(matchingExcerpt(h.message, terms), terms)
      .replace(/\b([0-9a-f]{16,32})\b/g, `<span class="tid" data-tid="$1">$1</span>`)
  })
  return map
})
async function ctxSearch(h) {
  const request = ++searchRequest
  router.push('/search')
  const p = new URLSearchParams()
  if (svc.value) p.set('service', svc.value)
  if (environment.value) p.set('environment', environment.value)
  if (host.value) p.set('host', host.value)
  if (lv.value.length) p.set('level', lv.value.join(','))
  p.set('start', h.ts - 5000); p.set('end', h.ts + 5000); p.set('limit', '300')
  try {
    const response = await fetch('api/search?' + p)
    const d = await response.json().catch(() => ({}))
    if (request !== searchRequest) return
    if (!response.ok) throw new Error(d.error || '日志查询失败')
    hits.value = (d.hits || []).reverse()
    hasMore.value = !!d.has_more
    executedQuery.value = ''
    openIdx.value = -1
    toast('已加载该时刻 ±5s 的日志')
  } catch (e) {
    if (request === searchRequest) toast(e.message || '日志查询失败', 'error')
  }
}
function openTrace(traceId) {
  const hash = '#/trace?trace=' + encodeURIComponent(traceId)
  if (window.parent !== window) {
    window.parent.postMessage({ type: 'workspace.open', plugin: 'monitor', hash }, location.origin)
  } else {
    window.open(location.origin + '/api/plugins/monitor/ui' + hash, '_blank')
  }
}
function applyPlatformContext(ctx) {
  if (!ctx) return false
  svc.value = ctx.service || ''
  environment.value = ctx.environment || ''
  if (ctx.rangeStart && ctx.rangeEnd && ctx.rangeEnd > ctx.rangeStart) {
    range.value = [ctx.rangeStart, ctx.rangeEnd]
    recentMinutes.value = Number(ctx.rangeMinutes) > 0 ? Number(ctx.rangeMinutes) : inferRecentMinutes(range.value)
  }
  search()
  return true
}
function timeRangeChanged(value) { recentMinutes.value = inferRecentMinutes(value); search() }
watch(platformContext, applyPlatformContext)
onMounted(() => { if (!applyPlatformContext(platformContext.value)) search() })
</script>
<template>
  <div style="display:flex;flex-direction:column;height:100%">
    <div class="toolbar">
      <Sel v-if="!embedded" v-model="svc" :options="services" all-label="全部应用" width="140px" @change="search()" />
      <span v-if="!embedded && environment" style="color:var(--brand);font-size:11px">环境 {{ environment }}</span>
      <Sel v-model="host" :options="hosts" all-label="全部机器" width="130px" @change="search()" />
      <Sel v-model="lv" :options="LOG_LEVELS" multiple all-label="全部级别（可多选）" width="220px" @change="search()" />
      <RangePicker v-if="!embedded" v-model="range" @change="timeRangeChanged" width="225px" />
      <ElInput v-model="kw" placeholder='搜索日志，例如 timeout OR refused' style="flex:1;min-width:200px" clearable @keydown.enter="search()" />
      <HelpTip :text="QUERY_HELP" align="right" />
      <ElButton type="primary" @click="search()">搜索</ElButton>
    </div>
    <div class="stream">
      <template v-for="(h, i) in hits" :key="h.ts + '-' + i">
        <div class="lrow" :class="{ open: openIdx === i }" @click="openIdx = openIdx === i ? -1 : i">
          <span class="ts">{{ fmtTs(h.ts) }}</span>
          <span class="sv">{{ h.service }}<small v-if="h.environment"> · {{ h.environment }}</small></span>
          <span class="badge" :class="'lv-' + (h.level || 'INFO')">{{ h.level || '-' }}</span>
          <span class="msg" v-html="msgHTML[h.ts + '|' + h.message]"></span>
        </div>
        <div v-if="openIdx === i" class="detail">
          <div class="full">{{ h.message }}</div>
          <div class="acts">
            <button @click.stop="navigator.clipboard?.writeText(h.message); toast('已复制')">复制</button>
            <button @click.stop="ctxSearch(h)">上下文 ±5s</button>
            <button v-if="h.trace_id" @click.stop="openTrace(h.trace_id)">调用链 →</button>
          </div>
        </div>
      </template>
      <div v-if="!hits.length" class="empty">无匹配日志</div>
    </div>
    <div class="statusbar">已加载 {{ hits.length }} 条{{ hasMore ? '，仍有更多结果' : '' }} · 点击行展开 · 黄色为查询命中位置</div>
  </div>
</template>
<style scoped>
.toolbar{display:flex;gap:8px;align-items:center;padding:10px 14px;border-bottom:1px solid var(--border);background:var(--panel);flex-shrink:0;flex-wrap:wrap}
.seg{display:flex;border:1px solid var(--border);border-radius:6px;overflow:hidden}
.seg button{background:transparent;border:none;color:var(--muted);padding:5px 10px;border-right:1px solid var(--border);cursor:pointer}
.seg button:last-child{border-right:none}
.seg button.on{background:#1f2937;color:var(--text)}
.stream{flex:1;overflow-y:auto;font-family:var(--mono);font-size:12px}
.lrow{display:grid;grid-template-columns:160px 90px 60px 1fr;gap:10px;padding:3px 14px;cursor:pointer;align-items:baseline}
.lrow:hover{background:var(--panel2)}
.lrow.open{background:#1a2233;box-shadow:inset 3px 0 0 var(--brand)}
.ts{color:var(--muted)}.sv{color:var(--muted)}.sv small{color:var(--dim)}
.msg{white-space:nowrap;overflow:hidden;text-overflow:ellipsis;color:#c9d1d9}
.msg :deep(mark){padding:0 2px;border-radius:2px;background:#ffe58f;color:#5c3b00}
.badge{text-align:center;border-radius:4px;font-size:10px;padding:1px 0;font-weight:600}
.lv-ERROR{background:rgba(248,81,73,.15);color:var(--error)}
.lv-WARN{background:rgba(210,153,34,.15);color:var(--warn)}
.lv-INFO{background:rgba(88,166,255,.12);color:var(--info)}
.lv-DEBUG{background:rgba(139,148,158,.12);color:var(--debug,#8b949e)}
.tid{color:var(--trace);cursor:pointer;text-decoration:underline dotted}
.detail{background:#11161d;border-top:1px solid var(--border);border-bottom:1px solid var(--border);padding:12px 14px 12px 18px;font-family:var(--mono);font-size:12px}
.full{white-space:pre-wrap;word-break:break-all;color:#c9d1d9;line-height:1.7}
.acts{margin-top:10px;display:flex;gap:8px}
.acts button{background:var(--panel2);color:var(--muted)}
.statusbar{height:28px;border-top:1px solid var(--border);background:var(--panel);display:flex;align-items:center;padding:0 14px;color:var(--dim);font-size:11px;flex-shrink:0}
</style>
