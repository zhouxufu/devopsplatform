<script setup>
import {Sidebar,SidebarContent,SidebarFooter,SidebarTrigger,useSidebar} from '@/components/ui/sidebar'
const embedded=window.parent!==window
const {state}=useSidebar()
</script>
<template><Sidebar v-if="!embedded" collapsible="icon" class="workspace-sidebar"><SidebarContent class="p-2" :data-collapsed="state==='collapsed'"><slot :collapsed="state==='collapsed'"/></SidebarContent><SidebarFooter><SidebarTrigger/></SidebarFooter></Sidebar></template>
<style scoped>
:deep([data-slot=sidebar-content]>.logo),:deep([data-slot=sidebar-content]>b){display:flex;align-items:center;gap:8px;height:40px;min-height:40px;padding:8px;font-size:.875rem;line-height:1.5;white-space:nowrap;overflow:hidden}
:deep([data-slot=sidebar-content]>.logo svg),:deep([data-slot=sidebar-content]>b svg){width:20px;height:20px;flex-shrink:0}
:deep([data-slot=sidebar-content][data-collapsed=true]>.logo),:deep([data-slot=sidebar-content][data-collapsed=true]>b){justify-content:center;gap:0;padding-inline:0;font-size:0}
</style>
