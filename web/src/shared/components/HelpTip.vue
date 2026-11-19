<script setup>
import {CircleHelpIcon} from '@lucide/vue'
defineProps({text:{type:String,required:true},align:{type:String,default:'left'}})


import {Button,Popover,PopoverContent,PopoverTrigger} from '../shadcn-ui.js'
</script>
<template><Popover><PopoverTrigger as-child><Button variant="ghost" size="icon-xs" aria-label="查看说明"><CircleHelpIcon class="size-4 text-muted-foreground"/></Button></PopoverTrigger><PopoverContent :align="align==='right'?'end':'start'" class="max-h-[70dvh] w-96 max-w-[calc(100vw-2rem)] overflow-auto whitespace-pre-line text-sm leading-relaxed">{{text}}</PopoverContent></Popover></template>
