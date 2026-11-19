<script setup>
import {ArrowLeftIcon} from '@lucide/vue'
import HelpTip from './HelpTip.vue'
import {Button} from '../shadcn-ui.js'

defineProps({title:String,help:String,headingTag:{type:String,default:'h2'},showBack:Boolean,compact:Boolean})
defineEmits(['back'])
</script>

<template>
  <header data-slot="page-header" class="min-w-0" :class="compact?'mb-4 space-y-3':'mb-5 space-y-4'">
    <div class="flex min-w-0 flex-wrap items-center justify-between gap-x-6 gap-y-3">
      <div class="flex min-w-0 flex-1 basis-64 items-center gap-3">
        <Button v-if="showBack" variant="ghost" size="icon" aria-label="返回列表" @click="$emit('back')"><ArrowLeftIcon/></Button>
        <div class="flex min-w-0 flex-wrap items-center gap-3">
          <div class="flex min-w-0 flex-wrap items-center gap-2">
            <slot name="title">
              <component :is="headingTag" class="min-w-0 break-words font-semibold tracking-tight" :class="headingTag==='h3'?'text-base leading-6':'text-2xl leading-8'">{{title}}</component>
              <HelpTip v-if="help" :text="help"/>
            </slot>
          </div>
          <slot name="context"/>
        </div>
      </div>
      <div v-if="$slots.actions" class="flex min-w-0 max-w-full shrink-0 flex-wrap items-center gap-2"><slot name="actions"/></div>
    </div>
    <div v-if="$slots.subtitle" class="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1 text-sm text-muted-foreground"><slot name="subtitle"/></div>
    <div v-if="$slots.filters" class="flex min-w-0 flex-wrap items-center gap-2"><slot name="filters"/></div>
    <div v-if="$slots.navigation" class="min-w-0 border-b pb-0.5"><slot name="navigation"/></div>
  </header>
</template>
