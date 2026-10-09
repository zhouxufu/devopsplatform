<script setup>
import { reactive, ref, onMounted, onBeforeUnmount, computed } from 'vue'
import RangePicker from '../shared/components/RangePicker.vue'
import Sel from '../shared/components/Sel.vue'
import { ElButton, ElInput } from '../shared/components/ui.js'
import { isDark, toggleTheme } from '../shared/composables/theme.js'
import PlatformPanel from '../shared/components/PlatformPanel.vue'
import UserManagement from '../shared/components/UserManagement.vue'
import UiIcon from '../shared/components/UiIcon.vue'
import { DEFAULT_RECENT_MINUTES, inferRecentMinutes, recentRange, restoreTimeRange } from '../shared/timeRange.js'

const dark = ref(isDark())
function switchTheme() {
  dark.value = toggleTheme()
  // 广播给全部工作区 iframe（EP/自有变量同源各自动态生效）
  st.workspaces.forEach(w => { try { w.el.contentWindow.postMessage({ type: 'theme.sync', dark: dark.value }, location.origin) } catch {} })
}

const CTX_KEY = 'lp-global-context'
function savedContext() {
  try {
    const v = JSON.parse(localStorage.getItem(CTX_KEY) || '{}')
    const time = restoreTimeRange(v)
    return { service: typeof v.service === 'string' ? v.service : '', environment: typeof v.environment === 'string' ? v.environment : '', ...time }
  } catch {
    return { service: '', environment: '', range: recentRange(), rangeMinutes: DEFAULT_RECENT_MINUTES }
  }
}
const saved = savedContext()

// 平台壳：工作区 tab（iframe 保活）+ 全局上下文广播
const st = reactive({
  plugins: [], services: [], deployServices: [], environments: [], workspaces: [], active: 0, gSvc: saved.service, gEnv: saved.environment, range: saved.range, rangeMinutes: saved.rangeMinutes,
	showLogin: false, username: 'admin', pwd: '', me: {}, users: [], showUsers: false, plusOpen: false, _seq: 0,
  platformPanel: '',
  showSetup: false, setupStep: 1, setupTemplate: 'simple', setupRows: [], setupError: '', setupSaving: false,
})
const activeWorkspace = computed(() => st.workspaces.find(w => w.id === st.active))
const showTimeContext = computed(() => ['logs', 'monitor'].includes(activeWorkspace.value?.plugin?.name))
const contextFields=computed(()=>activeWorkspace.value?.contextFields||{service:showTimeContext.value,environment:showTimeContext.value})
const showContext=computed(()=>showTimeContext.value||contextFields.value.service||contextFields.value.environment)
const defaultPlugin = () => st.plugins.find(p => p.name === 'deploy') || st.plugins.find(p => p.name !== 'devtools')
function openDefault() { const plugin=defaultPlugin(); if(plugin) openPlugin(plugin); else if(st.plugins.some(p=>p.name==='devtools')) st.platformPanel='mcp' }

function openPlugin(p, hash = '') {
  const ws = { id: ++st._seq, plugin: p }
  const el = document.createElement('iframe')
  // 内联样式：scoped CSS 对 JS 动态创建的元素不生效
  el.style.cssText = 'position:absolute;inset:0;width:100%;height:100%;border:none;display:none'
  el.addEventListener('load', () => {
    ctxSync([ws])
    try { el.contentWindow.postMessage({ type: 'theme.sync', dark: dark.value }, location.origin) } catch {}
  })
  el.src = p.url + (hash || '#/?ws=' + ws.id)
  document.getElementById('wsarea').appendChild(el)
  ws.el = el
  st.workspaces.push(ws)
  switchWs(ws.id)
}
function switchWs(id) {
  st.active = id
  st.workspaces.forEach(w => w.el.style.display = w.id === id ? 'block' : 'none')
}
function closeWs(id) {
  const i = st.workspaces.findIndex(w => w.id === id)
  if (i < 0) return
  st.workspaces[i].el.remove()
  st.workspaces.splice(i, 1)
  if (!st.workspaces.length) return
  if (!st.workspaces.some(w => w.id === st.active)) switchWs(st.workspaces[Math.max(0, i - 1)].id)
}
function ctxSync(list) {
  if (st.rangeMinutes > 0) st.range = recentRange(st.rangeMinutes)
  localStorage.setItem(CTX_KEY, JSON.stringify({ service: st.gSvc, environment: st.gEnv, range: st.range, rangeMinutes: st.rangeMinutes }))
  const msg = { type: 'context.sync', service: st.gSvc, environment: st.gEnv, rangeStart: st.range?.[0] || 0, rangeEnd: st.range?.[1] || 0, rangeMinutes: st.rangeMinutes }
  ;(list || st.workspaces).forEach(w => { try { w.el.contentWindow.postMessage(msg, location.origin) } catch {} })
}

