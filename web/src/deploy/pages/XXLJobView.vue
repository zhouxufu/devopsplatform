<script setup>
import { computed, inject, nextTick, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ChevronLeftIcon, ChevronRightIcon, PlayIcon, PlusIcon, RefreshCwIcon } from '@lucide/vue'
import PageHeader from '../../shared/components/PageHeader.vue'
import OptionPicker from '../../shared/components/OptionPicker.vue'
import XXLJobFields from '../../delivery-editor/XXLJobFields.vue'
import ApplicationResourcesEditor from '../components/ApplicationResourcesEditor.vue'
import { applicationOption } from '../../shared/applicationName.js'
import { toast } from '../../shared/shadcnFeedback.js'
import { randomUUID } from '../../shared/uuid.js'
import {
  Accordion, AccordionContent, AccordionItem, AccordionTrigger, Badge, Button, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
  Field, FieldLabel, Input, Spinner, Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
  Tabs, TabsList, TabsTrigger, Textarea,
} from '../../shared/shadcn-ui.js'
import { validateDeliveryChange } from '../deliveryValidation.js'

const api = inject('api')
const openResourceCenter = inject('openResourceCenter')
const router = useRouter()
const route = useRoute()
const environments = ref([])
const applications = ref([])
const connections = ref([])
const me = ref({})
const tab = ref('tasks')
const environment = ref('')
const service = ref('')
const alias = ref('')
const bindings = ref([])
const canTrigger = ref(false)
const group = ref(null)
const taskPage = ref({ items: [], total: 0 })
const task = ref(null)
const detailPanel = ref(null)
const runs = ref({ items: [], total: 0 })
const run = ref(null)
const log = ref('')
const fromLine = ref(1)
const logEnd = ref(false)
const runOffset = ref(0)
const search = ref('')
const query = ref('')
const offset = ref(0)
const pageSize = 20
const loading = ref(false)
const detailLoading = ref(false)
const logLoading = ref(false)
const error = ref('')
const detailError = ref('')
const logError = ref('')
const modal = ref('')
const form = ref({})
const busy = ref(false)
const modalError = ref('')
const showBindings = ref(false)
const refreshingConnections = ref(false)
const savedDraft = ref(null)
let scopeRequest = 0
let tableRequest = 0
let detailRequest = 0
let logRequest = 0

const admin = computed(() => me.value.role === 'admin' || me.value.username === 'local')
const envOptions = computed(() => environments.value.map(item => ({ value: item.name, label: item.display_name || item.name })))
const connectionOptions = computed(() => connections.value.filter(item => item.kind === 'xxl_job' && item.config?.environment === environment.value)
  .map(item => ({ value: item.name, label: item.name })))
const bindOptions = computed(() => bindings.value.map(item => ({ value: item.alias, label: `${item.alias} · ${item.config?.app_name}` })))
const selectedBinding = computed(() => bindings.value.find(item => item.alias === alias.value))
const statusLabel = computed(() => group.value?.addressType === 1 ? '已配置地址' : group.value?.registryList?.length ? '有实例注册' : '等待注册')
const newJob = () => ({ action: 'create', resource_alias: alias.value, config: { scheduleType: 'CRON', author: me.value.username || '' } })
const request = (operation, parameters = {}) => api('api/xxl-job/'+operation+'?'+new URLSearchParams({ service: service.value, environment: environment.value, alias: alias.value, ...parameters }))
const post = (path, data) => api(path, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(data) })
const status = entry => !entry ? '暂无运行' : entry.triggerCode !== 200 ? (entry.triggerCode ? '派发失败' : '派发中') : !entry.handleCode ? '等待回调' : entry.handleCode === 200 ? '执行成功' : '执行失败'
const failed = entry => ['派发失败','执行失败'].includes(status(entry))
const date = value => value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '—'
const message = value => String(value || '').replace(/<br\s*\/?>/gi, '\n').replace(/<\/?(?:span|b|strong)\b[^>]*>/gi, '').trim()
const schedulerPath = name => 'api/xxl-job/connections/'+encodeURIComponent(name)+'/groups'

