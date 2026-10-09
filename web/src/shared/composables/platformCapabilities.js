import { onMounted, ref } from 'vue'

// Shared per workspace. Standalone plugins without a platform gateway do not
// advertise unavailable utilities.
const mcpAvailable = ref(false)
let loading
export function usePlatformCapabilities() {
  onMounted(() => {
    loading ||= fetch('/api/plugins').then(r => r.ok ? r.json() : []).then(rows => {
      mcpAvailable.value = Array.isArray(rows) && rows.some(p => p.name === 'devtools')
    }).catch(() => {})
  })
  return { mcpAvailable }
}