async function getJSON(url) {
  try {
    const r = await fetch(url)
    return r.ok ? await r.json() : null
  } catch { return null }
}

// 服务目录合并注册表与各插件当前观测到的服务，插件暂时离线也不影响其他来源。
async function loadServices() {
  const has = name => st.plugins.some(p => p.name === name)
  const sources = await Promise.all([
    getJSON('/api/registry/services'),
    has('logs') ? getJSON('/api/plugins/logs/api/services') : null,
    has('monitor') ? getJSON('/api/plugins/monitor/api/services') : null,
    has('monitor') ? getJSON('/api/plugins/monitor/api/otlp/services') : null,
    has('deploy') ? getJSON('/api/plugins/deploy/api/services') : null,
    getJSON('/api/registry/deployments'),
  ])
  const byName = new Map()
  const add = (name, meta = {}) => {
    if (typeof name !== 'string' || !name.trim()) return
    const key = name.trim()
    byName.set(key, { ...(byName.get(key) || {}), ...meta, name: key })
  }
  ;(sources[0] || []).forEach(s => add(s.name, { ...s, observed: true }))
  Object.keys(sources[1] || {}).forEach(name => add(name, { observed: true }))
  ;(sources[2] || []).forEach(name => add(name, { observed: true }))
  ;(sources[3] || []).forEach(name => add(name, { observed: true }))
  st.deployServices = sources[4] || []
  st.deployServices.forEach(s => add(s.name, { managed: true }))
  ;(sources[5] || []).filter(d => !st.gEnv || d.environment === st.gEnv).forEach(d => add(d.service, { version: d.version, environment: d.environment }))
  st.services = [...byName.values()].sort((a, b) => a.name.localeCompare(b.name, 'zh-CN'))
}
async function environmentChanged() { await loadServices(); ctxSync() }
async function closePlatformPanel() {
  const previous = st.platformPanel
  st.platformPanel = ''
  if (previous === 'resources' && st.plugins.some(p => p.name === 'deploy')) {
    st.environments = await getJSON('/api/resources/environments') || st.environments
    if (st.gEnv && !st.environments.some(e => e.name === st.gEnv)) st.gEnv = st.environments[0]?.name || ''
  }
  await loadServices()
  ctxSync()
}
function useRecentRange() { st.rangeMinutes = DEFAULT_RECENT_MINUTES; st.range = recentRange(); ctxSync() }
function timeRangeChanged(value) { st.rangeMinutes = inferRecentMinutes(value); ctxSync() }
async function login() {
	const r = await fetch('/api/login', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ username: st.username, password: st.pwd }) })
	if (r.ok) location.reload(); else alert('用户名或密码错误')
}
async function logout() { await fetch('/api/logout', { method: 'POST' }); location.reload() }
async function loadIdentity() {
	st.me = await getJSON('/api/me') || {}
	st.users = await getJSON('/api/users') || []
}

