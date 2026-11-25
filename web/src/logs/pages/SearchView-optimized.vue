<script setup>
import { ref, computed, inject, watch } from 'vue'
import { useSidebar } from '../../shared/composables/useSidebar.js'

const globalEnvironment = inject('globalEnvironment', ref(''))
const { isCollapsed, toggle, sidebarWidth } = useSidebar()

const services = ref([
  { name: 'gateway', color: '#2f81f7', logCount: 1234 },
  { name: 'order-api', color: '#3fb950', logCount: 856 },
  { name: 'user-svc', color: '#d29922', logCount: 432 },
  { name: 'pay-svc', color: '#f85149', logCount: 2156 },
  { name: 'notify-svc', color: '#a371f7', logCount: 123 },
])

const selectedService = ref('')
const searchText = ref('')
const liveMode = ref(false)
const logs = ref([])

const filteredLogs = computed(() => {
  return logs.value.filter(log => {
    const matchService = !selectedService.value || log.service === selectedService.value
    const matchSearch = !searchText.value || log.message.includes(searchText.value)
    return matchService && matchSearch
  })
})

const getServiceColor = (name) => {
  const svc = services.value.find(s => s.name === name)
  return svc?.color || '#8b949e'
}

// 加载日志数据
const loadLogs = async () => {
  // 模拟 API 调用
  logs.value = Array.from({ length: 100 }, (_, i) => ({
    id: i,
    timestamp: new Date(Date.now() - i * 1000),
    service: services.value[i % services.value.length].name,
    level: ['INFO', 'WARN', 'ERROR', 'DEBUG'][i % 4],
    message: `Log message ${i}: Sample log content for demonstration purposes`,
  }))
}

watch(selectedService, () => {
  loadLogs()
}, { immediate: true })
</script>

<template>
  <div class="search-view">
    <!-- 侧栏 -->
    <aside class="sidebar" :class="{ collapsed: isCollapsed }">
      <!-- 侧栏头部：标题 + 折叠按钮 -->
      <div class="sidebar-header">
        <span class="sidebar-title">服务</span>
        <button class="collapse-toggle" @click="toggle" :title="isCollapsed ? '展开' : '折叠'">
          <span class="icon">{{ isCollapsed ? '▶' : '◀' }}</span>
        </button>
      </div>
      
      <!-- 展开时显示详细列表 -->
      <div class="sidebar-content">
        <div v-for="svc in services" 
             :key="svc.name" 
             class="svc-item"
             :class="{ active: selectedService === svc.name }"
             @click="selectedService = svc.name">
          <span class="svc-dot" :style="{ background: svc.color }"></span>
          <span class="svc-name">{{ svc.name }}</span>
          <span class="svc-count">{{ svc.logCount }}</span>
        </div>
      </div>
      
      <!-- 折叠时显示图标视图 -->
      <div class="sidebar-icons">
        <div v-for="svc in services"
             :key="svc.name"
             class="service-icon"
             :style="{ background: svc.color, opacity: selectedService === svc.name ? 1 : 0.6 }"
             :title="svc.name"
             @click="selectedService = svc.name">
        </div>
      </div>
    </aside>
    
    <!-- 主内容区 -->
    <main class="content">
      <!-- 工具栏 -->
      <div class="toolbar">
        <div class="tb-row">
          <!-- 搜索框 -->
          <div class="search-wrap">
            <span class="icon">🔍</span>
            <input v-model="searchText" 
                   type="text" 
                   placeholder="搜索日志内容..."
                   @keyup.enter="loadLogs">
          </div>
          
          <!-- 实时模式按钮 -->
          <button class="live-btn" :class="{ on: liveMode }" @click="liveMode = !liveMode">
            <span class="tri"></span>
            <span>{{ liveMode ? '实时中...' : '实时' }}</span>
          </button>
        </div>
      </div>
      
      <!-- 日志列表 -->
      <div class="stream">
        <div v-for="log in filteredLogs"
             :key="log.id"
             class="log-row">
          <span class="ts">{{ log.timestamp.toLocaleTimeString() }}</span>
          <span class="sv" :style="{ color: getServiceColor(log.service) }">
            {{ log.service }}
          </span>
          <span class="badge" :class="`lv-${log.level}`">{{ log.level }}</span>
          <span class="msg">{{ log.message }}</span>
        </div>
      </div>
      
      <!-- 状态栏 -->
      <div class="statusbar">
        <span>{{ filteredLogs.length }} 条日志</span>
        <span>|</span>
        <span>{{ selectedService || '全部服务' }}</span>
      </div>
    </main>
  </div>
