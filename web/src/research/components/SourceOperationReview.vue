<script setup>
import {ref,computed} from 'vue'
import {Button,Spinner} from '../../shared/shadcn-ui.js'
import {post} from '../api.js'
import {toast} from '../../shared/composables/toast.js'
import {sourceActionLabel,safeSourceUrl,verifySourceOperation} from '../sourceOperation.js'
const props=defineProps({operations:{type:Array,default:()=>[]},sourceUrl:String,disabled:Boolean})
const emit=defineEmits(['verified','busy'])
const checking=ref(''),messages=ref({})
const sourceLink=computed(()=>safeSourceUrl(props.sourceUrl))
async function verify(op){
 if(checking.value||props.disabled)return
 checking.value=op.id;emit('busy',true)
 try{const result=await verifySourceOperation(op,post);messages.value[op.id]=result.message;if(result.verified){emit('verified',result.operation);toast(result.message,'success')}else toast(result.message,'warning')}
 catch(e){messages.value[op.id]=e.message;toast(e.message,'warning')}
 finally{checking.value='';emit('busy',false)}
}
</script>
<template>
<section class="min-w-0 space-y-3 py-4" aria-label="来源操作核对">
 <div class="flex flex-wrap items-center justify-between gap-2"><h3 class="font-semibold">来源操作待核对（{{operations.length}}）</h3><Button v-if="sourceLink" as-child variant="outline" size="sm"><a :href="sourceLink" target="_blank" rel="noopener noreferrer">前往来源平台</a></Button></div>
 <p class="text-sm text-muted-foreground">读取来源历史，核对原操作编号和账号；不会重复发送评论或状态操作。</p>
 <div v-for="op in operations" :key="op.id" :data-source-operation="op.id" class="flex min-w-0 flex-wrap items-start justify-between gap-3 border-t py-3">
  <div class="min-w-0 flex-1 basis-56 space-y-1"><div class="flex flex-wrap items-center gap-2"><span class="font-medium">{{sourceActionLabel(op.request?.action)}}</span><span class="font-mono text-xs text-muted-foreground">{{op.id.slice(0,8)}}</span></div><p v-if="op.request?.comment" class="line-clamp-2 break-words text-sm text-muted-foreground">{{op.request.comment}}</p><p role="status" class="break-words text-sm text-muted-foreground">{{messages[op.id]||op.message||'来源响应未确认，请核对原操作结果'}}</p></div>
  <Button variant="outline" size="sm" :disabled="disabled||!!checking" @click="verify(op)"><Spinner v-if="checking===op.id"/>{{checking===op.id?'核对中…':'核对结果'}}</Button>
 </div>
</section>
</template>
