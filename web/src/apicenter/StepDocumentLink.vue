<script setup>
import {Badge,Button} from '../shared/shadcn-ui.js'
defineProps({service:String,step:Object,fallbackDocument:Number,disabled:Boolean})
defineEmits(['select'])
</script>
<template><div class="document-link space-y-2"><Button variant="outline" type="button" :disabled="disabled" @click="$emit('select')">{{step.operation_id?'更换文档接口':'从接口文档选择接口'}}</Button><div v-if="step.document_id||fallbackDocument" class="flex min-w-0 flex-wrap items-center gap-2 text-xs text-muted-foreground"><Badge variant="secondary">来源快照 #{{step.document_id||fallbackDocument}}</Badge><span class="break-all">{{step.operation_id||'未指定接口'}}</span></div></div></template>
<style scoped>.document-link{margin-bottom:16px}</style>
