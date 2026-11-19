<script setup>
import {computed} from 'vue'
import {LayoutDashboardIcon,UsersRoundIcon,SendIcon,DatabaseIcon,LogsIcon,ChartNoAxesCombinedIcon,SettingsIcon,BoxesIcon,FolderCogIcon,WorkflowIcon,FileDiffIcon,PlugIcon,BracesIcon,SearchIcon,ActivityIcon,BugIcon,Code2Icon,FolderGit2Icon,HistoryIcon,ZapIcon,UserRoundIcon,CloudIcon,WrenchIcon,PanelLeftIcon} from '@lucide/vue'
const props=defineProps({name:String,size:{type:[String,Number],default:16}})
const icons={user:UserRoundIcon,research:UsersRoundIcon,deploy:SendIcon,dbms:DatabaseIcon,redis:ZapIcon,logs:LogsIcon,monitor:ChartNoAxesCombinedIcon,settings:SettingsIcon,apps:BoxesIcon,resources:FolderCogIcon,orchestration:WorkflowIcon,changes:FileDiffIcon,plugins:PlugIcon,devtools:BracesIcon,'api-center':CloudIcon,search:SearchIcon,live:ActivityIcon,arthas:BugIcon,java:Code2Icon,build:FolderGit2Icon,history:HistoryIcon,sidebar:PanelLeftIcon}
const icon=computed(()=>icons[props.name]||LayoutDashboardIcon)
</script>
<template><component :is="icon" :size="size" class="shrink-0" aria-hidden="true"/></template>
