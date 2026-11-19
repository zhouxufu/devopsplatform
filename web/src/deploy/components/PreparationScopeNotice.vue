<script setup>
import {computed,onBeforeUnmount,ref,watch} from 'vue'
import {openWorkspace} from '../../research/api.js'
import {Accordion,AccordionContent,AccordionItem,AccordionTrigger,Button,Spinner} from '../../shared/shadcn-ui.js'
const props=defineProps({preparation:Object,load:Function,canManage:Boolean,busy:Boolean,inPlace:Boolean})
const emit=defineEmits(['state','cancel','restart'])
const review=ref(null),loading=ref(false),error=ref('')
let sequence=0
const terminal=computed(()=>['failed','canceled','rejected','success'].includes(review.value?.run_status))
const groups=computed(()=>[{label:'新增',items:review.value?.added||[]},{label:'移出',items:review.value?.removed||[]},{label:'内容修改',items:review.value?.modified||[]}])
function state(){emit('state',{review:review.value,loading:loading.value,error:error.value})}
async function refresh(){
 const id=props.preparation?.id,generation=++sequence
 review.value=null;error.value=''
 if(!props.preparation?.preparation_run_id||!props.canManage){loading.value=false;state();return}
 loading.value=true;state()
 try{const value=await props.load('delivery/code-preparations/'+encodeURIComponent(id)+'/scope-review');if(sequence===generation)review.value=value}
 catch(e){if(sequence===generation)error.value=e.message}
 finally{if(sequence===generation){loading.value=false;state()}}
}
watch(()=>[props.preparation?.id,props.preparation?.revision,props.canManage],refresh,{immediate:true})
defineExpose({refresh})
onBeforeUnmount(()=>++sequence)
</script>
<template>
<section v-if="loading||error||review?.changed" class="my-4 min-w-0 space-y-3 rounded-lg border p-4" aria-label="准备范围核对">
 <div class="flex flex-wrap items-center justify-between gap-3"><h4 class="text-sm font-semibold">{{review?.changed?'交付范围已变化':'核对交付范围'}}</h4><Button variant="ghost" size="sm" :disabled="loading||busy" @click="refresh"><Spinner v-if="loading"/>重新核对差异</Button></div>
 <p v-if="loading" role="status" class="text-sm text-muted-foreground">正在比较原准备单与当前内容，尚未执行合并或构建。</p>
 <p v-else-if="error" role="alert" class="text-sm text-destructive">{{error}}。请核对差异后再恢复，当前任务及草稿保留。</p>
 <template v-else-if="review?.changed">
  <p class="text-sm leading-6">{{review.message}}</p>
  <Accordion v-if="groups.some(g=>g.items.length)" type="multiple"><AccordionItem value="changes"><AccordionTrigger>查看具体差异 · 原 {{review.frozen_count}} 项 / 当前 {{review.current_count}} 项</AccordionTrigger><AccordionContent><section v-for="group in groups.filter(g=>g.items.length)" :key="group.label" class="mb-3 space-y-1"><h5 class="text-sm font-medium">{{group.label}} · {{group.items.length}} 项</h5><ul class="space-y-1 text-sm text-muted-foreground"><li v-for="item in group.items" :key="item.id">#{{item.id}} · {{item.title}} · {{item.service||item.kind}}</li></ul></section></AccordionContent></AccordionItem></Accordion>
  <p class="text-sm text-muted-foreground">{{terminal?'旧准备已结束；新建准备只打开范围选择，仍需重新预览、确认和审批。':'先核对差异，再终止旧准备以释放内容维护占用；已有推送和制品保留，不会回滚。'}}</p>
  <div class="flex flex-wrap gap-2"><Button v-if="review.can_cancel&&canManage" variant="outline" :disabled="busy||loading" @click="emit('cancel')">终止旧准备，保留记录</Button><Button v-if="terminal&&review.new_preparation_hash" :disabled="busy" @click="inPlace?emit('restart'):openWorkspace('deploy',review.new_preparation_hash)">按当前范围重新准备</Button></div>
  <p v-if="!terminal&&!review.can_cancel&&review.cancel_blocked_reason" class="text-sm text-muted-foreground">{{review.cancel_blocked_reason}}</p>
 </template>
</section>
</template>
