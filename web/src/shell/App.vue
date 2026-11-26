<script setup>

import {AUTH_REQUIRED_EVENT, resetPlatformAuth} from '../shared/platformAuth.js'
import {workspaceTitle as formatWorkspaceTitle} from './workspaceTitle.js'
import PersonalHome from './PersonalHome.vue'
import LoginView from './LoginView.vue'
import {loginPageURL,requestedLoginReturn} from '../shared/loginNavigation.js'
import ConsoleNavigation from './ConsoleNavigation.vue'
import {workspaceNavigation, workspaceBreadcrumb} from './navigation.js'
import {platformPage,platformWorkspacePlugin} from './platformPages.js'
import {workspaceCloseTargets,nextWorkspaceAfterClose} from './workspaceActions.js'
import {chooseWorkspace} from './workspaceTarget.js'
import {createWorkspaceHistory} from './workspaceHistory.js'


import AppearanceSettings from './AppearanceSettings.vue'
import {safeWorkspaceHash, runtimeWorkspaceHash, linkedWorkspaceContext, windowContext, workspaceSnapshot, restoreWorkspaceSnapshot, workspaceStorageKey} from './workspaceSession.js'
import {randomUUID} from '../shared/uuid.js'

import {reactive, ref, onMounted, onBeforeUnmount, computed, nextTick, watch} from 'vue'
import TimeRangeControl from './TimeRangeControl.vue'
import OptionPicker from '../shared/components/OptionPicker.vue'
import {confirmDialog} from '../shared/shadcnFeedback.js'

import {themeDark, themeSyncPayload, toggleTheme} from '../shared/composables/theme.js'

import MembersDialog from './MembersDialog.vue'
import ConsoleIcon from './ConsoleIcon.vue'
import {hasInlineLogContext, updateWorkspaceContext} from './workspaceContext.js'
import {applicationOption} from '../shared/applicationName.js'
import {currentApplications} from './applicationCatalog.js'
import {DEFAULT_RECENT_MINUTES, inferRecentMinutes, recentRange, restoreTimeRange} from '../shared/timeRange.js'
import HeaderWithTabs from './HeaderWithTabs.vue'
import GlobalSearchCommand from '../shared/components/GlobalSearchCommand.vue'
import { useGlobalShortcuts } from '../shared/composables/useGlobalShortcuts.js'
import '../shell/layout-optimization.css'