const environmentTemplates = {
  simple: [
    { name: 'dev', display_name: '开发', protected: false, require_reason: false, sort_order: 10, approvers: '' },
    { name: 'test', display_name: '测试', protected: false, require_reason: false, sort_order: 20, approvers: '' },
    { name: 'prod', display_name: '生产', protected: false, require_reason: true, sort_order: 30, approvers: '' },
  ],
  standard: [
    { name: 'dev', display_name: '开发', protected: false, require_reason: false, sort_order: 10, approvers: '' },
    { name: 'test', display_name: '测试', protected: false, require_reason: false, sort_order: 20, approvers: '' },
    { name: 'staging', display_name: '预发', protected: false, require_reason: true, sort_order: 30, approvers: '' },
    { name: 'prod', display_name: '生产', protected: true, require_reason: true, sort_order: 40, approvers: '' },
  ],
  custom: [
    { name: '', display_name: '', protected: false, require_reason: false, sort_order: 10, approvers: '' },
  ],
}
function chooseEnvironmentTemplate(name) {
  st.setupTemplate = name
	st.setupRows = environmentTemplates[name].map(row => ({ ...row, approver_list: row.protected && st.me.username ? [st.me.username] : [] }))
  st.setupError = ''
}
function addSetupEnvironment() {
	st.setupRows.push({ name: '', display_name: '', protected: false, require_reason: false, sort_order: (st.setupRows.length + 1) * 10, approvers: '', approver_list: [] })
}
function removeSetupEnvironment(index) {
  if (st.setupRows.length > 1) st.setupRows.splice(index, 1)
}
function validateSetupRows() {
  const names = new Set()
  for (const row of st.setupRows) {
    row.name = String(row.name || '').trim()
    row.display_name = String(row.display_name || '').trim()
		row.approvers = (row.approver_list || []).join(',')
    if (!/^[a-z][a-z0-9_-]{0,31}$/.test(row.name)) return '环境标识需以小写字母开头，只能使用字母、数字、下划线或横线'
    if (!row.display_name) return `请填写 ${row.name} 的显示名称`
    if (names.has(row.name)) return `环境标识重复：${row.name}`
    if (row.protected && !row.approvers) return `受保护环境 ${row.display_name} 需要填写审批人`
    names.add(row.name)
  }
  return ''
}
function nextSetupStep() {
  if (st.setupStep === 2) {
    st.setupError = validateSetupRows()
    if (st.setupError) return
  }
  st.setupError = ''
  st.setupStep++
}
async function finishSetup() {
  st.setupError = validateSetupRows()
  if (st.setupError) { st.setupStep = 2; return }
  st.setupSaving = true
  try {
		const environments = st.setupRows.map(({ approver_list, ...row }) => ({ ...row, approvers: (approver_list || []).join(',') }))
		const r = await fetch('/api/resources/setup/environments', {
			method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ environments }),
    })
    const body = await r.json().catch(() => ({}))
    if (!r.ok) { st.setupError = body.error || '环境初始化失败'; return }
    st.environments = body.environments || []
    st.gEnv = st.environments.find(e => e.name === 'test')?.name || st.environments[0]?.name || ''
    st.showSetup = false
    await loadServices()
    ctxSync()
    if (!st.workspaces.length && st.plugins.length) openDefault()
  } catch {
    st.setupError = '无法连接部署服务，请检查服务状态后重试'
  } finally {
    st.setupSaving = false
  }
}
window.addEventListener('message', e => {
  // 只响应当前工作区 iframe，避免页面中其他 frame 伪造上下文消息。
  if (e.origin !== location.origin) return
  const ws = st.workspaces.find(w => w.el.contentWindow === e.source)
  if (ws && e.data?.type === 'context.request') ctxSync([ws])
  if (ws && e.data?.type === 'context.visibility') ws.contextFields={service:e.data.service===true,environment:e.data.environment===true}
  if (ws && e.data?.type === 'workspace.open') {
    const p = st.plugins.find(x => x.name === e.data.plugin)
    const hash = typeof e.data.hash === 'string' && e.data.hash.startsWith('#/') ? e.data.hash : ''
    if (p) openPlugin(p, hash)
  }
  if (ws && e.data?.type === 'platform.open' && ['apps', 'resources', 'mcp'].includes(e.data.view) && (e.data.view !== 'mcp' || st.plugins.some(p=>p.name==='devtools'))) {
    st.platformPanel = e.data.view
    st.plusOpen = false
  }
  // 工作区内切换应用或环境时回写平台上下文。这样从发布页打开日志、
  // 监控时仍然落在同一个应用和环境，不需要用户再次筛选。
  if (ws && e.data?.type === 'context.update') {
    let changed = false
    if (Object.prototype.hasOwnProperty.call(e.data, 'service') && typeof e.data.service === 'string' && e.data.service !== st.gSvc) {
      st.gSvc = e.data.service
      changed = true
    }
    if (Object.prototype.hasOwnProperty.call(e.data, 'environment') && typeof e.data.environment === 'string' && e.data.environment !== st.gEnv) {
      st.gEnv = e.data.environment
      changed = true
    }
    if (changed) {
      ctxSync()
      loadServices()
    }
  }
})

