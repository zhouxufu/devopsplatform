<script setup>


import {computed, inject, onBeforeUnmount, ref, watch} from 'vue'
import {onBeforeRouteLeave, onBeforeRouteUpdate} from 'vue-router'

import {toast} from '../../shared/composables/toast.js'
import {copyText} from '../../shared/clipboard.js'
const props=defineProps({kind:{type:String,required:true},recordId:{type:Number,required:true},editable:Boolean,contextRevision:[String,Number]})
const emit=defineEmits(['state'])
const api=inject('api'),data=ref(null),baseline=ref(''),busy=ref(false),error=ref(''),editing=ref(false),opened=ref(false)
const dirty=computed(()=>props.editable&&!!data.value&&data.value.text!==baseline.value)
const endpoint=computed(()=>`api/release-notices/${props.kind}/${props.recordId}`)
function sync(){emit('state',{dirty:dirty.value,busy:busy.value})}
watch(()=>props.contextRevision,()=>{if(data.value)data.value.stale=true})
const confirmingLeave=ref(false)
async function leave(){if(busy.value||confirmingLeave.value)return false;if(!dirty.value)return true;confirmingLeave.value=true;try{const confirmed=await confirmAction('发布说明尚未保存，放弃修改并离开？');if(confirmed){data.value.text=baseline.value;editing.value=false;sync()}return confirmed}finally{confirmingLeave.value=false}}
onBeforeRouteLeave(leave);onBeforeRouteUpdate((to,from)=>to.fullPath===from.fullPath||leave())
function warn(e){if(dirty.value){e.preventDefault();e.returnValue=''}}
window.addEventListener('beforeunload',warn)
async function call(method,value){return api(endpoint.value,{method,...(value?{headers:{'Content-Type':'application/json'},body:JSON.stringify(value)}:{})})}
async function load(){busy.value=true;error.value='';sync();try{data.value=await call('GET');baseline.value=data.value.text||'';if(!data.value.text)data.value=await call('POST');sync()}catch(e){error.value=e.message;toast(e.message,'error')}finally{busy.value=false;sync()}}
function toggle(value){opened.value=value;if(opened.value&&!data.value&&!busy.value)load()}
async function regenerate(){if(busy.value)return;if(dirty.value&&!await confirmAction('重新生成会替换当前未保存的发布说明，是否继续？'))return;busy.value=true;error.value='';sync();try{const result=await call('POST');data.value=result;editing.value=props.editable;sync()}catch(e){error.value=e.message;toast(e.message,'error')}finally{busy.value=false;sync()}}
async function save(){if(busy.value||!props.editable)return false;busy.value=true;error.value='';sync();try{data.value=await call('PUT',data.value);baseline.value=data.value.text;editing.value=false;toast('发布说明已保存，未发送通知','success');return true}catch(e){error.value=e.message;toast(e.message,'error');return false}finally{busy.value=false;sync()}}
async function copy(){if(!data.value?.text||busy.value)return;if(props.editable&&dirty.value&&!await save())return;return copyText(data.value.text,{successMessage:'已复制通知文案；文字 @ 不等于群内真实提醒',failureMessage:'无法复制，请选中文案手工复制'})}
onBeforeUnmount(()=>{window.removeEventListener('beforeunload',warn);emit('state',{dirty:false,busy:false})})
defineExpose({hasDraft:()=>dirty.value,canLeave:leave})

import {confirmAction} from '../../shared/shadcnFeedback.js'

import {ref as _viewRef} from 'vue'
const _view1=_viewRef([])
const _view2=_viewRef([])



import {Accordion,AccordionContent,AccordionItem,AccordionTrigger,Button,Field,FieldLabel,Spinner,Textarea} from '../../shared/shadcn-ui.js'
</script>
<template><Accordion class="release-notice platform-collapse" type="multiple" :model-value="(_view1||[]).map(String)" @update:model-value="keys=>{_view1=keys;toggle(keys.length>0)}"><AccordionItem :value="String(&quot;panel&quot;)"><AccordionTrigger>发布说明 / 通知文案 <small>按本次 Bug 标题与反馈者生成</small></AccordionTrigger><AccordionContent force-mount v-show="!(!(_view1||[]).map(String).includes(String(&quot;panel&quot;)))"><div v-if="opened" :aria-busy="busy"><p v-if="busy" role="status">正在处理发布说明…</p><p v-if="data?.stale" class="warning">发布范围或状态已变化，原文案保留；请重新生成并核对后再使用。</p><template v-if="data"><Field v-if="editing" class="notice-label"><FieldLabel as="div">发布说明</FieldLabel><Textarea v-model="data.text" :disabled="busy" @input="sync" :rows="({minRows:(7),maxRows:Math.max((7),16)}).minRows||4"/></Field><pre v-else class="notice-text">{{data.text||'尚无发布说明'}}</pre><Accordion v-if="data.warnings?.length" class="notice-warnings platform-collapse" type="multiple" :model-value="(_view2||[]).map(String)" @update:model-value="_view2=$event"><AccordionItem :value="String(&quot;panel&quot;)"><AccordionTrigger>待核对 {{data.warnings.length}} 项</AccordionTrigger><AccordionContent force-mount v-show="!(!(_view2||[]).map(String).includes(String(&quot;panel&quot;)))"><p v-for="(warning,i) in data.warnings" :key="i">{{warning}}</p></AccordionContent></AccordionItem></Accordion><div class="notice-tools"><small>仅保存或复制，不会自动发送。真实 @ 提醒需后续对接通知渠道。</small><Button @click="regenerate" variant="outline" size="default" type="button" :disabled="(busy)">按 Bug 重新生成</Button><Button v-if="editable&&!editing" @click="editing=true" variant="outline" size="default" type="button" :disabled="(busy)">编辑说明</Button><Button v-if="editable&&(editing||dirty)" @click="save" variant="outline" size="default" type="button" :disabled="(busy||!data.text?.trim())||(busy)"><Spinner v-if="busy"/>保存说明</Button><Button @click="copy" variant="default" size="default" type="button" :disabled="(busy||!data.text?.trim()||data.stale)">复制通知</Button></div></template><p v-if="error" role="alert" class="notice-error">{{error}} <Button v-if="!data" @click="load" variant="outline" size="default" type="button" :disabled="(busy)">重试</Button></p></div></AccordionContent></AccordionItem></Accordion></template>
<style scoped>.release-notice{min-width:0;margin:12px 0}.release-notice small{color:var(--muted-foreground);font-weight:400}.notice-text{white-space:pre-wrap;overflow-wrap:anywhere;max-height:240px;overflow:auto;user-select:text;padding:12px;background:var(--panel2);font:.875rem/1.8 inherit;margin:12px 0}.notice-label{display:grid;gap:8px;margin:12px 0}.notice-tools{display:flex;align-items:center;gap:8px;flex-wrap:wrap}.notice-tools>small{flex:1;min-width:180px}.notice-warnings{font-size:.75rem;color:var(--muted-foreground);margin:10px 0}.notice-error{color:var(--error)}.warning{color:var(--warn);font-size:.875rem;margin:10px 0}</style>
