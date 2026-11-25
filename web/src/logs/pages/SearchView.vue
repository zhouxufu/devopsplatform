<script setup>

import {copyText} from '../../shared/clipboard.js'
import PageHeader from '../../shared/components/PageHeader.vue'
import {ref, onMounted, onBeforeUnmount, computed, inject, watch} from 'vue'
import {useRouter} from 'vue-router'
import {toast} from '../../shared/composables/toast.js'
import UiIcon from '../../shared/components/UiIcon.vue'
import BugEvidenceLink from '../../shared/components/BugEvidenceLink.vue'
import {evidenceLogInput} from '../../shared/bugEvidence.js'
const bugEvidence=ref(null),bugTitle=ref('')
function syncBugState(state){if(window.parent!==window)window.parent.postMessage({type:'workspace.dirty',...state},location.origin)}
function linkLogBug(hit,source=''){bugEvidence.value=evidenceLogInput(hit,source);bugTitle.value=(hit.service||'应用')+' 日志异常'}


import TimeRangeControl from '../../shell/TimeRangeControl.vue'
import HelpTip from '../../shared/components/HelpTip.vue'
import StatePanel from '../../shared/components/StatePanel.vue'
import {searchLogs} from '../externalSource.js'
import {QUERY_HELP} from '../queryHelp.js'
import {LOG_LEVELS} from '../levels.js'
import {DEFAULT_RECENT_MINUTES, inferRecentMinutes, recentRange, resolveTimeRange} from '../../shared/timeRange.js'

const router = useRouter()
const linkedRequest=router.currentRoute.value.query
const linkedRequestID=String(linkedRequest.request_id||'')
const linkedRange=[Number(linkedRequest.from),Number(linkedRequest.to)]
const hasLinkedEvidence=linkedRequest.evidence==='1'
const hasLinkedRange=(/^[a-f0-9]{32}$/.test(linkedRequestID)||hasLinkedEvidence)&&linkedRange.every(Number.isFinite)&&linkedRange[0]>0&&linkedRange[1]>linkedRange[0]&&linkedRange[1]-linkedRange[0]<=86400000
let linkedRangeApplied=false
const embedded = window.parent !== window
const services = inject('logServices', ref([]))
const platformContext = inject('platformContext', ref(null))
const contextOptions=inject('logContextOptions',ref({applications:[],environments:[]}))
const environments=ref([])
const applicationOptions=computed(()=>embedded?contextOptions.value.applications:services.value)
const environmentOptions=computed(()=>embedded?contextOptions.value.environments:environments.value)
const svc = ref(''), environment = ref(''), host = ref(''), lv = ref([]), kw = ref('')
if(/^[a-f0-9]{32}$/.test(linkedRequestID)){kw.value=linkedRequestID;svc.value=String(linkedRequest.service||'').slice(0,191);environment.value=String(linkedRequest.environment||'').slice(0,191)}
if(hasLinkedEvidence){svc.value=String(linkedRequest.service||'').slice(0,191);environment.value=String(linkedRequest.environment||'').slice(0,191);host.value=String(linkedRequest.host||'').slice(0,191);if(/^[A-Za-z0-9_-]{8,128}$/.test(String(linkedRequest.trace_id||'')))kw.value=String(linkedRequest.trace_id)}
const range = ref(recentRange()), recentMinutes = ref(DEFAULT_RECENT_MINUTES)
if(hasLinkedRange){range.value=linkedRange;recentMinutes.value=0}
const hits = ref([]), openIdx = ref(-1), hosts = ref([]), executedQuery = ref(''), hasMore = ref(false)
let searchRequest = 0
const source=inject('externalLogSource',ref('')),sources=ref([]),sourceName=ref('本地日志'),sourceUnavailable=ref(false),truncated=ref(false),nextCursor=ref('')
if(hasLinkedEvidence)source.value=String(linkedRequest.log_source||'').slice(0,191)
let activeController=null,discoveryTimer=null,querySnapshot=null
const sourceOptions=computed(()=>[{value:'',label:'本地日志'},...sources.value.map(v=>({value:v.manifest.id,label:v.manifest.name+(v.state==='running'?'':' · 未启用')})),...(sourceUnavailable.value&&!sources.value.some(v=>v.manifest.id===source.value)?[{value:source.value,label:sourceName.value+' · 不可用'}]:[])])
async function discoverSources(){try{const r=await fetch('/api/extensions');if(!r.ok)return;const list=await r.json();sources.value=(Array.isArray(list)?list:[]).filter(v=>v.manifest.capabilities.includes('logs.search.v1'));const selected=sources.value.find(v=>v.manifest.id===source.value);sourceUnavailable.value=!!source.value&&selected?.state!=='running';if(selected)sourceName.value=selected.manifest.name}catch{sourceUnavailable.value=!!source.value}}
function sourceChanged(){searchRequest++;activeController?.abort();hits.value=[];hosts.value=[];host.value='';hasMore.value=false;truncated.value=false;sourceName.value=sources.value.find(v=>v.manifest.id===source.value)?.manifest.name||'本地日志';searched.value=false;queryError.value='';loading.value=false;if(!source.value||(svc.value&&environment.value))search()}
async function loadMore(){if(!querySnapshot||!nextCursor.value||loading.value)return;const request=++searchRequest;activeController?.abort();activeController=new AbortController();loading.value=true;queryError.value='';try{const p=new URLSearchParams(querySnapshot.params);p.set('cursor',nextCursor.value);const d=await searchLogs(querySnapshot.source,p,activeController.signal);if(request!==searchRequest)return;hits.value=[...(d.hits||[]).reverse(),...hits.value];nextCursor.value=d.next_cursor||'';hasMore.value=!!d.has_more;truncated.value=!!d.truncated;loadHosts()}catch(e){if(request===searchRequest){queryError.value=e.message;toast(e.message,'error')}}finally{if(request===searchRequest)loading.value=false}}
const searched=ref(false)
const loading=ref(false),queryError=ref(''),agentState=ref(null)
async function inspectCollection(request){try{const response=await fetch('api/stats');if(!response.ok)return;const state=await response.json();if(request===searchRequest&&Array.isArray(state.agents))agentState.value=state.agents}catch{}}
function collectionSettings(){if(embedded)window.parent.postMessage({type:'platform.open',view:'resources'},location.origin);else router.push('/status')}
const emptyTitle=computed(()=>loading.value?'正在查询日志…':queryError.value?'日志查询未完成':source.value&&!searched.value?'选择应用和环境后查询':agentState.value?.length===0?'暂未发现采集 Agent':agentState.value?.length&&!agentState.value.some(a=>a.online)?'采集 Agent 当前离线':'当前条件下无匹配日志')
const emptyMessage=computed(()=>queryError.value|| (source.value&&!searched.value?'按插件中已配置的应用和环境查询，关键词为字面量文本。':agentState.value?.length===0?'平台暂未收到 Agent 上报，请核对本应用的采集配置、日志路径与接收地址。':agentState.value?.length&&!agentState.value.some(a=>a.online)?'历史日志仍可查询；请检查采集状态，或调整时间范围。':'可以调整时间、级别和关键词；没有匹配结果不代表应用运行正常。'))

