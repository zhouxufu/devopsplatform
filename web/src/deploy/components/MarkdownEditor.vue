<script setup>

import {uploadSelection} from '../../shared/uploadSelection.js'
import {inject, onBeforeUnmount, ref} from 'vue'
import RichDescription from './RichDescription.vue'
const props=defineProps({modelValue:{type:String,default:''},format:{type:String,default:'markdown'},sourceUrl:String,disabled:Boolean,allowImages:{type:Boolean,default:true}})
const emit=defineEmits(['update:modelValue','uploading'])
const api=inject('api'),preview=ref(false),uploading=ref(false),error=ref('')
let alive=true
onBeforeUnmount(()=>{alive=false})
async function upload(files){
 if(props.disabled||uploading.value||!props.allowImages)return
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
 }}catch(e){if(alive)error.value=e.message}finally{if(alive){uploading.value=false;emit('uploading',false)}}
}
const chooseImages=uploadSelection(upload)
function paste(e){const images=Array.from(e.clipboardData?.files||[]).filter(f=>f.type.startsWith('image/'));if(images.length){e.preventDefault();upload(images)}}
function drop(e){if(e.dataTransfer?.files.length){e.preventDefault();upload(e.dataTransfer.files)}}



import {Button,Input,Textarea} from '../../shared/shadcn-ui.js'
</script>
<template><div @paste="paste" @dragover.prevent @drop="drop" class="markdown-editor"><div class="editor-toolbar"><div role="group" aria-label="说明编辑模式"><Button :aria-pressed="!preview" @click="preview=false" variant="outline" size="default" type="button">编辑{{format==='html'?'富文本源码':' Markdown'}}</Button><Button :aria-pressed="preview" @click="preview=true" variant="outline" size="default" type="button">预览</Button></div><div v-if="allowImages" class="inline-flex min-w-0 items-center gap-2"><Input type="file" aria-label="选择上传文件" class="max-w-xs" accept="image/png,image/jpeg,image/gif" multiple :disabled="disabled||uploading" @change="e=>{for(const file of e.target.files||[])(chooseImages)(file);e.target.value=''}"/></div></div><Textarea v-show="!preview" :model-value="modelValue" :disabled="disabled" :placeholder="format==='html'?'保留来源平台的 HTML 富文本内容':'支持 Markdown；可以粘贴、拖入或选择多张图片'" @update:model-value="emit('update:modelValue',$event)" v-bind="{&quot;aria-label&quot;:&quot;需求说明编辑器&quot;}" :rows="({minRows:9,maxRows:18}).minRows||4"/><RichDescription v-if="preview" :content="modelValue" :format="format" :source-url="sourceUrl" title="说明预览"/><p v-if="error" role="alert" class="upload-error">{{error}}</p></div></template>
<style scoped>.markdown-editor{border:1px solid var(--border);border-radius:var(--radius);overflow:hidden;background:var(--panel)}.editor-toolbar{display:flex;align-items:center;justify-content:space-between;padding:6px 8px;background:var(--panel2);gap:8px}.markdown-editor :deep(.description-panel){border:0;margin:0}.upload-error{padding:8px 12px;color:var(--red,#d33)}@media(pointer:coarse){}</style>
