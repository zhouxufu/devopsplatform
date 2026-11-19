<script setup>
import {InfoIcon} from '@lucide/vue'
defineProps({text:String,label:{type:String,default:'查看说明'}})


import {Button,Tooltip,TooltipContent,TooltipTrigger} from '../shadcn-ui.js'
</script>
<template><Tooltip><TooltipTrigger as-child><Button type="button" variant="ghost" size="icon-sm" :aria-label="label"><InfoIcon class="size-3.5 text-muted-foreground"/></Button></TooltipTrigger><TooltipContent class="max-w-80 text-wrap leading-6">{{text}}</TooltipContent></Tooltip></template>