const dark = themeDark
function switchTheme() {
  dark.value = toggleTheme()
  // 广播给全部工作区 iframe（EP/自有变量同源各自动态生效）
  st.workspaces.forEach(w => { try { w.el.contentWindow.postMessage(themeSyncPayload(), location.origin) } catch {} })
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

// 工作区保活，每个窗口独立上下文；只有主题仍全局广播。
const st = reactive({
  plugins: [], services: [], deployServices: [], environments: [], workspaces: [], active: 0, gSvc: saved.service, gEnv: saved.environment, range: saved.range, rangeMinutes: saved.rangeMinutes,
	showLogin: location.pathname==='/login', username: 'admin', pwd: '', loginError:'', loginBusy:false, loginExpired:new URLSearchParams(location.search).get('reason')==='expired', reauthUser:'', me: {}, users: [], showUsers: false, plusOpen: false, _seq: 0,
  platformPanel: '',
  showSetup: false, setupStep: 1, setupTemplate: 'simple', setupRows: [], setupError: '', setupSaving: false,
})
const workspaceTabBar=ref(null)
let workspaceTabObserver
function revealActiveWorkspaceTab(){
 const scroller=workspaceTabBar.value?.querySelector('[data-slot="tabs"]')
 const trigger=scroller?.querySelector('[role="tab"][aria-selected="true"]')
 if(!trigger||!scroller.clientWidth)return
 const item=trigger.closest('[data-workspace-tab]')||trigger
 const viewport=scroller.getBoundingClientRect(),bounds=item.getBoundingClientRect()
 // Adjust only this strip's horizontal position; keep the page and iframe scroll intact.
 if(bounds.left<viewport.left||bounds.width>viewport.width)scroller.scrollLeft+=bounds.left-viewport.left
 else if(bounds.right>viewport.right)scroller.scrollLeft+=bounds.right-viewport.right
}
watch(workspaceTabBar,bar=>{
 workspaceTabObserver?.disconnect()
 if(!bar)return
 workspaceTabObserver=new ResizeObserver(revealActiveWorkspaceTab)
 workspaceTabObserver.observe(bar)
}, {flush:'post'})
watch(()=>[st.active,...st.workspaces.map(w=>workspaceTitle(w))],()=>nextTick(revealActiveWorkspaceTab),{flush:'post'})
onBeforeUnmount(()=>workspaceTabObserver?.disconnect())
const activeWorkspace = computed(() => st.workspaces.find(w => w.id === st.active))
const homeWorkspaces=computed(()=>st.workspaces.map(w=>({...w,displayTitle:workspaceTitle(w)})))
const windowsReady=ref(false),authReady=ref(false)
st._activationSeq=0
const workspaceHistory=createWorkspaceHistory(window,{
 activeName:()=>activeWorkspace.value?.el?.name||'',
 hasWorkspace:name=>st.workspaces.some(w=>w.el.name===name),
 activate:name=>switchWs(st.workspaces.find(w=>w.el.name===name)?.id||0,{history:false}),
 canRestore:()=>windowsReady.value&&!st.showLogin,
})
onBeforeUnmount(()=>workspaceHistory.dispose())
let loginResumeURL='/'
watch(()=>st.showLogin,value=>{document.documentElement.toggleAttribute('data-console-auth-page',value);document.title=value?'登录 · DevOps 控制台':'DevOps 控制台'},{immediate:true})
function currentWorkspaceHash(w){
 try{if(w.el.contentWindow.location.href!=='about:blank'&&w.el.contentWindow.location.origin===location.origin)return runtimeWorkspaceHash(w.el.contentWindow.location.hash||'#/',w.plugin.name)}catch{}
 return w.hash
}
function rememberWindows(){
 if(!windowsReady.value)return
 try{sessionStorage.setItem(workspaceStorageKey(st.me.username),JSON.stringify(workspaceSnapshot(st.workspaces.map(w=>({...w,hash:currentWorkspaceHash(w)})),st.active)))}catch{}
}
watch(()=>[st.active,...st.workspaces.map(w=>[w.id,w.title,w.hash,w.context?.service,w.context?.environment])],rememberWindows,{deep:true})
function openHome(){switchWs(0);st.plusOpen=false}
function openPlatformWorkspace(view){
 if(!platformPage(view,st.me,st.plugins))return
 openTarget(platformWorkspacePlugin,'#/'+view,{context:windowContext({})},{menu:true})
}

function workspaceRoute(w){try{if(w.el?.contentWindow?.location.origin===location.origin)return w.el.contentWindow.location.hash||w.hash}catch{}return w.hash}
function openTarget(plugin,hash,source={context:windowContext({})},{menu=false,forceNew=false}={}){
 const match=chooseWorkspace(st.workspaces.map(w=>({...w,hash:currentWorkspaceHash(w),routeHash:workspaceRoute(w)})),plugin.name,hash,{menu,forceNew,active:st.active,context:source.context})
 if(match){const workspace=st.workspaces.find(w=>w.id===match.workspace.id),replace=st.active!==workspace.id;switchWs(workspace.id);if(match.navigate)workspace.el.contentWindow.postMessage({type:'workspace.navigate',hash:match.hash||hash,replace},location.origin)}
 else openPlugin(plugin,hash,source)
}
function navigationOpen(action){
 const plugin=st.plugins.find(p=>p.name===action.plugin);if(!plugin)return
 openTarget(plugin,action.hash||'#/',{context:windowContext({})},{menu:true,forceNew:action.forceNew===true});st.plusOpen=false
}
function homeOpen(action){
 const plugin=st.plugins.find(p=>p.name===action.plugin);if(!plugin)return
 openTarget(plugin,action.hash||'#/',{context:windowContext({})},{forceNew:action.forceNew===true})
}
const renameWorkspace=ref(null),workspaceName=ref('')
const controlsContext=()=>({service:st.gSvc,environment:st.gEnv,range:[...(st.range||[])],rangeMinutes:st.rangeMinutes})
function duplicateWorkspace(id){const w=st.workspaces.find(w=>w.id===Number(id));if(w)openTarget(w.plugin,workspaceRoute(w),{context:windowContext(w.context)},{forceNew:true})}
function workspaceTitle(w){return formatWorkspaceTitle(w,st.workspaces)}
function renameWs(w){renameWorkspace.value=w;workspaceName.value=workspaceTitle(w)}
function saveWorkspaceName(){const value=workspaceName.value.trim();if(!value||value.length>60)return;renameWorkspace.value.title=value;renameWorkspace.value=null}
const showContext = computed(() => ['logs', 'monitor'].includes(activeWorkspace.value?.plugin?.name) && !hasInlineLogContext(activeWorkspace.value))
const showTimeContext=computed(()=>{
 const workspace=activeWorkspace.value
 if(!showContext.value)return false
 const path=new URL((workspace.hash||'#/').slice(1),'http://context').pathname
 return workspace.plugin.name==='logs'?['/','/search'].includes(path):!['/alerts','/rules'].includes(path)
})
const contextFields=computed(()=>activeWorkspace.value?.contextFields||{service:showContext.value,environment:showContext.value})

const globalSearchOpen = ref(false)
const { register: registerShortcut } = useGlobalShortcuts()

const appMenuGroups = computed(() => {
  if (!showContext.value) return []
  return [{
    label: '应用',
    items: st.services.map(s => ({
      value: s.name,
      label: applicationOption(s).label + (s.managed ? '' : ' · 未接入部署'),
      selected: st.gSvc === s.name
    }))
  }]
})

const envMenuGroups = computed(() => {
  if (!showContext.value) return []
  return [{
    label: '环境',
    items: st.environments.map(e => ({
      value: e.name,
      label: (e.protected ? '🔒 ' : '') + e.display_name,
      selected: st.gEnv === e.name
    }))
  }]
})

const headerTabs = computed(() => {
  const result = [{ value: 0, label: '个人工作台' }]
  for (const w of st.workspaces) {
    result.push({
      value: w.id,
      label: workspaceTitle(w)
    })
  }
  return result
})

function handleHeaderTabChange(value) {
  switchWs(Number(value))
}

function handleGlobalSearch(item) {
  if (item.type === 'app') {
    st.gSvc = item.value
    ctxSync()
  } else if (item.type === 'env') {
    st.gEnv = item.value
    environmentChanged()
  }
}

function openDefault() { openHome() }

function openPlugin(p, hash = '', source=activeWorkspace.value, restored=null) {
  if(activeWorkspace.value)activeWorkspace.value.context=controlsContext()
  const ws = { id: ++st._seq, plugin: p, context:windowContext(source?.context||controlsContext()), hash:runtimeWorkspaceHash(hash||'#/',p.name), title:restored?.title||'' }
  const el = document.createElement('iframe')
  el.allowFullscreen = true
  el.allow = 'fullscreen'
  el.title=p.display||p.name
  el.name=restored?.name||'lp-workspace-'+randomUUID()
  // 内联样式：scoped CSS 对 JS 动态创建的元素不生效
  el.style.cssText = 'position:absolute;inset:0;width:100%;height:100%;border:none;display:none'
  el.addEventListener('load', () => {
    const readRoute=()=>{try{const target=st.workspaces.find(w=>w.id===ws.id);if(target)target.hash=runtimeWorkspaceHash(el.contentWindow.location.hash,p.name);rememberWindows()}catch{}}
    readRoute()
    try{el.contentWindow.addEventListener('hashchange',readRoute)}catch{}
    if(st.me.auth_enabled)getJSON('/api/me')
    ctxSync([ws])
    try { el.contentWindow.postMessage(themeSyncPayload(), location.origin) } catch {}
  })
  el.src = p.url + (hash || '#/?ws=' + ws.id)
  document.getElementById('wsarea').appendChild(el)
  ws.el = el
  st.workspaces.push(ws)
  switchWs(ws.id)
}
function switchWs(id,{history:recordHistory=true}={}) {
  if(activeWorkspace.value)activeWorkspace.value.context=controlsContext()
  st.active = id
  if(activeWorkspace.value)activeWorkspace.value.lastActivated=++st._activationSeq
  const c=activeWorkspace.value?.context
  if(c){st.gSvc=c.service;st.gEnv=c.environment;st.range=[...(c.range||[])];st.rangeMinutes=c.rangeMinutes;if(c.rangeMinutes>0)st.range=recentRange(c.rangeMinutes)}
  st.workspaces.forEach(w => {w.el.style.display = w.id === id ? 'block' : 'none';try{w.el.contentWindow.postMessage({type:'workspace.visibility',active:w.id===id},location.origin)}catch{}})
  rememberWindows()
  if(recordHistory)workspaceHistory.record()
}
const closingWorkspace = ref(false)
async function closeWorkspaceIDs(ids){
 if(closingWorkspace.value)return
 const targets=st.workspaces.filter(w=>ids.includes(w.id));if(!targets.length)return
 closingWorkspace.value=true
 try{
  const unsafe=targets.filter(w=>w.dirty||w.busy)
  if(unsafe.length){
   const names=unsafe.slice(0,5).map(workspaceTitle).join('、')+(unsafe.length>5?' 等':'')
   try{await confirmDialog.confirm(`将关闭 ${targets.length} 个标签，其中 ${unsafe.length} 个存在未保存编辑或进行中的请求（${names}）。关闭会丢弃未保存内容，不会取消远程任务，也不会撤销已保存配置。`, '确认关闭标签',{confirmButtonText:'确认关闭',cancelButtonText:'继续编辑'})}catch{return}
  }
  const active=nextWorkspaceAfterClose(st.workspaces,st.active,ids)
  st.workspaces.filter(w=>ids.includes(w.id)).forEach(w=>w.el.remove())
  st.workspaces=st.workspaces.filter(w=>!ids.includes(w.id))
  switchWs(active)
 }finally{closingWorkspace.value=false}
}
function closeWorkspaceGroup(action,anchor=st.active){return closeWorkspaceIDs(workspaceCloseTargets(st.workspaces,anchor,action))}
function hasCloseTargets(action,anchor=st.active){return !closingWorkspace.value&&workspaceCloseTargets(st.workspaces,anchor,action).length>0}

function ctxSync(list) {
  if (st.rangeMinutes > 0) st.range = recentRange(st.rangeMinutes)
  if(activeWorkspace.value)activeWorkspace.value.context=controlsContext()
  localStorage.setItem(CTX_KEY, JSON.stringify({ service: st.gSvc, environment: st.gEnv, range: st.range, rangeMinutes: st.rangeMinutes }))
  ;(list || (activeWorkspace.value?[activeWorkspace.value]:[])).forEach(w => {const c=w.context||controlsContext();if(c.rangeMinutes>0)c.range=recentRange(c.rangeMinutes);const msg={type:'context.sync',service:c.service,environment:c.environment,rangeStart:c.range?.[0]||0,rangeEnd:c.range?.[1]||0,rangeMinutes:c.rangeMinutes,...(w.plugin.name==='logs'?{applications:st.services.map(s=>({value:s.name,label:applicationOption(s).label+(s.managed?'':' · 未接入部署')})),environments:st.environments.map(e=>({value:e.name,label:e.display_name||e.name}))}:{})};try { w.el.contentWindow.postMessage(msg, location.origin) } catch {} })
  rememberWindows()
}

async function getJSON(url) {
  try {
    const r = await fetch(url)
    return r.ok ? await r.json() : null
  } catch { return null }
}

// 当前目录决定可见应用；历史日志、监控和部署记录不重新加入下拉框。
let servicesRequest = 0
async function loadServices() {
  const request = ++servicesRequest
  const has = name => st.plugins.some(p => p.name === name)
  const sources = await Promise.all([
    getJSON('/api/registry/applications'),
    has('deploy') ? getJSON('/api/plugins/deploy/api/application-references') : null,
  ])
  if (request !== servicesRequest) return
  if (sources[1]?.complete === true && Array.isArray(sources[1].applications)) st.deployServices = sources[1].applications
  // An unavailable catalog must not resurrect historical names or clear a
  // workspace's selected query scope. Keep the last successful presentation.
  if (Array.isArray(sources[0])) st.services = currentApplications(sources[0], st.deployServices)
}
watch(() => [st.services, st.environments], () => {
  const logs = st.workspaces.filter(w => w.plugin.name === 'logs')
  if (logs.length) ctxSync(logs)
})
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
function loginReturnTarget(){return requestedLoginReturn(location.search,location.hash)}
function requireLogin(){
 if(st.showLogin)return
 loginResumeURL=location.pathname==='/'?location.pathname+location.search+location.hash:'/'
 st.reauthUser=st.me.username||'';st.username=st.reauthUser||st.username;st.pwd='';st.loginError='';st.loginExpired=!!st.reauthUser;st.showLogin=true
 history.replaceState(history.state,'',loginPageURL(loginReturnTarget(),st.loginExpired))
 for(const ws of st.workspaces)ws.el.contentWindow?.postMessage({type:'workspace.visibility',active:false},location.origin)
}
function loginRouteChanged(){if(st.showLogin&&location.pathname!=='/login')history.replaceState(history.state,'',loginPageURL(loginReturnTarget(),st.loginExpired))}
window.addEventListener('popstate',loginRouteChanged)
window.addEventListener(AUTH_REQUIRED_EVENT,requireLogin)
async function login() {
 if(st.loginBusy)return;st.loginBusy=true;st.loginError=''
 try{
  const r=await fetch('/api/login',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({username:st.username,password:st.pwd})})
  const result=await r.json().catch(()=>({}))
  if(!r.ok){st.loginError=result.error||'登录失败，请重试';return}
  st.pwd='';resetPlatformAuth()
  if(st.loginExpired&&st.reauthUser===result.username){
   await loadIdentity();st.showLogin=false;st.loginExpired=false
   history.replaceState(history.state,'',loginResumeURL)
   for(const ws of st.workspaces)ws.el.contentWindow?.postMessage({type:'platform.auth.restored'},location.origin)
   switchWs(st.active)
   return
  }
  const target=loginReturnTarget()
  location.replace(target||'/')
 }catch{st.loginError='无法连接平台，请重试'}finally{st.loginBusy=false}
}

