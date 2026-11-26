<script setup>
import {inject,ref,watch,onBeforeUnmount,computed,nextTick} from 'vue'
import {deliveryDrafts,deliveryDraftKey} from '../deliveryDrafts.js'
import ApplicationResourcesEditor from './ApplicationResourcesEditor.vue'
import {editorAssetURL,editorProps,editorRequestAllowed,bridgeError} from '../../delivery-editor/bridge.js'
import {deliveryNextWorkspace} from '../deliveryEntry.js'
import {openWorkspace} from '../../research/api.js'
const props=defineProps({focusChangeId:Number,focusPrerequisites:Boolean,inline:Boolean,embeddedSubmit:Boolean,hideNextSteps:Boolean,show:Boolean,batch:Boolean,item:Object,services:Array,workItems:Array,canManage:Function})
const emit=defineEmits(['close','saved','open-sync','state'])
const api=inject('api'),frame=ref(null),mounted=ref(false),ready=ref(false),height=ref(520),state=ref({dirty:false,busy:false}),showBindings=ref(false),bindingService=ref(''),failure=ref('')
const uiActor=ref(''),uiWorkspace=window.name||location.pathname
const bridgedProps=()=>({...editorProps(props),actor:uiActor.value,workspace:uiWorkspace})
watch(uiActor,()=>{if(port&&ready.value)port.postMessage({type:'props',props:bridgedProps()})})
const source=editorAssetURL(document.baseURI),key=computed(()=>deliveryDraftKey(props))
let port,commandID=0,inertElements=[],disposed=false,returnFocus;const commands=new Map()
const clone=value=>JSON.parse(JSON.stringify(value))
function restoreFocus(){for(const [element,inert] of inertElements)element.inert=inert;inertElements=[]}
function updateInert(){restoreFocus();if(props.show&&!props.inline&&!showBindings.value){for(const element of document.body.children)if(element!==frame.value&&!element.contains(frame.value)&&element.tagName!=='SCRIPT'){inertElements.push([element,element.inert]);element.inert=true}}}
watch(()=>props.show,shown=>{if(shown){mounted.value=true;returnFocus=document.activeElement}nextTick(()=>{updateInert();if(!shown&&returnFocus?.isConnected)returnFocus.focus?.({preventScroll:true})})},{immediate:true})
watch(showBindings,()=>{nextTick(updateInert);if(!showBindings.value)command('refreshResources')})
watch(()=>editorProps(props),()=>{if(port&&ready.value)port.postMessage({type:'props',props:bridgedProps()})},{deep:true})
function open(){
 api('/api/me').then(me=>{uiActor.value=me.username||''}).catch(()=>{})
 port?.close();ready.value=false;failure.value=''
 const channel=new MessageChannel();port=channel.port1
 port.onmessage=async({data})=>{
  if(disposed)return
  if(data.type==='api'){
   const method=data.options?.method||'GET'
   if(!Number.isSafeInteger(data.id)||!editorRequestAllowed(data.path,method)){port.postMessage({type:'api-result',id:data.id,error:{message:'编辑器请求不在允许范围内'}});return}
   const reply=port
   try{const result=await api(data.path,{method,...(data.options?.body!==undefined?{body:data.options.body,headers:{'Content-Type':'application/json'}}:{})});reply.postMessage({type:'api-result',id:data.id,result,...(Array.isArray(result?.changes)?{editableIDs:result.changes.filter(c=>props.canManage?.(c)).map(c=>c.id)}:{})})}catch(error){reply.postMessage({type:'api-result',id:data.id,error:bridgeError(error)})}
  }else if(data.type==='command-result'){const pending=commands.get(data.id);if(!pending)return;clearTimeout(pending.timer);commands.delete(data.id);data.error?pending.reject(Error(data.error.message)):pending.resolve(data.result)}
  else if(data.type==='event'){
   if(data.name==='ready'){ready.value=true;port.postMessage({type:'props',props:bridgedProps()})}
   if(data.name==='state'){state.value=data.value;emit('state',data.value)}
   if(data.name==='height'&&Number.isFinite(data.value))height.value=Math.min(12000,Math.max(220,data.value))
   if(data.name==='draft'){const entry=data.value;if(entry?.draft)deliveryDrafts.set(entry.key,entry.draft);else if(entry)deliveryDrafts.delete(entry.key)}
   if(data.name==='configure'){bindingService.value=data.value;showBindings.value=true;updateInert()}
   if(data.name==='next-step'&&!props.hideNextSteps&&!props.embeddedSubmit&&!state.value.dirty&&!state.value.busy){
    const target=props.batch?deliveryNextWorkspace('changes',data.value):deliveryNextWorkspace(data.value,props.item?.id)
    if(target){emit('close');openWorkspace(target.plugin,target.hash)}
   }
   if(data.name==='saved'&&data.value!=='reopened')deliveryDrafts.delete(key.value)
   if(['close','saved','open-sync'].includes(data.name))emit(data.name,data.value)
  }
 }
 port.start()
 frame.value.contentWindow.postMessage({type:'delivery-editor.connect',props:bridgedProps(),draft:clone(deliveryDrafts.get(key.value)||null)},location.origin,[channel.port2])
 updateInert()
}
function command(name){if(!port||!ready.value)return Promise.resolve(false);return new Promise((resolve,reject)=>{const id=++commandID;const timer=setTimeout(()=>{commands.delete(id);reject(Error('编辑器响应超时，请核对草稿，不自动重试'))},120000);commands.set(id,{resolve,reject,timer});port.postMessage({type:'command',id,name})})}
function unload(event){if(props.show&&(state.value.dirty||state.value.busy)){event.preventDefault();event.returnValue=''}}
window.addEventListener('beforeunload',unload)
onBeforeUnmount(()=>{disposed=true;restoreFocus();port?.close();window.removeEventListener('beforeunload',unload);for(const pending of commands.values()){clearTimeout(pending.timer);pending.reject(Error('编辑窗口已关闭'))}})
defineExpose({hasDraft:()=>deliveryDrafts.has(key.value)||state.value.dirty,refreshAfterSync:()=>command('refreshAfterSync'),saveDrafts:()=>command('saveDrafts')})
</script>
<template>
 <Teleport to="body" :disabled="inline"><iframe v-if="mounted" v-show="show&&!showBindings" ref="frame" :src="source" title="交付内容编辑" :class="inline?'delivery-frame-inline':'delivery-frame-modal'" :style="inline?{height:height+'px'}:{}" @load="open"/></Teleport>
 <ApplicationResourcesEditor :show="showBindings" :services="services" :initial-service="bindingService" @close="showBindings=false"/>
</template>
<style scoped>
.delivery-frame-modal{position:fixed;inset:0;width:100%;height:100dvh;border:0;background:transparent;z-index:2000}
.delivery-frame-inline{display:block;width:100%;border:0;min-height:220px;background:transparent}
</style>
