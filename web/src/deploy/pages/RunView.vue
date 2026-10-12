<script setup>
import PageHeader from '../../shared/components/PageHeader.vue'
import {buildReturn} from '../buildNavigation.js'
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
const follow=ref(true),logQuery=ref(''),selectedStage=ref(-1),capabilities=ref({}),control=ref(''),controlReason=ref(''),controlling=ref(false),controlError=ref(''),metadataTimer=ref(null),loadError=ref('')
let disposed=false,runSequence=0,connectionEpoch=0
const stageLog=computed(()=>{
 if(selectedStage.value<0)return logText.value
 const stage=run.value?.stages?.[selectedStage.value];if(!stage)return logText.value
 const marker='━━ '+stage.name+'（',start=logText.value.indexOf(marker)
 if(start<0)return '该阶段尚无日志'
 const end=logText.value.indexOf('\n━━ ',start+marker.length)
 return logText.value.slice(start,end<0?undefined:end)
})
const visibleLog=computed(()=>logQuery.value?stageLog.value.split('\n').filter(line=>line.toLowerCase().includes(logQuery.value.toLowerCase())).join('\n'):stageLog.value)
const elapsed=computed(()=>run.value?.started_at?Math.max(0,(run.value.ended_at||Math.floor(Date.now()/1000))-run.value.started_at)+'s':'—')
function back(){router.push(buildReturn(route.query.return,run.value?.service||''))}
function pauseForScroll(){if(!logEl.value)return;const el=logEl.value;if(el.scrollHeight-el.scrollTop-el.clientHeight>32)follow.value=false}
function selectLog(){if(window.getSelection()?.toString())follow.value=false}
function toggleFollow(){follow.value=!follow.value;if(follow.value)scrollBottom()}
async function copyLog(){try{await navigator.clipboard.writeText(visibleLog.value);toast('已复制当前显示日志','success')}catch{toast('浏览器未允许复制，可选中文本后手动复制','warning')}}
async function loadCapabilities(){const requested=id.value;try{const r=await fetch('api/pipeline/runs/'+requested+'/capabilities');if(r.ok){const v=await r.json();if(!disposed&&id.value===requested)capabilities.value=v}}catch{}}
async function submitControl(){if(controlling.value)return;controlling.value=true;controlError.value='';try{
 const r=await fetch('api/pipeline/runs/'+id.value+'/'+(control.value==='retry'?'retry':'stop'),{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({confirm:true,reason:controlReason.value})}),v=await r.json()
 if(!r.ok)throw Error(v.error||'操作未完成')
 const action=control.value;control.value='';controlReason.value=''
 if(action==='retry')router.replace({path:'/run/'+v.id,query:{return:buildReturn(route.query.return,run.value.service)}})
 else{toast('取消请求已提交，等待执行器停止；不会发布','success');await loadRun();await loadCapabilities()}
}catch(e){controlError.value=e.message}finally{controlling.value=false}}
function openControl(action){control.value=action;controlReason.value='';controlError.value=''}
function metadataPoll(){clearTimeout(metadataTimer.value);if(disposed||run.value?.status!=='running')return;metadataTimer.value=setTimeout(async()=>{if(disposed)return;await loadRun();await loadCapabilities();metadataPoll()},2000)}

const showDeployConfirm = ref(false), deploying = ref(false)
let ws = null, autoDeployRefresh = null

