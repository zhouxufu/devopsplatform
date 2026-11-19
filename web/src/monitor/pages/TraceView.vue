<script setup>
import {ref, inject, onMounted, onBeforeUnmount, watch} from 'vue'


import PageHeader from '../../shared/components/PageHeader.vue'
import StatePanel from '../../shared/components/StatePanel.vue'

import {useRoute} from 'vue-router'

const route = useRoute()
const curSvc = inject('curSvc', ref(''))
const curEnvironment = inject('curEnvironment', ref(''))
const traceId = ref(''), spans = ref([]), recent = ref([]), instances = ref([]), instance = ref('')
const COLORS = ['#2f81f7', '#3fb950', '#d29922', '#f85149', '#a371f7', '#39c5cf']
const color = s => { let h = 0; for (const c of String(s||'')) h = (h * 31 + c.charCodeAt(0)) >>> 0; return COLORS[h % COLORS.length] }

const loading=ref(false),recentLoading=ref(false),traceError=ref(''),recentError=ref(''),searched=ref(false),loadedTraceId=ref(''),loadedTraceEnvironment=ref('')
let traceSequence=0,recentSequence=0,disposed=false
async function read(url){const r=await fetch(url),data=await r.json();if(!r.ok)throw Error(data.error||'读取失败，请稍后重试');if(!Array.isArray(data))throw Error('响应格式无效，请重试');return data}
async function loadTrace(id,environment=curEnvironment.value||''){
 id=String(id||'').trim();if(!id)return
 const sequence=++traceSequence;loading.value=true;traceError.value='';searched.value=true;spans.value=[]
 try{const data=await read('api/otlp/trace?'+new URLSearchParams({id,environment,instance:instance.value}));if(!disposed&&sequence===traceSequence){spans.value=data;loadedTraceId.value=id;loadedTraceEnvironment.value=environment}}
 catch(e){if(!disposed&&sequence===traceSequence)traceError.value=e.message}
 finally{if(!disposed&&sequence===traceSequence)loading.value=false}
}
async function loadRecent(){
 const sequence=++recentSequence;recentLoading.value=true;recentError.value='';recent.value=[]
 try{const data=await read('api/otlp/traces/recent?'+new URLSearchParams({service:curSvc.value||'',environment:curEnvironment.value||'',instance:instance.value}));if(!disposed&&sequence===recentSequence)recent.value=data}
 catch(e){if(!disposed&&sequence===recentSequence)recentError.value=e.message}
 finally{if(!disposed&&sequence===recentSequence)recentLoading.value=false}
}
function openRecent(t){traceId.value=t.trace_id;loadTrace(t.trace_id,t.environment||curEnvironment.value||'')}
onBeforeUnmount(()=>{disposed=true;traceSequence++;recentSequence++})
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
const depth = s => { let d = 0, p = s; const by = {}; spans.value.forEach(x => by[x.span_id] = x); while (p?.parent_id && by[p.parent_id] && d<spans.value.length) { d++; p = by[p.parent_id] } return d }
const errs = computed(() => spans.value.filter(s => s.status === 2).length)
import {computed} from 'vue'

import OptionPicker from '../../shared/components/OptionPicker.vue'

import {copyText} from '../../shared/clipboard.js'


import {Badge,Button,Card,CardAction,CardContent,CardFooter,CardHeader,CardTitle,Empty,EmptyContent,EmptyDescription,EmptyHeader,Input,Spinner,Table,TableBody,TableCell,TableHead,TableHeader,TableRow} from '../../shared/shadcn-ui.js'

import {InputGroup,InputGroupInput,InputGroupAddon,InputGroupButton} from '../../shared/shadcn-ui.js'

