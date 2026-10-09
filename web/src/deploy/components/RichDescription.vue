<script setup>
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { prepareDescription, safeDescriptionURL } from '../richDescription.js'
import Modal from '../../shared/components/Modal.vue'
const props = defineProps({ content: { type: String, default: '' }, format: { type: String, default: '' }, sourceUrl: { type: String, default: '' }, title: { type: String, default: '需求说明' } })
const prepared = computed(() => prepareDescription(props.content, props.sourceUrl, props.format))
const body = ref(null), expanded = ref(false), overflowing = ref(false)
const preview = ref(null), imageLoading = ref(false), imageFailed = ref(false)
const previewIndex=ref(-1)
function changeImage(delta){const index=previewIndex.value+delta,next=prepared.value.images[index];if(next)openImage(next,index)}
const externalRecord = computed(() => safeDescriptionURL(props.sourceUrl))
function openImage(image,index) { previewIndex.value=index;imageLoading.value = true; imageFailed.value = false; preview.value = image }
let observer
function measure() {
  if (!expanded.value && body.value) overflowing.value = body.value.scrollHeight > body.value.clientHeight + 1
}
watch(() => [props.content, props.sourceUrl], async () => { expanded.value = false; preview.value = null; await nextTick(); measure() })
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
    <div v-if="prepared.images.length" class="description-images" aria-label="说明图片">
      <span>图片 {{ prepared.images.length }} 张 · 点击后加载</span>
      <button v-for="(image, index) in prepared.images" :key="index" type="button" @click="openImage(image,index)">查看图片 {{ index + 1 }}<template v-if="image.label"> · {{ image.label }}</template></button>
    </div>
  </section>
  <Modal :show="!!preview" title="图片预览" extra-wide @close="preview = null">
    <div v-if="preview" class="image-preview">
      <p v-if="imageLoading" role="status">图片加载中…</p>
      <p v-if="imageFailed" role="alert">图片加载失败，可能需要登录来源平台、网络不可达或附件已失效。可打开原图或外部记录查看。</p>
      <img :key="preview.url" v-show="!imageFailed" :src="preview.url" :alt="preview.label || '说明图片'" referrerpolicy="no-referrer" @load="imageLoading = false" @error="imageLoading = false; imageFailed = true" />
      <div class="image-actions"><template v-if="prepared.images.length>1"><button type="button" :disabled="previewIndex<=0" @click="changeImage(-1)">上一张</button><span>{{previewIndex+1}} / {{prepared.images.length}}</span><button type="button" :disabled="previewIndex>=prepared.images.length-1" @click="changeImage(1)">下一张</button></template><a :href="preview.url" target="_blank" rel="noopener noreferrer" referrerpolicy="no-referrer">打开原图</a><a v-if="externalRecord" :href="externalRecord" target="_blank" rel="noopener noreferrer">打开外部记录</a><button type="button" @click="preview = null">关闭预览</button></div>
    </div>
  </Modal>
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
.description-images { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; margin-top: 12px; max-height: 100px; overflow: auto; font-size: 12px; color: var(--muted); }
.description-images button { border: 1px solid var(--border); border-radius: 5px; padding: 4px 8px; background: var(--panel2); color: var(--brand); font: inherit; cursor: pointer; overflow-wrap: anywhere; max-width: 100%; }
.image-preview { text-align: center; }
.image-preview img { display: block; max-width: 100%; max-height: 60vh; object-fit: contain; margin: 0 auto; }
.image-preview p { overflow-wrap: anywhere; }
.image-actions { display: flex; gap: 16px; justify-content: center; align-items: center; flex-wrap: wrap; margin-top: 16px; }
.image-actions a, .image-actions button { color: var(--brand); font: inherit; font-size: 13px; }
.image-actions button { background: none; border: 0; cursor: pointer; }
</style>
