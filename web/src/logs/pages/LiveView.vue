<script setup>

import PageHeader from '../../shared/components/PageHeader.vue'
import {ref, onMounted, onUnmounted, nextTick, inject, watch} from 'vue'
import {useRouter} from 'vue-router'
import LogEventRow from '../LogEventRow.vue'
import {logRowKey} from '../logReading.js'
import {evidenceWorkspace} from '../../shared/bugEvidence.js'
import {useWS} from '../../shared/composables/ws.js'
import {useLogScopeOptions} from '../useLogScopeOptions.js'
import BugEvidenceLink from '../../shared/components/BugEvidenceLink.vue'
import {evidenceLogInput} from '../../shared/bugEvidence.js'
const bugEvidence=ref(null),bugTitle=ref('')
function syncBugState(state){if(window.parent!==window)window.parent.postMessage({type:'workspace.dirty',...state},location.origin)}
function linkLogBug(hit,source=''){bugEvidence.value=evidenceLogInput(hit,source);bugTitle.value=(hit.service||'应用')+' 日志异常'}


import HelpTip from '../../shared/components/HelpTip.vue'
import {toast} from '../../shared/composables/toast.js'
import {QUERY_HELP} from '../queryHelp.js'
import {LOG_LEVELS} from '../levels.js'

const svc = ref(''), environment = ref(''), lv = ref([]), kw = ref('')
const platformContext = inject('platformContext', ref(null))
const {applicationOptions, environmentOptions} = useLogScopeOptions()
const hits = ref([]), pending = ref(0), connected = ref(false), wrap=ref(false)
const streamEl = ref(null)
const openHit = ref(null), paused = ref(false)
const router = useRouter()
const limit = 2000
let backlog = [], connection = 0
let ws = null

const atBottom = () => { const el = streamEl.value; return el.scrollHeight - el.scrollTop - el.clientHeight < 60 }

function push(h) {
  if (paused.value) {
    backlog.push(h)
    if (backlog.length > limit) backlog.shift()
    pending.value++
    return
  }
  hits.value.push(h)
  if (hits.value.length > limit) hits.value.shift()
  followLatest()
}
let scheduled = false
function followLatest() {
  if (scheduled) return
  scheduled = true
  nextTick(() => {
    scheduled = false
    if (!paused.value && streamEl.value) streamEl.value.scrollTop = streamEl.value.scrollHeight
  })
}
function jump() {
  hits.value = hits.value.concat(backlog).slice(-limit)
  backlog = []
  openHit.value = null
  pending.value = 0
  paused.value = false
  followLatest()
}
function onScroll() { if (!paused.value && !atBottom()) paused.value = true }
function toggleHit(hit) {
  paused.value = true
  openHit.value = openHit.value === hit ? null : hit
}
function viewContext(hit) {
  const query = {evidence:'1', service:hit.service || '', environment:hit.environment || '', host:hit.host || '', from:String(hit.ts-5000), to:String(Number(hit.ts)+5000)}
  if (window.parent === window) router.push({path:'/search',query})
  else evidenceWorkspace('logs','#/search?'+new URLSearchParams(query))
}

function connect() {
  const current = ++connection
  const p = new URLSearchParams()
  if (svc.value) p.set('service', svc.value)
  if (environment.value) p.set('environment', environment.value)
  if (lv.value.length) p.set('level', lv.value.join(','))
  if (kw.value.trim()) p.set('q', kw.value.trim())
  p.set('replay', '200')
  const base = location.pathname.replace(/\/ui.*$/, '')
  ws?.close()
  hits.value = []
  backlog = []
  paused.value = false
  pending.value = 0
  openHit.value = null
  connected.value = false
  ws = useWS(`${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}${base}/api/tail?` + p, {
    onMessage: d => {
      if (current !== connection) return
      const m = JSON.parse(d)
      if (m.type === 'error') { toast(m.error || '实时过滤语法错误', 'error'); return false }
      if (m.type === 'log' && m.hit) push(m.hit)
    },
    onOpen: () => { if (current === connection) connected.value = true },
    onClose: () => { if (current === connection) connected.value = false },
  })
}
function scopeChanged() {
  if (window.parent !== window) {
    window.parent.postMessage({type:'context.update', service:svc.value, environment:environment.value}, location.origin)
  }
  connect()
}
onMounted(() => {
  if (platformContext.value) { svc.value = platformContext.value.service || ''; environment.value = platformContext.value.environment || '' }
  connect()
})
watch(platformContext, ctx => {
  if (!ctx) return
  const next = ctx.service || '', nextEnvironment = ctx.environment || ''
  if (next !== svc.value || nextEnvironment !== environment.value) { svc.value = next; environment.value = nextEnvironment; connect() }
})
onUnmounted(() => { connection++; ws?.close() })