onMounted(async () => {
  const r = await fetch('/api/plugins')
  if (r.status === 401) { st.showLogin = true; return }
	await loadIdentity()
  st.plugins = (await r.json()) ?? []
  if (st.plugins.some(p => ['deploy','dbms','redis'].includes(p.name))) {
    const setup = await getJSON('/api/resources/setup')
    if (setup && setup.completed === false) {
      chooseEnvironmentTemplate('simple')
      st.showSetup = true
      return
    }
    st.environments = setup?.environments || await getJSON('/api/resources/environments') || []
    if (!st.environments.some(e => e.name === st.gEnv)) st.gEnv = st.environments.find(e => e.name === 'test')?.name || st.environments[0]?.name || ''
  }
  await loadServices()
  // 默认工作区是部署平台。日志/监控里仍有历史数据的服务不等于已接入的
  // 部署应用，不能用缓存上下文把部署平台带进一个不存在的应用详情页。
  if (st.gSvc && !st.deployServices.some(s => s.name === st.gSvc)) {
    st.gSvc = ''
    ctxSync()
  }
  // platform 与插件并行启动，稍后再合并一次，补上启动较慢的插件数据。
  setTimeout(loadServices, 2500)
  if (st.plugins.length) openDefault()
})
const recentClock = setInterval(() => { if (st.rangeMinutes > 0) st.range = recentRange(st.rangeMinutes) }, 30000)
onBeforeUnmount(() => clearInterval(recentClock))
</script>