async function logout() { await fetch('/api/logout', { method: 'POST' }); location.replace('/login') }
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
    windowsReady.value=true
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
  if(ws&&e.data?.type==='platform.auth.required'){requireLogin();return}
  if(ws&&e.data?.type==='workspace.history'){workspaceHistory.fromFrame(ws.el.name);return}
  if(ws&&e.data?.type==='workspace.route'){ws.hash=currentWorkspaceHash(ws);rememberWindows()}
  if(ws&&e.data?.type==='workspace.label'&&typeof e.data.label==='string')ws.label=e.data.label.trim().slice(0,160)
  if(ws&&e.data?.type==='application.presentation.changed')loadServices()
  if(ws&&e.data?.type==='workspace.dirty'){ws.dirty=e.data.dirty===true;ws.busy=e.data.busy===true}
  if (ws && e.data?.type === 'context.request') ctxSync([ws])
  if (ws && e.data?.type === 'workspace.activity.request') ws.el.contentWindow.postMessage({type:'workspace.visibility',active:ws.id===st.active},location.origin)
  if (ws && e.data?.type === 'context.visibility') ws.contextFields={service:e.data.service===true,environment:e.data.environment===true}
  if (['dbms','research','deploy'].includes(ws?.plugin.name) && e.data?.type === 'work-item.changed' && Number.isSafeInteger(e.data.id) && e.data.id > 0) {
    for (const target of st.workspaces.filter(w => ['deploy','research'].includes(w.plugin.name))) target.el.contentWindow?.postMessage({ type: 'work-item.changed', id: e.data.id }, location.origin)
  }
  if (ws && e.data?.type === 'workspace.open') {
    const p = st.plugins.find(x => x.name === e.data.plugin)
    const hash = typeof e.data.hash === 'string' && e.data.hash.startsWith('#/') ? e.data.hash : ''
    if (p) openTarget(p,hash,{...ws,context:linkedWorkspaceContext(ws.context,e.data.context)},{forceNew:e.data.forceNew===true})
  }
  if (ws && e.data?.type === 'platform.open' && platformPage(e.data.view,st.me,st.plugins)) {
    st.platformPanel = e.data.view
    st.plusOpen = false
  }
  if(ws&&e.data?.type==='platform.home')openHome()
  // 回写消息来源窗口；从该窗口打开日志/监控时继承条件，不影响其他窗口。
  if (ws && e.data?.type === 'context.update') {
    const changed = updateWorkspaceContext(ws.context, e.data)
    if (changed) {
      if(ws.id===st.active){st.gSvc=ws.context.service;st.gEnv=ws.context.environment;st.range=[...ws.context.range];st.rangeMinutes=ws.context.rangeMinutes}
      ctxSync([ws])
    }
  }
})