async function loadRun() {
  const requested=id.value,sequence=++runSequence
  try{
   const r=await fetch(`api/pipeline/runs/${requested}`),v=await r.json()
   if(!r.ok||!v.id)throw Error(v.error||'构建记录读取失败')
   if(disposed||requested!==id.value||sequence!==runSequence)return
   run.value=v;loadError.value=''
   if (run.value?.service) setPlatformContext({ service: run.value.service })
  }catch(e){if(!disposed&&requested===id.value&&sequence===runSequence)loadError.value=e.message}
}
const statusText = computed(() => ({ running: '运行中', waiting_deploy: '等待部署确认', success: '成功', failed: '失败', interrupted: '已中断', canceled: '已取消' })[run.value?.status] || run.value?.status || '…')
const statusClass = computed(() => run.value?.status === 'success' ? 'st-ok' : ['running', 'waiting_deploy'].includes(run.value?.status) ? 'st-run' : 'st-fail')
const intendedTarget = computed(() => targets.value.find(target => target.environment === run.value?.deploy_environment))
const intendedTargetLabel = computed(() => intendedTarget.value?.display_name || run.value?.deploy_environment || '')
const failureDiagnosis = computed(() => {
  if (run.value?.status !== 'failed') return null
  const text = logText.value || ''
  if (/services\.gradle\.org/.test(text)&&/UnknownHostException|Download|failed|timeout/i.test(text)) return { title: 'Gradle Wrapper 下载失败', detail: 'Runner 无法访问 Gradle 官方发行站。到构建配置中将执行方式改为“构建环境内置 Gradle”，内网环境同时检查构建网络。' }
  const missing = text.match(/Could not find\s+([^\s]+:[^\s]+:[^\s]+)\.(?:\s|$)/)
  if (missing) return { title: '依赖仓库中找不到构件', detail: `${missing[1]} 未能解析。请绑定包含该构件的 Maven 私服；Gradle 项目如使用自定义属性名，可在 -P 项目属性中配置 aliyun.username=\${env.GRADLE_REPOSITORY_USERNAME} 和 aliyun.password=\${env.GRADLE_REPOSITORY_PASSWORD}。` }
  if (/Permission denied \(publickey\)/i.test(text)) return {title:'Git SSH 私钥授权失败',detail:'检查所选 Git 凭据对应公钥是否有该仓库读取权限。'}
  if (/UnsupportedClassVersionError|Unsupported class file major version|不兼容/.test(text)) return {title:'构建工具链不兼容',detail:'核对构建 JDK 与 Maven / Gradle Wrapper 版本；部署运行 JDK 与构建 JDK 独立。'}
  if (/Host key verification failed|knownhosts: key is unknown/.test(text)) return { title: 'Git 主机指纹未确认', detail: '到资源中心编辑 Git 凭据，获取并确认服务器指纹后重新构建。' }
  if (/Could not resolve host|UnknownHostException|Temporary failure in name resolution/.test(text)) return { title: '构建网络或 DNS 不可用', detail: '宿主机可访问但隔离容器不可访问时，在构建环境中选择“宿主机网络”；否则检查 Runner 的 DNS、代理和防火墙。' }
  if (/permission denied.*docker|docker\.sock.*permission denied/i.test(text)) return { title: 'Runner 无权访问 Docker', detail: '请为平台运行用户配置 Docker 权限，重新登录或重启服务后再试。' }
  return null
})