<template>
  <div class="layout">
    <header class="topbar">
      <div class="logo"><span class="mark">DO</span>DevOps 控制台</div>
      <div class="wstabs">
        <div class="wstab-list">
          <div v-for="w in st.workspaces" :key="w.id" class="wstab" :class="{ on: w.id === st.active }" role="button" tabindex="0" :aria-current="w.id === st.active ? 'page' : undefined" @click="switchWs(w.id)" @keydown.enter.self="switchWs(w.id)" @keydown.space.self.prevent="switchWs(w.id)">
            <UiIcon :name="w.plugin.name" :size="16"/> {{ w.plugin.display }}
            <button class="x" type="button" :aria-label="`关闭${w.plugin.display}工作区`" @click.stop="closeWs(w.id)">×</button>
          </div>
        </div>
        <div class="wslauncher">
          <button class="wsplus" type="button" aria-label="打开插件工作区" :aria-expanded="st.plusOpen" @click.stop="st.plusOpen = !st.plusOpen">＋</button>
          <div class="pm" v-if="st.plusOpen" @click.stop>
            <span class="launcher-label">打开工作区</span>
            <button v-for="p in st.plugins.filter(p=>p.name!=='devtools')" :key="p.name" type="button" @click="openPlugin(p); st.plusOpen = false"><UiIcon :name="p.name"/> {{ p.display }}</button>
          </div>
        </div>
      </div>
		<button class="account" type="button" :title="st.me.role==='admin'?'管理平台成员':'当前登录用户'" @click="st.me.role==='admin'&&(st.showUsers=true)">{{st.me.display_name||st.me.username||'本地'}} · {{st.me.role==='admin'?'管理员':'成员'}}</button>
		<button v-if="st.me.auth_enabled" class="logout" type="button" @click="logout">退出</button>
		<button class="topr" type="button" :aria-label="dark ? '切换浅色主题' : '切换深色主题'" :title="dark ? '切换浅色主题' : '切换深色主题'" @click="switchTheme"><UiIcon :name="dark ? 'sun' : 'moon'"/></button>
    </header>
    <div v-if="showContext" class="contextbar">
      <span class="context-title">当前上下文</span>
      <div v-if="contextFields.service" class="context-field"><label>应用</label>
        <Sel v-model="st.gSvc" :options="st.services.map(s => ({ value: s.name, label: s.managed ? s.name : s.name + ' · 未接入部署' }))"
          all-label="全部应用" filterable width="190px" @change="ctxSync()" />
      </div>
      <div v-if="contextFields.environment" class="context-field"><label>环境</label>
        <Sel v-model="st.gEnv" :options="st.environments.map(e => ({ value: e.name, label: (e.protected ? '🔒 ' : '') + e.display_name }))"
          all-label="全部环境" width="125px" @change="environmentChanged" />
      </div>
      <template v-if="showTimeContext">
        <span class="context-divider"></span>
        <div class="context-field"><label>时间范围</label>
        <div class="seg">
          <button :class="{on:st.rangeMinutes===15}" title="将所有工作区切回当前时间；动态窗口会在每次查询时推进到现在" @click="useRecentRange">最近15分钟{{st.rangeMinutes===15?'（动态）':''}}</button>
          <RangePicker v-model="st.range" @change="timeRangeChanged" width="200px" />
        </div>
      </div>
      </template>
      <span class="context-tip">切换后同步到所有已打开工作区</span>
    </div>
    <div class="wsarea" id="wsarea">
      <div class="emptyws" v-if="!st.workspaces.length"><div>点击 ＋ 打开一个插件工作区</div><ElButton v-if="st.plugins.some(p=>p.name==='devtools')" @click="st.platformPanel='mcp'">打开 MCP</ElButton></div>
    </div>
		<PlatformPanel :view="st.platformPanel" :plugin="activeWorkspace?.plugin?.name || 'deploy'" @switch="st.platformPanel=$event" @close="closePlatformPanel" />
		<UserManagement :show="st.showUsers" :current="st.me" @close="st.showUsers=false;loadIdentity()" />
		<div class="mask" v-if="st.showLogin"><div class="box"><h3>DevOps 控制台登录</h3>
			<ElInput v-model="st.username" placeholder="用户名" style="width:220px" />
			<ElInput v-model="st.pwd" type="password" placeholder="密码" style="width:220px" @keydown.enter="login" />
      <ElButton type="primary" @click="login">登录</ElButton></div></div>
    <div class="setup-mask" v-if="st.showSetup">
      <section class="setup-dialog">
        <header><div><span class="eyebrow">首次使用向导</span><h2>配置团队的发布环境</h2></div><span class="step-count">{{ st.setupStep }} / 3</span></header>
        <div class="setup-steps"><span :class="{on:st.setupStep>=1}">1 选择模板</span><i></i><span :class="{on:st.setupStep>=2}">2 配置策略</span><i></i><span :class="{on:st.setupStep>=3}">3 确认创建</span></div>

        <div v-if="st.setupStep === 1" class="setup-body">
          <p class="setup-lead">环境是主机、发布和监控数据的隔离边界。模板只负责快速填写，下一步仍可修改。</p>
          <div class="template-grid">
            <button type="button" :class="{selected:st.setupTemplate==='simple'}" @click="chooseEnvironmentTemplate('simple')"><b>简单流程</b><small>开发 → 测试 → 生产</small><em>推荐小团队</em></button>
            <button type="button" :class="{selected:st.setupTemplate==='standard'}" @click="chooseEnvironmentTemplate('standard')"><b>标准流程</b><small>开发 → 测试 → 预发 → 生产</small><em>生产默认审批</em></button>
            <button type="button" :class="{selected:st.setupTemplate==='custom'}" @click="chooseEnvironmentTemplate('custom')"><b>完全自定义</b><small>从一个空环境开始</small><em>适合已有规范</em></button>
          </div>
        </div>

        <div v-else-if="st.setupStep === 2" class="setup-body setup-config">
          <div class="config-head"><p>环境名称和策略以后还能修改；已被资源引用的环境不能直接删除。</p><button type="button" @click="addSetupEnvironment">＋ 添加环境</button></div>
          <div class="env-row env-row-head"><span>环境标识</span><span>显示名称</span><span>发布策略</span><span>审批人</span><span></span></div>
          <div class="env-row" v-for="(row,index) in st.setupRows" :key="index">
            <input v-model="row.name" placeholder="如 uat" />
            <input v-model="row.display_name" placeholder="如 用户验收" />
            <div class="checks"><label><input v-model="row.require_reason" type="checkbox" /> 原因必填</label><label><input v-model="row.protected" type="checkbox" /> 审批保护</label></div>
			<Sel v-model="row.approver_list" :disabled="!row.protected" :options="st.users.filter(u=>u.enabled).map(u=>({value:u.username,label:u.display_name+' · '+u.username}))" multiple width="100%" placeholder="选择审核人员" />
            <button type="button" class="remove-env" :disabled="st.setupRows.length<=1" @click="removeSetupEnvironment(index)">×</button>
          </div>
		  <p class="policy-tip">审核人员从平台成员中选择；发布发起人不能审批自己的发布单。</p>
        </div>

        <div v-else class="setup-body">
          <p class="setup-lead">将一次性创建以下环境。平台不会额外生成 <code>default</code> 环境。</p>
          <div class="confirm-envs"><div v-for="row in st.setupRows" :key="row.name"><span :class="{protected:row.protected}">{{row.protected?'🔒':'●'}}</span><b>{{row.display_name}}</b><code>{{row.name}}</code><small>{{row.protected?'需审批':row.require_reason?'原因必填':'普通发布'}}</small></div></div>
        </div>

        <p v-if="st.setupError" class="setup-error">{{ st.setupError }}</p>
        <footer><button v-if="st.setupStep>1" type="button" @click="st.setupStep--;st.setupError=''">上一步</button><span></span><button v-if="st.setupStep<3" class="primary" type="button" @click="nextSetupStep">下一步</button><button v-else class="primary" type="button" :disabled="st.setupSaving" @click="finishSetup">{{st.setupSaving?'创建中…':'创建并进入平台'}}</button></footer>
      </section>
    </div>
  </div>
