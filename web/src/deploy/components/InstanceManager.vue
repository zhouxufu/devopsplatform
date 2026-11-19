<script setup>
import {computed,nextTick,onBeforeUnmount,ref,watch} from 'vue'
import {Button,Badge,Checkbox,Field,FieldLabel,FieldError,Textarea,Spinner} from '../../shared/shadcn-ui.js'
import ReleaseRecovery from './ReleaseRecovery.vue'
import {blockingRelease} from '../releaseRecovery.js'
import DataTable from '../../shared/components/DataTable.vue'
import HelpTip from '../../shared/components/HelpTip.vue'
import {toast} from '../../shared/composables/toast.js'
import {randomUUID} from '../../shared/uuid.js'
import {instanceActionPayload,instanceStateText} from '../instanceActions.js'
const props=defineProps({service:String,target:Object,me:Object,releases:{type:Array,default:()=>[]},deploys:{type:Array,default:()=>[]}})
const emit=defineEmits(['close','busy','refresh'])
const blocker=computed(()=>blockingRelease(props.releases,props.service,props.target.environment))
function recoveryBusy(value){busy.value=value;emit('busy',value);if(!value)load()}
async function recovered(){emit('refresh');await load()}
const rows=ref([]),loading=ref(false),error=ref(''),busy=ref(false),pending=ref(null),logHost=ref(null),log=ref(''),logLoading=ref(false),logError=ref('')
const pendingForm=ref(null),logSection=ref(null)
let sequence=0,logSequence=0,disposed=false
const canManage=computed(()=>props.me?.role==='admin'||props.me?.username==='local')
const endpoint=computed(()=>`api/services/${encodeURIComponent(props.service)}/environments/${encodeURIComponent(props.target.environment)}/instances`)
const columns=[{id:'host',label:'主机 / 实例',required:true,size:210,pin:'left',flex:true},{id:'state',label:'状态',required:true,size:140},{id:'version',label:'当前版本',size:150},{id:'image',label:'镜像 / 进程',size:260,defaultHidden:true},{id:'checked_at',label:'检查时间',size:145},{id:'actions',label:'操作',required:true,size:230,pin:'right'}]
async function request(path,options){const r=await fetch(path,options),body=await r.json().catch(()=>({}));if(!r.ok)throw Error(body.error||'读取失败');return body}
async function load(){if(busy.value)return;const seq=++sequence;loading.value=true;error.value='';try{const data=await request(endpoint.value);if(seq===sequence&&!disposed){rows.value=data;pending.value=null}}catch(e){if(seq===sequence){error.value=e.message;toast(e.message,'error')}}finally{if(seq===sequence)loading.value=false}}
async function prepare(instance,action){if(busy.value||loading.value||error.value)return;pending.value={instance:{...instance},action,requestId:randomUUID(),confirmed:false,reason:'',attempted:false,error:''};logHost.value=null;await nextTick();pendingForm.value?.scrollIntoView({behavior:'smooth',block:'nearest'});pendingForm.value?.querySelector('textarea')?.focus({preventScroll:true})}
async function execute(){if(busy.value||pending.value?.attempted)return;const p=pending.value;let body;try{body=instanceActionPayload(p.instance,props.target,p)}catch(e){p.error=e.message;return}
 p.error='';p.attempted=true;busy.value=true;emit('busy',true)
 try{await request(`${endpoint.value}/${encodeURIComponent(p.instance.host)}/${p.action}`,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});pending.value=null;toast('操作已完成，实例状态已刷新','success')}
 catch(e){p.error=e.message+'。请刷新状态核对结果，同一操作不会自动重试。';toast(e.message,'error')}
 finally{busy.value=false;emit('busy',false);if(!disposed&&!pending.value)await load()}
}
async function showLogs(instance){if(busy.value)return;const seq=++logSequence;logHost.value={...instance};log.value='';logLoading.value=true;logError.value='';pending.value=null;await nextTick();logSection.value?.scrollIntoView({behavior:'smooth',block:'nearest'})
 try{const data=await request(`${endpoint.value}/${encodeURIComponent(instance.host)}/logs?fingerprint=${encodeURIComponent(instance.fingerprint)}`);if(seq===logSequence&&!disposed)log.value=data.text||'最近未输出日志'}catch(e){if(seq===logSequence)logError.value=e.message}finally{if(seq===logSequence)logLoading.value=false}
}
watch(()=>[props.service,props.target.environment],()=>{sequence++;logSequence++;logHost.value=null;pending.value=null;load()},{immediate:true})
onBeforeUnmount(()=>{disposed=true;sequence++;logSequence++})
</script>
<template><section class="my-4 min-w-0 space-y-4 rounded-lg border bg-background p-4" aria-label="应用实例管理">
 <div class="flex flex-wrap items-center justify-between gap-3"><div class="flex flex-wrap items-center gap-2"><h3 class="font-semibold">实例管理 · {{target.display_name||target.environment}}</h3><Badge variant="secondary">{{target.target_type==='docker-host'?'Docker':'Java 进程'}}</Badge><HelpTip text="操作当前实例，保留已有制品、挂载和变量。启动或重启返回运行状态不等同于业务验证通过；Docker 健康状态单独展示。"/></div><div class="flex gap-2"><Button variant="outline" :disabled="loading||busy" @click="load"><Spinner v-if="loading"/>刷新状态</Button><Button variant="outline" :disabled="busy" @click="emit('close')">返回环境</Button></div></div>
 <ReleaseRecovery :order="blocker" :deploys="deploys" :me="me" @busy="recoveryBusy" @updated="recovered" @refresh="emit('refresh')"/>
 <FieldError v-if="error">{{error}}</FieldError>
 <DataTable :rows="rows" :columns="columns" :table-key="'application-instances:'+service+':'+target.environment" :actor="me?.username" row-key="host" :loading="loading" empty-text="该环境没有可管理的主机实例">
  <template #cell-host="{record}"><b>{{record.host}}</b><p class="text-xs text-muted-foreground">{{record.address}}</p><code v-if="record.container" class="text-xs break-all">{{record.container}}</code></template>
  <template #cell-state="{record}"><Badge :variant="record.state==='running'?'secondary':['unknown','unreachable','inspection-failed'].includes(record.state)?'destructive':'outline'">{{instanceStateText(record.state)}}</Badge><p v-if="record.kind==='docker-host'&&['running','stopped','restarting'].includes(record.state)" class="mt-1 text-xs text-muted-foreground">{{({healthy:'容器健康',unhealthy:'健康检查失败',starting:'健康检查中'})[record.health]||'未配置容器健康检查'}}</p><p v-if="record.message" class="mt-1 text-xs break-words text-muted-foreground">{{record.message}}</p></template>
  <template #cell-version="{record}"><code>{{record.version||record.release||'—'}}</code></template>
  <template #cell-image="{record}"><code class="text-xs break-all">{{record.image||'PID '+(record.pid||'—')}}</code><p v-if="record.container_id" class="text-xs break-all">{{record.container_id}}</p></template>
  <template #cell-checked_at="{record}"><span class="text-sm">{{record.checked_at?new Date(record.checked_at*1000).toLocaleTimeString('zh-CN'):'—'}}</span></template>
  <template #cell-actions="{record}"><div class="flex flex-wrap gap-1"><template v-if="record.controllable&&!blocker"><Button v-if="record.state==='stopped'&&canManage" variant="outline" size="sm" :disabled="busy||loading||!!error" @click="prepare(record,'start')">启动</Button><template v-if="record.state==='running'&&canManage"><Button variant="outline" size="sm" :disabled="busy||loading||!!error" @click="prepare(record,'restart')">重启</Button><Button variant="ghost" size="sm" :disabled="busy||loading||!!error" @click="prepare(record,'stop')">停止</Button><Button v-if="['ssh-jar','ssh-java-bundle'].includes(record.kind)" variant="ghost" size="sm" :disabled="busy||loading||!!error" @click="prepare(record,'ready')">就绪检查</Button></template><Button v-if="record.state==='restarting'&&canManage" variant="ghost" size="sm" :disabled="busy||loading||!!error" @click="prepare(record,'stop')">停止</Button></template><Button v-if="record.owned" variant="ghost" size="sm" :disabled="busy||loading||!!error" @click="showLogs(record)">日志</Button><span v-if="!record.owned" class="text-xs text-muted-foreground">归属或状态待核对</span></div></template>
 </DataTable>
 <form v-if="pending" ref="pendingForm" class="space-y-3 rounded-lg border p-4" aria-label="确认实例操作" @submit.prevent="execute"><h4 class="font-semibold">确认{{({start:'启动',stop:'停止',restart:'重启',ready:'检查'})[pending.action]}} · {{service}} / {{target.display_name||target.environment}} / {{pending.instance.host}}</h4><p class="text-sm text-muted-foreground">当前版本 {{pending.instance.version||'—'}}{{pending.action==='restart'||pending.action==='stop'?'，操作期间可能短暂不可用。':'。'}}</p><Field><FieldLabel for="instance-reason">操作原因{{target.protected||target.require_reason?'（必填）':'（选填）'}}</FieldLabel><Textarea id="instance-reason" v-model="pending.reason" :disabled="busy||pending.attempted" rows="2"/></Field><label class="flex items-center gap-2 text-sm"><Checkbox v-model="pending.confirmed" :disabled="busy||pending.attempted"/>我已核对应用、环境和当前实例</label><FieldError v-if="pending.error">{{pending.error}}</FieldError><div class="flex justify-end gap-2"><Button variant="outline" :disabled="busy" type="button" @click="pending=null">取消</Button><Button :disabled="busy||!pending.confirmed||pending.attempted" type="submit"><Spinner v-if="busy"/>确认{{({start:'启动',stop:'停止',restart:'重启',ready:'检查'})[pending.action]}}</Button></div></form>
 <section v-if="logHost" ref="logSection" class="min-w-0 rounded-lg border p-4" aria-label="实例运行日志"><div class="mb-3 flex flex-wrap items-center justify-between gap-2"><h4 class="font-semibold">{{logHost.host}} · 最近日志<HelpTip text="按需读取最后 200 行、最多 64 KiB；Docker 额外限制最近 15 分钟。敏感值在服务端脱敏，完整日志检索请进入日志模块。"/></h4><div class="flex gap-2"><Button variant="outline" size="sm" :disabled="logLoading||busy" @click="showLogs(logHost)"><Spinner v-if="logLoading"/>刷新日志</Button><Button variant="ghost" size="sm" @click="logHost=null;logSequence++">收起日志</Button></div></div><FieldError v-if="logError">{{logError}}</FieldError><pre v-else class="max-h-[45dvh] min-h-24 overflow-auto whitespace-pre-wrap break-all rounded-md bg-muted p-3 text-xs">{{logLoading?'正在读取…':log}}</pre></section>
</section></template>
