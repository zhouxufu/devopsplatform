// 主题切换：html.dark 控制（自有变量 + Element Plus 暗色变量共用同一 class）
const KEY = 'lp-theme'
export function isDark() { return localStorage.getItem(KEY) !== 'light' } // 默认深色
function setClass(dark) { document.documentElement.classList.toggle('dark', dark) }
export function initTheme() { setClass(isDark()) }
export function toggleTheme() {
  const dark = !isDark()
  localStorage.setItem(KEY, dark ? 'dark' : 'light')
  setClass(dark)
  return dark
}
