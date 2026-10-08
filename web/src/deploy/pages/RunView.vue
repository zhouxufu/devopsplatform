<script setup>
import { ref, computed, onMounted, onUnmounted, nextTick, watch, inject } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useWS } from '../../shared/composables/ws.js'
import { toast } from '../../shared/composables/toast.js'
import Sel from '../../shared/components/Sel.vue'
import Modal from '../../shared/components/Modal.vue'
import { ElButton, ElInput } from '../../shared/components/ui.js'

const route = useRoute(), router = useRouter()
const globalEnvironment = inject('globalEnvironment', ref(''))
const setPlatformContext = inject('setPlatformContext', () => {})
const id = computed(() => route.params.id)
const run = ref(null), logText = ref(''), targets = ref([])
const logEl = ref(null)
const showDeployConfirm = ref(false), deploying = ref(false)
let ws = null, autoDeployRefresh = null

async function loadRun() {
  run.value = await fetch(`api/pipeline/runs/${id.value}`).then(r => r.json())
  if (run.value?.service) setPlatformContext({ service: run.value.service })
}
const statusText = computed(() => ({ running: '运行中', waiting_deploy: '等待部署确认', success: '成功', failed: '失败', interrupted: '已中断', canceled: '已放弃部署' })[run.value?.status] || run.value?.status || '…')
const statusClass = computed(() => run.value?.status === 'success' ? 'st-ok' : ['running', 'waiting_deploy'].includes(run.value?.status) ? 'st-run' : 'st-fail')
const intendedTarget = computed(() => targets.value.find(target => target.environment === run.value?.deploy_environment))
const intendedTargetLabel = computed(() => intendedTarget.value?.display_name || run.value?.deploy_environment || '')
const failureDiagnosis = computed(() => {
  if (run.value?.status !== 'failed') return null
  const text = logText.value || ''
  if (/services\.gradle\.org|GradleWrapperMain/.test(text)) return { title: 'Gradle Wrapper 下载失败', detail: 'Runner 无法访问 Gradle 官方发行站。到构建配置中将执行方式改为“构建环境内置 Gradle”，内网环境同时检查构建网络。' }
  const missing = text.match(/Could not find\s+([^\s]+:[^\s]+:[^\s]+)\.(?:\s|$)/)
  if (missing) return { title: '依赖仓库中找不到构件', detail: `${missing[1]} 未能解析。请绑定包含该构件的 Maven 私服；Gradle 项目如使用自定义属性名，可在 -P 项目属性中配置 aliyun.username=\${env.GRADLE_REPOSITORY_USERNAME} 和 aliyun.password=\${env.GRADLE_REPOSITORY_PASSWORD}。` }
  if (/Host key verification failed|knownhosts: key is unknown/.test(text)) return { title: 'Git 主机指纹未确认', detail: '到资源中心编辑 Git 凭据，获取并确认服务器指纹后重新构建。' }
  if (/Could not resolve host|UnknownHostException|Temporary failure in name resolution/.test(text)) return { title: '构建网络或 DNS 不可用', detail: '宿主机可访问但隔离容器不可访问时，在构建环境中选择“宿主机网络”；否则检查 Runner 的 DNS、代理和防火墙。' }
  if (/permission denied.*docker|docker\.sock.*permission denied/i.test(text)) return { title: 'Runner 无权访问 Docker', detail: '请为平台运行用户配置 Docker 权限，重新登录或重启服务后再试。' }
  return null
})

function scrollBottom() { nextTick(() => { if (logEl.value) logEl.value.scrollTop = logEl.value.scrollHeight }) }
function scheduleAutoDeployRefresh() {
  if (!run.value?.auto_deploy || !run.value?.deploy_environment) return
  clearTimeout(autoDeployRefresh)
  autoDeployRefresh = setTimeout(async () => {
    const latest = await fetch(`api/pipeline/runs/${id.value}/log`).then(r => r.json())
    logText.value = latest.log || ''
    await loadRun()
    scrollBottom()
  }, 1500)
}

