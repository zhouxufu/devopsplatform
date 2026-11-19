<script setup>
import {computed,onMounted,onBeforeUnmount,ref,watch} from 'vue'



import {toast} from '../../shared/composables/toast.js'
const props=defineProps({environment:Object})
const emit=defineEmits(['close','state'])
const connections=ref([]),connection=ref(''),previous=ref(''),loading=ref(true),saving=ref(false),error=ref(''),ready=ref(false)
const dirty=computed(()=>ready.value&&connection.value!==previous.value)
watch([dirty,saving],()=>emit('state',{dirty:dirty.value,busy:saving.value}),{immediate:true})
let sequence=0
const url=()=>'/api/resources/environments/'+encodeURIComponent(props.environment.name)+'/nacos'
async function read(url,opt){const r=await fetch(url,opt),data=await r.json();if(!r.ok)throw Error(data.error||'请求失败');return data}
async function load(){const seq=++sequence;loading.value=true;error.value='';try{const [c,b]=await Promise.all([read('/api/resources/connections'),read(url())]);if(seq!==sequence)return;if(!Array.isArray(c)||!b||Array.isArray(b))throw Error('Nacos 接入数据格式异常');connections.value=c.filter(v=>v.kind==='nacos');connection.value=previous.value=b.connection||'';ready.value=true}catch(e){if(seq===sequence)error.value=e.message}finally{if(seq===sequence)loading.value=false}}
async function save(){if(saving.value||loading.value||error.value)return;saving.value=true;try{const data=await read(url(),{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({connection:connection.value,previous:previous.value})});previous.value=data.connection||'';toast('Nacos 接入已保存，未发布配置','success');emit('close')}catch(e){toast(e.message,'error')}finally{saving.value=false}}
async function close(){if(saving.value)return;if(dirty.value&&!await confirmAction('关闭将放弃未保存的 Nacos 接入修改。','放弃修改？',{confirmButtonText:'放弃修改',cancelButtonText:'继续编辑'}))return;sequence++;emit('close')}
function cancel(){close();return false}
onMounted(load)
onBeforeUnmount(()=>sequence++)

import OptionPicker from '../../shared/components/OptionPicker.vue'

import {confirmAction} from '../../shared/shadcnFeedback.js'


import {Button,Empty,EmptyContent,EmptyDescription,EmptyHeader,EmptyTitle,Field,FieldDescription,FieldError,FieldLabel,Sheet,SheetContent,SheetDescription,SheetFooter,SheetHeader,SheetTitle,Spinner} from '../../shared/shadcn-ui.js'
</script>
<template><Sheet :open="true" @update:open="async v=>{if(!v){if(await (cancel)()===false)return;}}"><SheetContent class="data-[side=right]:w-full gap-0 overflow-hidden p-0 data-[side=right]:sm:max-w-[var(--editor-width)]" :class="false?'inset-0 h-dvh max-h-dvh w-screen max-w-none translate-x-0 translate-y-0 rounded-none sm:max-w-none':''" :style="{'--editor-width':typeof ('min(640px, 100vw)')==='number'?('min(640px, 100vw)')+'px':('min(640px, 100vw)')}" :show-close-button="!saving" @interact-outside="e=>{if(!(false))e.preventDefault()}" @escape-key-down="e=>{if(!(!saving))e.preventDefault()}"><SheetHeader class="shrink-0 border-b p-5 pr-12"><SheetTitle>{{environment.display_name+' · Nacos 接入'}}</SheetTitle><SheetDescription class="sr-only">详情与操作</SheetDescription></SheetHeader><div class="min-h-0 flex-1 overflow-auto p-5"><div v-if="loading" tip="正在读取接入配置…" class="relative min-w-0" :aria-busy="true"><div v-if="true" class="flex items-center justify-center gap-2 p-4" role="status"><Spinner/><span class="sr-only">正在读取</span></div></div><Empty v-else-if="error"><EmptyHeader><EmptyTitle>{{"Nacos 接入加载失败"}}</EmptyTitle><EmptyDescription>{{error}}</EmptyDescription></EmptyHeader><EmptyContent><Button @click="load" variant="outline" size="default" type="button">重新读取</Button></EmptyContent></Empty><fieldset v-else :disabled="saving" class="min-w-0 space-y-5" ><Field><FieldLabel>本环境 Nacos 连接</FieldLabel><OptionPicker v-model="connection" :options="connections.map(c=>({value:c.name,label:c.name+' · '+(c.config?.namespace||'public')}))" all-label="未配置"  :style="{width:'100%',maxWidth:'100%'}" aria-label="环境 Nacos 连接"/><FieldDescription>包含本环境的 Namespace 与认证；应用再选择 Data ID，此处保存不会发布任何配置。</FieldDescription></Field><small v-if="!connections.length">暂无 Nacos 连接，请先在“连接”分类中创建。</small></fieldset></div><SheetFooter class="shrink-0 flex-wrap border-t p-4 sm:flex-row sm:justify-end"><Button @click="close" variant="outline" size="default" type="button" :disabled="(saving)">取消</Button><Button @click="save" variant="default" size="default" type="button" :disabled="(loading||!!error)||(saving)"><Spinner v-if="saving"/>保存接入</Button></SheetFooter></SheetContent></Sheet></template>
<style scoped>small{color:var(--muted-foreground)}</style>
