<script setup>
import {computed,ref,watch} from 'vue'
import {post} from '../api.js'
import {toast} from '../../shared/composables/toast.js'
import {Dialog,DialogContent,DialogHeader,DialogTitle,DialogDescription,DialogFooter,Button,Field,FieldLabel,Textarea,Spinner} from '../../shared/shadcn-ui.js'
const props=defineProps({record:Object})
const emit=defineEmits(['close','saved','state'])
const comment=ref(''),busy=ref(false),failure=ref('')
const marking=computed(()=>props.record?.target===true)
const title=computed(()=>marking.value?'标记已上线':'撤销已上线标记')
watch(()=>props.record,()=>{comment.value='';failure.value=''})
watch([comment,busy],()=>emit('state',{dirty:!!comment.value.trim(),busy:busy.value}))
function close(){if(!busy.value){emit('state',{dirty:false,busy:false});emit('close')}}
async function save(){
 if(busy.value||!props.record)return
 busy.value=true;failure.value=''
 try{
  await post('research/work-items/'+props.record.item.id+'/release-mark',{marked:props.record.target,revision:props.record.mark.revision,comment:comment.value},'PUT')
  toast(marking.value?'已标记上线，已移出待上线列表':'已撤销标记，将重新按交付记录判断待上线','success')
  comment.value='';emit('state',{dirty:false,busy:false});emit('saved')
 }catch(e){failure.value=e.message;toast(e.message,'error')}
 finally{busy.value=false}
}
</script>
<template>
 <Dialog :open="!!record" @update:open="value=>{if(!value)close()}">
  <DialogContent v-if="record" class="sm:max-w-lg" :show-close-button="!busy" @interact-outside="event=>{if(busy||comment.trim())event.preventDefault()}" @escape-key-down="event=>{if(busy)event.preventDefault()}">
   <DialogHeader>
    <DialogTitle>{{title}}</DialogTitle>
    <DialogDescription>{{marking?'确认该工作项已完成生产上线。标记后移出待上线列表，并记录操作人和时间。':'撤销这条人工标记后，将重新依据生产交付记录判断是否待上线。'}}</DialogDescription>
   </DialogHeader>
   <p class="break-words text-sm">{{record.item.external_id||'#'+record.item.id}} · {{record.item.title}}</p>
   <p v-if="marking" class="text-sm text-muted-foreground">此次记录为人工确认，交付执行结果保留原样。</p>
   <form class="space-y-5" :aria-busy="busy" @submit.prevent="save">
    <Field><FieldLabel for="release-mark-comment">说明（选填）</FieldLabel><Textarea id="release-mark-comment" v-model="comment" :disabled="busy" placeholder="例如：已随今天的生产版本上线" :maxlength="500"/></Field>
    <p v-if="failure" role="alert" class="text-sm text-destructive">{{failure}}</p>
    <DialogFooter><Button type="button" variant="outline" :disabled="busy" @click="close">取消</Button><Button type="submit" :disabled="busy||record.mark.marked===record.target"><Spinner v-if="busy"/>{{title}}</Button></DialogFooter>
   </form>
  </DialogContent>
 </Dialog>
</template>
