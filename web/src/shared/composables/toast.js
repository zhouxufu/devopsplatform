// 轻 toast：Element Plus ElMessage（全局单例由 EP 自管理）
import { ElMessage } from 'element-plus'
import 'element-plus/dist/index.css'
import 'element-plus/theme-chalk/dark/css-vars.css'
export function toast(t, kind, options = {}) {
  return ElMessage({ message: String(t), type: kind || 'info', duration: 2400, showClose: true, zIndex: 3000, ...options })
}
