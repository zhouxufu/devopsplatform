<script setup>
import PageSurface from '../shared/components/PageSurface.vue'
import {useRoute as useMenuRoute,useRouter as useMenuRouter} from 'vue-router'

import {provide, watch} from 'vue'
import WorkspaceSidebar from '../shared/components/WorkspaceSidebar.vue'
import PlatformNav from '../shared/components/PlatformNav.vue'
import PlatformPanel from '../shared/components/PlatformPanel.vue'
import UiIcon from '../shared/components/UiIcon.vue'
import {ref} from 'vue'
import {useRoute} from 'vue-router'
import {api} from './api.js'
provide('api',(path,opt)=>{if(path.startsWith('api/services/')&&path.endsWith('/resources'))return api('/api/resources/'+path.slice(4),opt);return api(path.startsWith('api/')?path.slice(4):path,opt)})
const panel=ref(''),embedded=window.parent!==window
const route=useRoute()
watch(()=>route.fullPath,()=>{if(embedded)window.parent.postMessage({type:'workspace.label',label:({'/':'任务与交付','/work':'需求与 Bug','/work/delivery':'需求与 Bug · 待上线','/versions':'发布版本','/settings':'工作台设置','/flows':'交付流程'})[route.path]||(route.params.id==='new'?'新建工作项':'工作项 #'+route.params.id)},location.origin)},{immediate:true})
if(embedded)window.parent.postMessage({type:'context.visibility',service:false,environment:false},location.origin)
const menuRoute=useMenuRoute(),menuRouter=useMenuRouter()



import {SidebarMenu,SidebarMenuButton,SidebarMenuItem} from '../shared/shadcn-ui.js'
</script>
<template><div :class="{embedded}" class="research-layout"><WorkspaceSidebar v-slot="{collapsed}"><span class="logo"><UiIcon name="research"/>研发工作台</span><SidebarMenu class="platform-navigation"><SidebarMenuItem v-for="n in [['/','user','个人工作台'],['/work','changes','需求与 Bug'],['/versions','history','发布版本'],['/settings','settings','设置']]" :aria-label="n[2]"><SidebarMenuButton class="h-10 text-sm" :is-active="([menuRoute.path.startsWith('/work/')?'/work':menuRoute.path]).includes(n[0])" @click="(key=>menuRouter.push(key))(n[0])"><UiIcon :name="n[1]"/><span><span>{{n[2]}}</span></span></SidebarMenuButton></SidebarMenuItem></SidebarMenu><PlatformNav @open="panel=$event"/></WorkspaceSidebar><main><PageSurface :fill="route.path==='/work'" :class="{'p-4!':route.path==='/work'}"><RouterView v-slot="{Component}"><KeepAlive include="ResearchWorkItems,ResearchInbox"><component :is="Component" :key="route.matched[0]?.path"/></KeepAlive></RouterView></PageSurface></main><PlatformPanel :view="panel" plugin="research" @switch="panel=$event" @close="panel=''"/></div></template>


<style scoped>main{flex:1;min-width:0;min-height:0;overflow:auto}.logo{display:flex;align-items:center;gap:.5rem;padding:.75rem;font-weight:600}.standalone-context{display:flex;flex-wrap:wrap;align-items:end;gap:1rem;margin-bottom:1.5rem}</style>
