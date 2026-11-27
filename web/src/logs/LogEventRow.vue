<script setup>
import {computed, ref, watch} from 'vue'
import {ChevronRightIcon} from '@lucide/vue'
import {Button} from '../shared/shadcn-ui.js'
import {copyText} from '../shared/clipboard.js'
import {evidenceWorkspace} from '../shared/bugEvidence.js'
import {logPreview, logTime} from './logReading.js'

const props = defineProps({hit:{type:Object,required:true}, expanded:Boolean, service:String, environment:String, pattern:RegExp, wrap:Boolean})
defineEmits(['toggle','bug','context'])
const source = computed(() => [props.hit.service !== props.service && props.hit.service, props.hit.environment !== props.environment && props.hit.environment].filter(Boolean).join(' · '))
const raw = computed(() => props.hit.message || '')
const preview = computed(() => logPreview(raw.value, props.pattern))
const pieces = computed(() => props.pattern ? preview.value.split(props.pattern) : [preview.value])
const fullText = ref(null)
const match = computed(() => props.expanded && props.pattern ? props.pattern.exec(raw.value) : null)
watch([() => props.expanded, match], () => {
  const block = fullText.value
  if (!block) return
  const mark = block?.querySelector('mark')
  if (mark) block.scrollTop += mark.getBoundingClientRect().top - block.getBoundingClientRect().top - block.clientHeight / 3
  const stream = block.closest('.stream')
  if (stream) {
    const event = block.closest('.log-event').getBoundingClientRect()
    const viewport = stream.getBoundingClientRect()
    stream.scrollTop += Math.min(Math.max(0, event.bottom - viewport.bottom), Math.max(0, event.top - viewport.top))
  }
}, {flush:'post'})
</script>

<template>
  <div class="log-event">
    <button type="button" class="event-row" :aria-expanded="expanded" @click="$emit('toggle')">
      <span class="event-time">{{ logTime(hit.ts) }}</span>
      <span class="event-level" :data-level="hit.level">{{ hit.level || '—' }}</span>
      <span class="event-message" :class="{'wrap-message':wrap}"><template v-for="(piece,index) in pieces" :key="index"><mark v-if="index%2">{{piece}}</mark><template v-else>{{piece}}</template></template></span>
      <span v-if="source" class="event-source" :title="source">{{ source }}</span>
      <ChevronRightIcon aria-hidden="true" class="size-3.5 shrink-0 text-muted-foreground" :class="{'rotate-90':expanded}"/>
    </button>
    <section v-if="expanded" class="event-detail" aria-label="日志详情">
      <div class="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
        <span>记录时间 {{logTime(hit.ts,true)}}</span><span v-if="hit.service">应用 {{hit.service}}</span><span v-if="hit.environment">环境 {{hit.environment}}</span>
        <span v-if="hit.host">主机 {{hit.host}}</span><span v-if="hit.path">路径 {{hit.path}}</span><span v-if="hit.trace_id">Trace ID {{hit.trace_id}}</span>
      </div>
      <pre ref="fullText" class="event-original" tabindex="0" aria-label="日志原文"><template v-if="match">{{raw.slice(0,match.index)}}<mark>{{match[0]}}</mark>{{raw.slice(match.index+match[0].length)}}</template><template v-else>{{raw}}</template></pre>
      <div class="flex flex-wrap gap-2">
        <Button variant="outline" size="sm" @click="copyText(raw)">复制原文</Button>
        <Button v-if="hit.ts>0" variant="outline" size="sm" @click="$emit('context')">查看前后 5 秒</Button>
        <Button v-if="hit.trace_id" variant="outline" size="sm" @click="evidenceWorkspace('monitor','#/trace?trace='+encodeURIComponent(hit.trace_id))">查看链路</Button>
        <Button variant="ghost" size="sm" @click="$emit('bug')">关联 Bug</Button>
      </div>
    </section>
  </div>
</template>

<style scoped>
.event-row{display:flex;align-items:baseline;gap:.55rem;width:100%;min-width:0;padding:.3rem 1rem;text-align:left;cursor:pointer;font-family:var(--mono,monospace);font-size:.8125rem;line-height:1.55}
.event-row:hover,.event-row[aria-expanded=true]{background:var(--muted)}
.event-row:focus-visible{outline:2px solid var(--ring);outline-offset:-2px}
.event-time,.event-source{color:var(--muted-foreground);font-size:.75rem}
.event-time,.event-level{flex:none;white-space:nowrap}
.event-level{width:5ch;text-align:center;font-size:.6875rem;font-weight:600;border-radius:.25rem;background:var(--muted);color:var(--muted-foreground)}
.event-level[data-level=ERROR],.event-level[data-level=FATAL]{color:var(--destructive)}
.event-level[data-level=WARN]{background:var(--warn-soft,var(--muted));color:var(--warn)}
.event-message,.event-source{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.event-message{min-width:0;flex:1;color:var(--foreground)}
.event-source{max-width:min(18%,20ch)}
.wrap-message,.event-original{white-space:pre-wrap;overflow-wrap:anywhere}
.event-detail{display:grid;gap:.65rem;padding:.75rem 1rem;background:var(--muted);border-block:1px solid var(--border);overflow-wrap:anywhere}
.event-original{max-height:min(42vh,24rem);overflow:auto;margin:0;padding:.65rem .8rem;background:var(--background);border-radius:var(--radius);font-family:var(--mono,monospace);font-size:.8125rem;line-height:1.55}
mark{background:#ffe58f;color:#523900}
@media(max-width:620px){.event-row{display:grid;grid-template-columns:auto auto minmax(0,1fr) auto;gap:0 .5rem;padding:.35rem .75rem}.event-message{grid-row:2;grid-column:1/-1}.event-source{grid-column:3;grid-row:1;max-width:100%}.event-row>svg{grid-column:4;grid-row:1}.event-detail{padding:.65rem .75rem}}
</style>
