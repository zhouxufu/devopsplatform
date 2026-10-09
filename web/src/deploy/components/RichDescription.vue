<script setup>
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { prepareDescription } from '../richDescription.js'
const props = defineProps({ content: { type: String, default: '' }, sourceUrl: { type: String, default: '' }, title: { type: String, default: '需求说明' } })
const prepared = computed(() => prepareDescription(props.content, props.sourceUrl))
const body = ref(null), expanded = ref(false), overflowing = ref(false)
let observer
function measure() {
  if (!expanded.value && body.value) overflowing.value = body.value.scrollHeight > body.value.clientHeight + 1
}
watch(() => [props.content, props.sourceUrl], async () => { expanded.value = false; await nextTick(); measure() })
watch(expanded, async () => { await nextTick(); measure() })
onMounted(() => { observer = new ResizeObserver(measure); observer.observe(body.value); measure() })
onBeforeUnmount(() => observer?.disconnect())
</script>

<template>
  <section class="description-panel">
    <div class="description-heading">
      <h3>{{ title }}</h3>
    </div>
    <div ref="body" class="description-body" :class="{ expanded, plain: prepared.plain }" v-html="prepared.html || '暂无说明'" />
    <button v-if="overflowing || expanded" class="description-toggle" type="button" :aria-expanded="expanded" @click="expanded = !expanded">{{ expanded ? '收起说明' : '展开完整说明' }}</button>
  </section>
</template>

<style scoped>
.description-panel { margin: 16px 0; padding: 16px 20px; border: 1px solid var(--border); border-radius: 10px; background: var(--panel); min-width: 0; }
.description-heading h3 { margin: 0 0 12px; font-size: 14px; }
.description-body { max-height: 160px; overflow: hidden; overflow-wrap: anywhere; color: var(--text); font-size: 13px; line-height: 1.7; }
.description-body.plain { white-space: pre-wrap; }
.description-body.expanded { max-height: min(55vh, 560px); overflow: auto; }
.description-body :deep(p), .description-body :deep(blockquote), .description-body :deep(ul), .description-body :deep(ol) { margin: 0 0 10px; }
.description-body :deep(h1), .description-body :deep(h2), .description-body :deep(h3), .description-body :deep(h4), .description-body :deep(h5), .description-body :deep(h6) { font-size: 15px; margin: 12px 0 8px; }
.description-body :deep(a) { color: var(--brand); }
.description-body :deep(pre) { white-space: pre-wrap; background: var(--panel2); padding: 12px; border-radius: 6px; }
.description-body :deep(code) { font-family: monospace; white-space: pre-wrap; }
.description-body :deep(blockquote) { border-left: 3px solid var(--border); padding-left: 12px; color: var(--muted); }
.description-body :deep(table) { display: block; max-width: 100%; overflow-x: auto; border-collapse: collapse; }
.description-body :deep(td), .description-body :deep(th) { border: 1px solid var(--border); padding: 6px 10px; }
.description-toggle { margin-top: 10px; padding: 0; background: none; border: 0; color: var(--brand); font: inherit; font-size: 12px; cursor: pointer; }
</style>