onMounted(async () => {
  registerShortcut('ctrl+k', () => {
    globalSearchOpen.value = true
  })
  const r = await fetch('/api/plugins')
  if (r.status === 401) { requireLogin(); return }
  const returnTo=loginReturnTarget();if(r.ok&&(location.pathname==='/login'||returnTo!=='/')){location.replace(returnTo);return}
	await loadIdentity();authReady.value=true
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
  loadServices()
  // 默认工作区是部署平台。日志/监控里仍有历史数据的服务不等于已接入的
  // 部署应用，不能用缓存上下文把部署平台带进一个不存在的应用详情页。
  if (st.gSvc && !st.deployServices.some(s => s.name === st.gSvc)) {
    st.gSvc = ''
    ctxSync()
  }
  // platform 与插件并行启动，稍后再合并一次，补上启动较慢的插件数据。
  setTimeout(loadServices, 2500)
  let restored={windows:[],active:-1}
  try{restored=restoreWorkspaceSnapshot(JSON.parse(sessionStorage.getItem(workspaceStorageKey(st.me.username))||'null'),[...st.plugins,platformWorkspacePlugin],st.me)}catch{}
  for(const row of restored.windows)openPlugin(row.plugin,row.hash,row,row)
  if(restored.active>=0)switchWs(st.workspaces[restored.active]?.id||0)
  else openDefault()
  windowsReady.value=true;workspaceHistory.start();rememberWindows()
})
window.addEventListener('pagehide',rememberWindows)
const recentClock = setInterval(() => { if (st.rangeMinutes > 0) st.range = recentRange(st.rangeMinutes) }, 30000)
onBeforeUnmount(() => {rememberWindows();clearInterval(recentClock);window.removeEventListener('pagehide',rememberWindows);window.removeEventListener(AUTH_REQUIRED_EVENT,requireLogin);window.removeEventListener('popstate',loginRouteChanged);document.documentElement.removeAttribute('data-console-auth-page')})
watch(()=>st.platformPanel,view=>{if(view){openPlatformWorkspace(view);st.platformPanel=''}})
import {SidebarProvider,SidebarInset,SidebarTrigger} from '@/components/ui/sidebar'
import {Stepper,StepperItem,StepperTrigger,StepperIndicator,StepperTitle,StepperSeparator} from '@/components/ui/stepper'
import {ChevronDownIcon,XIcon,PlusIcon,CheckIcon,LogOutIcon,UserRoundIcon} from '@lucide/vue'
import ContextHelp from '../shared/components/ContextHelp.vue'
import FeedbackHost from '../shared/components/FeedbackHost.vue'

