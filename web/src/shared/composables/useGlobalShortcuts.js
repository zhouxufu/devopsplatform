import { onMounted, onBeforeUnmount } from 'vue'

const handlers = new Map()

export function useGlobalShortcuts() {
  function register(key, handler) {
    handlers.set(key, handler)
  }

  function unregister(key) {
    handlers.delete(key)
  }

  function handleKeyDown(e) {
    // 格式化按键组合 (e.g., "ctrl+k", "ctrl+shift+k")
    const parts = []
    if (e.ctrlKey || e.metaKey) parts.push('ctrl')
    if (e.shiftKey) parts.push('shift')
    if (e.altKey) parts.push('alt')
    parts.push(e.key.toLowerCase())
    const key = parts.join('+')

    const handler = handlers.get(key)
    if (handler) {
      e.preventDefault()
      handler(e)
    }
  }

  onMounted(() => {
    window.addEventListener('keydown', handleKeyDown)
  })

  onBeforeUnmount(() => {
    window.removeEventListener('keydown', handleKeyDown)
  })

  return { register, unregister, handleKeyDown }
}