function scrollBottom() {if(follow.value) nextTick(() => { if (logEl.value) logEl.value.scrollTop = logEl.value.scrollHeight }) }
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
  try { await connectLog() } catch(e) {if(!disposed)loadError.value='日志连接失败，请重试读取'}
}
async function connectLog() {
  const epoch=++connectionEpoch,requested=id.value
  if (ws) { ws.close(); ws = null }
  const hist = await fetch(`api/pipeline/runs/${id.value}/log`).then(r => r.json())
  if(disposed||epoch!==connectionEpoch||requested!==id.value)return
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
      if(disposed||epoch!==connectionEpoch)return
      logText.value += d
      scrollBottom()
      if (d.includes('⏸') || d.includes('部署结束') || d.includes('✓ 产物')) { loadRun(); loadTargets() }
    },
    onClose: () => setTimeout(async () => {
      if(disposed||epoch!==connectionEpoch)return
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
  if(!run.value?.service)return
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
  await loadCapabilities();metadataPoll()
  const preferred = globalEnvironment.value || sessionStorage.getItem('lp-deploy-environment') || ''
  deployEnv.value = preferred || targets.value[0]?.environment || ''
})
onUnmounted(() => {
  disposed=true;clearTimeout(metadataTimer.value)
  if (ws) ws.close()
  clearTimeout(autoDeployRefresh)
})
watch(id,async()=>{run.value=null;targets.value=[];logText.value='';loadError.value='';follow.value=true;selectedStage.value=-1;logQuery.value='';capabilities.value={};await connect();await loadCapabilities();metadataPoll()})
</script>
<template>
  <div>
    <PageHeader>
 <template #title><button class="op-btn" @click="back" aria-label="返回构建记录">←</button><h2>构建 #{{id}}</h2><span :class="statusClass">{{statusText}}</span></template>
 <template #actions><ElButton v-if="run?.artifact_id" type="primary" size="small" @click="router.push('/svc/'+encodeURIComponent(run.service)+'/release?artifact='+run.artifact_id)">发布本次制品</ElButton><ElButton v-if="capabilities.can_retry" size="small" @click="openControl('retry')">按原源码与配置重试</ElButton><ElButton v-if="!capabilities.can_retry&&run&&['failed','success','canceled','interrupted'].includes(run.status)" size="small" :title="capabilities.retry_unavailable" @click="router.push('/svc/'+encodeURIComponent(run.service)+'/build')">按当前配置新建构建</ElButton><ElButton v-if="capabilities.can_stop" size="small" type="danger" @click="openControl('stop')">取消构建</ElButton></template>
 </PageHeader>
 <p v-if="loadError" role="alert">{{loadError}} <ElButton size="small" @click="loadRun">重试读取</ElButton></p>
 <div class="run-context"><b>{{run?.service}}</b><span>分支 {{run?.branch||'默认'}}</span><span>Commit {{capabilities.commit||run?.version||'尚未解析'}}</span><span>操作者 {{run?.actor||'—'}}</span><span>耗时 {{elapsed}}</span><span v-if="run?.auto_deploy" class="deploy-intent">构建并发布 → {{intendedTargetLabel}}</span></div>
 <div v-if="canDeploy" class="legacy-deploy"><Sel v-model="deployEnv" :options="targets.map(t=>({value:t.environment,label:t.display_name}))" placeholder="选择环境"/><ElButton size="small" @click="cancelWaiting">放弃部署</ElButton><ElButton type="primary" size="small" :disabled="!targetReady" @click="startDeployFlow">{{selectedTarget?.protected?'提交发布单':'部署到该环境'}}</ElButton></div>
 <nav class="run-stage-tabs" aria-label="日志阶段"><button :class="{active:selectedStage===-1}" @click="selectedStage=-1">全部日志</button><button v-for="(s,i) in run?.stages||[]" :key="i" :class="{active:selectedStage===i}" @click="selectedStage=i">{{s.name}} · {{({success:'成功',failed:'失败',running:'执行中',canceled:'已取消',pending:'待执行'})[s.status]||s.status||'待执行'}}</button></nav>
    <div v-if="failureDiagnosis" class="failure-diagnosis"><span>!</span><div><b>{{failureDiagnosis.title}}</b><p>{{failureDiagnosis.detail}}</p></div><ElButton size="small" @click="router.push(`/svc/${encodeURIComponent(run.service)}/build`)">打开构建配置</ElButton></div>
    <div class="run-log-tools"><ElInput v-model="logQuery" clearable placeholder="搜索当前阶段日志" aria-label="搜索构建日志"/><span v-if="logQuery">{{visibleLog.split('\n').filter(Boolean).length}} 行命中</span><ElButton size="small" :type="follow?'primary':'default'" @click="toggleFollow">{{follow?'暂停跟随':'继续跟随'}}</ElButton><ElButton size="small" @click="copyLog">复制日志</ElButton></div>
 <div ref="logEl" class="run-log" @scroll="pauseForScroll" @mouseup="selectLog">{{visibleLog||(logQuery?'没有匹配日志':'正在连接日志…')}}</div>
 <Modal :show="!!control" :busy="controlling" :title="control==='retry'?'确认重试构建':'确认取消构建'" @close="control=''"><div class="run-control"><p>{{control==='retry'?'使用原 Commit、原构建配置与 Runner；凭据和变量重新授权，步骤实现与依赖按当前版本执行。只生成制品，不自动发布。':'仅停止当前构建的进程和容器，不停止部署实例；等待执行器退出后释放运行锁。'}}</p><code v-if="control==='retry'">{{capabilities.commit}}</code><label>操作原因<ElInput v-model="controlReason" :disabled="controlling"/></label><p v-if="controlError" role="alert">{{controlError}}</p><ElButton type="primary" :loading="controlling" :disabled="!controlReason.trim()" @click="submitControl">确认{{control==='retry'?'重试':'取消'}}</ElButton></div></Modal>

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
<style scoped>
.run-context,.legacy-deploy,.run-stage-tabs,.run-log-tools{display:flex;align-items:center;gap:10px;flex-wrap:wrap;margin-bottom:12px;font-size:12px}.run-context{color:var(--muted)}.run-context b{color:var(--text)}.run-stage-tabs button{border:1px solid var(--border);border-radius:6px;padding:6px 10px;color:var(--muted);background:var(--panel);cursor:pointer}.run-stage-tabs button.active{background:var(--brand-soft);color:var(--brand)}.run-log-tools>.el-input{max-width:330px}.run-log{height:calc(100dvh - 245px);min-height:240px}.run-control{display:grid;gap:12px}.run-control label{display:grid;gap:6px}.run-control p{font-size:12px;color:var(--muted);margin:0}.run-control code{overflow-wrap:anywhere}
</style>
