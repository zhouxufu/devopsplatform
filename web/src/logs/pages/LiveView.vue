<script setup>

import PageHeader from '../../shared/components/PageHeader.vue'
import {ref, onMounted, onUnmounted, nextTick, inject, watch} from 'vue'
import {useWS} from '../../shared/composables/ws.js'
import {serviceNames} from '../services.js'
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
const services = ref([])
const hits = ref([]), pending = ref(0), connected = ref(false), wrap=ref(false)
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

import OptionPicker from '../../shared/components/OptionPicker.vue'



import {Button,Input} from '../../shared/shadcn-ui.js'

import {InputGroup,InputGroupInput,InputGroupAddon,InputGroupButton} from '../../shared/shadcn-ui.js'

import {XIcon,BugIcon,WrapTextIcon} from '@lucide/vue'
</script>
<template><div class="log-page"><div class="log-page-heading"><PageHeader compact title="实时日志" help="查看本地采集的实时日志，按服务、级别和关键词过滤。"/></div><div class="live-toolbar"><OptionPicker v-model="svc" :options="services" all-label="全部服务" :style="{width:'130px',maxWidth:'100%'}" @change="connect"/><span v-if="environment" style="color:var(--brand);font-size:.75rem">环境 {{ environment }}</span><OptionPicker v-model="lv" :options="LOG_LEVELS" multiple placeholder="全部级别（可多选）" all-label="全部级别（可多选）" :style="{width:'220px',maxWidth:'100%'}" @change="connect"/><InputGroup style="flex:1;min-width:180px" class="min-w-0 "><InputGroupInput v-model="kw" placeholder="实时过滤，例如 level:ERROR timeout*" @keydown.enter="connect"/><InputGroupAddon align="inline-end"><InputGroupButton v-if="kw" :disabled="false" size="icon-xs" aria-label="清空输入" @click="kw='' "><XIcon/></InputGroupButton></InputGroupAddon></InputGroup><HelpTip :text="QUERY_HELP" align="right"/><Button @click="connect" variant="default" size="default" type="button">应用</Button><Button @click="wrap=!wrap" :aria-pressed="wrap" :variant="wrap?'secondary':'outline'" size="default" type="button"><WrapTextIcon/>换行</Button><span :style="{ color: connected ? 'var(--ok)' : 'var(--dim)', fontSize: '.75rem' }">{{ connected ? '● Live' : '○ 未连接' }}</span></div><div ref="streamEl" @scroll="onScroll" class="stream" :class="{'wrap-lines':wrap}"><div v-for="(h, i) in hits" :key="h.ts + '-' + i" class="lrow"><span class="ts">{{ fmtTs(h.ts) }}</span><span class="sv" :title="h.service+(h.environment?' · '+h.environment:'')">{{ h.service }}<small v-if="h.environment"> · {{ h.environment }}</small></span><span :class="'lv-' + (h.level || 'INFO')" class="badge">{{ h.level || '-' }}</span><span class="msg" :title="h.message">{{ h.message }}</span><div class="log-bug-action"><Button aria-label="从日志关联 Bug" @click.stop="linkLogBug(h)" variant="ghost" size="icon-sm" type="button" title="关联 Bug"><BugIcon/></Button></div></div><div v-if="!hits.length" class="empty">等待日志…</div></div><Button v-if="pending" @click="jump()" class="jump" variant="outline" size="default" type="button">↓ 回到最新 · {{ pending }} 条新日志</Button><div style="height:28px;border-top:1px solid var(--border);background:var(--panel);display:flex;align-items:center;padding:0 14px;color:var(--dim);font-size:.75rem">本地采集 · 已接收 {{ hits.length }} 条 · 上滚暂停跟随</div></div><BugEvidenceLink :input="bugEvidence" :title="bugTitle" @state="syncBugState" @close="bugEvidence=null"/></template>
<style scoped>.live-toolbar{display:flex;gap:12px;align-items:center;padding:0 24px 16px;border-bottom:1px solid var(--border);flex-shrink:0;flex-wrap:wrap;background:var(--background)}@media(max-width:767px){.live-toolbar{padding:0 16px 16px}.live-toolbar :deep([data-slot="input-group"]){flex-basis:100%!important}}.log-page{position:relative;display:flex;flex-direction:column;height:100%;min-height:0;min-width:0}.log-page-heading{padding:24px 24px 0;flex-shrink:0}.log-page-heading :deep([data-slot="page-header"]){margin-bottom:20px}@media(max-width:767px){.log-page-heading{padding:16px 16px 0}}.stream{flex:1;overflow-y:auto;font-family:var(--mono);font-size:.75rem;position:relative}
.lrow{display:grid;grid-template-columns:112px 180px 60px minmax(0,1fr) auto;gap:12px;padding:6px 24px;align-items:start}
.lrow:hover{background:var(--panel2)}
.ts{color:var(--muted-foreground)}.sv{min-width:0;color:var(--muted-foreground);overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.sv small{color:var(--dim)}
.msg{white-space:nowrap;overflow:hidden;text-overflow:ellipsis;color:var(--text)}
.stream.wrap-lines .msg{white-space:pre-wrap;overflow-wrap:anywhere}.lrow{line-height:1.7}.badge{text-align:center;border-radius:var(--radius);font-size:.75rem;padding:1px 0;font-weight:600}
.lv-ERROR{background:rgba(248,81,73,.15);color:var(--error)}
.lv-WARN{background:rgba(210,153,34,.15);color:var(--warn)}
.lv-INFO{background:rgba(88,166,255,.12);color:var(--info)}
.jump{position:absolute;bottom:44px;right:20px}</style>

<style scoped>.log-bug-action{opacity:0}.lrow:hover .log-bug-action,.lrow:focus-within .log-bug-action{opacity:1}@media(hover:none){.log-bug-action{opacity:1}}</style>
