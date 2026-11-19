<script setup>
import {onMounted,onBeforeUnmount,ref,watch} from 'vue'

import PlatformContent from '../shared/components/PlatformContent.vue'
import {platformPage,platformPageFromHash} from './platformPages.js'
import {AUTH_RESTORED_EVENT} from '../shared/platformAuth.js'
const me=ref(null),plugins=ref([]),view=ref(platformPageFromHash(location.hash)?.id||''),active=ref(true),content=ref(null),state=ref({dirty:false,busy:false})
const notify=data=>{if(window.parent!==window)window.parent.postMessage(data,location.origin)}
watch(state,value=>notify({type:'workspace.dirty',...value}),{deep:true,immediate:true})
async function identity(){const response=await fetch('/api/me');if(response.ok)me.value=await response.json();plugins.value=await fetch('/api/plugins').then(r=>r.ok?r.json():[]).catch(()=>[])}
async function navigate(id){if(!platformPage(id,me.value||{},plugins.value)||id===view.value)return;if(await content.value?.canLeave()===false)return;location.hash='#/'+id}
function route(){const page=platformPageFromHash(location.hash);view.value=page?.id||'';document.title=page?.label||'平台管理';notify({type:'context.visibility',service:false,environment:false})}
function message(event){if(event.source!==window.parent||event.origin!==location.origin)return;if(event.data?.type==='workspace.visibility')active.value=event.data.active===true;if(event.data?.type==='workspace.navigate')navigate(platformPageFromHash(event.data.hash)?.id)}
onMounted(()=>{identity();window.addEventListener('hashchange',route);window.addEventListener('message',message);window.addEventListener(AUTH_RESTORED_EVENT,identity);route()})
onBeforeUnmount(()=>{window.removeEventListener('hashchange',route);window.removeEventListener('message',message);window.removeEventListener(AUTH_RESTORED_EVENT,identity)})
function beforeUnload(event){if(state.value.dirty||state.value.busy){event.preventDefault();event.returnValue=''}}
onMounted(()=>window.addEventListener('beforeunload',beforeUnload));onBeforeUnmount(()=>window.removeEventListener('beforeunload',beforeUnload))



import {Spinner} from '../shared/shadcn-ui.js'
</script>
<template><main class="platform-page"><div v-if="!me" tip="正在读取平台身份…" class="relative min-w-0" :aria-busy="true"><div v-if="true" class="flex items-center justify-center gap-2 p-4" role="status"><Spinner/><span class="sr-only">正在读取</span></div></div><PlatformContent v-else ref="content" :view="platformPage(view,me,plugins)?view:''" :current-user="me" :active="active" @state="state=$event" @switch="navigate"/></main></template>
<style scoped>.platform-page{height:100vh;overflow:auto;background:var(--bg);box-sizing:border-box;min-width:0}</style>
