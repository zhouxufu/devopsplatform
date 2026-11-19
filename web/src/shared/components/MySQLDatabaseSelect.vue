<script setup>
import {ref,watch,onBeforeUnmount} from 'vue'


const props=defineProps({connection:String,modelValue:{type:String,default:''},disabled:Boolean,locked:Boolean,required:Boolean,label:{type:String,default:'数据库（schema）'}})
const emit=defineEmits(['update:modelValue'])
const names=ref([]),loading=ref(false),loaded=ref(false),problem=ref('')
let generation=0,controller=null
async function load(force=false){
 if(!props.connection||props.disabled||props.locked||loading.value||loaded.value&&!force)return
 const token=++generation;controller?.abort();const current=new AbortController();controller=current;const timeout=setTimeout(()=>current.abort(),12000)
 loading.value=true;problem.value=''
 try{const r=await fetch('/api/resources/connections/'+encodeURIComponent(props.connection)+'/databases',{signal:current.signal}),body=await r.json();if(!r.ok)throw Error([body.error,body.suggestion].filter(Boolean).join('；'));if(!Array.isArray(body.databases))throw Error('数据库列表格式错误');if(token===generation){names.value=body.databases;loaded.value=true;problem.value=body.truncated?'仅列出前 1000 个数据库，可输入已知库名':''}}
 catch(e){if(token===generation){names.value=[];problem.value=e.name==='AbortError'?'读取超时，可重试；已填库名仍保留':(e.message||'数据库目录读取失败')}}
 finally{clearTimeout(timeout);if(token===generation)loading.value=false}
}
watch(()=>props.connection,()=>{generation++;controller?.abort();names.value=[];loaded.value=false;loading.value=false;problem.value=''})
onBeforeUnmount(()=>{generation++;controller?.abort()})

import OptionPicker from './OptionPicker.vue'

import {RefreshCwIcon as IconRefresh} from '@lucide/vue'


import {Button,Spinner,Tooltip,TooltipContent,TooltipTrigger} from '../shadcn-ui.js'
</script>
<template><div class="mysql-database-select"><div class="database-controls"><OptionPicker :model-value="modelValue" :disabled="disabled||!connection||locked" :aria-label="label" placeholder="选择或输入库名后回车" @update:model-value="emit('update:modelValue',String($event||'').trim())" class="min-w-36" :options="names.map(value=>({value,label:value}))" allow-create @open="(visible=>{if(visible)load() })(true)" :all-label="required?'选择数据库':'不限'"/><Tooltip><TooltipTrigger as-child><Button :aria-label="'刷新'+label+'列表'" @click="load(true)" variant="outline" size="default" type="button" :disabled="(disabled||!connection)||(loading)"><Spinner v-if="loading"/><IconRefresh/></Button></TooltipTrigger><TooltipContent>{{"读取此账号可见的数据库"}}</TooltipContent></Tooltip></div><small v-if="problem" role="status">{{problem}}</small></div></template>
<style scoped>.mysql-database-select{min-width:0;width:100%}.database-controls{display:flex;gap:8px;min-width:0}small{display:block;margin-top:6px;color:var(--muted-foreground);font-size:.75rem}</style>
