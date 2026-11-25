import {createApp,h,reactive,ref,provide,nextTick} from 'vue'
import Editor from './Editor.vue'
import {deliveryDrafts,deliveryDraftKey} from '../deploy/deliveryDrafts.js'
import {TooltipProvider} from '../shared/shadcn-ui.js'
import FeedbackHost from '../shared/components/FeedbackHost.vue'
import {initTheme,themeSyncPayload} from '../shared/composables/theme.js'
import '../shared/shadcn.css'
import './editor.css'
import {editorMessage} from './bridge.js'
initTheme({designSystem:'shadcn'})
document.body.dataset.deliveryEditor=''
let booted=false
window.addEventListener('message',function initialize(event){
 if(booted||event.source!==window.parent||event.origin!==location.origin||event.data?.type!=='delivery-editor.connect'||!event.ports[0])return
 booted=true;window.removeEventListener('message',initialize)
 const port=event.ports[0],props=reactive(event.data.props),editable=new Set(),requests=new Map();let serial=0
 const notify=(name,value)=>port.postMessage(editorMessage({type:'event',name,value}))
 if(event.data.draft)deliveryDrafts.set(deliveryDraftKey(props),event.data.draft)
 const api=(path,options={})=>new Promise((resolve,reject)=>{const id=++serial;requests.set(id,{resolve,reject});port.postMessage({type:'api',id,path,options:{method:options.method||'GET',body:options.body}})})
 const instance=ref(null)
 port.onmessage=async({data})=>{
  if(data.type==='api-result'){const request=requests.get(data.id);if(!request)return;requests.delete(data.id);if(data.error)request.reject(Object.assign(Error(data.error.message),data.error));else{if(data.editableIDs){editable.clear();data.editableIDs.forEach(id=>editable.add(id))}request.resolve(data.result)}}
  if(data.type==='props')Object.assign(props,data.props)
  if(data.type==='command'){try{const result=await instance.value?.[data.name]?.();port.postMessage({type:'command-result',id:data.id,result:result??false})}catch(error){port.postMessage({type:'command-result',id:data.id,error:{message:error.message}})}}
 }
 port.start()
 createApp({setup(){provide('api',api);return()=>h(TooltipProvider,{},()=>[h(Editor,{...props,canManage:item=>editable.has(item.id),ref:instance,onState:value=>notify('state',value),onClose:()=>notify('close'),onSaved:reason=>notify('saved',reason),onOpenSync:()=>notify('open-sync'),onConfigure:service=>notify('configure',service),onNextStep:value=>notify('next-step',value),onDraft:value=>notify('draft',value)}),h(FeedbackHost)])}}).mount('#app')
 // Inline editors participate in the host dialog's footer. They don't create a second modal.
 let scheduled=false
 const measure=()=>{if(scheduled)return;scheduled=true;requestAnimationFrame(()=>{scheduled=false;if(!props.inline)return;const root=document.querySelector('#app > section');if(root)notify('height',Math.ceil(root.getBoundingClientRect().height))})}
 const observer=new ResizeObserver(measure);nextTick(()=>{const root=document.querySelector('#app > section');if(root)observer.observe(root);measure();notify('ready')})
 window.addEventListener('beforeunload',()=>{observer.disconnect();port.close();for(const request of requests.values())request.reject(Error('编辑窗口已关闭；进行中的写入请核对结果，不要重复提交'))},{once:true})
})
