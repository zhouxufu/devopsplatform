<script setup>
import {ref,watch,onBeforeUnmount} from 'vue'
import {Button,Input,Spinner} from '../shared/shadcn-ui.js'
const props=defineProps({modelValue:[String,Number],disabled:Boolean,filter:Boolean}),emit=defineEmits(['update:modelValue'])
const query=ref(''),items=ref([]),selected=ref(null),loading=ref(false),error=ref(''),searched=ref(false)
let timer,searchSeq=0,selectionSeq=0
async function search(){
 clearTimeout(timer)
 const term=query.value.trim(),seq=++searchSeq
 if(!term)return
 loading.value=true;error.value='';searched.value=false
 try{
  const number=term.replace(/^#/,'')
  const lookup=/^[1-9]\d{0,18}$/.test(number)?fetch('api/work-items/'+number).then(r=>r.ok?r.json():null):Promise.resolve(null)
  const [response,exact]=await Promise.all([fetch('api/work-items?'+new URLSearchParams({q:number,limit:'20'})),lookup])
  const rows=await response.json()
  if(!response.ok)throw Error(rows.error||'读取工作项失败')
  if(seq===searchSeq){items.value=exact?[exact,...rows.filter(item=>item.id!==exact.id)]:rows;searched.value=true}
 }catch(e){if(seq===searchSeq)error.value=e.message}
 finally{if(seq===searchSeq)loading.value=false}
}
watch(query,()=>{clearTimeout(timer);searchSeq++;loading.value=false;searched.value=false;items.value=[];error.value='';if(query.value.trim())timer=setTimeout(search,250)})
watch(()=>props.modelValue,async id=>{
 const seq=++selectionSeq
 if(id&&String(selected.value?.id)===String(id))return
 selected.value=null
 if(!id)return
 try{const r=await fetch('api/work-items/'+encodeURIComponent(id));if(r.ok){const item=await r.json();if(seq===selectionSeq)selected.value=item}}catch{}
},{immediate:true})
function choose(item){selected.value=item;emit('update:modelValue',item.id);items.value=[];query.value=''}
onBeforeUnmount(()=>{searchSeq++;selectionSeq++;clearTimeout(timer)})
</script>
<template>
 <div class="min-w-0 space-y-2">
  <div v-if="modelValue" class="flex items-center gap-2 rounded-md border p-2 text-sm">
   <span class="min-w-0 flex-1 break-words">#{{selected?.external_id||modelValue}} · {{selected?.title||'已选工作项'}}</span>
   <Button variant="ghost" size="sm" type="button" :disabled="disabled" @click="emit('update:modelValue','')">{{filter?'清除筛选':'移除关联'}}</Button>
  </div>
  <div class="flex min-w-0 gap-2">
   <Input v-model="query" placeholder="输入需求 / Bug 的标题或编号" aria-label="搜索关联需求或 Bug" :disabled="disabled" @keydown.enter.prevent="search"/>
   <Button variant="outline" type="button" :disabled="disabled||loading||!query.trim()" @click="search"><Spinner v-if="loading"/>查找</Button>
  </div>
  <div v-if="items.length" class="max-h-48 overflow-auto rounded-md border">
   <Button v-for="item in items" :key="item.id" type="button" variant="ghost" class="h-auto w-full justify-start whitespace-normal text-left" :disabled="disabled" @click="choose(item)">#{{item.external_id||item.id}} · {{item.title}}</Button>
  </div>
  <p v-if="error" role="alert" class="text-xs text-destructive">{{error}}</p>
  <p v-else-if="searched&&!items.length" class="text-xs text-muted-foreground">没有匹配的需求或 Bug</p>
 </div>
</template>
