<script setup>
import {computed,ref,watch} from 'vue'
import {arthasDiagnostics,arthasStateLabel,arthasDiagnosticSections,arthasDiagnosticHighlights,arthasDiagnosticBars} from '../arthas.js'
import {Accordion,AccordionContent,AccordionItem,AccordionTrigger,Badge,Card,CardContent,Empty,EmptyDescription,EmptyHeader,Progress,Table,TableBody,TableCell,TableHead,TableHeader,TableRow} from '../../shared/shadcn-ui.js'
const props=defineProps({result:{type:Object,required:true}})
const title=computed(()=>arthasDiagnostics.find(d=>d.key===props.result.diagnostic)?.label||'诊断结果')
const sections=computed(()=>arthasDiagnosticSections(props.result))
const highlights=computed(()=>arthasDiagnosticHighlights(props.result))
const bars=computed(()=>arthasDiagnosticBars(props.result))
const rawOpen=ref([])
watch(()=>props.result,()=>rawOpen.value=[])
const sampledAt=computed(()=>{const date=new Date(props.result.sampled_at);return Number.isNaN(date.getTime())?'采样时间未返回':date.toLocaleString('zh-CN')})
</script>
<template>
 <section aria-label="常用诊断结果" aria-live="polite" class="min-w-0 space-y-4 rounded-xl border bg-card p-4 sm:p-5">
  <div class="flex flex-wrap items-start justify-between gap-3">
   <div class="min-w-0 space-y-1"><div class="flex items-center gap-2"><h2 class="font-semibold">{{title}}</h2><Badge :variant="result.state==='failed'?'destructive':'secondary'">{{arthasStateLabel(result.state)}}</Badge></div><p class="break-words text-sm text-muted-foreground">{{result.target?.name}} · {{result.target?.service}} / {{result.target?.environment}} · {{sampledAt}}</p></div>
   <span class="text-xs text-muted-foreground">单次采样</span>
  </div>
  <p class="text-sm" :class="result.state==='failed'?'text-destructive':'text-muted-foreground'">{{result.summary}}</p>
  <div v-if="highlights.length" class="grid gap-3 sm:grid-cols-2 xl:grid-cols-4" aria-label="关键指标">
   <Card v-for="metric in highlights" :key="metric.label" class="gap-0 py-3 shadow-none"><CardContent class="space-y-1 px-4"><p class="text-xs text-muted-foreground">{{metric.label}}</p><p class="break-words text-lg font-semibold tabular-nums">{{metric.value}}</p></CardContent></Card>
  </div>
  <div v-if="bars.length" class="space-y-4 rounded-lg border p-4" :aria-label="result.diagnostic==='memory'?'内存占用图':'热点线程 CPU 排行'">
   <h3 class="text-sm font-medium">{{result.diagnostic==='memory'?'内存占用':'热点线程 CPU 排行'}}<span class="ml-2 text-xs font-normal text-muted-foreground">本次采样</span></h3>
   <div v-for="bar in bars" :key="bar.id" class="space-y-1.5"><div class="flex items-baseline justify-between gap-3 text-sm"><span class="min-w-0 break-words">{{bar.label}}</span><strong class="shrink-0 tabular-nums">{{bar.percent.toFixed(1)}}%</strong></div><Progress :model-value="Math.min(100,bar.percent)" class="h-2" :aria-label="bar.label+' 占用率'" :aria-valuetext="bar.percent.toFixed(1)+'%'"/><p class="break-words text-xs text-muted-foreground">{{bar.detail}}</p></div>
  </div>
  <div v-if="sections.length" class="max-h-[65dvh] min-w-0 space-y-4 overflow-auto pr-1" aria-label="诊断指标明细" tabindex="0">
   <section v-for="section in sections" :key="section.id" class="min-w-0 space-y-2" :aria-label="section.label">
    <h3 class="flex items-center gap-2 text-sm font-medium">{{section.label}}<Badge variant="secondary">{{section.rows.length}} 项</Badge></h3>
    <div class="overflow-hidden rounded-lg border"><Table class="w-full table-fixed"><TableHeader><TableRow><TableHead class="w-2/5 sm:w-1/3">指标</TableHead><TableHead>采样值</TableHead></TableRow></TableHeader><TableBody><TableRow v-for="row in section.rows" :key="row.id"><TableCell class="whitespace-normal break-words align-top text-muted-foreground">{{row.label}}</TableCell><TableCell class="whitespace-pre-wrap break-words align-top tabular-nums [overflow-wrap:anywhere]">{{row.value}}</TableCell></TableRow></TableBody></Table></div>
   </section>
  </div>
  <Empty v-else class="border"><EmptyHeader><EmptyDescription>未返回可展示的指标，请展开原始输出核对；不能据此判断应用健康或没有阻塞线程。</EmptyDescription></EmptyHeader></Empty>
  <Accordion v-model="rawOpen" type="multiple"><AccordionItem value="raw"><AccordionTrigger>查看原始输出</AccordionTrigger><AccordionContent><pre class="max-h-[40dvh] overflow-auto whitespace-pre-wrap break-words rounded-md bg-muted p-3 text-xs [overflow-wrap:anywhere]">{{JSON.stringify(result.output,null,2)}}</pre></AccordionContent></AccordionItem></Accordion>
 </section>
</template>
