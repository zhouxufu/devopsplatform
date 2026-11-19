import {onScopeDispose, ref, shallowRef} from 'vue'

// A scope change invalidates the previous response, even if its transport
// ignores AbortSignal. Never display old scope data under a new scope label.
export function useAsyncData(initial = null) {
  const data = shallowRef(initial), loading = ref(false), error = ref('')
  let revision = 0, controller, disposed = false
  function cancel() { revision++; controller?.abort(); loading.value = false }
  async function load(loader) {
    if (disposed) return
    cancel()
    const current = revision
    controller = new AbortController()
    data.value = initial; error.value = ''; loading.value = true
    try {
      const result = await loader(controller.signal)
      if (current === revision && !disposed) data.value = result
      return current === revision && !disposed
    } catch (e) {
      if (current === revision && !disposed && e.name !== 'AbortError') error.value = e.message || '读取失败，请重试'
      return false
    } finally { if (current === revision && !disposed) loading.value = false }
  }
  onScopeDispose(() => { disposed = true; cancel() })
  return {data, loading, error, load, cancel}
}

export async function requestJSON(url, options = {}) {
  const response = await fetch(url, options)
  const text = await response.text()
  let value
  try { value = text ? JSON.parse(text) : null } catch { throw new Error('服务响应格式错误，请重试') }
  if (!response.ok) throw new Error(value?.error || '请求失败（' + response.status + '），请重试')
  return value
}
export async function requestList(url, options) {
  const value = await requestJSON(url, options)
  if (value == null) return []
  if (!Array.isArray(value)) throw new Error('列表响应格式错误，请重试')
  return value
}