</template>

<style scoped>
.search-view {
  display: flex;
  height: 100%;
  overflow: hidden;
  background: var(--bg);
}

/* 导入侧栏样式 */
@import '../../shared/styles/sidebar.css';

.content {
  flex: 1;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.toolbar {
  padding: 10px 16px;
  border-bottom: 1px solid var(--border);
  background: var(--panel);
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.tb-row {
  display: flex;
  align-items: center;
  gap: 8px;
}

.search-wrap {
  flex: 1;
  position: relative;
}

.search-wrap .icon {
  position: absolute;
  left: 10px;
  top: 50%;
  transform: translateY(-50%);
  color: var(--dim);
  font-size: 13px;
  pointer-events: none;
}

.search-wrap input {
  width: 100%;
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: 6px;
  color: var(--text);
  padding: 6px 10px 6px 30px;
  font-size: 13px;
  outline: none;
  font-family: var(--mono);
  transition: all 200ms;
}

.search-wrap input:focus {
  border-color: var(--brand);
  box-shadow: 0 0 0 2px rgba(47, 129, 247, 0.1);
}

.live-btn {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 5px 14px;
  border-radius: 6px;
  border: 1px solid var(--border);
  background: var(--panel2);
  color: var(--muted);
  cursor: pointer;
  font-size: 12px;
  transition: all 200ms;
}

.live-btn:hover {
  border-color: var(--muted);
}

.live-btn.on {
  background: rgba(47, 129, 247, 0.15);
  border-color: var(--brand);
  color: var(--brand);
}

.live-btn .tri {
  width: 0;
  height: 0;
  border-left: 6px solid currentColor;
  border-top: 4px solid transparent;
  border-bottom: 4px solid transparent;
}

.stream {
  flex: 1;
  overflow-y: auto;
  font-family: var(--mono);
  font-size: 12px;
}

.log-row {
  display: grid;
  grid-template-columns: 150px 96px 64px 1fr;
  gap: 12px;
  padding: 3px 16px;
  cursor: pointer;
  align-items: baseline;
  border-bottom: 1px solid #21262d;
}

.log-row:hover {
  background: var(--panel2);
}

.log-row .ts {
  color: var(--muted);
}

.log-row .sv {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.log-row .msg {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  color: #c9d1d9;
}

.badge {
  display: inline-block;
  width: 100%;
  text-align: center;
  border-radius: 4px;
  font-size: 10px;
  padding: 1px 0;
  font-weight: 600;
  letter-spacing: 0.03em;
}

.lv-ERROR {
  background: rgba(248, 81, 73, 0.15);
  color: var(--error);
}

.lv-WARN {
  background: rgba(210, 153, 34, 0.15);
  color: var(--warn);
}

.lv-INFO {
  background: rgba(88, 166, 255, 0.12);
  color: var(--info);
}

.lv-DEBUG {
  background: rgba(139, 148, 158, 0.12);
  color: var(--debug);
}

.statusbar {
  height: 32px;
  border-top: 1px solid var(--border);
  background: var(--panel);
  display: flex;
  align-items: center;
  padding: 0 16px;
  gap: 16px;
  color: var(--dim);
  font-size: 12px;
  flex-shrink: 0;
}

/* 响应式调整 */
@media (max-width: 1024px) {
  .log-row {
    grid-template-columns: 100px 80px 60px 1fr;
    gap: 8px;
    padding: 2px 12px;
  }
}

@media (max-width: 768px) {
  .log-row {
    grid-template-columns: 80px 1fr;
    gap: 8px;
  }
  
  .log-row .sv,
  .log-row .msg {
    grid-column: 1 / -1;
  }
}
</style>
