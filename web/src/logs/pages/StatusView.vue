<script setup>
import { ref, onMounted, onUnmounted } from 'vue'

const stats = ref({ agents: [], services: {}, disk_used: 0 })
let timer = null
const load = async () => { stats.value = await fetch('api/stats').then(r => r.json()).catch(() => stats.value) }
onMounted(() => { load(); timer = setInterval(load, 10000) })
onUnmounted(() => clearInterval(timer))
const gb = b => (b / 1024 ** 3).toFixed(2)
const bytes = b => {
  if (!b) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  const i = Math.min(Math.floor(Math.log(b) / Math.log(1024)), units.length - 1)
  return `${(b / 1024 ** i).toFixed(i ? 1 : 0)} ${units[i]}`
}
</script>
<template>
  <div style="padding:16px;overflow-y:auto;height:100%">
    <div class="stats-grid">
      <div class="card"><div class="t">Agent 在线</div><div class="v">{{ stats.agents.filter(a => a.online).length }} / {{ stats.agents.length }}</div><div class="s">{{ stats.agents.map(a => a.id).join(' · ') || '暂无' }}</div></div>
      <div class="card"><div class="t">今日事件</div><div class="v">{{ Object.values(stats.services || {}).reduce((s, n) => s + n, 0).toLocaleString() }}</div><div class="s">{{ Object.keys(stats.services || {}).length }} 个服务</div></div>
      <div class="card"><div class="t">磁盘占用</div><div class="v">{{ gb(stats.disk_used) }} GB</div><div class="s">按天分表 · 保留 7 天</div>
        <div style="height:6px;background:var(--panel2);border-radius:3px;margin-top:10px"><i :style="{ display:'block', height:'100%', width: Math.min(100, gb(stats.disk_used) / 20 * 100) + '%', background:'var(--brand)', borderRadius:'3px' }"></i></div></div>
      <div class="card"><div class="t">Agent 待发送</div><div class="v">{{ bytes(stats.agents.reduce((s, a) => s + (a.spool_bytes || 0), 0)) }}</div><div class="s">内存队列 {{ stats.agents.reduce((s, a) => s + (a.queue_depth || 0), 0) }} 条 · WAL 可断点重放</div></div>
    </div>
    <h3 style="font-size:14px;margin-bottom:10px">Agent 列表</h3>
    <table>
      <thead><tr><th>Agent</th><th>状态</th><th>最近上报</th><th>内存队列</th><th>WAL 待发送</th><th>累计事件</th><th>累计丢弃</th></tr></thead>
      <tbody>
        <tr v-for="a in stats.agents" :key="a.id">
          <td>{{ a.id }}</td>
          <td><span :class="a.online ? 'st-ok' : ''" :style="!a.online && 'color:var(--dim)'">{{ a.online ? '● 在线' : '○ 离线' }}</span></td>
          <td style="color:var(--dim)">{{ a.last_seen ? new Date(a.last_seen * 1000).toLocaleTimeString('zh-CN', { hour12: false }) : '—' }}</td>
          <td :style="{ color: a.queue_depth ? 'var(--warn)' : 'var(--dim)' }">{{ a.queue_depth || 0 }}</td>
          <td :style="{ color: a.spool_bytes ? 'var(--warn)' : 'var(--dim)' }">{{ bytes(a.spool_bytes) }}</td>
          <td>{{ (a.events || 0).toLocaleString() }}</td>
          <td :style="{ color: a.dropped ? 'var(--warn)' : 'var(--dim)' }">{{ a.dropped || 0 }}</td>
        </tr>
      </tbody>
    </table>
    <h3 style="font-size:14px;margin:20px 0 10px">服务（今日）</h3>
    <table>
      <thead><tr><th>服务</th><th>事件数</th></tr></thead>
      <tbody><tr v-for="(n, s) in stats.services" :key="s"><td>{{ s }}</td><td>{{ n.toLocaleString() }}</td></tr></tbody>
    </table>
  </div>
</template>
<style scoped>
.card .t{color:var(--muted);font-size:12px}
.card .v{font-size:22px;font-weight:600;font-family:var(--mono);margin-top:4px}
.card .s{color:var(--dim);font-size:11px;margin-top:2px}
.stats-grid{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:14px;margin-bottom:20px}
@media (max-width:1100px){.stats-grid{grid-template-columns:repeat(2,minmax(0,1fr))}}
</style>
