<script setup>
import { ref, computed, watch } from 'vue'
import { Dialog, DialogContent, DialogTitle, Input } from '@/shared/shadcn-ui.js'
import { applicationOption } from '../applicationName.js'

const props = defineProps({
  modelValue: { type: Boolean, default: false },
  applications: { type: Array, default: () => [] },
  environments: { type: Array, default: () => [] },
  actions: { type: Object, required: true }
})

const emit = defineEmits(['update:modelValue', 'select'])

const search = ref('')
const selectedIndex = ref(0)

const results = computed(() => {
  const q = search.value.trim().toLowerCase()
  if (!q) return []
  
  const appResults = props.applications
    .map(app => ({ type: 'service', ...applicationOption(app) }))
  
  const envResults = props.environments
    .map(env => ({ type: 'environment', value: env.name, label: env.display_name || env.name }))
  
  return [...appResults, ...envResults].filter(item =>
    item.value && [item.label, item.value].some(text => text.toLowerCase().includes(q))
  ).slice(0, 10)
})

watch(results, () => { selectedIndex.value = 0 })
watch(() => props.modelValue, open => { if (!open) search.value = '' })

function handleKeyDown(e) {
  if (e.isComposing || e.keyCode === 229) return
  if (e.key === 'Escape') {
    emit('update:modelValue', false)
  } else if (e.key === 'ArrowDown') {
    e.preventDefault()
    selectedIndex.value = Math.max(0, Math.min(selectedIndex.value + 1, results.value.length - 1))
  } else if (e.key === 'ArrowUp') {
    e.preventDefault()
    selectedIndex.value = Math.max(selectedIndex.value - 1, 0)
  } else if (e.key === 'Enter' && results.value[selectedIndex.value]) {
    e.preventDefault()
    selectResult(results.value[selectedIndex.value])
  }
}

function selectResult(item) {
  if (!props.actions[item.type]) return
  emit('select', item)
  emit('update:modelValue', false)
}
</script>

<template>
  <Dialog :open="modelValue" @update:open="$emit('update:modelValue', $event)">
    <DialogContent class="max-w-md gap-0 p-0">
      <DialogTitle class="sr-only">搜索应用、环境</DialogTitle>
      <div class="border-b p-3">
        <Input
          v-model="search"
          placeholder="搜索应用、环境... (Esc 关闭)"
          class="border-0 shadow-none focus-visible:ring-0"
          autofocus
          @keydown="handleKeyDown"
        />
      </div>
      <div v-if="results.length" class="max-h-80 overflow-y-auto">
        <button
          v-for="(item, idx) in results"
          :key="`${item.type}-${item.value}`"
          :disabled="!actions[item.type]"
          :class="[
            'w-full px-3 py-2 text-left text-sm hover:bg-muted transition-colors',
            idx === selectedIndex ? 'bg-muted' : ''
          ]"
          @click="selectResult(item)"
        >
          <div class="font-medium">{{ item.label }}</div>
          <div class="text-xs text-muted-foreground">
            {{ item.type === 'service' ? '应用' : '环境' }}<template v-if="item.type === 'environment' && item.label !== item.value"> · {{ item.value }}</template>
            · {{ actions[item.type] || '日志查询不可用' }}
          </div>
        </button>
      </div>
      <div v-else class="px-3 py-8 text-center text-sm text-muted-foreground">
        {{ search ? '未找到结果' : '开始输入以搜索' }}
      </div>
      <div class="border-t bg-muted px-3 py-2 text-xs text-muted-foreground">
        <div class="flex gap-4">
          <div><kbd class="rounded border px-1.5 py-0.5 bg-background text-xs">↓↑</kbd> 导航</div>
          <div><kbd class="rounded border px-1.5 py-0.5 bg-background text-xs">Enter</kbd> 选择</div>
          <div><kbd class="rounded border px-1.5 py-0.5 bg-background text-xs">Esc</kbd> 关闭</div>
        </div>
      </div>
    </DialogContent>
  </Dialog>
</template>
