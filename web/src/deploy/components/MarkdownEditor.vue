<script setup>
import {inject,onBeforeUnmount,ref} from 'vue'
import RichDescription from './RichDescription.vue'
const props=defineProps({modelValue:{type:String,default:''},format:{type:String,default:'markdown'},sourceUrl:String,disabled:Boolean})
const emit=defineEmits(['update:modelValue','uploading'])
const api=inject('api'),preview=ref(false),uploading=ref(false),error=ref(''),input=ref(null)
let alive=true
onBeforeUnmount(()=>{alive=false})
async function upload(files){
 if(props.disabled||uploading.value)return
 const images=Array.from(files||[]);if(!images.length)return
 if(images.length>30){error.value='一次最多选择 30 张图片';return}
 uploading.value=true;emit('uploading',true);error.value=''
 try{for(const file of images){
  if(!['image/png','image/jpeg','image/gif'].includes(file.type)||file.size>8*1024*1024)throw new Error('仅支持 PNG、JPEG、GIF 图片；每张最多 8 MiB')
  const body=new FormData();body.append('file',file)
  const result=await api('api/work-item-attachments',{method:'POST',body})
  if(!alive)return
  const name=String(result.name||'图片').replace(/[\[\]<>"\r\n]/g,'').slice(0,80)
  const image=props.format==='html'?`<p><img src="${result.reference}" alt="${name}"></p>`:`![${name}](${result.reference})`
  emit('update:modelValue',(props.modelValue||'')+'\n\n'+image+'\n')
 }}catch(e){if(alive)error.value=e.message}finally{if(alive){uploading.value=false;emit('uploading',false);if(input.value)input.value.value=''}}
}
function paste(e){const images=Array.from(e.clipboardData?.files||[]).filter(f=>f.type.startsWith('image/'));if(images.length){e.preventDefault();upload(images)}}
function drop(e){if(e.dataTransfer?.files.length){e.preventDefault();upload(e.dataTransfer.files)}}
</script>
<template>
 <div class="markdown-editor" @paste="paste" @dragover.prevent @drop="drop">
  <div class="editor-toolbar"><div role="group" aria-label="说明编辑模式"><button type="button" :aria-pressed="!preview" @click="preview=false">编辑{{format==='html'?'富文本源码':' Markdown'}}</button><button type="button" :aria-pressed="preview" @click="preview=true">预览</button></div><button type="button" :disabled="disabled||uploading" @click="input.click()">{{uploading?'正在上传图片…':'添加图片'}}</button><input ref="input" hidden type="file" accept="image/png,image/jpeg,image/gif" multiple @change="upload($event.target.files)"/></div>
  <textarea v-show="!preview" :value="modelValue" :disabled="disabled" aria-label="需求说明编辑器" rows="9" :placeholder="format==='html'?'保留来源平台的 HTML 富文本内容':'支持 Markdown；可以粘贴、拖入或选择多张图片'" @input="emit('update:modelValue',$event.target.value)"/>
  <RichDescription v-if="preview" :content="modelValue" :format="format" :source-url="sourceUrl" title="说明预览"/>
  <p v-if="error" class="upload-error" role="alert">{{error}}</p>
 </div>
</template>
<style scoped>
.markdown-editor{border:1px solid var(--border);border-radius:8px;overflow:hidden;background:var(--panel)}.editor-toolbar{display:flex;align-items:center;justify-content:space-between;padding:6px 8px;background:var(--panel2);gap:8px}.editor-toolbar button{border:0;border-radius:5px;padding:7px 10px;cursor:pointer;background:transparent;color:var(--muted);font:inherit;font-size:12px}.editor-toolbar button[aria-pressed=true]{background:var(--panel);color:var(--brand)}.editor-toolbar button:disabled{opacity:.6;cursor:wait}.markdown-editor textarea{box-sizing:border-box;width:100%;resize:vertical;min-height:180px;max-height:50vh;border:0;padding:12px;background:transparent;color:var(--text);font:13px/1.6 monospace}.markdown-editor :deep(.description-panel){border:0;margin:0}.upload-error{padding:8px 12px;color:var(--red,#d33)}button:focus-visible,textarea:focus-visible{outline:2px solid var(--brand);outline-offset:-2px}@media(pointer:coarse){.editor-toolbar button{min-height:44px}}
</style>
