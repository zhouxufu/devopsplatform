<script setup>
import {ref,watch,onBeforeUnmount} from 'vue'

import {applicationName,applicationIdentifier,applicationNameError,announceApplicationName} from '../applicationName.js'
import {toast} from '../composables/toast.js'
const props=defineProps({show:Boolean,service:Object,catalog:Boolean})
const emit=defineEmits(['close','saved'])
const value=ref(''),expected=ref(''),current=ref(null),loading=ref(false),saving=ref(false),error=ref('')
let controller
async function load(){
 controller?.abort();const request=new AbortController();controller=request
 loading.value=true;error.value='';current.value=null
 try{const r=await fetch(props.catalog?'/api/registry/applications':'api/services',{signal:request.signal});if(!r.ok)throw Error('读取当前应用名称失败');const all=await r.json();if(request!==controller)return;const app=all.find(s=>s.name===props.service?.name);if(!app)throw Error('应用已不存在');current.value=app;value.value=expected.value=applicationName(app)}catch(e){if(e.name!=='AbortError')error.value=e.message}finally{if(request===controller)loading.value=false}
}
watch(()=>[props.show,props.service?.name],([show])=>{if(show)load();else{controller?.abort();controller=null}})
onBeforeUnmount(()=>controller?.abort())
async function save(){
 if(saving.value||loading.value||!current.value)return
 const message=applicationNameError(value.value);if(message)return toast(message,'warning')
 const label=value.value.trim();if(label===expected.value)return emit('close')
 saving.value=true
 try{const url=props.catalog?`/api/registry/applications/${encodeURIComponent(current.value.application_id)}/display-name`:`api/services/${encodeURIComponent(current.value.name)}/display-name`;const r=await fetch(url,{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({display_name:label,expected_display_name:expected.value})});const body=await r.json().catch(()=>({}));if(!r.ok){toast(body.error||'保存名称失败','error');if(r.status===409){error.value='名称可能已更新，请重新读取后核对。当前输入仍保留。';current.value=null}return}const app={...current.value,...body};emit('saved',app);announceApplicationName(app);emit('close');toast('应用名称已更新，标识和部署配置保持不变','success')}catch{toast('请求结果未确认，请刷新核对名称后再操作','error')}finally{saving.value=false}
}

import {copyText} from '../clipboard.js'


import {Alert,AlertDescription,AlertTitle,Button,Dialog,DialogContent,DialogDescription,DialogFooter,DialogHeader,DialogTitle,Field,FieldDescription,FieldError,FieldLabel,Input,Spinner} from '../shadcn-ui.js'
</script>
<template><Dialog :open="show" @update:open="async v=>{if(!v){!saving&amp;&amp;emit('close')}}"><DialogContent class="flex max-h-[92dvh] flex-col gap-0 overflow-hidden p-0 sm:max-w-[var(--editor-width)]" :class="false?'inset-0 h-dvh max-h-dvh w-screen max-w-none translate-x-0 translate-y-0 rounded-none sm:max-w-none':''" :style="{'--editor-width':typeof (&quot;min(480px,92vw)&quot;)==='number'?(&quot;min(480px,92vw)&quot;)+'px':(&quot;min(480px,92vw)&quot;)}" :show-close-button="!saving" @interact-outside="e=>{if(!(!saving))e.preventDefault()}" @escape-key-down="e=>{if(!(!saving))e.preventDefault()}"><DialogHeader class="shrink-0 border-b p-5 pr-12"><DialogTitle>修改应用名称</DialogTitle><DialogDescription class="sr-only">详情与操作</DialogDescription></DialogHeader><div class="min-h-0 flex-1 overflow-auto p-5"><fieldset :disabled="saving||loading" class="min-w-0 space-y-5" @submit="save"><Field><FieldLabel>应用名称 <span class="text-destructive">*</span></FieldLabel><Input v-model="value" placeholder="支持中文，例如智能搜索服务" :maxlength="100" @keydown.enter="save"/><FieldDescription>用于页面显示，保存后不改变部署配置。</FieldDescription></Field><Field><FieldLabel>应用标识（固定）</FieldLabel><span class="min-w-0 break-words"><code>{{applicationIdentifier(current||service)}}</code><Button variant="ghost" size="xs" aria-label="复制内容" @click="copyText(applicationIdentifier(current||service))">复制</Button></span><FieldDescription>不可修改；历史记录、资源绑定及实际部署名称保持不变。</FieldDescription></Field></fieldset><Alert v-if="error"><AlertDescription>{{error}}<Button @click="load" variant="ghost" size="default" type="button" :disabled="(loading)"><Spinner v-if="loading"/>重新读取</Button></AlertDescription></Alert></div><DialogFooter class="shrink-0 flex-wrap border-t p-4"><Button @click="emit('close')" variant="outline" size="default" type="button" :disabled="(saving)">取消</Button><Button @click="save" variant="default" size="default" type="button" :disabled="(!current||!!error)||(saving||loading)"><Spinner v-if="saving||loading"/>保存名称</Button></DialogFooter></DialogContent></Dialog></template>