import {Breadcrumb,BreadcrumbList,BreadcrumbItem,BreadcrumbPage,BreadcrumbSeparator} from '@/components/ui/breadcrumb'
import {Badge,Button,Checkbox,ContextMenu,ContextMenuContent,ContextMenuItem,ContextMenuSeparator,ContextMenuTrigger,Dialog,DialogContent,DialogDescription,DialogFooter,DialogHeader,DialogTitle,DropdownMenu,DropdownMenuContent,DropdownMenuItem,DropdownMenuSeparator,DropdownMenuTrigger,Field,FieldGroup,FieldLabel,Input,Separator,Spinner,Tabs,TabsList,TabsTrigger,TooltipProvider} from '../shared/shadcn-ui.js'
</script>

<template>
<TooltipProvider>
<LoginView v-if="st.showLogin" v-model:username="st.username" v-model:password="st.pwd" :busy="st.loginBusy" :error="st.loginError" :expired="st.loginExpired" :preserve-workspace="!!st.reauthUser" @submit="login"/>
<SidebarProvider v-show="authReady&&!st.showLogin" class="h-svh min-h-0" :style="{'--sidebar-width':'var(--console-sidebar-width,15rem)',...(activeWorkspace?.plugin?.name==='api-center'&&activeWorkspace.hash?.startsWith('#/documents')?{'--sidebar-width':'12.5rem'}:{})}" :inert="st.showLogin?true:undefined">
 <ConsoleNavigation :plugins="st.plugins" :workspace="activeWorkspace" :home="st.active===0" :admin="st.me.role==='admin'" @home="openHome" @open="navigationOpen" @platform="openPlatformWorkspace"/>
 <SidebarInset class="h-svh min-w-0 overflow-hidden">
  <HeaderWithTabs
    :breadcrumbs="workspaceBreadcrumb(st.active===0?null:activeWorkspace)"
    :tabs="headerTabs"
    :can-close="hasCloseTargets"
    :active-tab="String(st.active)"
    :app-menu-groups="appMenuGroups"
    :env-menu-groups="envMenuGroups"
    :current-app="st.gSvc"
    :current-env="st.gEnv"
    @tab-change="handleHeaderTabChange" @tab-duplicate="duplicateWorkspace"
    @app-select="item => { st.gSvc = item.value; ctxSync() }"
    @env-select="item => { st.gEnv = item.value; environmentChanged() }"
    @search-open="globalSearchOpen = true"
    @tab-close="closeWorkspaceGroup"
  >
    <template #actions>
      <SidebarTrigger aria-label="切换主导航" class="mr-2" />
      <Separator orientation="vertical" class="mx-1 h-4" />
      <AppearanceSettings />
      <DropdownMenu>
        <DropdownMenuTrigger as-child>
          <Button variant="ghost" class="max-w-48 gap-2">
            <UserRoundIcon class="size-4" />
            <span class="truncate">{{ st.me.display_name || st.me.username || '本地' }}</span>
            <ChevronDownIcon class="size-3.5" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuItem disabled>{{ st.me.role === 'admin' ? '管理员' : '成员' }}</DropdownMenuItem>
          <DropdownMenuItem v-if="st.me.role === 'admin'" @select="st.showUsers = true">成员管理</DropdownMenuItem>
          <DropdownMenuSeparator v-if="st.me.auth_enabled" />
          <DropdownMenuItem v-if="st.me.auth_enabled" @select="logout"><LogOutIcon class="size-4" />退出登录</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </template>
  </HeaderWithTabs>

  <GlobalSearchCommand
    v-model="globalSearchOpen"
    :applications="st.services"
    :environments="st.environments"
    :current-app="st.gSvc"
    :current-env="st.gEnv"
    @select="handleGlobalSearch"
  />

  <div id="wsarea" class="relative min-h-0 min-w-0 flex-1 overflow-hidden"><PersonalHome v-if="windowsReady" v-show="st.active===0" :active="st.active===0&&!st.showLogin" :me="st.me" :plugins="st.plugins" :workspaces="homeWorkspaces" @open="homeOpen" @resume="switchWs" @platform="openPlatformWorkspace"/></div>
 </SidebarInset>