async function loadHosts() { hosts.value = [...new Set(hits.value.map(h => h.host).filter(Boolean))] }

async function search() {
  const request = ++searchRequest
  activeController?.abort();activeController=new AbortController();const signal=activeController.signal
  loading.value=true;queryError.value='';agentState.value=null;truncated.value=false;nextCursor.value=''
  searched.value=true
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
    querySnapshot={source:source.value,params:p.toString()}
    let out = [], cursor = '', more = false
    for (let i = 0; i < (source.value?1:3); i++) {
      if (cursor) p.set('cursor', cursor)
      const d = await searchLogs(source.value,p,signal)
      if (request !== searchRequest) return
      truncated.value=!!d.truncated;nextCursor.value=d.next_cursor||''
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
    if(!out.length&&!source.value)inspectCollection(request)
    const el = document.querySelector('.stream'); if (el) el.scrollTop = el.scrollHeight
  } catch (e) {
    if (request === searchRequest){queryError.value=e.message||'日志查询失败';toast(queryError.value, 'error')}
  }finally{if(request===searchRequest)loading.value=false}
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
  activeController?.abort();activeController=new AbortController();const signal=activeController.signal
  loading.value=true;queryError.value='';agentState.value=null;truncated.value=false;nextCursor.value=''
  router.push('/search')
  const p = new URLSearchParams()
  if (svc.value) p.set('service', svc.value)
  if (environment.value) p.set('environment', environment.value)
  if (host.value) p.set('host', host.value)
  if (lv.value.length) p.set('level', lv.value.join(','))
  p.set('start', h.ts - 5000); p.set('end', h.ts + 5000); p.set('limit', '300')
  try {
    querySnapshot={source:source.value,params:p.toString()}
    const d = await searchLogs(source.value,p,signal)
    if (request !== searchRequest) return
    truncated.value=!!d.truncated;nextCursor.value=d.next_cursor||''
    hits.value = (d.hits || []).reverse()
    hasMore.value = !!d.has_more
    executedQuery.value = ''
    openIdx.value = -1
    toast('已加载该时刻 ±5s 的日志')
  } catch (e) {
    if (request === searchRequest){queryError.value=e.message||'日志查询失败';toast(queryError.value, 'error')}
  }finally{if(request===searchRequest)loading.value=false}
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
  const unchanged=svc.value===(ctx.service||'')&&environment.value===(ctx.environment||'')&&recentMinutes.value===Number(ctx.rangeMinutes||0)&&(recentMinutes.value>0||(range.value[0]===ctx.rangeStart&&range.value[1]===ctx.rangeEnd))
  if(unchanged&&searched.value){if(recentMinutes.value>0&&ctx.rangeStart&&ctx.rangeEnd)range.value=[ctx.rangeStart,ctx.rangeEnd];return true}
  if(svc.value!==(ctx.service||'')||environment.value!==(ctx.environment||''))host.value=''
  svc.value = ctx.service || ''
  environment.value = ctx.environment || ''
  if (ctx.rangeStart && ctx.rangeEnd && ctx.rangeEnd > ctx.rangeStart) {
    range.value = [ctx.rangeStart, ctx.rangeEnd]
    recentMinutes.value = Number(ctx.rangeMinutes) > 0 ? Number(ctx.rangeMinutes) : inferRecentMinutes(range.value)
  }
  if(hasLinkedRange&&!linkedRangeApplied){range.value=linkedRange;recentMinutes.value=0;svc.value=String(linkedRequest.service||'').slice(0,191);environment.value=String(linkedRequest.environment||'').slice(0,191);if(hasLinkedEvidence)host.value=String(linkedRequest.host||'').slice(0,191);linkedRangeApplied=true}
  search()
  return true
}
function updateContext() {
  // Query immediately; the shell echo only persists this window's context.
  if(embedded)window.parent.postMessage({type:'context.update',service:svc.value,environment:environment.value,range:[...range.value],rangeMinutes:recentMinutes.value},location.origin)
  search()
}
function scopeChanged(){host.value='';updateContext()}
function timeRangeChanged(value) { recentMinutes.value = inferRecentMinutes(value); updateContext() }
function useRecentRange(){recentMinutes.value=DEFAULT_RECENT_MINUTES;range.value=recentRange();updateContext()}

