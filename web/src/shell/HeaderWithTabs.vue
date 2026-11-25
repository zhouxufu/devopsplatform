<script setup>
import { computed, ref } from 'vue'
import { Tabs, TabsList, TabsTrigger, Button, Separator, ContextMenu, ContextMenuTrigger, ContextMenuContent, ContextMenuItem, ContextMenuSeparator } from '@/shared/shadcn-ui.js'
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

const emit = defineEmits(['tab-change', 'app-select', 'env-select', 'search-open', 'tab-close', 'tab-close-others', 'tab-close-left', 'tab-close-right'])

const appLabel = computed(() => props.currentApp || '应用')
const envLabel = computed(() => props.currentEnv || '环境')

const contextMenuTab = ref(null)

function handleTabContextMenu(tabValue) {
  contextMenuTab.value = tabValue
}
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
        <TabsTrigger
          v-for="tab in tabs"
          :key="tab.value"
          :value="String(tab.value)"
          class="h-10 flex-none shrink-0 gap-2 rounded-none px-3"
          @contextmenu.prevent="handleTabContextMenu(tab.value)"
        >
          <span class="truncate">{{ tab.label }}</span>
        </TabsTrigger>
      </TabsList>
    </Tabs>

    <!-- 标签页右键菜单 -->
    <ContextMenu v-if="tabs.length">
      <ContextMenuTrigger as-child>
        <div style="display: none" />
      </ContextMenuTrigger>
      <ContextMenuContent v-if="contextMenuTab" class="w-48">
        <ContextMenuItem @select="() => { $emit('tab-close', contextMenuTab); contextMenuTab = null }">
          关闭此标签页
        </ContextMenuItem>
        <ContextMenuItem @select="() => { $emit('tab-close-others', contextMenuTab); contextMenuTab = null }">
          关闭其他标签页
        </ContextMenuItem>
        <ContextMenuSeparator />
        <ContextMenuItem @select="() => { $emit('tab-close-left', contextMenuTab); contextMenuTab = null }">
          关闭左侧所有标签页
        </ContextMenuItem>
        <ContextMenuItem @select="() => { $emit('tab-close-right', contextMenuTab); contextMenuTab = null }">
          关闭右侧所有标签页
        </ContextMenuItem>
      </ContextMenuContent>
    </ContextMenu>

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
