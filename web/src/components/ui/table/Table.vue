<script setup lang="ts">
import type { HTMLAttributes } from 'vue'
import { cn } from 'cn'

const props = defineProps<{
  class?: HTMLAttributes['class']
  containerClass?: HTMLAttributes['class']
  containerStyle?: HTMLAttributes['style']
  containerTabindex?: number
  containerLabel?: string
}>()
</script>

<template>
  <div data-slot="table-container" :class="cn('relative w-full overflow-x-auto', props.containerClass)" :style="props.containerStyle" :tabindex="props.containerTabindex" :aria-label="props.containerLabel" :role="props.containerLabel ? 'region' : undefined">
    <table data-slot="table" :class="cn('w-full caption-bottom text-sm', props.class)">
      <slot />
    </table>
  </div>
</template>
