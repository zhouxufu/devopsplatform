<script setup>

import {uploadSelection} from '../../shared/uploadSelection.js'
import {inject, ref, onBeforeUnmount} from 'vue'
import {descriptionImageURL} from '../../deploy/richDescription.js'
import {toast} from '../../shared/composables/toast.js'
const props=defineProps({modelValue:String,attachments:{type:Array,default:()=>[]},disabled:Boolean,busy:Boolean,label:{type:String,default:'发表评论'},inputLabel:{type:String,default:'评论内容'},placeholder:{type:String,default:'输入评论，可粘贴、拖入或添加图片'},showSubmit:{type:Boolean,default:true},compact:Boolean})
const emit=defineEmits(['update:modelValue','update:attachments','uploading','submit'])
const api=inject('api'),textarea=ref(null),uploading=ref(false)
let alive=true
onBeforeUnmount(()=>{alive=false;if(uploading.value)emit('uploading',false)})
defineExpose({focus:()=>focusControl(textarea.value)})
async function upload(files){
 if(props.disabled||props.busy||uploading.value){toast(uploading.value?'图片正在上传，请稍后再添加':'当前操作进行中，请稍后添加图片','warning');return}
 const images=Array.from(files||[]);if(!images.length)return
 if(images.length+props.attachments.length>20){toast('最多附加 20 张图片','warning');return}
 // Validate the whole selection before uploading any part of it.
 if(images.some(file=>!['image/png','image/jpeg','image/gif'].includes(file.type)||file.size>8*1024*1024)){toast('仅支持 PNG、JPEG、GIF；每张图片最多 8 MiB','warning');return}
 if([...props.attachments,...images].reduce((sum,a)=>sum+(a.size||0),0)>16*1024*1024){toast('图片总大小最多 16 MiB','warning');return}
 uploading.value=true;emit('uploading',true)
 const added=[...props.attachments]
 try{for(const file of images){
  const body=new FormData();body.append('file',file)
  const result=await api('api/work-item-attachments',{method:'POST',body})
  if(!alive)return
  added.push({...result,url:descriptionImageURL(result.reference||'attachment:'+result.id)})
  emit('update:attachments',[...added])
 }}catch(e){if(alive)toast(e.message,'error')}finally{if(alive){uploading.value=false;emit('uploading',false)}}
}
const chooseImages=uploadSelection(upload)
function paste(e){let files=Array.from(e.clipboardData?.files||[]).filter(f=>f.type.startsWith('image/'));if(!files.length)files=Array.from(e.clipboardData?.items||[]).filter(i=>i.kind==='file'&&i.type.startsWith('image/')).map(i=>i.getAsFile()).filter(Boolean);if(files.length){e.preventDefault();upload(files)}}
function drop(e){if(e.dataTransfer?.files.length){e.preventDefault();upload(e.dataTransfer.files)}}

import VueEasyLightbox from 'vue-easy-lightbox'
const _imagePreview=ref(false),_imageIndex=ref(0)

import {focusControl} from '../../shared/focusControl.js'



import {Button,Field,FieldLabel,Input,Textarea} from '../../shared/shadcn-ui.js'
</script>
<template><section :class="{compact}" :aria-label="label" :aria-busy="uploading" @paste="paste" @dragover.prevent @drop="drop" class="comment-composer"><Field ><FieldLabel as="div">{{label}}</FieldLabel><Textarea ref="textarea" :model-value="modelValue" :disabled="disabled||busy" :placeholder="placeholder" @update:model-value="emit('update:modelValue',$event)" :maxlength="32000" v-bind="{&quot;aria-label&quot;:(inputLabel)}" :rows="({minRows:3,maxRows:10}).minRows||4"/></Field><ul v-if="attachments.length" aria-label="待发送图片" class="comment-attachments"><li v-for="(file,i) in attachments" :key="file.id"><img :src="file.url" :alt="file.name||'待发送图片'" :width="112" :height="80" fit="contain" referrerpolicy="no-referrer" class="h-20 w-28 cursor-zoom-in object-contain" @click="_imageIndex=i;_imagePreview=true"/><span>{{file.name||'图片'}}</span><Button :aria-label="'移除图片 '+(i+1)" @click="emit('update:attachments',attachments.filter(a=>a.id!==file.id))" variant="outline" size="default" type="button" :disabled="(busy||uploading||disabled)">×</Button></li></ul><div class="composer-tools"><div class="inline-flex min-w-0 items-center gap-2"><Input type="file" aria-label="选择上传文件" class="max-w-xs" accept="image/png,image/jpeg,image/gif" multiple :disabled="disabled||busy||uploading" @change="e=>{for(const file of e.target.files||[])(chooseImages)(file);e.target.value=''}"/></div><small role="status" aria-live="polite">{{uploading?'图片上传中，请稍候…':attachments.length?attachments.length+' 张待提交 · 可继续粘贴或拖入图片':'可直接粘贴或拖入截图 · 单张 8 MiB，总计 16 MiB'}}</small><Button v-if="showSubmit" @click="emit('submit')" class="send" variant="outline" size="default" type="button" :disabled="(disabled||busy||uploading||(!modelValue?.trim()&amp;&amp;!attachments.length))">{{busy?'正在发送…':'发送评论'}}</Button></div></section><VueEasyLightbox :visible="_imagePreview" :index="_imageIndex" :imgs="attachments.map(file=>file.url)" teleport="body" @hide="_imagePreview=false"/></template>
<style scoped>.comment-composer{border-top:1px solid var(--border);margin-top:20px;padding-top:16px}.comment-composer label{display:grid;gap:8px;font-size:.875rem;font-weight:600}.composer-tools{display:flex;gap:10px;align-items:center;flex-wrap:wrap;margin-top:10px}.composer-tools small{color:var(--muted-foreground);font-size:.75rem}.composer-tools .send{margin-left:auto}.comment-attachments{display:flex;flex-wrap:wrap;gap:10px;padding:0;list-style:none}.comment-attachments li{position:relative;width:120px;min-width:0;border:1px solid var(--border);border-radius:var(--radius);padding:4px}.comment-attachments img{width:120px;height:80px;object-fit:contain}.comment-attachments span{display:block;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-size:.75rem}@media(pointer:coarse){}</style>
<style scoped>.comment-composer.compact{border-top:0;margin-top:0;padding-top:0}.compact .composer-tools{margin-top:6px}.comment-attachments img{max-width:100%}</style>
