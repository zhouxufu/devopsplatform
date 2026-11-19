<script setup>


import {computed, nextTick, onBeforeUnmount, onMounted, ref, watch} from 'vue'
import '../../shared/sqlHighlight.css'
import {prepareDescription, safeDescriptionURL} from '../richDescription.js'
const props = defineProps({ content: { type: String, default: '' }, format: { type: String, default: '' }, sourceUrl: { type: String, default: '' }, title: { type: String, default: '需求说明' },imageResolver:Function,full:Boolean,compact:Boolean,thumbnails:Boolean })
const prepared = computed(() => {
 const value=prepareDescription(props.content, props.sourceUrl, props.format)
 value.images=value.images.map((image,index)=>({...image,index,url:props.imageResolver?props.imageResolver(image.url):image.url})).filter(image=>image.url)
 if(props.imageResolver||props.thumbnails){
  const container=document.createElement('div');container.innerHTML=value.html
  for(const marker of container.querySelectorAll('[data-description-image]')){
   const image=value.images.find(image=>image.index===Number(marker.dataset.descriptionImage))
   if(!image||(props.thumbnails&&safeThumbnail(image.url)))marker.remove()
  }
  value.html=container.innerHTML
 }
 return value
})
const body = ref(null), expanded = ref(false), overflowing = ref(false)
const previewVisible=ref(false),previewAttempt=ref(0)
const previewIndex=ref(0),thumbnailFailures=ref(new Set()),thumbnailAttempts=ref({})
function thumbnailFailed(url){thumbnailFailures.value=new Set([...thumbnailFailures.value,url])}
const externalRecord = computed(() => safeDescriptionURL(props.sourceUrl))
function openImage(image,index) { if(thumbnailFailures.value.has(image.url)){const failures=new Set(thumbnailFailures.value);failures.delete(image.url);thumbnailFailures.value=failures;thumbnailAttempts.value={...thumbnailAttempts.value,[image.url]:(thumbnailAttempts.value[image.url]||0)+1}} previewIndex.value=index;previewVisible.value=true }
function safeThumbnail(address){try{const u=new URL(address,location.href);return u.origin===location.origin&&/\/api\/(?:plugins\/[^/]+\/api\/)?(?:work-item-attachments\/[a-f0-9]{32}|research\/work-items\/[1-9]\d*\/images\/[1-9]\d*)$/.test(u.pathname)}catch{return false}}
let observer
function measure() {
  if (!expanded.value && body.value) overflowing.value = body.value.scrollHeight > body.value.clientHeight + 1
}
watch(() => [props.content, props.sourceUrl], async () => { expanded.value = false; previewVisible.value = false; await nextTick(); measure() })
watch(expanded, async () => { await nextTick(); measure() })
onMounted(() => { observer = new ResizeObserver(measure); observer.observe(body.value); measure() })
onBeforeUnmount(() => observer?.disconnect())

import VueEasyLightbox from 'vue-easy-lightbox'


import {Button,Card,CardContent,CardHeader,CardTitle} from '../../shared/shadcn-ui.js'
</script>