import {XIcon} from '@lucide/vue'
</script>
<template><div class="trace-page"><PageHeader title="调用链"/><div class="trace-toolbar"><OptionPicker v-model="instance" :options="instances" all-label="全部实例" :style="{width:'170px',maxWidth:'100%'}" aria-label="调用链实例"/><InputGroup  class="min-w-0 "><InputGroupInput v-model="traceId" placeholder="输入 Trace ID" @keydown.enter="loadTrace(traceId)" aria-label="Trace ID"/><InputGroupAddon align="inline-end"><InputGroupButton v-if="traceId" :disabled="false" size="icon-xs" aria-label="清空输入" @click="traceId='' "><XIcon/></InputGroupButton></InputGroupAddon></InputGroup><Button @click="loadTrace(traceId)" variant="default" size="default" type="button" :disabled="(!traceId.trim())||(loading)"><Spinner v-if="loading"/>查询</Button></div><Card v-if="spans.length" class="trace-summary"><CardContent class="min-w-0"><dl class="trace-summary-fields"><div class="min-w-0 space-y-1"><dt class="text-sm text-muted-foreground">Trace ID</dt><dd class="min-w-0 break-words"><span class="min-w-0 break-words">{{loadedTraceId}}<Button variant="ghost" size="xs" aria-label="复制内容" @click="copyText(Array.from($event.currentTarget.parentElement.childNodes).filter(node=>node!==$event.currentTarget).map(node=>node.textContent).join(''))">复制</Button></span></dd></div><div class="min-w-0 space-y-1"><dt class="text-sm text-muted-foreground">查询环境</dt><dd class="min-w-0 break-words">{{loadedTraceEnvironment||'全部环境'}}</dd></div><div class="min-w-0 space-y-1"><dt class="text-sm text-muted-foreground">Span 数</dt><dd class="min-w-0 break-words">{{spans.length}}</dd></div><div class="min-w-0 space-y-1"><dt class="text-sm text-muted-foreground">总耗时</dt><dd class="min-w-0 break-words">{{total}} ms</dd></div><div class="min-w-0 space-y-1"><dt class="text-sm text-muted-foreground">状态</dt><dd class="min-w-0 break-words"><Badge :variant="(errs?'red':'green')==='red'?'destructive':'secondary'">{{errs?errs+' 个错误':'未发现错误状态'}}</Badge></dd></div></dl></CardContent></Card><Card :loading="loading" class="trace-waterfall"><CardHeader><CardTitle>调用链时序</CardTitle></CardHeader><CardContent class="min-w-0"><StatePanel v-if="traceError" title="调用链读取失败" :message="traceError" error><Button @click="loadTrace(traceId)" variant="outline" size="default" type="button">重试</Button></StatePanel><div v-else-if="spans.length" tabindex="0" aria-label="调用链时序，可横向滚动" class="waterfall-scroll"><div class="waterfall-content"><div class="waterfall-axis"><span>Span / 耗时</span><div><span>0</span><span>{{total}} ms</span></div></div><div v-for="s in spans" :key="s.span_id" :title="`${s.service} / ${s.instance||'未知实例'} · ${s.dur_ms}ms${s.err?' · '+s.err:''}`" class="waterfall-row"><div :style="{paddingLeft:Math.min(depth(s),12)*14+'px'}" class="waterfall-name" :title="s.name"><span :style="{color:color(s.service)}">●</span> {{s.name}} <small>{{s.dur_ms}} ms</small><Badge v-if="s.status===2" :variant="(&quot;red&quot;)==='red'?'destructive':'secondary'">错误</Badge></div><div class="waterfall-track"><div :style="{left:((s.start_ms-t0)/total*100)+'%',width:Math.max(s.dur_ms/total*100,0.6)+'%',background:color(s.service),outline:s.status===2?'2px solid var(--error)':'none'}"/></div></div></div></div><Empty v-else><EmptyHeader><EmptyDescription>{{searched?'当前范围没有该调用链数据':'输入 Trace ID，或从日志中打开调用链'}}</EmptyDescription></EmptyHeader></Empty></CardContent></Card><Card class="recent-traces"><CardHeader><CardTitle>最近调用链</CardTitle><CardAction><span class="trace-caption">最近 2 小时 · 错误优先</span></CardAction></CardHeader><CardContent class="min-w-0"><StatePanel v-if="recentError" title="最近调用链读取失败" :message="recentError" error><Button @click="loadRecent" variant="outline" size="default" type="button">重试</Button></StatePanel><div v-else class="console-table min-w-0 max-w-full"><div v-if="recentLoading" class="flex items-center gap-2 py-2" role="status"><Spinner/>正在读取</div><div class="max-w-full overflow-auto rounded-lg border" :style="{maxHeight:({x:920}).y?({x:920}).y+'px':undefined}"><Table class="table-fixed min-w-[var(--table-min-width)]" :style="{'--table-min-width':(({x:920}).x||({x:920}).minWidth||920)+'px'}"><TableHeader><TableRow><TableHead :style="{width:(330)+'px'}">Trace ID</TableHead><TableHead :style="{width:(150)+'px'}">环境</TableHead><TableHead :style="{width:(100)+'px'}">Span 数</TableHead><TableHead :style="{width:(100)+'px'}">错误</TableHead><TableHead :style="{width:(100)+'px'}">耗时</TableHead><TableHead :style="{width:(140)+'px'}">时间</TableHead></TableRow></TableHeader><TableBody><template v-for="(record,rowIndex) in (recent||[])" :key="typeof (t=>t.environment+':'+t.trace_id)==='function'?(t=>t.environment+':'+t.trace_id)(record):record[t=>t.environment+':'+t.trace_id]??rowIndex"><TableRow><TableCell class="align-top"><div class="min-w-0 break-words" :class="{truncate:!!(&quot;&quot;)}"><template v-for="{record:t} in [{record,rowIndex}]"><Button @click="openRecent(t)" variant="ghost" size="sm" type="button">{{t.trace_id}}</Button></template></div></TableCell><TableCell class="align-top"><div class="min-w-0 break-words" :class="{truncate:!!(&quot;&quot;)}"><template v-for="{record:t} in [{record,rowIndex}]">{{t.environment||'未标记环境'}}</template></div></TableCell><TableCell class="align-top"><div class="min-w-0 break-words" :class="{truncate:!!(false)}">{{record["spans"]}}</div></TableCell><TableCell class="align-top"><div class="min-w-0 break-words" :class="{truncate:!!(false)}"><template v-for="{record:t} in [{record,rowIndex}]"><Badge :variant="(t.errors?'red':'gray')==='red'?'destructive':'secondary'">{{t.errors||0}}</Badge></template></div></TableCell><TableCell class="align-top"><div class="min-w-0 break-words" :class="{truncate:!!(false)}"><template v-for="{record:t} in [{record,rowIndex}]">{{t.total_ms}} ms</template></div></TableCell><TableCell class="align-top"><div class="min-w-0 break-words" :class="{truncate:!!(false)}"><template v-for="{record:t} in [{record,rowIndex}]">{{new Date(t.start_ms).toLocaleTimeString('zh-CN',{hour12:false})}}</template></div></TableCell></TableRow></template><TableRow v-if="!(recent||[]).length"><TableCell :colspan="6"><Empty><EmptyHeader><EmptyDescription>{{"当前范围暂无调用链记录"}}</EmptyDescription></EmptyHeader></Empty></TableCell></TableRow></TableBody></Table></div></div></CardContent></Card></div></template>
<style scoped>.trace-page{min-width:0}.trace-toolbar{display:flex;align-items:center;gap:12px;flex-wrap:wrap;margin-bottom:20px}.trace-summary,.trace-waterfall{margin-bottom:20px}.trace-summary-fields{display:grid;grid-template-columns:minmax(260px,2fr) repeat(4,minmax(0,1fr));gap:16px}.trace-toolbar :deep([data-slot=input-group]){flex:1;min-width:220px;width:auto}.trace-caption{color:var(--muted-foreground);font-size:.75rem}.waterfall-scroll{overflow:auto;max-height:min(55dvh,560px)}.waterfall-content{min-width:720px}.waterfall-axis,.waterfall-row{display:grid;grid-template-columns:300px minmax(0,1fr);gap:16px}.waterfall-axis{position:sticky;top:0;z-index:1;background:var(--card);color:var(--muted-foreground);font-size:.75rem;border-bottom:1px solid var(--border);padding-bottom:12px}.waterfall-axis>div{display:flex;justify-content:space-between}.waterfall-row{align-items:center;min-height:40px;border-bottom:1px dashed var(--border)}.waterfall-name{min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font:.875rem/1.6 var(--mono)}.waterfall-name small{color:var(--muted-foreground);font-size:.75rem}.waterfall-track{position:relative;height:40px}.waterfall-track>div{position:absolute;top:13px;height:14px;border-radius:var(--radius)}.recent-traces{min-width:0}@media(max-width:760px){}</style>
