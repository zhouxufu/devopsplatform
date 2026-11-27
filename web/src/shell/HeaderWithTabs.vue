<script setup>
import { computed } from 'vue'
import { Tabs, TabsList, TabsTrigger, Button, ContextMenu, ContextMenuTrigger, ContextMenuContent, ContextMenuItem } from '@/shared/shadcn-ui.js'
import { Breadcrumb, BreadcrumbList, BreadcrumbItem, BreadcrumbPage, BreadcrumbSeparator } from '@/components/ui/breadcrumb'
import { ChevronDownIcon, SearchIcon } from '@lucide/vue'
import ContextMenuButton from '../shared/components/ContextMenuButton.vue'

const props = defineProps({
  breadcrumbs: {
    type: Array,
    default: () => []
  },
  tabs: {
    type: Array,
    default: () => []
  },
  canClose: {
    type: Function,
    required: true
  },
  activeTab: {
    type: [String, Number],
    default: '0'
  },
  appMenuGroups: {
    type: Array,
    default: () => []
  },
  envMenuGroups: {
    type: Array,
    default: () => []
  },
  currentApp: {
    type: String,
    default: ''
  },
  currentEnv: {
    type: String,
    default: ''
  }
})

defineEmits(['tab-change', 'tab-duplicate', 'app-select', 'env-select', 'search-open', 'tab-close'])

const appLabel = computed(() => props.currentApp || '应用')
const envLabel = computed(() => props.currentEnv || '环境')

const closeActions = [
  { value: 'current', label: '关闭当前标签页' },
  { value: 'other', label: '关闭其他标签页' },
  { value: 'left', label: '关闭左侧标签页' },
  { value: 'right', label: '关闭右侧标签页' }
]
</script>

<template>
  <header data-slot="shell-header-merged" class="flex h-14 shrink-0 items-center gap-2 border-b px-4">
    <!-- 面包屑导航 -->
    <Breadcrumb class="min-w-0">
      <BreadcrumbList class="flex-nowrap text-sm">
        <template v-for="(item, i) in breadcrumbs" :key="i">
          <BreadcrumbSeparator v-if="i" class="hidden sm:block" />
          <BreadcrumbItem :class="i === 0 ? 'hidden sm:inline-flex' : 'min-w-0'">
            <BreadcrumbPage v-if="i === breadcrumbs.length - 1" class="truncate font-normal">
              {{ item.label }}
            </BreadcrumbPage>
            <span v-else>{{ item.label }}</span>
          </BreadcrumbItem>
        </template>
      </BreadcrumbList>
    </Breadcrumb>

    <!-- 内联 Tabs -->
    <Tabs
      v-if="tabs.length"
      :model-value="String(activeTab)"
      activation-mode="manual"
      class="h-10 min-h-0 min-w-0 flex-1 overflow-x-auto [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
      @update:model-value="value => $emit('tab-change', value)"
    >
      <TabsList variant="line" class="h-full! w-max min-w-full shrink-0 justify-start rounded-none bg-transparent p-0">
        <ContextMenu v-for="tab in tabs" :key="tab.value">
          <ContextMenuTrigger class="inline-flex shrink-0">
            <TabsTrigger
              :value="String(tab.value)"
              :title="tab.label"
              class="h-10 flex-none gap-2 rounded-none px-3"
              @mousedown.middle.prevent
              @auxclick.middle.prevent.stop="canClose('current', tab.value) && $emit('tab-close', 'current', tab.value)"
            >
              <span class="truncate">{{ tab.label }}</span>
            </TabsTrigger>
          </ContextMenuTrigger>
          <ContextMenuContent :aria-label="`${tab.label}的标签页操作`">
            <ContextMenuItem v-if="String(tab.value)!=='0'" @select="$emit('tab-duplicate',tab.value)">在新标签打开</ContextMenuItem>
            <ContextMenuItem
              v-for="action in closeActions"
              :key="action.value"
              :disabled="!canClose(action.value, tab.value)"
              @select="$emit('tab-close', action.value, tab.value)"
            >
              {{ action.label }}
            </ContextMenuItem>
          </ContextMenuContent>
        </ContextMenu>
      </TabsList>
    </Tabs>

    <div class="ml-auto flex items-center gap-1.5">
      <!-- 应用/环境下拉菜单 -->
      <ContextMenuButton
        v-if="appMenuGroups.length"
        :label="`⚙️ ${appLabel}`"
        :groups="appMenuGroups"
        @select="$emit('app-select', $event)"
      />
      <ContextMenuButton
        v-if="envMenuGroups.length"
        :label="`🌍 ${envLabel}`"
        :groups="envMenuGroups"
        @select="$emit('env-select', $event)"
      />

      <!-- 全局搜索按钮 -->
      <Button
        variant="ghost"
        size="sm"
        class="gap-1"
        aria-label="搜索应用、环境"
        title="搜索应用、环境 (Ctrl+K / ⌘K)"
        @click="$emit('search-open')"
      >
        <SearchIcon class="size-4" />
        <span class="text-xs hidden sm:inline">Ctrl+K</span>
      </Button>

      <!-- 其他操作槽位 -->
      <slot name="actions" />
    </div>
  </header>
</template>

<style scoped>
::-webkit-scrollbar {
  display: none;
}
</style>
