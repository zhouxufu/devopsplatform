<script setup>
import {computed,inject,ref,watch,onBeforeUnmount} from 'vue'
import {RefreshCwIcon} from '@lucide/vue'
import OptionPicker from '../shared/components/OptionPicker.vue'
import ContextHelp from '../shared/components/ContextHelp.vue'
const props=defineProps({modelValue:String,repositories:{type:Array,default:()=>[]},disabled:Boolean}),emit=defineEmits(['update:modelValue']),api=inject('api')
const branches=ref([]),loading=ref(false),attempted=ref(false),error=ref('');let generation=0
const ready=computed(()=>props.repositories.length&&props.repositories.every(r=>r.service||r.git_url?.trim()))
watch(()=>JSON.stringify(props.repositories),()=>{generation++;branches.value=[];attempted.value=false;loading.value=false;error.value=''})
onBeforeUnmount(()=>generation++)
async function sync(){if(loading.value||props.disabled||!ready.value)return;const seq=++generation;loading.value=true;attempted.value=true;error.value='';try{if(props.repositories.length>20)throw Error('一次最多核对 20 个应用');const result=[];for(let i=0;i<props.repositories.length;i+=3)result.push(...await Promise.all(props.repositories.slice(i,i+3).map(r=>api('api/git/branches',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(r)}))));if(result.some(r=>!Array.isArray(r.branches)))throw Error('分支响应格式无效');if(seq===generation){const sets=result.map(r=>new Set(r.branches.map(b=>b.name)));branches.value=[...sets[0]].filter(n=>sets.every(s=>s.has(n))).sort()}}catch(e){if(seq===generation)error.value=e.message}finally{if(seq===generation)loading.value=false}}

import {Button,Spinner} from '../shared/shadcn-ui.js'
</script>
<template><div class="flex min-w-0 items-center gap-2"><div class="min-w-0 flex-1"><OptionPicker :model-value="modelValue" :options="branches" :disabled="disabled" allow-create placeholder="选择或输入分支" @update:model-value="emit('update:modelValue',$event)" @open="()=>{if(!attempted)sync()}"/></div><Button variant="outline" size="icon" :disabled="disabled||loading||!ready" aria-label="同步分支" @click="sync"><Spinner v-if="loading"/><RefreshCwIcon v-else class="size-4"/></Button><ContextHelp :text="error?error+'；当前分支仍保留，可重试。':'同步远程分支，不拉取代码。多个仓库仅展示共同分支；可以搜索并确认手动输入的分支。'" :label="error?'查看分支同步失败原因':'分支选择说明'"/></div></template>
