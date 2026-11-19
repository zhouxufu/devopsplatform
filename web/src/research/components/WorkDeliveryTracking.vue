<script setup>
import {computed,onBeforeUnmount, ref, watch} from 'vue'
import {api, openWorkspace} from '../api.js'
import {deliveryHash,deliveryStateLabel} from '../../deploy/deliveryFlow.js'
import {Accordion,AccordionContent,AccordionItem,AccordionTrigger,Badge,Button} from '../../shared/shadcn-ui.js'
const props=defineProps({itemId:[Number,String]})
const emit=defineEmits(['updated'])
const data=ref({deliveries:[],versions:[]}),busy=ref(false),error=ref(''),expanded=ref(false)
const latest=computed(()=>data.value.deliveries[0])
let seq=0,timer,fingerprint=''
async function load(){
 const request=++seq;busy.value=true;clearTimeout(timer)
 try{
  const result=await api('research/work-items/'+props.itemId+'/tracking')
  if(!Array.isArray(result.deliveries)||!Array.isArray(result.versions))throw Error('交付进度响应无效')
  if(request!==seq)return
  result.deliveries=result.deliveries.map(t=>({...t,preparation:result.preparations?.[t.run_id]??t.preparation}));data.value=result;error.value=''
  const current=JSON.stringify(result.deliveries.map(t=>[t.run_id,t.status,t.updated_at,t.stage,t.progress,t.blocked_reason]))
  if(fingerprint&&current!==fingerprint)emit('updated');fingerprint=current
 }catch(e){if(request===seq)error.value='进度暂未更新：'+e.message}
 finally{if(request===seq){busy.value=false;timer=setTimeout(()=>document.hidden?schedule():load(),5000)}}
}
function schedule(){clearTimeout(timer);timer=setTimeout(()=>document.hidden?schedule():load(),5000)}
onBeforeUnmount(()=>{seq++;clearTimeout(timer)})
watch(()=>props.itemId,()=>{seq++;clearTimeout(timer);data.value={deliveries:[],versions:[]};expanded.value=false;fingerprint='';load()},{immediate:true})
const enter=t=>openWorkspace('deploy',deliveryHash({...t,id:t.run_id}))
</script>
<template>
<section v-if="latest||data.versions.length||error" class="mb-5 space-y-3 border-b pb-4" aria-label="当前交付进度">
 <div class="flex flex-wrap items-center justify-between gap-3"><div class="flex flex-wrap items-center gap-2"><h3 class="text-sm font-semibold">当前交付</h3><Badge v-if="latest" :variant="latest.status==='blocked'?'destructive':'secondary'">{{deliveryStateLabel(latest)}}</Badge></div><span class="text-xs text-muted-foreground" role="status">{{error||'进度自动更新；后续处理集中在交付工作台'}}</span></div>
 <template v-if="latest"><div class="flex items-start justify-between gap-5"><div class="min-w-0 space-y-1 text-sm"><p>{{latest.preparation?'准备单':'执行单'}} #{{latest.run_id}} · {{latest.environment}} · {{latest.stage}} · {{latest.progress}}</p><p v-if="latest.assignees?.length" class="text-muted-foreground">当前处理人：{{latest.assignees.join('、')}}</p><p>{{latest.next_action}}</p><p v-if="latest.blocked_reason" class="break-words text-destructive">{{latest.blocked_reason}}</p></div><Button class="shrink-0" @click="enter(latest)">进入交付工作台</Button></div></template>
 <Button v-if="error" variant="outline" size="sm" :disabled="busy" @click="load">重试读取进度</Button>
 <Accordion v-if="data.deliveries.length>1||data.versions.length" type="single" collapsible><AccordionItem value="history"><AccordionTrigger>其他交付与关联版本 · {{Math.max(0,data.deliveries.length-1)}} 条交付</AccordionTrigger><AccordionContent><div class="space-y-3"><div v-for="t in data.deliveries.slice(1)" :key="t.id" class="flex items-center justify-between gap-4 text-sm"><span>{{t.preparation?'准备单':'执行单'}} #{{t.run_id}} · {{t.environment}} · {{deliveryStateLabel(t)}}</span><Button variant="ghost" size="sm" @click="enter(t)">进入工作台</Button></div><p v-for="v in data.versions" :key="'version-'+v.id" class="text-sm text-muted-foreground">关联版本：{{v.version}}</p></div></AccordionContent></AccordionItem></Accordion>
</section>
</template>
