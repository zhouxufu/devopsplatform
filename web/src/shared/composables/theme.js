import { ref } from 'vue'

// One theme per document. Embedded documents accept only their same-origin parent;
// independent tabs also follow the shared preference through the storage event.
const KEY = 'lp-theme'
function savedTheme() {
  try { return localStorage.getItem(KEY) !== 'light' } catch { return true }
}
export const themeDark = ref(savedTheme())
export function isDark() { return themeDark.value }
function applyTheme(dark) {
  themeDark.value = dark
  document.documentElement.classList.toggle('dark', dark)
  document.documentElement.style.colorScheme = dark ? 'dark' : 'light'
}
let initialized = false
export function initTheme() {
  applyTheme(savedTheme())
  if (initialized) return
  initialized = true
  window.addEventListener('message', event => {
    if (window.parent === window || event.source !== window.parent || event.origin !== location.origin) return
    if (event.data?.type === 'theme.sync' && typeof event.data.dark === 'boolean') applyTheme(event.data.dark)
  })
  window.addEventListener('storage', event => {
    if (event.key !== KEY && event.key !== null) return
    try { if (event.storageArea !== localStorage) return } catch { return }
    applyTheme(savedTheme())
  })
}
export function toggleTheme() {
  const dark = !themeDark.value
  try { localStorage.setItem(KEY, dark ? 'dark' : 'light') } catch { /* Keep this window usable without storage. */ }
  applyTheme(dark)
  return dark
}
