<script setup lang="ts">
import type { HTMLAttributes } from 'vue'
import { cn } from 'cn'

const props = defineProps<{ class?: HTMLAttributes['class'] }>()
</script>

<template>
  <div
    data-slot="sheet-footer"
    :class="cn('mt-auto min-w-0 max-w-full shrink-0 border-t bg-muted/50 p-4 flex flex-col-reverse gap-2 sm:flex-row sm:flex-wrap sm:justify-end', props.class)"
  >
    <slot />
  </div>
</template>
