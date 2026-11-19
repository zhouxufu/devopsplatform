<script setup>

import {computed,onMounted,ref} from 'vue'
import PlatformContent from './PlatformContent.vue'
import {usePlatformCapabilities} from '../composables/platformCapabilities.js'
const props=defineProps({view:{type:String,default:''},plugin:{type:String,default:'deploy'},directAPI:Boolean,currentUser:{type:Object,default:null}})
const emit=defineEmits(['close','switch'])
const {mcpAvailable}=usePlatformCapabilities()
const me=ref(null),content=ref(null),state=ref({dirty:false,busy:false}),leaving=ref(false)
const admin=computed(()=>(props.currentUser||me.value)?.role==='admin')
async function leave(view){if(leaving.value||view===props.view)return;leaving.value=true;try{if(await content.value?.canLeave()!==false){state.value={dirty:false,busy:false};emit(view?'switch':'close',view)}}finally{leaving.value=false}}
onMounted(()=>{if(!props.currentUser)fetch('/api/me').then(r=>r.ok?r.json():null).then(v=>me.value=v).catch(()=>{})})



import {Button,Dialog,DialogContent,DialogDescription,DialogFooter,DialogHeader,DialogTitle,Tabs,TabsContent,TabsList,TabsTrigger} from '../shadcn-ui.js'
</script>
<template><Dialog :open="!!view" @update:open="async v=>{if(!v){leave('')}}"><DialogContent class="flex max-h-[92dvh] flex-col gap-0 overflow-hidden p-0 sm:max-w-[var(--editor-width)]" :class="false?'inset-0 h-dvh max-h-dvh w-screen max-w-none translate-x-0 translate-y-0 rounded-none sm:max-w-none':''" :style="{'--editor-width':typeof (&quot;min(1200px, calc(100vw - 48px))&quot;)==='number'?(&quot;min(1200px, calc(100vw - 48px))&quot;)+'px':(&quot;min(1200px, calc(100vw - 48px))&quot;)}" :show-close-button="!state.busy" @interact-outside="e=>{if(!(!state.busy))e.preventDefault()}" @escape-key-down="e=>{if(!(!state.busy))e.preventDefault()}"><DialogHeader class="shrink-0 border-b p-5 pr-12"><DialogTitle>平台公共中心</DialogTitle><DialogDescription class="sr-only">详情与操作</DialogDescription></DialogHeader><div class="min-h-0 flex-1 overflow-auto p-5"><section class="platform-center"><header><Tabs :model-value="view" @update:model-value="leave($event)"><div class="flex min-w-0 items-center gap-2"><TabsList class="h-auto! max-w-full flex-wrap gap-y-1 [&>[data-slot=tabs-trigger]]:h-7"><TabsTrigger :value="&quot;apps&quot;">应用中心</TabsTrigger><TabsTrigger :value="&quot;resources&quot;">资源中心</TabsTrigger><TabsTrigger v-if="mcpAvailable" :value="&quot;mcp&quot;">MCP 与集成</TabsTrigger><TabsTrigger v-if="admin" :value="&quot;plugins&quot;">插件管理</TabsTrigger></TabsList></div><TabsContent :value="&quot;apps&quot;" force-mount v-show="!(view!==&quot;apps&quot;)" class="min-w-0"></TabsContent><TabsContent :value="&quot;resources&quot;" force-mount v-show="!(view!==&quot;resources&quot;)" class="min-w-0"></TabsContent><TabsContent v-if="mcpAvailable" :value="&quot;mcp&quot;" force-mount v-show="!(view!==&quot;mcp&quot;)" class="min-w-0"></TabsContent><TabsContent v-if="admin" :value="&quot;plugins&quot;" force-mount v-show="!(view!==&quot;plugins&quot;)" class="min-w-0"></TabsContent></Tabs></header><main :class="{'mcp-content':view==='mcp'}"><PlatformContent v-if="view" ref="content" :view="view" :current-user="currentUser||me" :directAPI="directAPI" @state="state=$event" @switch="leave"/></main></section></div></DialogContent></Dialog></template>
<style>.platform-center{height:min(820px,calc(100dvh - 80px));display:flex;flex-direction:column;min-width:0;background:var(--bg)}.platform-center>header{flex-shrink:0;padding:0 44px 0 24px;border-bottom:1px solid var(--border);background:var(--panel)}.platform-center>main{flex:1;min-height:0;overflow:auto;padding:20px 24px}.platform-center>main.mcp-content{padding:0;overflow:hidden}@media(max-width:650px){.platform-center>header{padding-left:12px}.platform-center>main{padding:12px}}</style>