watch(platformContext, applyPlatformContext)
onMounted(() => { if(!embedded)fetch('api/environments').then(r=>r.ok?r.json():[]).then(v=>{environments.value=Array.isArray(v)?v.map(e=>typeof e==='string'?e:{value:e.name,label:e.display_name||e.name}):[]}).catch(()=>{});discoverSources();discoveryTimer=setInterval(discoverSources,10000);if (!applyPlatformContext(platformContext.value)) search() })
onBeforeUnmount(()=>{clearInterval(discoveryTimer);activeController?.abort();searchRequest++})

import OptionPicker from '../../shared/components/OptionPicker.vue'



import {Badge,Button,Field,FieldDescription,FieldError,FieldLabel,Input,Spinner} from '../../shared/shadcn-ui.js'

import {InputGroup,InputGroupInput,InputGroupAddon,InputGroupButton} from '../../shared/shadcn-ui.js'

import {XIcon} from '@lucide/vue'
</script>
<template><div class="log-page"><div class="log-page-heading"><PageHeader compact title="日志搜索" help="按应用、环境、机器与级别筛选日志；关键词支持日志查询表达式。"/></div><fieldset class="log-query-toolbar min-w-0" :class="{'embedded-query':embedded}" @submit="search"><Field class="query-service"><Input v-if="source" v-model="svc" aria-label="日志应用" placeholder="绑定的应用名称" @change="scopeChanged" @keydown.enter.prevent="scopeChanged"/><OptionPicker v-else v-model="svc" :options="applicationOptions" :retain-missing="false" :placeholder="svc?'历史应用：'+svc:'请选择'" all-label="全部应用" aria-label="日志应用" @change="scopeChanged"/></Field><Field class="query-environment"><Input v-if="source" v-model="environment" aria-label="日志环境" placeholder="绑定的环境" @change="scopeChanged" @keydown.enter.prevent="scopeChanged"/><OptionPicker v-else v-model="environment" :options="environmentOptions" all-label="全部环境" aria-label="日志环境" @change="scopeChanged"/></Field><div class="query-range"><TimeRangeControl v-model="range" @change="timeRangeChanged"/><Button variant="ghost" size="sm" :aria-pressed="recentMinutes===15" @click="useRecentRange">最近15分钟</Button><Badge v-if="recentMinutes>0" variant="secondary">动态</Badge></div><Field class="query-source"><OptionPicker v-model="source" :options="sourceOptions" :style="{width:'100%',maxWidth:'100%'}" aria-label="日志数据源" @change="sourceChanged"/></Field><Field class="query-host"><OptionPicker v-model="host" :options="hosts" all-label="全部机器" :style="{width:'100%',maxWidth:'100%'}" aria-label="日志机器" @change="search"/></Field><Field class="query-level"><OptionPicker v-model="lv" :options="LOG_LEVELS" multiple placeholder="全部级别" all-label="全部级别" :style="{width:'100%',maxWidth:'100%'}" aria-label="日志级别（可多选）" @change="search"/></Field><Field class="query-keyword"><InputGroup  class="min-w-0 "><InputGroupInput v-model="kw" aria-label="日志关键词" :placeholder="source?'输入字面量关键词':'搜索日志，例如 timeout OR refused'" @keydown.enter.prevent="search"/><InputGroupAddon align="inline-end"><HelpTip :text="QUERY_HELP"/><InputGroupButton v-if="kw" :disabled="false" size="icon-xs" aria-label="清空输入" @click="kw='' "><XIcon/></InputGroupButton></InputGroupAddon></InputGroup></Field><Button @click="search" class="query-submit" variant="default" size="default" type="button" :disabled="(loading)"><Spinner v-if="loading"/><UiIcon name="search" size="small"/>查询</Button></fieldset><small v-if="source" class="source-scope">当前数据源仅支持历史查询；最多 1000 条，应用、环境按服务端绑定校验。</small><div class="stream"><template v-for="(h, i) in hits" :key="h.ts + '-' + i"><div :class="{ open: openIdx === i }" @click="openIdx = openIdx === i ? -1 : i" class="lrow"><span class="ts">{{ fmtTs(h.ts) }}</span><span class="sv" :title="h.service+(h.environment?' · '+h.environment:'')">{{ h.service }}<small v-if="h.environment"> · {{ h.environment }}</small></span><span :class="'lv-' + (h.level || 'INFO')" class="badge">{{ h.level || '-' }}</span><span v-html="msgHTML[h.ts + '|' + h.message]" class="msg"></span></div><div v-if="openIdx === i" class="detail"><div class="full">{{ h.message }}</div><div class="acts"><Button @click.stop="linkLogBug(h,source)" variant="outline" size="sm" type="button">关联 Bug</Button><Button @click.stop="copyText(h.message)" variant="outline" size="default" type="button">复制</Button><Button @click.stop="ctxSearch(h)" variant="outline" size="default" type="button">上下文 ±5s</Button><Button v-if="h.trace_id" @click.stop="openTrace(h.trace_id)" variant="outline" size="default" type="button">调用链 →</Button></div></div></template><StatePanel v-if="!hits.length" :title="emptyTitle" :message="loading?'正在读取当前应用与环境的日志，请稍候。':emptyMessage" :loading="loading" :error="!!queryError"><template v-if="!loading"><Button v-if="queryError" @click="search" variant="outline" size="default" type="button">重试查询</Button><Button v-else-if="!source" @click="router.push('/status')" variant="outline" size="default" type="button">查看采集状态</Button><Button v-if="!source" @click="collectionSettings" variant="outline" size="default" type="button">检查采集配置</Button></template></StatePanel></div><div v-if="hasMore" class="more-results"><Button @click="loadMore" variant="outline" size="default" type="button" :disabled="(loading)"><Spinner v-if="loading"/>加载更早日志</Button></div><div class="statusbar">{{sourceName}} · {{loading?'正在查询…':queryError?'本次查询失败；当前内容不代表最新结果':source&&!searched?'尚未查询':'已加载 '+hits.length+' 条'}}{{ hasMore ? '，仍有更多结果' : '' }}{{truncated?' · 结果达到上限，请缩小范围':''}}{{sourceUnavailable?' · 数据源已停用或不可用':''}} · 点击行展开 · 黄色为查询命中位置</div></div><BugEvidenceLink :input="bugEvidence" :title="bugTitle" @state="syncBugState" @close="bugEvidence=null"/></template>
<style scoped>.log-page{display:flex;flex-direction:column;height:100%;min-height:0;min-width:0}.log-page-heading{padding:24px 24px 0;flex-shrink:0}.log-page-heading :deep([data-slot="page-header"]){margin-bottom:20px}@media(max-width:767px){.log-page-heading{padding:16px 16px 0}}.source-scope{padding:8px 24px;color:var(--muted-foreground);font-size:.75rem}.more-results{padding:12px 24px}
.log-query-toolbar{display:grid;grid-template-columns:minmax(0,14rem) repeat(2,minmax(0,10rem)) minmax(0,1fr) auto;align-items:center;gap:.75rem;padding:0 1.5rem 1rem;background:var(--background);border-bottom:1px solid var(--border);flex-shrink:0;min-width:0}.log-query-toolbar>*{min-width:0}.query-service{grid-column:1}.query-environment{grid-column:2}.query-range{grid-column:3/-1;display:flex;flex-wrap:wrap;justify-content:flex-end;align-items:center;gap:.5rem}.query-source{grid-column:1}.query-host{grid-column:2}.query-level{grid-column:3}.query-keyword{grid-column:4}.query-submit{grid-column:5;white-space:nowrap}
@media(max-width:900px){.log-query-toolbar{grid-template-columns:repeat(3,minmax(0,1fr)) auto}.query-service,.query-environment{grid-row:1}.query-range{grid-row:2;grid-column:1/-1;justify-content:flex-start}.query-keyword{grid-column:1/4}.query-submit{grid-column:4}}
@media(max-width:560px){.log-query-toolbar{grid-template-columns:repeat(2,minmax(0,1fr));padding:0 1rem 1rem;gap:.625rem}.query-service,.query-source{grid-column:1}.query-environment,.query-host{grid-column:2}.query-range{grid-column:1/-1}.query-level{grid-column:1/-1}.query-keyword{grid-column:1/-1}.query-submit{grid-column:1/-1}}
.stream{flex:1;overflow-y:auto;font-size:.875rem}
.lrow{font-family:var(--mono);display:grid;grid-template-columns:112px 180px 60px minmax(0,1fr);gap:12px;padding:6px 24px;cursor:pointer;align-items:baseline}
.lrow:hover{background:var(--panel2)}
.lrow.open{background:var(--accent);box-shadow:inset 3px 0 0 var(--brand)}
.ts{color:var(--muted-foreground)}.sv{color:var(--muted-foreground);overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.sv small{color:var(--dim)}
.msg{white-space:nowrap;overflow:hidden;text-overflow:ellipsis;color:var(--foreground)}
.msg :deep(mark){padding:0 2px;border-radius:var(--radius);background:#ffe58f;color:#5c3b00}
.badge{text-align:center;border-radius:var(--radius);font-size:.75rem;padding:1px 0;font-weight:600}
.lv-ERROR{background:rgba(248,81,73,.15);color:var(--error)}
.lv-WARN{background:rgba(210,153,34,.15);color:var(--warn)}
.lv-INFO{background:rgba(88,166,255,.12);color:var(--info)}
.lv-DEBUG{background:rgba(139,148,158,.12);color:var(--debug,#8b949e)}
.tid{color:var(--trace);cursor:pointer;text-decoration:underline dotted}
.detail{background:var(--muted);border-top:1px solid var(--border);border-bottom:1px solid var(--border);padding:12px 14px 12px 18px;font-family:var(--mono);font-size:.75rem}
.full{font-family:var(--mono);white-space:pre-wrap;word-break:break-all;color:var(--foreground);line-height:1.7}
.acts{margin-top:10px;display:flex;gap:8px;flex-wrap:wrap}@media(max-width:760px){.lrow{grid-template-columns:100px 60px minmax(0,1fr);gap:8px;padding:6px 12px}.lrow .sv{display:none}.stream{font-size:12px}}

.statusbar{min-height:32px;height:auto;line-height:1.5;gap:4px;flex-wrap:wrap;border-top:1px solid var(--border);background:var(--panel);display:flex;align-items:center;padding:6px 24px;color:var(--dim);font-size:.75rem;flex-shrink:0}</style>

<style scoped>.stream{background:var(--panel)}.lrow{line-height:1.7}</style>
