import { ref, computed, onMounted } from 'vue'

/**
 * 侧栏折叠状态管理
 * 自动保存到 localStorage，刷新后恢复
 */
export const useSidebar = () => {
  const isCollapsed = ref(false)
  
  const toggle = () => {
    isCollapsed.value = !isCollapsed.value
    localStorage.setItem('sidebar-collapsed', isCollapsed.value)
  }
  
  const sidebarWidth = computed(() => 
    isCollapsed.value ? '48px' : '200px'
  )
  
  onMounted(() => {
    // 恢复用户偏好
    const saved = localStorage.getItem('sidebar-collapsed')
    if (saved !== null) {
      isCollapsed.value = saved === 'true'
    }
  })
  
  return {
    isCollapsed,
    toggle,
    sidebarWidth
  }
}