async function connect() {
  if (ws) { ws.close(); ws = null }
  const hist = await fetch(`api/pipeline/runs/${id.value}/log`).then(r => r.json())
  logText.value = hist.log || ''
  await loadRun()
  scrollBottom()
  if (run.value?.status !== 'running') {
    // The release order is created just after the build transaction commits.
    // Refresh once so the final automatic-release message is not missed when
    // the websocket has already closed with the build.
    if (run.value?.status === 'success') scheduleAutoDeployRefresh()
    return
  }
  const base = location.pathname.replace(/\/ui.*$/, '')
  ws = useWS(`${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}${base}/api/pipeline/runs/${id.value}/tail`, {
    onMessage: d => {
      logText.value += d
      scrollBottom()
      if (d.includes('⏸') || d.includes('部署结束') || d.includes('✓ 产物')) { loadRun(); loadTargets() }
    },
    onClose: () => setTimeout(async () => {
      await loadRun()
      if (run.value?.status !== 'running') {
        if (ws) { ws.close(); ws = null }
        scheduleAutoDeployRefresh()
      }
    }, 500),
  })
}
async function loadTargets() {
  if (!run.value?.service) await loadRun()
  targets.value = (await fetch(`api/services/${run.value.service}/environments`).then(r => r.json())) ?? []
}