<template><component :is="compact?'section':Card" class="description-panel" :class="{compact,full}"><CardHeader v-if="!compact"><CardTitle>{{title}}</CardTitle></CardHeader><component :is="compact?'div':CardContent"><div ref="body" class="description-body" :class="{expanded,plain:prepared.plain}" v-html="prepared.html||'暂无说明'"/><Button v-if="!full&&(overflowing||expanded)" variant="ghost" :aria-expanded="expanded" @click="expanded=!expanded">{{expanded?'收起说明':'展开完整说明'}}</Button><div v-if="prepared.images.length" class="description-images" aria-label="说明图片"><span v-if="!thumbnails">图片 {{prepared.images.length}} 张</span><Button v-for="(image,index) in prepared.images" :key="index" variant="outline" :class="thumbnails&&safeThumbnail(image.url)?'h-auto flex-col p-2':''" :aria-label="'查看图片 '+(index+1)" @click="openImage(image,index)"><img v-if="thumbnails&&safeThumbnail(image.url)" :key="image.url+':'+(thumbnailAttempts[image.url]||0)" :src="image.url" width="132" height="88" alt="说明图片" loading="lazy" referrerpolicy="no-referrer" class="h-22 w-33 object-contain" @error="thumbnailFailed(image.url)"/><span v-if="thumbnailFailures.has(image.url)">加载失败，点击重试</span><span>查看图片 {{index+1}}<template v-if="image.label"> · {{image.label}}</template></span></Button></div></component></component><VueEasyLightbox :key="previewAttempt" :visible="previewVisible" :index="previewIndex" :imgs="prepared.images.map(image=>image.url)" teleport="body" @hide="previewVisible=false" @on-index-change="(_,index)=>previewIndex=index"><template #title><div class="fixed bottom-20 left-1/2 flex -translate-x-1/2 gap-3 rounded-lg bg-background p-2 text-foreground"><Button variant="link" as-child><a :href="prepared.images[previewIndex]?.url" target="_blank" rel="noopener noreferrer" referrerpolicy="no-referrer">打开原图</a></Button><Button v-if="externalRecord" variant="link" as-child><a :href="externalRecord" target="_blank" rel="noopener noreferrer">外部记录</a></Button><Button variant="outline" @click="previewAttempt++">重新加载</Button></div></template><template #onerror><div class="grid gap-3 rounded-lg bg-background p-5 text-foreground"><p>图片加载失败</p><Button @click="previewAttempt++">重新加载</Button><Button variant="outline" @click="previewVisible=false">关闭</Button></div></template></VueEasyLightbox></template>

<style scoped>.description-panel{margin:0 0 20px;min-width:0}.description-panel.compact{margin:0;padding:0;border:0;background:transparent}.description-body{max-height:160px;overflow:hidden;overflow-wrap:anywhere;color:var(--foreground);font-size:1rem;line-height:24px}.description-body.plain{white-space:pre-wrap}.description-body.expanded{max-height:min(55vh,560px);overflow:auto}.description-panel.full .description-body{max-height:none;overflow:visible}
.description-body :deep(p),.description-body :deep(blockquote),.description-body :deep(ul),.description-body :deep(ol){margin:0 0 12px}.description-body :deep(:last-child){margin-bottom:0}.description-body :deep(ul),.description-body :deep(ol){padding-left:24px}.description-body :deep(ul){list-style:disc}.description-body :deep(ol){list-style:decimal}
.description-body :deep(h1),.description-body :deep(h2),.description-body :deep(h3),.description-body :deep(h4),.description-body :deep(h5),.description-body :deep(h6){font-size:1.125rem;font-weight:600;line-height:24px;margin:16px 0 8px}.description-body :deep(a){color:var(--brand)}.description-body :deep(pre){max-width:100%;max-height:26rem;overflow:auto;white-space:pre;background:var(--muted);padding:16px;border-radius:var(--radius)}.description-body :deep(code){font-family:var(--mono);white-space:inherit}.description-body :deep(blockquote){border-left:3px solid var(--border);padding-left:12px;color:var(--muted-foreground)}.description-body :deep(table){display:block;max-width:100%;overflow-x:auto;border-collapse:collapse}.description-body :deep(td),.description-body :deep(th){border:1px solid var(--border);padding:8px 12px}
.description-toggle{margin-top:8px}.description-images{display:flex;flex-wrap:wrap;align-items:flex-start;gap:12px;margin-top:16px;max-height:160px;overflow:auto;font-size:.75rem;color:var(--muted-foreground)}.description-panel.full .description-images{max-height:none;overflow:visible}.description-images .thumbnail{height:auto;display:flex;flex-direction:column;gap:4px;width:144px;padding:6px}.thumbnail-error{display:grid;place-items:center;width:132px;height:88px;font-size:.75rem;color:var(--muted-foreground);background:var(--accent)}.thumbnail :deep(img){object-fit:contain;background:var(--muted)}</style>
