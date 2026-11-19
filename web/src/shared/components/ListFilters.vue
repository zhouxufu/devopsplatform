<script setup>
import {SlidersHorizontalIcon} from '@lucide/vue'
import {Badge,Button,Collapsible,CollapsibleContent,CollapsibleTrigger} from '../shadcn-ui.js'
defineProps({modelValue:Boolean,activeCount:{type:Number,default:0}})
defineEmits(['update:modelValue'])
</script>

<template>
  <Collapsible :open="modelValue" @update:open="$emit('update:modelValue',$event)" data-slot="list-filters" class="mb-5 min-w-0 space-y-3" aria-label="列表筛选">
    <div class="flex min-w-0 flex-wrap items-center gap-2 [&>input]:min-w-48 [&>input]:max-w-sm [&>input]:flex-1 [&>[data-slot=input-group]]:min-w-48 [&>[data-slot=input-group]]:max-w-sm [&>[data-slot=input-group]]:flex-1">
      <slot/>
      <div class="ml-auto flex min-w-0 flex-wrap items-center gap-2">
        <CollapsibleTrigger as-child><Button variant="outline"><SlidersHorizontalIcon class="size-4"/>筛选<Badge v-if="activeCount" variant="secondary">{{activeCount}}</Badge></Button></CollapsibleTrigger>
        <slot name="actions"/>
      </div>
    </div>
    <CollapsibleContent force-mount v-show="!(!modelValue)">
      <div class="grid grid-cols-[repeat(auto-fit,minmax(min(100%,13rem),1fr))] items-end gap-4 rounded-lg border bg-muted/30 p-4 [&_[data-slot=field]]:min-w-0 [&_[data-slot=field]_button]:w-full! [&>button]:w-fit"><slot name="advanced"/></div>
    </CollapsibleContent>
  </Collapsible>
</template>