async function initialize() {
  try {
    const [envs, apps, centers, identity] = await Promise.all([
      api('api/environments'), api('api/services'), api('api/connections'), api('/api/me').catch(() => ({})),
    ])
    environments.value = envs || []
    applications.value = apps || []
    connections.value = centers || []
    me.value = identity
    environment.value = envs.find(item => item.name === route.query.environment)?.name || envs.find(item => item.purpose === 'development')?.name || envs[0]?.name || ''
    service.value = apps.find(item => item.name === route.query.service)?.name || ''
  } catch (err) {
    error.value = err.message
  }
}

async function loadScope() {
  const sequence = ++scopeRequest
  bindings.value = []
  group.value = null
  canTrigger.value = false
  clearTask()
  error.value = ''
  if (!service.value || !environment.value) {
    loading.value = false
    return
  }
  loading.value = true
  const previousAlias = alias.value
  try {
    const result = await request('bindings')
    if (sequence !== scopeRequest) return
    bindings.value = result.items || []
    canTrigger.value = result.can_trigger
    alias.value = bindings.value.find(item => item.alias === previousAlias)?.alias || bindings.value[0]?.alias || ''
    await loadTasks()
  } catch (err) {
    if (sequence === scopeRequest) error.value = err.message
  } finally {
    if (sequence === scopeRequest) loading.value = false
  }
}

async function loadTasks() {
  const sequence = ++tableRequest
  const scope = scopeRequest
  taskPage.value = { items: [], total: 0 }
  if (!selectedBinding.value) return
  loading.value = true
  error.value = ''
  try {
    const [executor, page] = await Promise.all([request('group'), request('tasks', { offset: offset.value, limit: pageSize, query: query.value })])
    if (scope !== scopeRequest || sequence !== tableRequest) return
    group.value = executor
    taskPage.value = page
  } catch (err) {
    if (scope === scopeRequest && sequence === tableRequest) error.value = err.message
  } finally {
    if (scope === scopeRequest && sequence === tableRequest) loading.value = false
  }
}

function clearTask() {
  detailRequest++
  logRequest++
  task.value = null
  run.value = null
  detailError.value = ''
  logError.value = ''
  detailLoading.value = false
  logLoading.value = false
}
function selectAlias(value) {
  alias.value = value
  group.value = null
  offset.value = 0
  clearTask()
  loadTasks()
}
function searchTasks() {
  offset.value = 0
  query.value = search.value.trim()
  loadTasks()
}
function selectTaskPage(delta) {
  offset.value += delta * pageSize
  loadTasks()
}
async function openTask(item, runID = '') {
  clearTask()
  const sequence = detailRequest
  task.value = item
  runOffset.value = 0
  await nextTick()
  if (sequence !== detailRequest) return
  if (detailPanel.value?.getBoundingClientRect().top >= window.innerHeight - 80) {
    detailPanel.value.scrollIntoView({ block: 'start' })
  }
  if (runID) await openRun(runID)
  else await loadRuns()
}
async function loadRuns() {
  const sequence = ++detailRequest
  const id = task.value.id
  run.value = null
  detailError.value = ''
  detailLoading.value = true
  try {
    const result = await request('runs', { task_id: id, offset: runOffset.value, limit: pageSize })
    if (sequence === detailRequest) runs.value = result
  } catch (err) {
    if (sequence === detailRequest) detailError.value = err.message
  } finally {
    if (sequence === detailRequest) detailLoading.value = false
  }
}
async function openRun(id) {
  const sequence = ++detailRequest
  logRequest++
  run.value = { id }
  log.value = ''
  fromLine.value = 1
  logEnd.value = false
  logError.value = ''
  logLoading.value = false
  detailError.value = ''
  detailLoading.value = true
  try {
    const detail = await request('run', { task_id: task.value.id, run_id: id })
    if (sequence !== detailRequest) return
    run.value = detail
    if (detail.triggerCode === 200) loadLog()
  } catch (err) {
    if (sequence === detailRequest) detailError.value = err.message
  } finally {
    if (sequence === detailRequest) detailLoading.value = false
  }
}
async function loadLog() {
  const sequence = ++logRequest
  logLoading.value = true
  logError.value = ''
  try {
    const chunk = await request('log', { task_id: task.value.id, run_id: run.value.id, from_line: fromLine.value })
    if (sequence !== logRequest) return
    log.value += chunk.logContent || ''
    fromLine.value = Math.max(fromLine.value, Number(chunk.toLineNum || 0) + 1)
    logEnd.value = !!chunk.end
  } catch (err) {
    if (sequence === logRequest) logError.value = err.message
  } finally {
    if (sequence === logRequest) logLoading.value = false
  }
}

