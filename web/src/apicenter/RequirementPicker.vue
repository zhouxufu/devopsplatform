<script setup>
import {ref,watch,onBeforeUnmount} from 'vue'
import {Button,Input,Spinner} from '../shared/shadcn-ui.js'
const props=defineProps({modelValue:[String,Number],disabled:Boolean}),emit=defineEmits(['update:modelValue'])
const query=ref(''),items=ref([]),selected=ref(null),loading=ref(false),error=ref('');let timer,seq=0
async function search(){const n=++seq;loading.value=true;error.value='';try{const r=await fetch('api/work-items?'+new URLSearchParams({q:query.value,limit:'20'}));const v=await r.json();if(!r.ok)throw Error(v.error||'读取工作项失败');if(n===seq)items.value=v}catch(e){if(n===seq)error.value=e.message}finally{if(n===seq)loading.value=false}}
watch(query,()=>{clearTimeout(timer);if(!query.value.trim()){items.value=[];return}timer=setTimeout(search,250)})
watch(()=>props.modelValue,async id=>{selected.value=null;if(!id)return;const n=++seq;try{const r=await fetch('api/work-items/'+encodeURIComponent(id));if(r.ok&&n===seq)selected.value=await r.json()}catch{}},{immediate:true})
function choose(item){selected.value=item;emit('update:modelValue',item.id);items.value=[];query.value=''}
onBeforeUnmount(()=>{seq++;clearTimeout(timer)})
</script>
<template><div class="min-w-0 space-y-2"><div v-if="modelValue" class="flex items-center gap-2 rounded-md border p-2 text-sm"><span class="min-w-0 flex-1 break-words">#{{selected?.external_id||modelValue}} · {{selected?.title||'已选工作项'}}</span><Button variant="ghost" size="sm" :disabled="disabled" @click="emit('update:modelValue','')">移除关联</Button></div><div class="flex min-w-0 gap-2"><Input v-model="query" placeholder="搜索已登记需求 / Bug 的标题或编号" aria-label="搜索关联需求或Bug" :disabled="disabled"/><Button variant="outline" :disabled="disabled||loading" @click="search"><Spinner v-if="loading"/>查找</Button></div><div v-if="items.length" class="max-h-48 overflow-auto rounded-md border"><Button v-for="item in items" :key="item.id" variant="ghost" class="h-auto w-full justify-start whitespace-normal text-left" :disabled="disabled" @click="choose(item)">#{{item.external_id||item.id}} · {{item.title}}</Button></div><p v-if="error" class="text-xs text-destructive">{{error}}</p></div></template>