</template>

<style scoped>
.layout{position:relative;display:flex;flex-direction:column;height:100vh}
.topbar{height:48px;display:flex;align-items:center;padding:0 16px;border-bottom:1px solid var(--border);background:var(--panel);gap:12px;flex-shrink:0}
.logo{display:flex;align-items:center;gap:8px;font-weight:600;font-size:14px;flex-shrink:0}
.logo .mark{width:22px;height:22px;border-radius:5px;background:linear-gradient(135deg,var(--brand),#1f6feb);display:flex;align-items:center;justify-content:center;color:#fff;font-size:11px;font-weight:700}
.wstabs{display:flex;align-items:center;gap:2px;align-self:stretch;margin-left:8px;min-width:0;overflow:visible}.wstab-list{display:flex;align-self:stretch;min-width:0;overflow-x:auto;overflow-y:hidden;scrollbar-width:none}.wstab-list::-webkit-scrollbar{display:none}
.wstab{display:flex;align-items:center;gap:6px;padding:0 10px;font-size:12px;color:var(--muted);cursor:pointer;border-bottom:2px solid transparent;white-space:nowrap;user-select:none}
.wstab:hover{color:var(--text);background:var(--panel2)}
.wstab.on{color:var(--text);border-bottom-color:var(--brand)}
.wstab .x{opacity:.5;font-size:14px;padding:0 2px;border-radius:3px}
.wstab .x:hover{opacity:1;background:var(--panel2)}
.wslauncher{position:relative;display:flex;align-self:stretch;flex-shrink:0}.wsplus{display:flex;align-items:center;padding:0 12px;border:0;background:transparent;color:var(--muted);cursor:pointer;font-size:14px}
.wsplus:hover,.wsplus[aria-expanded="true"]{color:var(--text);background:var(--panel2)}
.pm{position:absolute;top:42px;left:0;display:flex;flex-direction:column;padding:4px;background:var(--panel);border:1px solid var(--border);border-radius:8px;min-width:170px;z-index:80;box-shadow:0 8px 24px rgba(0,0,0,.28)}
.pm button{width:100%;padding:8px 10px;border:0;border-radius:5px;background:transparent;text-align:left;font-size:12px;color:var(--muted);cursor:pointer;white-space:nowrap}
.pm button:hover{background:var(--panel2);color:var(--text)}
.contextbar{height:44px;display:flex;align-items:center;gap:10px;padding:0 16px;border-bottom:1px solid var(--border);background:color-mix(in srgb,var(--panel) 88%,var(--bg));flex-shrink:0;overflow-x:auto}
.context-title{font-size:11px;font-weight:600;color:var(--muted);white-space:nowrap}.context-field{display:flex;align-items:center;gap:6px;white-space:nowrap}.context-field>label{font-size:11px;color:var(--dim)}.context-divider{height:20px;border-left:1px solid var(--border)}.context-tip{margin-left:auto;color:var(--dim);font-size:10px;white-space:nowrap}
.seg{display:flex;border:1px solid var(--border);border-radius:6px;overflow:hidden}
.seg button{background:transparent;border:none;color:var(--muted);padding:5px 10px;font-size:12px;cursor:pointer;border-right:1px solid var(--border)}
.seg button:last-child{border-right:none}
.seg button.on{background:#1f2937;color:var(--text)}
.account,.logout{border:0;border-radius:6px;background:transparent;color:var(--muted);font-size:11px;padding:6px 8px;cursor:pointer;white-space:nowrap}.account{margin-left:auto}.account:hover,.logout:hover{background:var(--panel2);color:var(--text)}.topr{font-size:15px;color:var(--muted);cursor:pointer;user-select:none;padding:5px 7px;border-radius:6px}.topr:hover{background:var(--panel2);color:var(--text)}
.wsarea{flex:1;position:relative;overflow:hidden;background:var(--bg)}
.ws{position:absolute;inset:0;border:none;width:100%;height:100%;display:none}
.ws.on{display:block}
.emptyws{position:absolute;inset:0;display:flex;flex-direction:column;gap:16px;align-items:center;justify-content:center;color:var(--dim)}
.mask{position:fixed;inset:0;background:var(--bg);z-index:100;display:flex;align-items:center;justify-content:center}
.mask .box{width:320px;background:var(--panel);border:1px solid var(--border);border-radius:10px;padding:24px;text-align:center}
.mask h3{margin-bottom:16px}
.mask input{width:100%;margin-bottom:12px;padding:8px 10px}
.mask button{width:100%;background:var(--brand);border-color:var(--brand);color:#fff;padding:8px}
.setup-mask{position:fixed;inset:0;z-index:110;display:flex;align-items:center;justify-content:center;padding:24px;background:color-mix(in srgb,var(--bg) 92%,transparent);backdrop-filter:blur(5px)}
.setup-dialog{width:min(920px,96vw);max-height:92vh;overflow:auto;background:var(--panel);border:1px solid var(--border);border-radius:14px;padding:24px;box-shadow:0 24px 70px rgba(0,0,0,.28)}
.setup-dialog header{display:flex;justify-content:space-between;align-items:flex-start}.setup-dialog h2{margin:4px 0 0;font-size:22px}.eyebrow{font-size:11px;color:var(--brand);font-weight:600}.step-count{color:var(--dim);font:12px var(--mono)}
.setup-steps{display:flex;align-items:center;justify-content:center;margin:24px 0;color:var(--dim);font-size:12px}.setup-steps span.on{color:var(--brand);font-weight:600}.setup-steps i{width:64px;border-top:1px solid var(--border);margin:0 12px}
.setup-body{min-height:255px}.setup-lead{color:var(--muted);margin:0 0 18px}.template-grid{display:grid;grid-template-columns:repeat(3,1fr);gap:12px}.template-grid button{display:flex;flex-direction:column;align-items:flex-start;gap:7px;padding:20px;text-align:left;background:var(--panel2);border:1px solid var(--border);border-radius:10px;color:var(--text);cursor:pointer}.template-grid button:hover,.template-grid button.selected{border-color:var(--brand);box-shadow:0 0 0 1px var(--brand)}.template-grid b{font-size:15px}.template-grid small{color:var(--muted)}.template-grid em{margin-top:10px;color:var(--brand);font-style:normal;font-size:11px}
.config-head{display:flex;align-items:center;margin-bottom:10px}.config-head p{margin:0;color:var(--muted);font-size:12px}.config-head button{margin-left:auto}.env-row{display:grid;grid-template-columns:1fr 1fr 1.45fr 1.15fr 30px;gap:8px;align-items:center;margin-bottom:8px}.env-row-head{color:var(--dim);font-size:10px;margin-bottom:5px}.env-row input:not([type=checkbox]){min-width:0;padding:8px;border:1px solid var(--border);border-radius:6px;background:var(--panel2);color:var(--text)}.env-row input:focus{outline:none;border-color:var(--brand)}.env-row input:disabled{opacity:.6}.env-row .checks{display:flex;gap:10px;font-size:11px;white-space:nowrap}.env-row .checks label{display:flex;align-items:center;gap:4px}.remove-env{border:0;background:transparent;color:var(--dim);font-size:18px;cursor:pointer}.remove-env:hover{color:var(--error)}.remove-env:disabled{opacity:.3}.policy-tip{font-size:11px;color:var(--dim);margin:12px 0 0}.confirm-envs{display:grid;grid-template-columns:repeat(2,1fr);gap:8px}.confirm-envs>div{display:grid;grid-template-columns:24px 1fr auto;align-items:center;gap:8px;padding:13px;background:var(--panel2);border-radius:8px}.confirm-envs span{color:var(--ok)}.confirm-envs span.protected{color:var(--warn)}.confirm-envs code{color:var(--muted)}.confirm-envs small{grid-column:2/4;color:var(--dim)}.setup-error{padding:9px 11px;border-radius:7px;background:color-mix(in srgb,var(--error) 10%,var(--panel));color:var(--error);font-size:12px}.setup-dialog footer{display:flex;gap:8px;border-top:1px solid var(--border);padding-top:16px;margin-top:18px}.setup-dialog footer span{flex:1}.setup-dialog footer button,.config-head button{padding:8px 14px;border:1px solid var(--border);border-radius:6px;background:var(--panel);color:var(--text);cursor:pointer}.setup-dialog footer button.primary{background:var(--brand);border-color:var(--brand);color:#fff}.setup-dialog footer button:disabled{opacity:.55;cursor:not-allowed}
@media(max-width:900px){.logo{font-size:0}.logo .mark{flex-shrink:0}.context-tip{display:none}.context-title{display:none}.contextbar{padding:0 10px}.wstab{padding:0 8px}}
@media(max-width:650px){.context-field>label{display:none}.context-divider{display:none}.seg button{display:none}.template-grid{grid-template-columns:1fr}.env-row{grid-template-columns:1fr 1fr}.env-row-head{display:none}.env-row .remove-env{justify-self:end}.setup-body{min-height:0}.confirm-envs{grid-template-columns:1fr}}
/* Refreshed chrome: workspace tabs remain alive; only their presentation changes. */
.topbar{height:56px;padding:0 20px;gap:16px}.logo{font-size:15px;gap:10px;letter-spacing:-.25px}.logo .mark{width:28px;height:28px;border-radius:9px;font-size:11px;box-shadow:0 3px 8px rgb(37 99 235 / 20%)}
.wstabs{margin-left:12px;padding-top:8px;flex:1}.wstab-list{gap:4px}.wstab{gap:8px;padding:0 14px;border-radius:10px 10px 0 0;font-size:13px}.wstab.on{background:var(--brand-soft);color:var(--brand-ink);font-weight:600}.wstab .x{border:0;background:transparent;color:inherit;padding:0;width:24px;height:28px;opacity:.65;line-height:1}.wsplus{align-self:center;width:36px;height:36px;justify-content:center;padding:0;border-radius:9px;font-size:20px}
.pm{top:48px;min-width:220px;padding:8px;box-shadow:var(--shadow-float);border-radius:12px}.pm button{display:flex;align-items:center;gap:12px;padding:11px 12px;font-size:13px}.launcher-label{padding:6px 12px 8px;font-size:11px;color:var(--dim);font-weight:600}
.account{margin-left:0;font-size:12px;max-width:220px;overflow:hidden;text-overflow:ellipsis}.topr{border:1px solid var(--border);background:var(--panel);width:34px;height:34px;display:grid;place-items:center;padding:0;flex-shrink:0}
.contextbar{height:48px;padding:0 20px;gap:12px;background:var(--panel)}.context-title{font-size:11px;padding-right:12px;border-right:1px solid var(--border);color:var(--muted)}.context-field>label{font-size:12px;color:var(--muted)}.context-tip{font-size:11px}
@media(max-width:900px){.topbar{padding:0 12px;gap:8px}.logo{font-size:0}.wstabs{margin-left:0}.wstab{padding:0 10px}.account{font-size:11px;max-width:150px}.contextbar{padding:0 12px}.context-title,.context-tip{display:none}}
@media(max-width:650px){.account{display:none}.wstab{font-size:12px;gap:5px}.wstab .x{width:20px}.logo .mark{width:26px;height:26px}.context-field{flex-shrink:0}}
</style>
