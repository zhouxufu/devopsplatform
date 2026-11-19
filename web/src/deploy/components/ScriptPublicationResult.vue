<script setup>
import {computed,inject,ref,watch,onBeforeUnmount} from 'vue'
import {Button} from '@/components/ui/button'
import {Field,FieldLabel,FieldError} from '@/components/ui/field'
import {Textarea} from '@/components/ui/textarea'
import {Checkbox} from '@/components/ui/checkbox'
import OptionPicker from '../../shared/components/OptionPicker.vue'
import {toast} from '../../shared/composables/toast.js'
import {scriptReconciliationPayload} from '../scriptPublication.js'
const props=defineProps({order:Object,me:{type:Object,default:()=>({})},draft:{type:Object,default:()=>({})}})
const emit=defineEmits(['updated','state'])
const api=inject('api')
const busy=ref(false),error=ref('')
const state=computed(()=>props.order?.script_publication)
const canRecord=computed(()=>state.value?.needs_reconciliation&&(props.me.role==='admin'||props.me.username===props.order?.actor))
watch(()=>props.order?.id,()=>{props.draft.result||='failed';error.value=''},{immediate:true})
watch(()=>[busy.value,props.draft.comment,props.draft.confirm_stopped],()=>emit('state',{busy:busy.value,dirty:!!props.draft.comment&&!props.draft.recorded}))
onBeforeUnmount(()=>emit('state',{busy:false,dirty:false}))
async function save(){
 if(busy.value||!canRecord.value)return
 let body;try{body=scriptReconciliationPayload(props.order,props.draft)}catch(e){error.value=e.message;return}
 busy.value=true;error.value=''
 try{const saved=await api('api/releases/'+props.order.id+'/script-result',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});props.draft.recorded=true;emit('updated',saved);toast('已保存人工核对结果，解除本次脚本执行保护','success')}
 catch(e){error.value=e.message;toast(e.message,'error')}
 finally{busy.value=false}
}
</script>
<template>
 <section v-if="state" class="my-4 min-w-0 space-y-4 rounded-lg border p-4" aria-label="脚本发布结果">
  <h3 class="font-semibold">{{state.needs_reconciliation?'脚本结果待核对':state.result?.reconciled_by?'脚本发布 · 人工核对记录':'脚本发布结果'}}</h3>
  <dl v-if="state.result" class="grid gap-3 text-sm sm:grid-cols-2">
   <div v-if="state.result.external_id" class="min-w-0"><dt class="text-muted-foreground">外部执行编号</dt><dd class="break-all font-mono">{{state.result.external_id}}</dd></div>
   <div v-for="(value,key) in state.result.values" :key="key" class="min-w-0"><dt class="text-muted-foreground">{{key}}</dt><dd class="break-all">{{value}}</dd></div>
   <div v-if="state.result.reconciled_by" class="sm:col-span-2"><dt class="text-muted-foreground">人工核对 · {{state.result.reconciled_by}}</dt><dd class="whitespace-pre-wrap break-words">{{state.result.comment}}</dd></div>
  </dl>
  <form v-if="canRecord" class="grid gap-4" @submit.prevent="save">
   <Field><FieldLabel>核对结果</FieldLabel><OptionPicker v-model="draft.result" :disabled="busy" :options="[{value:'failed',label:'未成功；结束本次发布'},{value:'success',label:'已核实外部发布成功'}]" aria-label="脚本核对结果"/></Field>
   <Field><FieldLabel>外部状态核对依据</FieldLabel><Textarea v-model="draft.comment" :disabled="busy" rows="3" placeholder="填写已检查的外部执行编号、状态及结果；保存不会重跑脚本" aria-label="脚本核对依据"/></Field>
   <Field orientation="horizontal"><Checkbox id="script-reconcile-stopped" v-model="draft.confirm_stopped" :disabled="busy"/><FieldLabel for="script-reconcile-stopped" class="font-normal">我已确认原脚本结束，并核对外部任务实际状态</FieldLabel></Field>
   <FieldError v-if="error">{{error}}</FieldError>
   <div><Button :disabled="busy" type="submit">{{busy?'正在保存…':'保存核对结果并解锁'}}</Button></div>
  </form>
 </section>
</template>
