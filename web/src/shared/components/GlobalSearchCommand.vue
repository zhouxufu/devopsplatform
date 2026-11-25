<script setup>
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { Dialog, DialogContent, Input, Button } from '@/shared/shadcn-ui.js'

const props = defineProps({
  modelValue: { type: Boolean, default: false },
  applications: { type: Array, default: () => [] },
  environments: { type: Array, default: () => [] },
  currentApp: { type: String, default: '' },
  currentEnv: { type: String, default: '' }
})

const emit = defineEmits(['update:modelValue', 'select'])

const search = ref('')
const selectedIndex = ref(0)

const results = computed(() => {
  const q = search.value.toLowerCase()
  if (!q) return []
  
  const appResults = props.applications
    .filter(a => a.name.toLowerCase().includes(q) || (a.label || '').toLowerCase().includes(q))
    .map(a => ({ type: 'app', ...a }))
  
  const envResults = props.environments
    .filter(e => e.name.toLowerCase().includes(q) || e.display_name.toLowerCase().includes(q))
    .map(e => ({ type: 'env', ...e }))
  
  return [...appResults, ...envResults].slice(0, 10)
})

function handleKeyDown(e) {
  if (e.key === 'Escape') {
    emit('update:modelValue', false)
  } else if (e.key === 'ArrowDown') {
    e.preventDefault()
    selectedIndex.value = Math.min(selectedIndex.value + 1, results.value.length - 1)
  } else if (e.key === 'ArrowUp') {
    e.preventDefault()
    selectedIndex.value = Math.max(selectedIndex.value - 1, 0)
  } else if (e.key === 'Enter' && results.value[selectedIndex.value]) {
    selectResult(results.value[selectedIndex.value])
  }
}

function selectResult(item) {
  emit('select', item)
  emit('update:modelValue', false)
  search.value = ''
  selectedIndex.value = 0
}

onMounted(() => {
  window.addEventListener('keydown', e => {
    if ((e.ctrlKey || e.metaKey) && e.key === 'k') {
      e.preventDefault()
      emit('update:modelValue', true)
    }
  })
})
</script>

<template>
  <Dialog :open="modelValue" @update:open="$emit('update:modelValue', $event)">
    <DialogContent class="max-w-md gap-0 p-0">
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
          :key="`${item.type}-${item.name}`"
          :class="[
            'w-full px-3 py-2 text-left text-sm hover:bg-muted transition-colors',
            idx === selectedIndex ? 'bg-muted' : ''
          ]"
          @click="selectResult(item)"
        >
          <div v-if="item.type === 'app'">
            <div class="font-medium">{{ item.label || item.name }}</div>
            <div class="text-xs text-muted-foreground">应用</div>
          </div>
          <div v-else>
            <div class="font-medium">{{ item.display_name }}</div>
            <div class="text-xs text-muted-foreground">{{ item.name }}</div>
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