function open(kind, item) {
  modalError.value = ''
  if (kind === 'executor') {
    form.value = { connection: selectedBinding.value?.connection || connectionOptions.value[0]?.value || '', app_name: '', title: '', manual: false, addresses: '', created: null }
  } else if (kind === 'trigger') {
    form.value = { parameters: '', item, nonce: randomUUID(), runIDs: [] }
  } else {
    const job = item ? { resource_alias: alias.value, action: 'update', target_title: item.jobDesc, target_handler: item.executorHandler, config: {} } : newJob()
    form.value = { title: item ? `${item.jobDesc} · 修改定时任务` : '', xxl_job: job, prerequisites: [] }
  }
  modal.value = kind
}
async function refreshConnections() {
  if (refreshingConnections.value) return
  refreshingConnections.value = true
  try {
    connections.value = await api('api/connections')
  } catch (err) {
    toast(err.message, 'error')
  } finally {
    refreshingConnections.value = false
  }
}
function openBindings() {
  modal.value = ''
  showBindings.value = true
}
function closeBindings() {
  showBindings.value = false
  refreshConnections()
  loadScope()
}
async function submit() {
  if (busy.value) return
  busy.value = true
  modalError.value = ''
  const current = form.value
  try {
    switch (modal.value) {
      case 'executor': {
        if (!current.connection) throw new Error('请选择调度中心')
        const addresses = current.manual ? current.addresses.split(/\s*[\n,]\s*/).map(value => value.trim()).filter(Boolean) : []
        if (current.manual && !addresses.length) throw new Error('请填写至少一个实例地址')
        current.created = await post(schedulerPath(current.connection), { environment: environment.value, app_name: current.app_name, title: current.title, addresses })
        break
      }
      case 'trigger': {
        const result = await post('api/xxl-job/trigger', { environment: environment.value, service: service.value, alias: alias.value, task_id: current.item.id, nonce: current.nonce, parameters: current.parameters })
        current.runIDs = result.run_ids
        if (current.runIDs.length === 1) {
          modal.value = ''
          await openTask(current.item, current.runIDs[0])
        }
        break
      }
      case 'draft': {
        const change = { ...current, kind: 'xxl_job', service: service.value, risk_level: 'medium', title: current.title.trim() || current.xxl_job.config?.jobDesc?.trim() || current.xxl_job.target_title }
        if (!change.title) throw new Error('请填写变更标题')
        if (change.xxl_job.created_by_change_id) change.prerequisites = [{ change_id: change.xxl_job.created_by_change_id, reason: '先创建本次要启用的任务' }]
        const problem = validateDeliveryChange(change, 'xxl-job')
        if (problem) throw new Error(problem.message)
        const [saved] = await post('api/changes/batch', { changes: [change] })
        savedDraft.value = saved
        modal.value = ''
        toast(`变更草稿 #${saved.id} 已保存`, 'success')
        break
      }
    }
  } catch (err) {
    modalError.value = err.message
  } finally {
    busy.value = false
  }
}

watch([service, environment], () => {
  offset.value = 0
  savedDraft.value = null
  loadScope()
})
onMounted(initialize)
</script>

