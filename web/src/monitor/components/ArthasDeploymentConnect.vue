<script setup>
import {ref,computed,watch,onBeforeUnmount} from 'vue'

import {requestJSON} from '../../shared/composables/asyncData.js'
const props=defineProps({service:String,environment:String})
const emit=defineEmits(['connected','busy'])
const visible=ref(false),instances=ref([]),selected=ref(''),processes=ref([]),fingerprint=ref(''),loading=ref(false),checking=ref(false),attaching=ref(false),error=ref('')
const instance=computed(()=>instances.value.find(i=>i.id===selected.value))
let generation=0,controller,disposed=false
const api=(path,method='GET',body,signal)=>requestJSON('api/arthas/deployed'+path,{method,signal,...(body?{headers:{'Content-Type':'application/json'},body:JSON.stringify(body)}:{})})
watch(attaching,value=>emit('busy',value))
async function open(){if(attaching.value)return;visible.value=true;await reload()}
async function reload(){
 const current=++generation;controller?.abort();controller=new AbortController()
 loading.value=true;checking.value=false;error.value='';instances.value=[];selected.value='';processes.value=[];fingerprint.value=''
 try{const result=await api('?'+new URLSearchParams({service:props.service||'',environment:props.environment||''}),'GET',undefined,controller.signal);if(current!==generation||disposed)return;if(!Array.isArray(result))throw Error('部署实例列表格式错误');instances.value=result;if(result.length===1){selected.value=result[0].id;await inspect()}}
 catch(e){if(current===generation&&e.name!=='AbortError')error.value=e.message}
 finally{if(current===generation)loading.value=false}
}
async function inspect(){
 const current=++generation;controller?.abort();controller=new AbortController();loading.value=false;processes.value=[];fingerprint.value='';error.value='';if(!selected.value)return
 checking.value=true;const id=selected.value
 try{const result=await api('/'+encodeURIComponent(id),'GET',undefined,controller.signal);if(current!==generation||disposed)return;if(result.instance?.id!==id||!Array.isArray(result.processes))throw Error('检测结果与所选实例不一致');processes.value=result.processes;if(result.processes.length===1)fingerprint.value=result.processes[0].fingerprint}
 catch(e){if(current===generation&&e.name!=='AbortError')error.value=e.message}
 finally{if(current===generation)checking.value=false}
}
async function attach(){
 if(attaching.value||checking.value||!instance.value||!fingerprint.value)return
 const snapshot={id:selected.value,fingerprint:fingerprint.value,service:instance.value.service,environment:instance.value.environment}
 attaching.value=true;error.value=''
 try{const result=await api('/'+encodeURIComponent(snapshot.id)+'/attach','POST',{fingerprint:snapshot.fingerprint});if(disposed)return;if(!result.managed||result.service!==snapshot.service||result.environment!==snapshot.environment)throw Error('接入结果身份不一致，请刷新核对');visible.value=false;emit('connected',result)}
 catch(e){if(!disposed){error.value=e.message;processes.value=[];fingerprint.value=''}}
 finally{attaching.value=false}
}
function close(){if(attaching.value)return;visible.value=false;generation++;controller?.abort()}
watch(()=>[props.service,props.environment],()=>{if(visible.value&&!attaching.value)reload()})
onBeforeUnmount(()=>{disposed=true;generation++;controller?.abort()})
defineExpose({open})

import OptionPicker from '../../shared/components/OptionPicker.vue'