const deployEnv = ref(''), reason = ref('')
async function continueDeploy() {
  if (!deployEnv.value) return toast('选择环境')
  deploying.value = true
  try {
    const res = await fetch(`api/pipeline/runs/${id.value}/deploy`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ environment: deployEnv.value, reason: reason.value, confirm_protected: !!selectedTarget.value?.protected }) })
    const r = await res.json()
    if (res.ok && r.ok) {
      showDeployConfirm.value = false
      toast('部署阶段已启动', 'success')
      await connect()
    } else toast('失败: ' + (r.error || '未知错误'), 'error')
  } finally {
    deploying.value = false
  }
}
async function cancelWaiting() {
  if (!confirm('放弃本次部署？制品和流水线日志仍会保留。')) return
  const res = await fetch(`api/pipeline/runs/${id.value}/cancel`, { method: 'POST' })
  const r = await res.json()
  if (!res.ok) return toast(r.error || '取消失败', 'error')
  toast('已放弃部署')
  await connect()
}
const canDeploy = computed(() => run.value?.status === 'waiting_deploy' && run.value?.artifact && targets.value.length)
const selectedTarget = computed(() => targets.value.find(t => t.environment === deployEnv.value))
const targetReady = computed(() => selectedTarget.value?.target_type === 'aliyun-sae' ? !!(selectedTarget.value?.deploy_config?.connection && selectedTarget.value?.deploy_config?.app_id && selectedTarget.value?.deploy_config?.namespace_id && selectedTarget.value?.deploy_config?.region) : !!(selectedTarget.value?.host_group && selectedTarget.value?.hosts?.length))
const targetLabel = computed(() => selectedTarget.value?.target_type === 'aliyun-sae' ? `SAE ${selectedTarget.value.deploy_config?.app_id} · ${selectedTarget.value.deploy_config?.region}` : selectedTarget.value?.hosts?.join('、') || '未配置')
function startDeployFlow() {
  if (selectedTarget.value?.protected) {
    router.push(`/svc/${encodeURIComponent(run.value.service)}/release?artifact=${run.value.artifact_id}&environment=${encodeURIComponent(deployEnv.value)}`)
    return
  }
  showDeployConfirm.value = true
}
watch(deployEnv, value => {
  if (value) setPlatformContext({ environment: value })
  reason.value = ''
})
onMounted(async () => {
  await connect()
  await loadTargets()
  const preferred = globalEnvironment.value || sessionStorage.getItem('lp-deploy-environment') || ''
  deployEnv.value = preferred || targets.value[0]?.environment || ''
})
onUnmounted(() => {
  if (ws) ws.close()
  clearTimeout(autoDeployRefresh)
})
</script>
<template>
  <div>
    <div class="card" style="display:flex;align-items:center;gap:10px;flex-wrap:wrap">
      <span style="color:var(--brand);cursor:pointer" @click="router.back()">← 返回</span>
      <h4 style="margin:0">流水线 #{{ id }} · {{ run?.version || '' }}</h4>
      <span :class="statusClass">{{ statusText }}</span>
      <span v-if="run?.auto_deploy && run?.deploy_environment" class="deploy-intent">一键部署 → {{ intendedTargetLabel }}</span>
      <span v-if="run?.actor" style="color:var(--dim);font-size:11px">操作者 {{ run.actor }}</span>
      <code v-if="run?.build_command" :title="run.build_command" style="color:var(--dim);font-size:10px;max-width:420px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap">{{ run.build_command }}</code>
      <template v-if="canDeploy">
        <Sel v-model="deployEnv" :options="targets.map(t => ({ value: t.environment, label: (t.protected ? '🔒 ' : '') + t.display_name }))" placeholder="选择部署环境" width="170px" style="margin-left:auto" />
        <ElButton size="small" @click="cancelWaiting">放弃部署</ElButton>
        <ElButton type="primary" size="small" :disabled="!targetReady" @click="startDeployFlow">{{selectedTarget?.protected ? '提交发布单' : '部署到该环境'}}</ElButton>
      </template>
    </div>
    <div v-if="failureDiagnosis" class="failure-diagnosis"><span>!</span><div><b>{{failureDiagnosis.title}}</b><p>{{failureDiagnosis.detail}}</p></div><ElButton size="small" @click="router.push(`/svc/${encodeURIComponent(run.service)}/build`)">打开构建配置</ElButton></div>
    <div ref="logEl" :style="{height: `calc(100vh - ${failureDiagnosis ? 246 : 170}px)`}" class="run-log">{{ logText || '连接中…' }}</div>

    <Modal :show="showDeployConfirm" title="确认续接部署" @close="showDeployConfirm = false">
      <div class="fld"><label>服务 / 版本</label><code>{{ run?.service }} · {{ run?.version }}</code></div>
      <div class="fld"><label>制品</label><code style="word-break:break-all">{{ run?.artifact }}</code></div>
      <div class="fld"><label>目标环境</label><span>{{ selectedTarget?.protected ? '🔒 ' : '' }}{{ selectedTarget?.display_name || '未选择' }}</span></div>
      <div class="fld"><label>执行目标</label><span>{{ targetLabel }}</span></div>
      <div v-if="selectedTarget?.require_reason" class="fld"><label>发布原因</label><ElInput v-model="reason" type="textarea" :rows="2" placeholder="受保护环境必填" /></div>
      <div style="color:var(--warn);font-size:12px;margin-top:10px">确认后将立即开始目标部署。</div>
      <div style="display:flex;gap:10px;margin-top:14px">
        <ElButton style="margin-left:auto" @click="showDeployConfirm = false">取消</ElButton>
        <ElButton type="primary" :loading="deploying" :disabled="!deployEnv || (selectedTarget?.require_reason && !reason.trim())" @click="continueDeploy">确认并部署</ElButton>
      </div>
    </Modal>
  </div>
</template>
<style scoped>
.deploy-intent{padding:3px 8px;border:1px solid color-mix(in srgb,var(--brand) 35%,var(--border));border-radius:999px;background:color-mix(in srgb,var(--brand) 8%,var(--panel));color:var(--brand);font-size:11px}.failure-diagnosis{display:flex;align-items:center;gap:10px;margin-bottom:10px;padding:10px 12px;border:1px solid color-mix(in srgb,var(--error) 35%,var(--border));border-radius:8px;background:color-mix(in srgb,var(--error) 8%,var(--panel))}.failure-diagnosis>span{display:grid;place-items:center;flex:0 0 24px;height:24px;border-radius:50%;background:var(--error);color:#fff;font-weight:700}.failure-diagnosis>div{min-width:0;margin-right:auto}.failure-diagnosis b{font-size:12px}.failure-diagnosis p{margin-top:2px;color:var(--muted);font-size:11px;line-height:1.5}.run-log{background:#0a0d12;border:1px solid var(--border);border-radius:8px;padding:12px;font-family:var(--mono);font-size:12px;white-space:pre-wrap;word-break:break-all;overflow-y:auto;color:#c9d1d9}
</style>
