<script setup>

import UiIcon from './UiIcon.vue'
import {usePlatformCapabilities} from '../composables/platformCapabilities.js'
const { mcpAvailable } = usePlatformCapabilities()
const emit = defineEmits(['open'])
const embedded=window.parent!==window
function open(view) {
  if (window.parent !== window) {
    if(view==='home'){window.parent.postMessage({type:'platform.home'},location.origin);return}
    window.parent.postMessage({ type: 'platform.open', view }, location.origin)
    return
  }
  emit('open', view)
}



import {SidebarMenu,SidebarMenuItem,SidebarMenuButton} from '../shadcn-ui.js'
</script>

<template>
 <nav class="platform-nav" aria-label="平台工作区">
  <SidebarMenu>
   <SidebarMenuItem v-if="embedded"><SidebarMenuButton class="h-10 text-sm" tooltip="个人工作台" aria-label="个人工作台" @click="open('home')"><UiIcon name="user"/><span>首页</span></SidebarMenuButton></SidebarMenuItem>
   <SidebarMenuItem><SidebarMenuButton class="h-10 text-sm" tooltip="应用中心" aria-label="应用中心" @click="open('apps')"><UiIcon name="apps"/><span>应用</span></SidebarMenuButton></SidebarMenuItem>
   <SidebarMenuItem><SidebarMenuButton class="h-10 text-sm" tooltip="资源中心" aria-label="资源中心" @click="open('resources')"><UiIcon name="resources"/><span>资源</span></SidebarMenuButton></SidebarMenuItem>
   <SidebarMenuItem v-if="mcpAvailable"><SidebarMenuButton class="h-10 text-sm" tooltip="MCP" aria-label="MCP" @click="open('mcp')"><UiIcon name="devtools"/><span>MCP</span></SidebarMenuButton></SidebarMenuItem>
  </SidebarMenu>
 </nav>
</template>

<style scoped>.platform-nav{width:100%;flex-shrink:0;padding-top:12px;margin-top:auto;border-top:1px solid var(--border)}</style>
