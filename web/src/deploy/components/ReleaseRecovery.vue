<script setup>
import {computed,ref} from 'vue'
import {Badge,Button,FieldError,Spinner} from '../../shared/shadcn-ui.js'
import {activeReleaseStatuses} from '../releasePolling.js'
import {releaseOrderLabel} from '../buildNumber.js'
import {canRecheckJavaRelease,originalReviewDeploys,autoRecheckBusy,autoRecheckText} from '../releaseRecovery.js'
import {toast} from '../../shared/composables/toast.js'
const props=defineProps({order:Object,deploys:{type:Array,default:()=>[]},me:Object})
const emit=defineEmits(['updated','busy','refresh'])
const busy=ref(false),error=ref('')
const supported=computed(()=>canRecheckJavaRelease(props.order))
const pending=computed(()=>originalReviewDeploys(props.order,props.deploys))
const status=computed(()=>({needs_review:'待核对',pending_approval:'待审批',waiting_next_batch:'等待下一批',waiting_manual:'待手动发布',running:'执行中',queued:'排队中',rolling_back:'回滚中'})[props.order?.status]||props.order?.status)
async function recheck(deploy){
 if(busy.value||autoRecheckBusy(deploy.auto_recheck))return
 const orderID=props.order.id
 busy.value=true;error.value='';emit('busy',true)
 try{
  const r=await fetch(`api/deploys/${deploy.id}/recheck`,{method:'POST',headers:{'Content-Type':'application/json'},body:'{}'})
  const body=await r.json().catch(()=>({}))
  if(!r.ok)throw Error(body.error||'核对未完成，请刷新原发布状态；不会自动重新执行')
  const state=await fetch(`api/releases/${orderID}`)
  if(!state.ok)throw Error('核对已返回，但发布单读取失败；请刷新状态，不要重新发布')
  emit('updated',await state.json())
  toast(body.status==='success'?'原发布已核对成功':'已确认原进程退出，发布结果为失败',body.status==='success'?'success':'warning')
 }catch(e){error.value=e.message;emit('refresh')}
 finally{busy.value=false;emit('busy',false)}
}
</script>
<template>
 <section v-if="order&&activeReleaseStatuses.includes(order.status)" class="my-3 min-w-0 space-y-3 rounded-md border p-3" aria-label="原发布处理">
  <div class="flex flex-wrap items-center justify-between gap-2"><h4 class="text-sm font-semibold">发布单 {{releaseOrderLabel(order)}} · {{order.environment}}</h4><Badge variant="secondary">{{status}}</Badge></div>
  <p class="text-sm text-muted-foreground">{{order.status==='needs_review'?'原发布结果尚未确认，当前环境暂不能再次发布或控制实例。':'当前环境已有未结束的发布单，请完成原流程后再发布。'}}</p>
  <template v-if="supported">
   <p class="text-sm text-muted-foreground">核对原制品、进程及就绪结果。不会重启、重新发布或自动执行下一批。</p>
   <div v-for="d in pending" :key="d.id" class="flex flex-wrap items-center justify-between gap-2"><div class="min-w-0 flex-1 space-y-1"><span class="break-words text-sm">{{d.host}} · {{d.version||order.version}}</span><p role="status" class="break-words text-sm" :class="d.auto_recheck?.state==='manual'?'text-destructive':'text-muted-foreground'">{{autoRecheckText(d.auto_recheck)}}</p></div><Button v-if="me?.role==='admin'" variant="outline" size="sm" :disabled="busy||autoRecheckBusy(d.auto_recheck)" @click="recheck(d)"><Spinner v-if="busy"/>核对原发布结果</Button></div>
   <p v-if="me?.role!=='admin'" class="text-sm text-muted-foreground">后台自动核对；如需人工处理，由管理员核对原发布结果。</p>
   <Button v-if="!pending.length" variant="outline" size="sm" :disabled="busy" @click="emit('refresh')">刷新原部署记录</Button>
  </template>
  <p v-else-if="order.status==='needs_review'" class="text-sm text-muted-foreground">该发布方式需通过原任务回执或原发布的专用核对入口处理。</p>
  <FieldError v-if="error" role="alert">{{error}}</FieldError>
 </section>
</template>
