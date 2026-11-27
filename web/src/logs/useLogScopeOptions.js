import {computed, inject, onMounted, ref} from 'vue'

export function useLogScopeOptions() {
  const embedded = window.parent !== window
  const context = inject('logContextOptions', ref({applications:[], environments:[]}))
  const services = inject('logServices', ref([]))
  const environments = ref([])

  onMounted(async () => {
    if (embedded) return
    try {
      const response = await fetch('api/environments')
      if (!response.ok) throw new Error()
      const list = await response.json()
      if (Array.isArray(list)) environments.value = list.map(item =>
        typeof item === 'string' ? item : {value:item.name, label:item.display_name || item.name})
    } catch {
      environments.value = []
    }
  })

  return {
    applicationOptions: computed(() => embedded ? context.value.applications : services.value),
    environmentOptions: computed(() => embedded ? context.value.environments : environments.value)
  }
}
