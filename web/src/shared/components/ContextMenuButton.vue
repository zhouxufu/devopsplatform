<script setup>
import { ref } from 'vue'
import { Button, DropdownMenu, DropdownMenuContent, DropdownMenuTrigger, DropdownMenuSeparator, DropdownMenuItem, DropdownMenuLabel } from '@/shared/shadcn-ui.js'
import { ChevronDownIcon } from '@lucide/vue'

defineProps({
  groups: {
    type: Array,
    required: true
    // [{ label: 'Apps', items: [{ label, value, selected: boolean }] }, ...]
  },
  label: {
    type: String,
    required: true
  },
  icon: {
    type: String,
    default: ''
  }
})

const emit = defineEmits(['select'])

const open = ref(false)
</script>

<template>
  <DropdownMenu :open="open" @update:open="v => open = v">
    <DropdownMenuTrigger as-child>
      <Button variant="ghost" size="sm" class="gap-1.5 px-2">
        <span class="text-xs">{{ label }}</span>
        <ChevronDownIcon class="size-3.5" />
      </Button>
    </DropdownMenuTrigger>
    <DropdownMenuContent align="start" class="max-h-80 overflow-y-auto">
      <template v-for="(group, idx) in groups" :key="`group-${idx}`">
        <div v-if="group.items?.length">
          <DropdownMenuLabel v-if="group.label" class="text-xs">{{ group.label }}</DropdownMenuLabel>
          <DropdownMenuItem
            v-for="item in group.items"
            :key="item.value"
            @select="$emit('select', item)"
          >
            <span v-if="item.selected" class="mr-2">✓</span>
            <span v-else class="mr-2 w-4" />
            <span class="text-sm">{{ item.label }}</span>
          </DropdownMenuItem>
          <DropdownMenuSeparator v-if="idx < groups.length - 1" />
        </div>
      </template>
    </DropdownMenuContent>
  </DropdownMenu>
</template>