import {Alert,AlertDescription,AlertTitle,Badge,Button,Dialog,DialogContent,DialogDescription,DialogFooter,DialogHeader,DialogTitle,Empty,EmptyContent,EmptyDescription,EmptyHeader,Field,FieldDescription,FieldError,FieldLabel,Spinner} from '../../shared/shadcn-ui.js'
</script>
<template><Dialog :open="visible" @update:open="async v=>{if(!v){visible=false;close()}}"><DialogContent class="flex max-h-[92dvh] flex-col gap-0 overflow-hidden p-0 sm:max-w-[var(--editor-width)]" :class="false?'inset-0 h-dvh max-h-dvh w-screen max-w-none translate-x-0 translate-y-0 rounded-none sm:max-w-none':''" :style="{'--editor-width':typeof (&quot;min(600px,calc(100vw - 24px))&quot;)==='number'?(&quot;min(600px,calc(100vw - 24px))&quot;)+'px':(&quot;min(600px,calc(100vw - 24px))&quot;)}" :show-close-button="!attaching" @interact-outside="e=>{if(!(false))e.preventDefault()}" @escape-key-down="e=>{if(!(!attaching))e.preventDefault()}"><DialogHeader class="shrink-0 border-b p-5 pr-12"><DialogTitle>接入已部署实例</DialogTitle><DialogDescription class="sr-only">详情与操作</DialogDescription></DialogHeader><div class="min-h-0 flex-1 overflow-auto p-5"><Alert v-if="error" @close="error=''" class="deployment-connect-error"><AlertDescription>{{error}}</AlertDescription></Alert><div class="deployment-connect-form relative min-w-0" :aria-busy="loading"><div v-if="loading" class="flex items-center justify-center gap-2 p-4" role="status"><Spinner/><span class="sr-only">正在读取</span></div><fieldset class="min-w-0 space-y-5" ><Field><FieldLabel>部署实例</FieldLabel><OptionPicker v-model="selected" aria-label="选择部署实例" :disabled="attaching||loading" placeholder="选择应用、环境和实例" @change="inspect" class="min-w-36" :options="[...(instances).map((item)=>({value:item.id,label:String(item.name)+&quot; · &quot;+String(item.environment)+&quot; · &quot;+String(item.kind==='docker-host'?'Docker':'Java 主机')}))]"/></Field><div v-if="instance" class="deployment-instance-summary"><Badge variant="secondary">{{instance.kind==='docker-host'?'Docker 容器':'主机 JVM'}}</Badge><span>{{instance.service}} / {{instance.environment}} / {{instance.host}}</span></div><div v-if="checking" tip="正在检测进程和接入方式…" class="relative min-w-0" :aria-busy="true"><div v-if="true" class="flex items-center justify-center gap-2 p-4" role="status"><Spinner/><span class="sr-only">正在读取</span></div></div><Field v-else-if="processes.length"><FieldLabel>目标 JVM</FieldLabel><OptionPicker v-model="fingerprint" aria-label="选择目标 JVM" :disabled="attaching" class="min-w-36" :options="[...(processes).map((item)=>({value:item.fingerprint,label:&quot;Java · PID &quot;+String(item.pid)+String(item.attach_mode?.startsWith('native-')?' · 轻量接入（无需完整 JDK）':'')}))]"/><FieldDescription>{{processes.length>1?'检测到多个 JVM，请明确选择目标进程。':'已自动识别此实例的唯一 JVM。'}}</FieldDescription></Field><Empty v-else-if="!loading&&!instances.length&&!error"><EmptyHeader><EmptyDescription>{{"当前范围没有可接入的部署实例。支持已在平台部署的 Linux Java 主机及 Docker 应用。"}}</EmptyDescription></EmptyHeader></Empty></fieldset></div><p class="deployment-connect-note">自动适配 Java 8+ 的 JDK / JRE，无需更换镜像或重启应用。通过 SSH 私有通道连接；Docker 宿主机需 root、nsenter 和 curl。</p></div><DialogFooter class="shrink-0 flex-wrap border-t p-4"><div class="deployment-connect-actions"><Button @click="reload" variant="outline" size="default" type="button" :disabled="(attaching)">刷新实例</Button><Button v-if="instance" @click="inspect" variant="outline" size="default" type="button" :disabled="(attaching||checking)">重新检测</Button><Button @click="close" variant="outline" size="default" type="button" :disabled="(attaching)">取消</Button><Button @click="attach" variant="default" size="default" type="button" :disabled="(!fingerprint||checking||loading)||(attaching)"><Spinner v-if="attaching"/>{{attaching?'正在接入，最长约 150 秒':'接入所选 JVM'}}</Button></div></DialogFooter></DialogContent></Dialog></template>
<style scoped>.deployment-connect-form{display:block;width:100%}.deployment-connect-error{margin-bottom:16px}.deployment-instance-summary{display:flex;gap:8px;align-items:center;flex-wrap:wrap;margin-bottom:16px;overflow-wrap:anywhere}.deployment-connect-note{color:var(--muted-foreground);line-height:1.7;margin:12px 0 20px}.deployment-connect-actions{display:flex;justify-content:flex-end;gap:8px;flex-wrap:wrap}</style>