<template>
  <section class="grid min-w-0 gap-4">
    <PageHeader title="定时任务" help="查看应用在各环境的 XXL-JOB 任务和运行记录。任务配置通过变更交付。">
      <template #actions>
        <Button v-if="tab==='tasks'" :disabled="!group || loading" @click="open('draft')"><PlusIcon/>新建任务草稿</Button>
        <Button v-else-if="admin" :disabled="!connectionOptions.length" @click="open('executor')"><PlusIcon/>新建执行器</Button>
      </template>
    </PageHeader>
    <Tabs v-model="tab" class="min-w-0">
      <TabsList aria-label="定时任务管理"><TabsTrigger value="tasks">任务与运行</TabsTrigger><TabsTrigger value="executors">执行器</TabsTrigger></TabsList>
    </Tabs>
    <div class="flex flex-wrap items-end gap-3 border-b pb-4">
      <Field class="w-44"><FieldLabel as="div">环境</FieldLabel><OptionPicker v-model="environment" :options="envOptions" placeholder="选择环境"/></Field>
      <Field class="w-56"><FieldLabel as="div">应用</FieldLabel><OptionPicker v-model="service" :options="applications.map(applicationOption)" searchable placeholder="选择应用"/></Field>
      <Field v-if="bindings.length" class="w-56"><FieldLabel as="div">执行器</FieldLabel><OptionPicker :model-value="alias" :options="bindOptions" @update:model-value="selectAlias"/></Field>
      <Button v-if="tab==='executors' && admin && service" variant="outline" @click="openBindings">应用资源绑定</Button>
    </div>
    <div v-if="savedDraft && tab==='tasks'" class="flex flex-wrap items-center gap-2 text-sm" role="status">
      <span>变更草稿 #{{ savedDraft.id }} 已保存</span><Button size="sm" variant="link" @click="router.push({path:'/changes',query:{item:savedDraft.id}})">查看变更及关联需求 / Bug</Button>
    </div>
    <div v-if="error" role="alert" class="flex flex-wrap items-center gap-3 text-sm text-destructive">
      {{ error }}<Button variant="outline" size="sm" :disabled="loading" @click="service?loadScope():initialize()">重试</Button>
    </div>
    <div v-else-if="loading && !group" class="flex items-center gap-2 py-10 text-sm text-muted-foreground" role="status"><Spinner/>正在读取执行器与任务</div>
    <div v-else-if="!service && tab==='tasks'" class="rounded-lg border px-5 py-10 text-sm text-muted-foreground">选择应用，查看该环境的任务和运行记录。</div>
    <div v-else-if="!selectedBinding && tab==='tasks'" class="rounded-lg border px-5 py-10">
      <h2 class="text-base font-medium">此应用尚未绑定执行器</h2>
      <Button v-if="admin" class="mt-3" variant="outline" @click="openBindings">应用资源绑定</Button>
    </div>
    <div v-else-if="tab==='executors'" class="grid gap-5 md:grid-cols-2">
      <section class="min-w-0 rounded-lg border">
        <div class="flex flex-wrap items-center justify-between gap-2 border-b px-4 py-3">
          <h2 class="font-semibold">本环境调度中心</h2>
          <div v-if="admin" class="flex items-center gap-1">
            <Button variant="ghost" size="sm" @click="openResourceCenter">资源中心</Button>
            <Button variant="ghost" size="icon" aria-label="刷新调度中心" :disabled="refreshingConnections" @click="refreshConnections"><Spinner v-if="refreshingConnections"/><RefreshCwIcon v-else/></Button>
          </div>
        </div>
        <div v-if="!connectionOptions.length" class="px-4 py-5 text-sm text-muted-foreground">此环境暂无调度中心连接。</div>
        <div v-for="center in connectionOptions" :key="center.value" class="flex flex-wrap items-center justify-between gap-2 border-b px-4 py-3 last:border-b-0">
          <span>{{ center.label }}</span>
        </div>
      </section>
      <section class="min-w-0 rounded-lg border">
        <div class="flex flex-wrap items-center gap-2 border-b px-4 py-3"><h2 class="font-semibold">{{ group?.title || '应用执行器' }}</h2><Badge v-if="group" variant="secondary">{{ statusLabel }}</Badge></div>
        <div v-if="group" class="grid gap-2 px-4 py-3 text-sm">
          <span>AppName <code class="ml-1">{{ group.appname }}</code></span>
          <span class="text-muted-foreground">{{ selectedBinding.connection }} · {{ group.addressType===1?'手动配置':'自动注册' }}</span>
          <code v-for="address in group.registryList" :key="address" class="break-all">{{ address }}</code>
        </div>
        <p v-else class="px-4 py-5 text-sm text-muted-foreground">{{ service?'此环境暂无执行器绑定。':'选择应用查看执行器绑定。' }}</p>
      </section>
    </div>
    <div v-else class="grid items-start gap-4" :class="task?'xl:grid-cols-[minmax(0,1.65fr)_minmax(22rem,1fr)]':''">
      <section class="min-w-0 rounded-lg border">
        <div class="flex flex-wrap items-center justify-between gap-2 border-b px-4 py-3">
          <form class="flex min-w-0 flex-1 gap-2" @submit.prevent="searchTasks">
            <Input v-model="search" placeholder="搜索任务标题或 Handler" aria-label="搜索任务标题或 Handler" class="max-w-xs"/>
            <Button type="submit" variant="outline" :disabled="loading">搜索</Button>
          </form>
          <Button variant="ghost" size="icon" aria-label="刷新任务" :disabled="loading" @click="loadTasks"><RefreshCwIcon/></Button>
        </div>
        <Table class="table-fixed sm:min-w-[34rem]">
          <TableHeader><TableRow><TableHead class="sm:w-2/5">任务 / Handler</TableHead><TableHead class="hidden sm:table-cell">调度规则</TableHead><TableHead class="hidden w-24 sm:table-cell">调度状态</TableHead><TableHead class="w-24 sm:w-28">最近运行</TableHead></TableRow></TableHeader>
          <TableBody>
            <TableRow v-for="item in taskPage.items" :key="item.id" :data-state="task?.id===item.id?'selected':undefined">
              <TableCell><button class="block max-w-full truncate text-left font-medium hover:underline" :title="item.jobDesc" @click="openTask(item)">{{ item.jobDesc }}</button><code class="block truncate text-xs text-muted-foreground" :title="item.executorHandler">{{ item.executorHandler || item.glueType }}</code><span class="block truncate text-xs text-muted-foreground sm:hidden">{{ item.triggerStatus===1?'已启用':'已停用' }} · {{ item.scheduleType==='FIX_RATE' ? `${item.scheduleConf} 秒` : item.scheduleConf || '仅手动' }}</span></TableCell>
              <TableCell class="hidden truncate tabular-nums sm:table-cell" :title="item.scheduleConf">{{ item.scheduleType==='FIX_RATE' ? `${item.scheduleConf} 秒` : item.scheduleConf || '仅手动' }}</TableCell>
              <TableCell class="hidden sm:table-cell"><Badge variant="secondary">{{ item.triggerStatus===1?'已启用':'已停用' }}</Badge></TableCell>
              <TableCell>
                <Button v-if="item.latestRun" size="sm" variant="ghost" :class="failed(item.latestRun)?'text-destructive':''" @click="openTask(item, item.latestRun.id)">{{ status(item.latestRun) }}</Button>
                <span v-else class="text-muted-foreground">暂无运行</span>
              </TableCell>
            </TableRow>
            <TableRow v-if="!taskPage.items.length"><TableCell :colspan="4" class="h-28 text-center text-muted-foreground">{{ loading?'正在读取任务…':query?'没有匹配的任务':'当前执行器暂无任务' }}</TableCell></TableRow>
          </TableBody>
        </Table>
        <div class="flex items-center justify-between border-t px-4 py-2 text-sm text-muted-foreground">
          <span>共 {{ taskPage.total }} 个任务</span>
          <div class="flex gap-1"><Button variant="ghost" size="icon" aria-label="上一页任务" :disabled="loading || offset===0" @click="selectTaskPage(-1)"><ChevronLeftIcon/></Button><Button variant="ghost" size="icon" aria-label="下一页任务" :disabled="loading || offset+pageSize>=taskPage.total" @click="selectTaskPage(1)"><ChevronRightIcon/></Button></div>
        </div>
      </section>
      <aside v-if="task" ref="detailPanel" class="min-w-0 rounded-lg border" aria-label="任务与运行详情">
        <div class="flex flex-wrap items-start justify-between gap-2 border-b px-4 py-3">
          <div class="min-w-0"><h2 class="break-words font-semibold">{{ task.jobDesc }}</h2><code class="break-all text-xs text-muted-foreground">#{{ task.id }} · {{ task.executorHandler || task.glueType }}</code></div>
          <Button size="sm" variant="ghost" @click="clearTask">关闭详情</Button>
        </div>
        <div class="grid gap-3 p-4">
          <div class="flex flex-wrap gap-2">
            <Button v-if="canTrigger" size="sm" variant="outline" @click="open('trigger',task)"><PlayIcon/>执行一次</Button>
            <Button v-if="task.glueType==='BEAN'" size="sm" variant="outline" @click="open('draft',task)">登记任务变更</Button>
          </div>
          <p v-if="detailError" class="text-sm text-destructive" role="alert">{{ detailError }} <Button size="sm" variant="link" @click="run?openRun(run.id):loadRuns()">重试</Button></p>
          <div v-else-if="detailLoading" class="flex items-center gap-2 text-sm text-muted-foreground" role="status"><Spinner/>正在读取运行记录</div>
          <template v-else-if="run">
            <div class="flex flex-wrap items-center justify-between gap-2"><Button size="sm" variant="ghost" @click="loadRuns"><ChevronLeftIcon/>运行记录</Button><Button size="sm" variant="ghost" @click="openRun(run.id)"><RefreshCwIcon/>刷新本次运行</Button></div>
            <div class="flex flex-wrap items-center gap-2"><h3 class="font-medium">运行 #{{ run.id }}</h3><Badge :variant="failed(run)?'destructive':'secondary'">{{ status(run) }}</Badge></div>
            <dl class="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 text-sm"><dt class="text-muted-foreground">触发时间</dt><dd>{{ date(run.triggerTime) }}</dd><dt class="text-muted-foreground">执行实例</dt><dd class="break-all">{{ run.executorAddress || '—' }}</dd><dt class="text-muted-foreground">回调时间</dt><dd>{{ date(run.handleTime) }}</dd></dl>
            <Accordion v-if="run.triggerMsg || run.handleMsg" :key="run.id" type="single" collapsible :default-value="failed(run)?'receipt':undefined">
              <AccordionItem value="receipt"><AccordionTrigger>派发与执行回执</AccordionTrigger><AccordionContent>
                <pre class="max-h-48 overflow-auto whitespace-pre-wrap break-all rounded-md bg-muted p-3 text-xs">{{ [message(run.triggerMsg),message(run.handleMsg)].filter(Boolean).join('\n\n') }}</pre>
              </AccordionContent></AccordionItem>
            </Accordion>
            <template v-if="run.triggerCode===200">
              <h3 class="text-sm font-medium">执行日志</h3>
              <p v-if="logError" class="text-sm text-destructive" role="alert">{{ logError }}</p>
              <pre v-if="log" class="max-h-[32rem] overflow-auto whitespace-pre-wrap break-all rounded-md bg-muted p-3 text-xs leading-5" tabindex="0">{{ log }}</pre>
              <p v-else-if="!logLoading && !logError" class="text-sm text-muted-foreground">暂无日志内容</p>
              <Button v-if="!logEnd || logError" size="sm" variant="outline" :disabled="logLoading" @click="loadLog">{{ logLoading?'正在读取…':logError?'重试读取日志':'加载后续日志' }}</Button>
            </template>
          </template>
          <template v-else>
            <h3 class="text-sm font-semibold">最近运行</h3>
            <p v-if="!runs.items.length" class="text-sm text-muted-foreground">此任务暂无运行记录。</p>
            <button v-for="entry in runs.items" :key="entry.id" class="flex flex-wrap items-center justify-between gap-2 rounded-md border px-3 py-2 text-left text-sm hover:bg-muted/60" @click="openRun(entry.id)">
              <span class="tabular-nums">#{{ entry.id }} · {{ date(entry.triggerTime) }}</span><Badge :variant="failed(entry)?'destructive':'secondary'">{{ status(entry) }}</Badge>
            </button>
            <div v-if="runs.total>pageSize" class="flex justify-end gap-2">
              <Button size="sm" variant="ghost" :disabled="runOffset===0" @click="runOffset-=pageSize;loadRuns()">上一页</Button>
              <Button size="sm" variant="ghost" :disabled="runOffset+pageSize>=runs.total" @click="runOffset+=pageSize;loadRuns()">下一页</Button>
            </div>
          </template>
        </div>
      </aside>
    </div>

    <ApplicationResourcesEditor v-if="showBindings" :show="showBindings" :services="applications" :initial-service="service" @close="closeBindings"/>
    <Dialog :open="!!modal" @update:open="value=>{if(!value&&!busy)modal=''}">
      <DialogContent v-if="modal" class="max-h-[88dvh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{{ { executor:'新建执行器',trigger:'执行一次',draft:'登记定时任务变更' }[modal] }}</DialogTitle>
          <DialogDescription>{{ modal==='draft'?`${service} · ${alias} · 需求 / Bug 归属在变更记录维护`:modal==='trigger'?`${environment} · ${form.item.jobDesc} · 本次参数不会改变周期调度`:environment }}</DialogDescription>
        </DialogHeader>
        <form id="xxl-job-form" class="grid gap-4 md:grid-cols-2" @submit.prevent="submit">
          <template v-if="modal==='executor'">
            <template v-if="form.created">
              <p class="md:col-span-2 text-sm" role="status">{{ form.created.appname }} 已创建 · {{ form.created.addressType===1?'已配置地址':'等待注册' }}</p>
            </template>
            <template v-else>
              <Field><FieldLabel as="div">调度中心</FieldLabel><OptionPicker v-model="form.connection" :options="connectionOptions" :disabled="busy"/></Field>
                <Field><FieldLabel for="xxl-appname">AppName</FieldLabel><Input id="xxl-appname" v-model="form.app_name" placeholder="与服务注册配置一致" :disabled="busy" pattern="[a-z][a-z0-9-]{3,63}" required/></Field>
                <Field><FieldLabel for="xxl-group-title">执行器名称</FieldLabel><Input id="xxl-group-title" v-model="form.title" :disabled="busy" maxlength="12" required/></Field>
                <Field><FieldLabel as="div">注册地址</FieldLabel><OptionPicker :model-value="form.manual?'manual':'auto'" :options="[{value:'auto',label:'自动注册'},{value:'manual',label:'手动配置'}]" :disabled="busy" @update:model-value="value=>form.manual=value==='manual'"/></Field>
                <Field v-if="form.manual" class="md:col-span-2"><FieldLabel for="xxl-addresses">实例地址（每行一个）</FieldLabel><Textarea id="xxl-addresses" v-model="form.addresses" :disabled="busy" required/></Field>
            </template>
          </template>
          <template v-else-if="modal==='trigger'">
            <Field v-if="!form.runIDs.length" class="md:col-span-2"><FieldLabel for="xxl-params">本次执行参数</FieldLabel><Textarea id="xxl-params" v-model="form.parameters" maxlength="512" placeholder="留空会传入空参数" :disabled="busy"/></Field>
            <div v-else class="flex flex-wrap gap-2 md:col-span-2"><Button v-for="id in form.runIDs" :key="id" size="sm" variant="outline" @click="modal='';openTask(form.item,id)">查看运行 #{{ id }}</Button></div>
          </template>
          <template v-else>
            <Field class="md:col-span-2"><FieldLabel for="xxl-draft-title">变更标题</FieldLabel><Input id="xxl-draft-title" v-model="form.title" placeholder="默认沿用任务标题" :disabled="busy"/></Field>
            <XXLJobFields :model="form.xxl_job" field-id="workbench-xxl-job" :disabled="busy"/>
          </template>
          <p v-if="modalError" role="alert" class="text-sm text-destructive md:col-span-2">{{ modalError }}</p>
        </form>
        <DialogFooter>
          <Button variant="outline" :disabled="busy" @click="modal=''">{{ form.created || form.runIDs?.length?'关闭':'取消' }}</Button>
          <Button v-if="form.created && service" :disabled="busy" @click="openBindings">应用资源绑定</Button>
          <Button v-else-if="!form.created&&!form.runIDs?.length" type="submit" form="xxl-job-form" :disabled="busy">
            <Spinner v-if="busy"/>{{ {executor:'创建执行器',trigger:'确认执行一次',draft:'保存变更草稿'}[modal] }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </section>
</template>
