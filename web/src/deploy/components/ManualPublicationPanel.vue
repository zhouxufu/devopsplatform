<script setup>
import {computed,inject,onBeforeUnmount,ref,watch} from 'vue'
import {Button} from '@/components/ui/button'
import {Field,FieldLabel,FieldError} from '@/components/ui/field'
import {Textarea} from '@/components/ui/textarea'
import {Checkbox} from '@/components/ui/checkbox'
import OptionPicker from '../../shared/components/OptionPicker.vue'
import DateFilter from '../../shared/components/DateFilter.vue'
import ArtifactOutputs from './ArtifactOutputs.vue'
import {toast} from '../../shared/composables/toast.js'
import {manualPublicationPayload} from '../manualPublication.js'
const props=defineProps({order:Object,me:{type:Object,default:()=>({})},draft:{type:Object,default:()=>({})}})
const emit=defineEmits(['updated','state'])
const api=inject('api',async(path,opt)=>{const r=await fetch(path,opt),body=await r.json();if(!r.ok)throw Error(body.error||'操作失败');return body})
const artifact=ref(null),loading=ref(false),busy=ref(false),error=ref('');let sequence=0
const canRecord=computed(()=>props.order?.status==='waiting_manual'&&(props.me.role==='admin'||props.me.username===props.order?.manual_publication?.assignee))
const localTime=()=>{const d=new Date();return `${d.getFullYear()}-${String(d.getMonth()+1).padStart(2,'0')}-${String(d.getDate()).padStart(2,'0')}T${String(d.getHours()).padStart(2,'0')}:${String(d.getMinutes()).padStart(2,'0')}:${String(d.getSeconds()).padStart(2,'0')}`}
async function load(){const seq=++sequence;artifact.value=null;loading.value=true;error.value='';try{const a=await api('api/artifacts/'+props.order.artifact_id);if(seq===sequence)artifact.value=a}catch(e){if(seq===sequence)error.value=e.message}finally{if(seq===sequence)loading.value=false}}
watch(()=>props.order?.id,id=>{if(!id)return;props.draft.result||='success';props.draft.performed_at||=localTime();load()},{immediate:true})
watch(()=>[busy.value,props.draft.comment,props.draft.confirmed],()=>emit('state',{busy:busy.value,dirty:!!props.draft.comment&&!props.draft.recorded}))
async function save(){
 if(busy.value||!canRecord.value)return
 let body;try{body=manualPublicationPayload(props.order,props.draft)}catch(e){error.value=e.message;return}
 const order=props.order
 busy.value=true;error.value=''
 try{const saved=await api('api/releases/'+order.id+'/manual-result',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});props.draft.recorded=true;toast('已登记手动发布结果；可继续后续验证','success');emit('updated',saved)}
 catch(e){error.value=e.message;toast(e.message,'error')}
 finally{busy.value=false}
}
onBeforeUnmount(()=>{sequence++;emit('state',{busy:false,dirty:false})})
</script>
<template><section v-if="order?.manual_publication" class="my-4 min-w-0 rounded-lg border bg-background p-4"><div class="mb-4 flex flex-wrap items-center justify-between gap-2"><h3 class="font-semibold">下载制品与手动发布</h3><span class="text-sm text-muted-foreground">负责人：{{order.manual_publication.assignee}}</span></div><p class="mb-4 whitespace-pre-wrap break-words text-sm">{{order.manual_publication.instructions}}</p><ArtifactOutputs v-if="artifact" :artifacts="[artifact]" :primary-id="order.artifact_id" primary-label="本次发布制品"/><p v-else class="text-sm text-muted-foreground">{{loading?'正在读取制品…':error||'制品暂不可用'}} <Button v-if="!loading" variant="outline" size="sm" @click="load">重新读取</Button></p><div v-if="order.manual_publication.recorded_at" class="mt-4 space-y-2 text-sm"><b>人工登记：{{order.manual_publication.result==='success'?'发布成功':'发布失败'}}</b><p>{{order.manual_publication.actor}} · {{new Date(order.manual_publication.performed_at*1000).toLocaleString('zh-CN')}}</p><p class="whitespace-pre-wrap break-words">{{order.manual_publication.comment}}</p><p class="text-muted-foreground">这是人工确认记录，自动检查结果见后续验证节点。</p></div><form v-else-if="canRecord" class="mt-5 grid gap-4" @submit.prevent="save"><div class="grid gap-4 sm:grid-cols-2"><Field><FieldLabel>发布结果</FieldLabel><OptionPicker v-model="draft.result" :disabled="busy" :options="[{value:'success',label:'发布成功'},{value:'failed',label:'发布失败'}]"/></Field><Field><FieldLabel>实际发布时间</FieldLabel><DateFilter v-model="draft.performed_at" type="datetime" :disabled="busy"/></Field></div><Field><FieldLabel for="manual-publication-comment">发布结果与核对依据</FieldLabel><Textarea id="manual-publication-comment" v-model="draft.comment" :disabled="busy" placeholder="填写实际操作结果、接口或日志核对情况；失败时说明原因" rows="3"/></Field><label class="flex items-start gap-2 text-sm"><Checkbox v-model="draft.confirmed" :disabled="busy"/><span>我已核对本次制品摘要，并按上述说明完成操作。</span></label><FieldError v-if="error">{{error}}</FieldError><div class="flex justify-end"><Button :disabled="busy||loading||!draft.confirmed||!draft.comment?.trim()" type="submit">{{busy?'登记中…':'登记发布结果'}}</Button></div></form><p v-else-if="order.status==='pending_approval'" class="mt-4 text-sm text-muted-foreground">等待审批通过后安排发布。</p><p v-else-if="order.status==='waiting_manual'" class="mt-4 text-sm text-muted-foreground">等待指定负责人登记发布结果，下载不会自动完成此节点。</p></section></template>