</SidebarProvider>
<Dialog :open="!!renameWorkspace" @update:open="v=>{if(!v)renameWorkspace=null}"><DialogContent class="sm:max-w-sm"><DialogHeader><DialogTitle>重命名工作区</DialogTitle><DialogDescription class="sr-only">仅修改当前页签名称</DialogDescription></DialogHeader><form class="space-y-5" @submit.prevent="saveWorkspaceName"><Field><FieldLabel for="workspace-name">工作区名称</FieldLabel><Input id="workspace-name" v-model="workspaceName" maxlength="60" required/></Field><DialogFooter><Button type="button" variant="outline" @click="renameWorkspace=null">取消</Button><Button type="submit" :disabled="!workspaceName.trim()">保存名称</Button></DialogFooter></form></DialogContent></Dialog>
<MembersDialog :show="st.showUsers" :current="st.me" @close="st.showUsers=false;loadIdentity()"/>
<Dialog :open="st.showSetup"><DialogContent :show-close-button="false" class="flex max-h-[94dvh] flex-col sm:max-w-4xl" @escape-key-down="e=>e.preventDefault()" @interact-outside="e=>e.preventDefault()"><DialogHeader><DialogTitle>配置团队的发布环境</DialogTitle><DialogDescription class="sr-only">选择模板，配置环境策略，确认创建</DialogDescription></DialogHeader>
 <Stepper :model-value="st.setupStep" class="flex w-full gap-2"><StepperItem v-for="(label,index) in ['选择模板','配置策略','确认创建']" :key="label" :step="index+1" class="relative flex flex-1 flex-col items-center gap-2"><StepperTrigger class="pointer-events-none flex items-center gap-2"><StepperIndicator>{{index+1}}</StepperIndicator><StepperTitle class="text-sm">{{label}}</StepperTitle></StepperTrigger><StepperSeparator v-if="index<2" class="absolute top-4 left-[calc(50%+70px)] h-px w-[calc(100%-140px)] bg-border"/></StepperItem></Stepper>
 <div class="min-h-0 overflow-auto py-5"><div v-if="st.setupStep===1" class="grid gap-3 sm:grid-cols-3"><Button v-for="entry in [{id:'simple',label:'简单流程',detail:'开发 → 测试 → 生产'},{id:'standard',label:'标准流程',detail:'开发 → 测试 → 预发 → 生产'},{id:'custom',label:'完全自定义',detail:'从空环境开始'}]" :key="entry.id" :variant="st.setupTemplate===entry.id?'secondary':'outline'" class="h-28 flex-col gap-2" @click="chooseEnvironmentTemplate(entry.id)"><strong>{{entry.label}}</strong><span class="text-xs text-muted-foreground">{{entry.detail}}</span></Button></div>
 <div v-else-if="st.setupStep===2" class="space-y-4"><div class="flex items-center justify-between"><ContextHelp text="审核人员从平台成员中选择；发起人不能审批自己的发布单。环境后续可修改，已被引用的环境不能直接删除。"/><Button variant="outline" size="sm" @click="addSetupEnvironment"><PlusIcon class="size-3.5"/>添加环境</Button></div><section v-for="(row,index) in st.setupRows" :key="index" class="grid items-start gap-4 rounded-lg border p-4 sm:grid-cols-2"><Field><FieldLabel :for="'env-id-'+index">环境标识</FieldLabel><Input :id="'env-id-'+index" v-model="row.name" placeholder="如 uat"/></Field><Field><FieldLabel :for="'env-label-'+index">显示名称</FieldLabel><Input :id="'env-label-'+index" v-model="row.display_name" placeholder="如 用户验收"/></Field><div class="flex items-center gap-4"><Field orientation="horizontal"><Checkbox :id="'env-reason-'+index" v-model="row.require_reason" :disabled="!row.protected"/><FieldLabel :for="'env-reason-'+index">原因必填</FieldLabel></Field><Field orientation="horizontal"><Checkbox :id="'env-protected-'+index" v-model="row.protected"/><FieldLabel :for="'env-protected-'+index">审批保护</FieldLabel></Field></div><Field><FieldLabel :for="'env-reviewer-'+index">审批人</FieldLabel><OptionPicker :id="'env-reviewer-'+index" v-model="row.approver_list" :disabled="!row.protected" multiple :options="st.users.filter(u=>u.enabled).map(u=>({value:u.username,label:u.display_name+' · '+u.username}))" placeholder="选择审核人员"/></Field><Button variant="ghost" size="sm" class="justify-self-end text-destructive sm:col-span-2" :disabled="st.setupRows.length<=1" @click="removeSetupEnvironment(index)">移除此环境</Button></section></div>
 <div v-else class="grid gap-3 sm:grid-cols-2"><div v-for="row in st.setupRows" :key="row.name" class="flex items-center justify-between gap-3 rounded-lg border p-4"><div><strong class="text-sm">{{row.display_name}}</strong><code class="ml-2 text-xs text-muted-foreground">{{row.name}}</code></div><Badge variant="outline">{{row.protected?'需审批':'普通发布'}}</Badge></div></div>
 </div><p v-if="st.setupError" role="alert" class="text-sm text-destructive">{{st.setupError}}</p><DialogFooter><Button v-if="st.setupStep>1" variant="outline" :disabled="st.setupSaving" @click="st.setupStep--;st.setupError=''">上一步</Button><Button v-if="st.setupStep<3" @click="nextSetupStep">下一步</Button><Button v-else :disabled="st.setupSaving" @click="finishSetup"><Spinner v-if="st.setupSaving"/>创建并进入平台</Button></DialogFooter></DialogContent></Dialog>
<FeedbackHost/>
</TooltipProvider>
</template>
