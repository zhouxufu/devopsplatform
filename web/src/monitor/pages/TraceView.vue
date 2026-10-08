<script setup>
import { ref, inject, onMounted, watch } from 'vue'
import { ElButton, ElInput } from '../../shared/components/ui.js'
import Sel from '../../shared/components/Sel.vue'
import { useRoute } from 'vue-router'

const route = useRoute()
const curSvc = inject('curSvc', ref(''))
const curEnvironment = inject('curEnvironment', ref(''))
const traceId = ref(''), spans = ref([]), recent = ref([]), instances = ref([]), instance = ref('')
const COLORS = ['#2f81f7', '#3fb950', '#d29922', '#f85149', '#a371f7', '#39c5cf']
const color = s => { let h = 0; for (const c of s) h = (h * 31 + c.charCodeAt(0)) >>> 0; return COLORS[h % COLORS.length] }

async function loadTrace(id) {
  if (!id) return
  spans.value = (await fetch('api/otlp/trace?' + new URLSearchParams({ id, environment: curEnvironment.value || '', instance: instance.value })).then(r => r.json())) ?? []
}
async function loadRecent() {
  recent.value = (await fetch('api/otlp/traces/recent?' + new URLSearchParams({ service: curSvc.value || '', environment: curEnvironment.value || '', instance: instance.value })).then(r => r.json())) ?? []
}
async function loadInstances() {
  instances.value = (await fetch('api/instances?' + new URLSearchParams({ service: curSvc.value || '', environment: curEnvironment.value || '' })).then(r => r.json()).catch(() => [])) ?? []
  if (instance.value && !instances.value.includes(instance.value)) instance.value = ''
  await loadRecent()
}
onMounted(() => {
  loadInstances()
  const id = typeof route.query.trace === 'string' ? route.query.trace : ''
  if (id) { traceId.value = id; loadTrace(id) }
})
watch(() => route.query.trace, id => {
  if (typeof id === 'string' && id && id !== traceId.value) {
    traceId.value = id
    loadTrace(id)
  }
})
watch(curEnvironment, () => {
  spans.value = []
  loadInstances()
  if (traceId.value) loadTrace(traceId.value)
})
watch(curSvc, loadInstances)
watch(instance, () => {
  spans.value = []
  loadRecent()
  if (traceId.value) loadTrace(traceId.value)
})

const t0 = computed(() => spans.value.length ? Math.min(...spans.value.map(s => s.start_ms)) : 0)
const t1 = computed(() => spans.value.length ? Math.max(...spans.value.map(s => s.start_ms + s.dur_ms)) : 0)
const total = computed(() => Math.max(t1.value - t0.value, 1))
const depth = s => { let d = 0, p = s; const by = {}; spans.value.forEach(x => by[x.span_id] = x); while (p?.parent_id && by[p.parent_id]) { d++; p = by[p.parent_id] } return d }
const errs = computed(() => spans.value.filter(s => s.status === 2).length)
import { computed } from 'vue'
</script>
<template>
  <div style="padding:16px">
    <div style="display:flex;gap:8px;margin-bottom:12px">
      <Sel v-model="instance" :options="instances" all-label="全部实例" width="170px" />
      <ElInput v-model="traceId" placeholder="traceId" style="flex:1;font-family:var(--mono)" clearable @keydown.enter="loadTrace(traceId)" />
      <ElButton type="primary" @click="loadTrace(traceId)">查询</ElButton>
    </div>
    <template v-if="spans.length">
      <div style="display:grid;grid-template-columns:repeat(4,1fr);gap:12px;margin-bottom:12px">
        <div class="card"><div class="t">Trace</div><div class="v" style="font-size:13px;font-family:var(--mono)">{{ traceId.slice(0, 20) }}…</div></div>
        <div class="card"><div class="t">Span 数</div><div class="v">{{ spans.length }}</div></div>
        <div class="card"><div class="t">总耗时</div><div class="v">{{ total }}<small style="font-size:12px;color:var(--dim)">ms</small></div></div>
        <div class="card"><div class="t">状态</div><div class="v" :style="{ color: errs ? 'var(--error)' : 'var(--ok)' }">{{ errs ? errs + ' 错误' : '正常' }}</div></div>
      </div>
      <div class="card">
        <div style="border-bottom:1px solid var(--border);padding-bottom:4px;margin-bottom:4px;display:flex;color:var(--dim);font-size:10px;font-family:var(--mono)">
          <div style="width:280px">span</div><div style="flex:1;position:relative"><span style="position:absolute;left:0">0</span><span style="position:absolute;right:0">{{ total }}ms</span></div>
        </div>
        <div v-for="s in spans" :key="s.span_id" style="display:flex;align-items:center;height:26px;border-bottom:1px dashed #21262d" :title="`${s.service} / ${s.instance || '未知实例'} · ${s.dur_ms}ms`">
          <div style="width:280px;flex-shrink:0;padding-left:depth(s) * 14;overflow:hidden;white-space:nowrap;text-overflow:ellipsis;font-size:11px;font-family:var(--mono)">
            <span :style="{ color: color(s.service) }">●</span> {{ s.name }} <span style="color:var(--dim)">{{ s.dur_ms }}ms</span><span v-if="s.err" style="color:var(--error)"> ⛔{{ s.err.slice(0, 60) }}</span>
          </div>
          <div style="flex:1;position:relative;height:100%">
            <div :style="{ position:'absolute', top:6, left:((s.start_ms - t0) / total * 100)+'%', width:Math.max(s.dur_ms / total * 100, 0.6)+'%', height:13, borderRadius:3, background:color(s.service), opacity:.85, outline: s.status===2 ? '2px solid var(--error)' : 'none' }"></div>
          </div>
        </div>
      </div>
    </template>
    <div v-else class="empty">输入 traceId 或从日志紫色链接跳入</div>
    <h3 style="font-size:14px;margin:16px 0 10px">最近 Trace（2h，错误优先）</h3>
    <div v-for="t in recent" :key="t.environment + ':' + t.trace_id" style="display:flex;gap:14px;padding:5px 0;border-bottom:1px solid #21262d;font-size:12px;cursor:pointer" @click="traceId = t.trace_id; loadTrace(t.trace_id)">
      <span style="font-family:var(--mono);color:var(--trace)">{{ t.trace_id.slice(0, 20) }}…</span>
      <span style="color:var(--brand)">{{ t.environment || '未标记环境' }}</span>
      <span style="color:var(--dim)">{{ t.spans }} spans</span>
      <span :style="{ color: t.errors ? 'var(--error)' : 'var(--dim)' }">{{ t.errors ? t.errors + ' 错误' : '—' }}</span>
      <span style="color:var(--dim);margin-left:auto">{{ t.total_ms }}ms · {{ new Date(t.start_ms).toLocaleTimeString('zh-CN', { hour12: false }) }}</span>
    </div>
  </div>
</template>
<style scoped>
.card .t{color:var(--muted);font-size:12px}
.card .v{font-size:20px;font-weight:600;font-family:var(--mono);margin-top:2px}
</style>