import OptionPicker from '../../shared/components/OptionPicker.vue'



import {Button,Input} from '../../shared/shadcn-ui.js'

import {InputGroup,InputGroupInput,InputGroupAddon,InputGroupButton} from '../../shared/shadcn-ui.js'

import {XIcon,WrapTextIcon} from '@lucide/vue'
</script>
<template><div class="log-page"><div class="log-page-heading"><PageHeader compact title="实时日志" help="查看本地采集的实时日志，按服务、环境、级别和关键词过滤。"/></div>
<div class="live-toolbar">
  <OptionPicker v-model="svc" aria-label="日志应用" :options="applicationOptions" all-label="全部服务" class="w-32" @change="scopeChanged"/>
  <OptionPicker v-model="environment" aria-label="日志环境" :options="environmentOptions" all-label="全部环境" class="w-32" @change="scopeChanged"/>
  <OptionPicker v-model="lv" :options="LOG_LEVELS" multiple placeholder="全部级别（可多选）" all-label="全部级别（可多选）" class="w-44" @change="connect"/>
  <InputGroup style="flex:1;min-width:180px" class="min-w-0"><InputGroupInput v-model="kw" placeholder="实时过滤，例如 level:ERROR timeout*" @keydown.enter="connect"/><InputGroupAddon align="inline-end"><InputGroupButton v-if="kw" size="icon-xs" aria-label="清空输入" @click="kw=''"><XIcon/></InputGroupButton></InputGroupAddon></InputGroup>
  <HelpTip :text="QUERY_HELP" align="right"/><Button @click="connect" variant="default" size="default" type="button">应用</Button><Button @click="wrap=!wrap" :aria-pressed="wrap" :variant="wrap?'secondary':'outline'" size="default" type="button"><WrapTextIcon/>换行</Button><span :style="{ color: connected ? 'var(--ok)' : 'var(--dim)', fontSize: '.75rem' }">{{ connected ? '● Live' : '○ 未连接' }}</span>
</div>
<div ref="streamEl" @scroll="onScroll" class="stream">
  <LogEventRow v-for="hit in hits" :key="logRowKey(hit)" :hit="hit" :expanded="openHit===hit" :service="svc" :environment="environment" :wrap="wrap" @toggle="toggleHit(hit)" @context="viewContext(hit)" @bug="linkLogBug(hit)"/>
  <div v-if="!hits.length" class="p-4 text-sm text-muted-foreground">等待日志…</div>
</div>
<footer class="flex min-h-9 shrink-0 flex-wrap items-center justify-between gap-2 border-t px-4 py-1 text-xs text-muted-foreground">
  <span>本地采集 · 当前 {{hits.length}} 条 · {{paused?'已暂停跟随':'跟随最新'}}<template v-if="pending>limit"> · 恢复后显示最近 {{limit}} 条</template></span>
  <Button v-if="paused" size="sm" variant="outline" @click="jump">回到最新<span v-if="pending"> · {{pending}} 条新日志</span></Button>
</footer></div>
<BugEvidenceLink :input="bugEvidence" :title="bugTitle" @state="syncBugState" @close="bugEvidence=null"/></template>
<style scoped>.live-toolbar{display:flex;gap:12px;align-items:center;padding:0 24px 16px;border-bottom:1px solid var(--border);flex-shrink:0;flex-wrap:wrap;background:var(--background)}@media(max-width:767px){.live-toolbar{padding:0 16px 16px}.live-toolbar :deep([data-slot="input-group"]){flex-basis:100%!important}}.log-page{position:relative;display:flex;flex-direction:column;height:100%;min-height:0;min-width:0}.log-page-heading{padding:24px 24px 0;flex-shrink:0}.log-page-heading :deep([data-slot="page-header"]){margin-bottom:20px}@media(max-width:767px){.log-page-heading{padding:16px 16px 0}}.stream{flex:1;overflow-y:auto;font-family:var(--mono);font-size:.75rem;position:relative}
</style>
