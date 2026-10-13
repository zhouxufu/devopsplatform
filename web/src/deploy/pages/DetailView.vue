<script setup>
import BuildRunDialog from '../components/BuildRunDialog.vue'
import BuildRecordsTable from '../components/BuildRecordsTable.vue'
import {buildScroll} from '../buildNavigation.js'
import { onBeforeRouteLeave, onBeforeRouteUpdate } from 'vue-router'
import PageHeader from "../../shared/components/PageHeader.vue"
import HelpTip from '../../shared/components/HelpTip.vue'
import BuildEnvironmentEditor from '../components/BuildEnvironmentEditor.vue'
import GitBranchSelect from '../components/GitBranchSelect.vue'
import ApplicationResourcesEditor from '../components/ApplicationResourcesEditor.vue'
import { ref, computed, onUnmounted, watch, inject } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import ReleasePolicyEditor from '../components/ReleasePolicyEditor.vue'
import Stages from '../../shared/components/Stages.vue'
import { toast } from '../../shared/composables/toast.js'
import Sel from '../../shared/components/Sel.vue'
import Modal from '../../shared/components/Modal.vue'
import { ElButton, ElInput, ElInputNumber } from '../../shared/components/ui.js'

const route = useRoute(), router = useRouter()
const globalEnvironment = inject('globalEnvironment', ref(''))
const setPlatformContext = inject('setPlatformContext', () => {})
const name = computed(() => route.params.name)
const showAppResources=ref(false)
if (name.value) setPlatformContext({ service: String(name.value) })
const tab = computed(() => ({ hist: 'build', dep: 'release', cfg: 'advanced',resources:'settings' }[route.params.tab] || route.params.tab || 'overview'))
const settingsSection=ref('source'),settingsReturn=ref(route.query.settings_return==='/'?'/':'build'),launchMode=ref('build'),launchEnvironment=ref(''),showPublishForm=ref(false),me=ref({})
const detailScroll=buildScroll('detail:'+name.value)
fetch('api/me').then(r=>r.json()).then(v=>me.value=v).catch(()=>{})
const runs = ref([]), latest = ref(null)
const builds = ref([]), artifacts = ref([]), deploys = ref([]), releaseOrders = ref([]), environments = ref([]), targets = ref([])
const delivery = ref({ runner: 'local', variable_groups: [] }), connections = ref([]), variableGroups = ref([]), runners = ref([]), hostGroups = ref([])
const savedBuildConfiguration = ref(''), deliveryLoaded = ref(false), savingBuildConfiguration = ref(false)
const showBuildConfiguration = ref(false), buildConfigurationBackup = ref(null)
const svc = ref(null), stepDefs = ref([]), steps = ref([])
const buildTagsText = ref('')
const newStep = ref('')
const savingSteps=ref(false),advancedNoticeShown=ref(false)
const artifactID = ref(''), deployEnv = ref(''), reason = ref('')
const showDeployConfirm = ref(false), deploying = ref(false)
const showDeployLog = ref(false), deployLog = ref('')
const showTarget = ref(false), targetForm = ref({}),targetSection=ref('basic'),savingTarget=ref(false)
const showReleaseDetail = ref(false), activeRelease = ref(null)
const showInstances = ref(false), instanceTarget = ref(null), instances = ref([]), instancesLoading = ref(false)
const pendingInstanceAction = ref(null), instanceReason = ref(''), instanceOperating = ref(false)
const showOneClickDeploy = ref(false), oneClickDeploying = ref(false)
const showBuildRun = ref(false), runBranch = ref(''), startingBuild = ref(false)
const oneClickForm = ref({ environment: '', branch: '', reason: '' })
let timer = null, deployTimer = null

const statusText = s => ({ queued:'排队中',pending_approval:'待审批',waiting_next_batch:'等待下一批',running: '运行中', waiting_deploy: '等待部署', success: '成功', failed: '失败', interrupted: '已中断', canceled: '已取消', rejected:'已拒绝',rolling_back:'回滚中','rollback-failed':'回滚失败', 'rolled-back': '已回滚' })[s] || s
const statusIcon = s => s === 'success' ? '✓' : s === 'running' ? '●' : s === 'waiting_deploy' ? '⏸' : '✗'
const statusClass = s => ['success'].includes(s) ? 'st-ok' : ['running','queued','pending_approval','waiting_deploy','waiting_next_batch','rolling_back'].includes(s) ? 'st-run' : 'st-fail'
const selectedArtifact = computed(() => artifacts.value.find(a => String(a.id) === String(artifactID.value)))
const selectedVersion = computed(() => selectedArtifact.value?.version || '')
const selectedTarget = computed(() => targets.value.find(t => t.environment === deployEnv.value))
const oneClickTarget = computed(() => targets.value.find(t => t.environment === oneClickForm.value.environment))
const isManagedTarget = t => t?.target_type === 'aliyun-sae'
const isManagedJavaTarget = t => t?.target_type === 'ssh-jar' && t?.deploy_config?.java_mode === 'managed'
const targetReady = t => isManagedTarget(t) ? !!(t?.deploy_config?.connection && t?.deploy_config?.app_id && t?.deploy_config?.namespace_id && t?.deploy_config?.region) : !!(t?.host_group && t?.hosts?.length)
const releasePolicyText=t=>t?.deploy_config?.release_mode==='canary'?`金丝雀 · 首批 ${t.deploy_config.canary_count||1} 实例 · 观察 ${t.deploy_config.observe_seconds??300}s · ${t.target_type==='aliyun-sae'?'剩余 '+(t.deploy_config.batch_count||2)+' 批（人工确认）':'后续每批 '+t.batch_size+' 台'}`:t?.target_type==='aliyun-sae'?t.deploy_config?.release_mode==='batch'?`SAE 分 ${t.deploy_config.batch_count||2} 批 · ${t.deploy_config.batch_mode==='auto'?'自动':'人工确认'}`:'SAE 原有发布策略':`分批 · 每批 ${t?.batch_size||1} 台 / 并发 ${t?.max_parallel||1}${t?.pause_between_batches?' / 批次暂停':''}`
const targetResource = t => isManagedTarget(t) ? `SAE ${t.deploy_config?.app_id || '未配置'} · ${t.deploy_config?.region || '未知地域'}` : t?.host_group ? `${t.host_group} · ${t.hosts?.length || 0} 台主机` : '未绑定环境主机组'
const saeConnections = computed(() => connections.value.filter(c => c.kind === 'aliyun-sae'))
const activeDeploy = computed(() => deploys.value.find(d => d.status === 'running' && d.environment === deployEnv.value))
const activeOrder = computed(() => releaseOrders.value.find(d => ['pending_approval','queued','running','waiting_next_batch','rolling_back'].includes(d.status) && d.environment === deployEnv.value))
const globalEnvironmentMissing = computed(() => !!globalEnvironment.value && !targets.value.some(t => t.environment === globalEnvironment.value))
const environmentTargetsLoaded=ref(false),notifiedEnvironmentContexts=new Set()
let environmentNotice
function environmentTargetsReady(){environmentTargetsLoaded.value=true}
watch(()=>environmentTargetsLoaded.value&&globalEnvironmentMissing.value?`${name.value}:${globalEnvironment.value}`:'',key=>{
  environmentNotice?.close();environmentNotice=null
  if(key&&!notifiedEnvironmentContexts.has(key)){
    notifiedEnvironmentContexts.add(key)
    environmentNotice=toast(`当前环境 ${globalEnvironment.value} 尚未配置该应用的发布目标，可在“环境”中配置。`,'warning',{duration:6000})
  }
})
onUnmounted(()=>environmentNotice?.close())
const typeMeta = computed(() => ({
  jar: { icon: '☕', label: 'Java JAR' },
  static: { icon: '🌐', label: '前端静态站点' },
  image: { icon: '🐳', label: 'Docker 镜像' },
})[svc.value?.artifact_type] || { icon: '📦', label: svc.value?.artifact_type || '应用' })
const latestArtifact = computed(() => artifacts.value[0])
const artifactOptions = computed(() => artifacts.value.map(a => ({
  value: String(a.id),
  label: `${a.version} · 制品#${a.id} · ${a.source}#${a.source_id || '—'} · ${(a.digest || '').replace('sha256:', '').slice(0, 10)} · ${sizeText(a.size)}`,
})))
const conflictingVersions = computed(() => {
  const digests = new Map()
  for (const a of artifacts.value) {
    const values = digests.get(a.version) || new Set()
    values.add(a.digest || a.uri)
    digests.set(a.version, values)
  }
  return new Set([...digests].filter(([, values]) => values.size > 1).map(([version]) => version))
})
const buildStepDefs = computed(() => stepDefs.value.filter(step => step.kind !== 'deploy'))
const envState = computed(() => Object.fromEntries(targets.value.map(target => {
  const records = deploys.value.filter(d => d.environment === target.environment)
  const current = records.find(d => d.status === 'success')
  const latestRecord = records[0]
  return [target.environment, { target, current, latest: latestRecord }]
})))
const recentReleases = computed(() => deploys.value.slice(0, 5))
const currentEnvironment = computed(() => environments.value.find(e => e.name === deployEnv.value))
const focusedDeployState = computed(() => envState.value[deployEnv.value] || {})
const deliveryFlow = computed(() => {
  const release = focusedDeployState.value.latest
  const current = focusedDeployState.value.current
  return [
    { id: 'build', label: '构建', value: latest.value ? `#${latest.value.id} · ${statusText(latest.value.status)}` : '等待首次构建', state: latest.value?.status === 'success' ? 'done' : latest.value?.status === 'running' ? 'active' : latest.value ? 'error' : 'todo' },
    { id: 'artifact', label: '制品', value: latestArtifact.value ? `${latestArtifact.value.version} · #${latestArtifact.value.id}` : '构建成功后生成', state: latestArtifact.value ? 'done' : 'todo' },
    { id: 'release', label: currentEnvironment.value?.display_name || deployEnv.value || '选择环境', value: release ? `${release.version} · ${statusText(release.status)}` : selectedTarget.value ? '尚未发布' : '尚未配置发布目标', state: release?.status === 'success' ? 'done' : ['running','queued','pending_approval','waiting_next_batch'].includes(release?.status) ? 'active' : release?.status ? 'error' : targetReady(selectedTarget.value) ? 'ready' : 'todo' },
    { id: 'observe', label: '运行观测', value: current ? `${current.version} · 日志与指标` : '发布成功后查看', state: current ? 'ready' : 'todo' },
  ]
})

async function loadRun() {
  const requested=name.value
  const result=(await fetch(`api/pipeline/runs?service=${requested}&limit=30`).then(r => r.json()))??[]
  if(requested!==name.value)return
  runs.value=result
  latest.value = runs.value[0]
  if (runs.value.some(r => r.status === 'running') && ['overview', 'build'].includes(tab.value)) {
    clearTimeout(timer); timer = setTimeout(loadRun, 3000)
  }
}
async function loadDeployData() {
  const requested=name.value
  const [buildData, artifactData, services, environmentData, targetData, groupData, connectionData] = await Promise.all([
    fetch(`api/builds?service=${name.value}&limit=50`).then(r => r.json()),
    fetch(`api/artifacts?service=${name.value}&limit=100`).then(r => r.json()),
    fetch('api/services').then(r => r.json()),
    fetch('api/environments').then(r => r.json()),
    fetch(`api/services/${name.value}/environments`).then(r => r.json()),
    fetch('api/host-groups').then(r => r.json()),
    fetch('api/connections').then(r => r.json()),
  ])
  if(requested!==name.value)return
  builds.value = (buildData ?? []).filter(b => b.status === 'success')
  artifacts.value = artifactData ?? []
  svc.value = (services ?? []).find(x => x.name === name.value)
  environments.value = environmentData ?? []
  targets.value = targetData ?? []
  environmentTargetsReady()
  hostGroups.value = groupData ?? []
  connections.value = connectionData ?? []

  const requestedArtifact = String(route.query.artifact || '')
  if(requestedArtifact){
 let selected=artifacts.value.find(a=>String(a.id)===requestedArtifact)
 if(!selected){const r=await fetch('api/artifacts/'+encodeURIComponent(requestedArtifact));if(r.ok){selected=await r.json();if(selected.service===name.value)artifacts.value.unshift(selected)}}
 if(selected?.service===name.value){artifactID.value=requestedArtifact;showPublishForm.value=true}
}
  if (targets.value.length && !deployEnv.value) {
    const preferred = String(route.query.environment || globalEnvironment.value || sessionStorage.getItem('lp-deploy-environment') || '')
    // 全局环境即使尚未配置也要保留，避免悄悄切到另一个环境造成误发布。
    deployEnv.value = preferred || targets.value[0].environment
  }
}
async function loadDelivery() {
  const requestedName=name.value
  deliveryLoaded.value = false
  const [d, c, v, r, es, serviceTargets] = await Promise.all([
    fetch(`api/services/${name.value}/delivery`).then(x => x.json()), fetch('api/connections').then(x => x.json()),
    fetch('api/variable-groups').then(x => x.json()), fetch('api/runners').then(x => x.json()), fetch('api/environments').then(x => x.json()),
    fetch(`api/services/${name.value}/environments`).then(x => x.json()),
  ])
  if(requestedName!==name.value)return
  delivery.value = { runner:'local', variable_groups:[], ...d }
  deliveryLoaded.value = true
  connections.value = c ?? []; variableGroups.value = v ?? []; runners.value = r ?? []; environments.value = es ?? []; targets.value = serviceTargets ?? []
  environmentTargetsReady()
  rememberBuildConfiguration()
}
async function loadCfg() {
  const requestedName=name.value
  const all = (await fetch('api/services').then(r => r.json())) ?? []
  const definitions=(await fetch('api/steps').then(r=>r.json()))??[]
  if(requestedName!==name.value)return
  svc.value = all.find(x => x.name === requestedName)
  buildTagsText.value = (svc.value?.tags || []).join(', ')
  stepDefs.value = definitions
  steps.value = JSON.parse(JSON.stringify(svc.value?.steps ?? []))
  rememberBuildConfiguration()
}
function setTab(t){
 if(savingBuildConfiguration.value||savingTarget.value||savingSteps.value)return toast('正在保存，请稍候','warning')
 if(tab.value==='advanced'&&advancedStepsDirty.value&&t!=='advanced'){if(!confirm('流水线有未保存修改，放弃修改并离开？'))return;steps.value=JSON.parse(JSON.stringify(svc.value?.steps||[]))}
 if(showTarget.value){if(!confirm('环境配置尚未保存，放弃修改并离开？'))return;showTarget.value=false}
 if(tab.value==='settings'&&buildConfigurationDirty.value&&t!=='settings'){
  if(!confirm('有未保存配置，放弃修改并离开？'))return
  if(buildConfigurationBackup.value){svc.value=buildConfigurationBackup.value.service;delivery.value=buildConfigurationBackup.value.delivery;buildTagsText.value=buildConfigurationBackup.value.tags}
  rememberBuildConfiguration()
 }
 detailScroll.remember();router.replace({path:'/svc/'+encodeURIComponent(name.value)+'/'+t,query:route.query})
}
onBeforeRouteLeave(()=>{if(savingSteps.value||savingBuildConfiguration.value||savingTarget.value)return false;if(((tab.value==='settings'&&buildConfigurationDirty.value)||(tab.value==='advanced'&&advancedStepsDirty.value))&&!confirm('有未保存配置，放弃修改并离开？'))return false})
onBeforeRouteUpdate(to=>{if(savingSteps.value||savingBuildConfiguration.value||savingTarget.value)return false;if((to.params.name!==name.value||to.params.tab!=='settings')&&tab.value==='settings'&&buildConfigurationDirty.value&&!confirm('有未保存配置，放弃修改并离开？'))return false;if((to.params.name!==name.value||to.params.tab!=='advanced')&&tab.value==='advanced'&&advancedStepsDirty.value&&!confirm('流水线有未保存修改，放弃修改并离开？'))return false})
function unloadBuild(event){if((tab.value==='settings'&&buildConfigurationDirty.value)||(tab.value==='advanced'&&advancedStepsDirty.value)){event.preventDefault();event.returnValue=''}}
window.addEventListener('beforeunload',unloadBuild)
onUnmounted(()=>window.removeEventListener('beforeunload',unloadBuild))
function loadTab(t) {
  clearTimeout(timer); clearTimeout(deployTimer)
  if (t === 'overview') { loadRun(); loadDeployData(); loadDeployHist(); loadReleaseOrders();loadDelivery() }
  if(t==='build'){loadRun();loadCfg();loadDelivery()}
 if((t==='settings'||t==='resources')&&!buildConfigurationBackup.value){loadCfg();loadDelivery()}
  if (t === 'artifact') {loadDeployData();loadDelivery()}
  if (t === 'environment') { loadDeployData(); loadDeployHist(); loadReleaseOrders();loadDelivery() }
  if (t === 'release') { loadDeployData(); loadDeployHist(); loadReleaseOrders(); loadDelivery() }
  if (t === 'advanced') {loadCfg();if(!advancedNoticeShown.value){advancedNoticeShown.value=true;toast('高级流水线仅编排构建步骤；部署使用发布单。保存配置不会执行步骤。','info',{duration:4500})}}
}
watch(tab, loadTab, { immediate: true })
watch(name, value => {
  environmentTargetsLoaded.value=false
  buildConfigurationBackup.value=null;savedBuildConfiguration.value='';deliveryLoaded.value=false;svc.value=null
  delivery.value={runner:'local',variable_groups:[]};targets.value=[];artifacts.value=[];runs.value=[];latest.value=null;showPublishForm.value=false
  artifactID.value = ''; deployEnv.value = ''
  if (value) setPlatformContext({ service: String(value) })
  loadTab(tab.value)
})
watch(deployEnv, value => {
  if (value) setPlatformContext({ environment: value })
  reason.value = ''
})
watch(globalEnvironment, value => {
  if (value && targets.value.some(t => t.environment === value)) deployEnv.value = value
})
onUnmounted(() => { clearTimeout(timer); clearTimeout(deployTimer) })

function openWorkspace(plugin, hash) {
  setPlatformContext({ service: name.value, environment: deployEnv.value || '' })
  if (window.parent !== window) {
    window.parent.postMessage({ type: 'workspace.open', plugin, hash }, location.origin)
    return
  }
  location.href = `/api/plugins/${plugin}/ui${hash}`
}
function openFlowStep(step) {
  if (step.id === 'observe') return openWorkspace('monitor', '#/')
  setTab(step.id)
}
function openSecondary(t, event) {
  event?.currentTarget?.closest('details')?.removeAttribute('open')
  setTab(t)
}

function currentBuildConfiguration() {
  if (!svc.value || !deliveryLoaded.value) return ''
  return JSON.stringify({ service: svc.value, tags: buildTagsText.value, delivery: delivery.value })
}
function rememberBuildConfiguration() {
  if (svc.value && deliveryLoaded.value) {
   savedBuildConfiguration.value=currentBuildConfiguration()
   if(tab.value==='settings'&&!buildConfigurationBackup.value)buildConfigurationBackup.value=JSON.parse(JSON.stringify({service:svc.value,delivery:delivery.value,tags:buildTagsText.value}))
  }
}
const buildConfigurationDirty = computed(() => !!savedBuildConfiguration.value && currentBuildConfiguration() !== savedBuildConfiguration.value)
const advancedStepsDirty=computed(()=>!!svc.value&&JSON.stringify(steps.value)!==JSON.stringify(svc.value.steps||[]))
const repositoryLabel = computed(() => {
  const url = String(svc.value?.git_url || '').replace(/\/$/, '')
  return url ? url.split(/[/:]/).pop()?.replace(/\.git$/, '') || url : (svc.value?.workdir || '未配置代码来源')
})
const buildCommandSummary = computed(() => {
  if (!svc.value) return '加载中'
  if (svc.value.artifact_type === 'static') return svc.value.build_cmd || '未配置构建命令'
  if (svc.value.artifact_type === 'image') return `Docker build → ${svc.value.image_name || '未配置镜像名'}`
  return svc.value.build_tool === 'maven'
    ? `${svc.value.maven_executable || 'auto'} ${svc.value.maven_goals || 'clean package'}`
    : `${svc.value.gradle_executable || 'auto'} ${svc.value.gradle_tasks || 'clean build'}`
})
const buildEnvironmentSummary = computed(() => {
  const environment = delivery.value?.build_environment || { mode: 'local' }
  return environment.mode === 'container'
    ? `隔离容器 · ${environment.preset === 'custom' ? (environment.image || '自定义镜像') : (environment.preset || '未选择工具环境')} · ${environment.network === 'host' ? '宿主机网络' : '默认网络'}`
    : 'Runner 本机工具'
})
const buildResourceSummary = computed(() => {
  const values = []
  if (delivery.value?.git_connection) values.push(`Git：${delivery.value.git_connection}`)
  if (delivery.value?.maven_connection) values.push(`依赖库：${delivery.value.maven_connection}`)
  if (delivery.value?.registry_connection) values.push(`镜像库：${delivery.value.registry_connection}`)
  if (delivery.value?.variable_groups?.length) values.push(`变量组：${delivery.value.variable_groups.length} 个`)
  return values.join(' · ') || '未引用凭据或变量组'
})
function openBuildConfiguration() {
  if (!svc.value || !deliveryLoaded.value) return toast('构建配置加载中，请稍候', 'warning')
  rememberBuildConfiguration()
  buildConfigurationBackup.value = JSON.parse(JSON.stringify({ service: svc.value, delivery: delivery.value, tags: buildTagsText.value }))
  settingsReturn.value=tab.value==='settings'?'build':tab.value
 settingsSection.value='source';showBuildConfiguration.value=true;setTab('settings')
}
function closeBuildConfiguration() {
  if (buildConfigurationBackup.value) {
    svc.value = buildConfigurationBackup.value.service
    delivery.value = buildConfigurationBackup.value.delivery
    buildTagsText.value = buildConfigurationBackup.value.tags
  }
  buildConfigurationBackup.value=null;showBuildConfiguration.value=false;rememberBuildConfiguration();exitSettings()
}
function exitSettings(){if(settingsReturn.value==='/')router.push('/');else setTab(settingsReturn.value)}
async function commitBuildConfiguration() {
  if (!(await saveBuildConfiguration(true))) return
  buildConfigurationBackup.value = null
  showBuildConfiguration.value=false;exitSettings()
}
async function openAdvancedBuildConfiguration() {
  if(buildConfigurationDirty.value)return toast('先保存配置，再进入高级流水线；当前修改仍保留。','warning')
  buildConfigurationBackup.value = null
  showBuildConfiguration.value = false
  setTab('advanced')
}
function setExecutableMode(tool, mode) {
  const field = tool === 'maven' ? 'maven_executable' : 'gradle_executable'
  svc.value[field] = mode === 'custom' ? '' : mode
}
function buildValidationError() {
  if (!svc.value?.git_url?.trim() && !svc.value?.workdir?.trim()) return '请填写 Git 仓库或本地工作目录'
  if (svc.value?.artifact_type === 'jar' && svc.value?.build_tool === 'maven' && (!svc.value.maven_executable?.trim() || !svc.value.maven_goals?.trim())) return '请填写 Maven 执行方式和 Goals'
  if (svc.value?.artifact_type === 'jar' && svc.value?.build_tool === 'gradle' && (!svc.value.gradle_executable?.trim() || !svc.value.gradle_tasks?.trim())) return '请填写 Gradle 执行方式和任务'
  if (svc.value?.artifact_type === 'image' && !svc.value.image_name?.trim()) return '请填写镜像名称'
  if (delivery.value.build_environment?.mode === 'container' && delivery.value.build_environment?.preset === 'custom' && !delivery.value.build_environment?.image?.trim()) return '请填写自定义构建镜像'
  return ''
}
function selectBuildTool(tool) {
  const oldTool = svc.value?.build_tool
  svc.value.build_tool = tool
  if (tool === 'gradle') {
    svc.value.gradle_executable ||= 'auto'
    svc.value.gradle_tasks ||= 'clean build'
    if (!svc.value.artifact_glob || oldTool === 'maven' && svc.value.artifact_glob === 'target/*.jar') svc.value.artifact_glob = 'build/libs/*.jar'
  } else {
    svc.value.maven_executable ||= 'auto'
    svc.value.maven_goals ||= 'clean package'
    if (!svc.value.artifact_glob || oldTool === 'gradle' && svc.value.artifact_glob === 'build/libs/*.jar') svc.value.artifact_glob = 'target/*.jar'
  }
}
async function saveBuildConfiguration(showSuccess = true) {
  if (!svc.value || !deliveryLoaded.value) { toast('构建配置加载中，请稍候', 'warning'); return false }
  const validationError = buildValidationError()
  if (validationError) { toast(validationError, 'warning'); return false }
  savingBuildConfiguration.value = true
  try {
    const body = { ...svc.value, tags: [...new Set(buildTagsText.value.split(/[,，\n]/).map(value => value.trim()).filter(Boolean))].slice(0, 12) }
    const serviceResponse = await fetch('api/services', { method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify(body) })
    const serviceResult = await serviceResponse.json().catch(() => ({}))
    if (!serviceResponse.ok) { toast(serviceResult.error || '应用构建配置保存失败', 'error'); return false }
    const deliveryResponse = await fetch(`api/services/${name.value}/delivery`, { method:'PUT', headers:{'Content-Type':'application/json'}, body:JSON.stringify(delivery.value) })
    const deliveryResult = await deliveryResponse.json().catch(() => ({}))
    if (!deliveryResponse.ok) { toast('应用配置已保存，但构建资源保存失败：'+(deliveryResult.error || '请重试保存，当前草稿仍保留'), 'error'); return false }
    svc.value = { ...body, ...(serviceResult?.name ? serviceResult : {}) }
    delivery.value = deliveryResult
    buildTagsText.value = (svc.value.tags || []).join(', ')
    rememberBuildConfiguration()
    if (showSuccess) toast('构建配置已保存', 'success')
    return true
  } catch(e) {
    toast('保存未完成，请检查连接后重试；当前草稿仍保留。','error');return false
  } finally {
    savingBuildConfiguration.value = false
  }
}
async function runPipeline() {
  if(tab.value==='settings'&&buildConfigurationDirty.value)return toast('请先保存配置，或返回构建页使用已保存配置。','warning')
 if(!svc.value)return toast('应用配置尚在读取','warning')
 launchMode.value='build';showBuildRun.value=true
}
async function startBuildRun() {
  startingBuild.value = true
  try {
    const res = await fetch('api/pipeline/runs', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ service: name.value, branch: runBranch.value.trim() }) })
    const r = await res.json()
    if (res.ok && r.id) { showBuildRun.value = false; router.push(`/run/${r.id}`) }
    else toast(r.error || '流水线发起失败', 'error')
  } catch(e) { toast(e.message, 'error') } finally { startingBuild.value = false }
}
function openOneClickDeploy(target=null){
 if(!targets.value.length)return toast('请先配置部署环境','warning')
 launchMode.value='deploy';launchEnvironment.value=target?.environment||selectedTarget.value?.environment||'';showBuildRun.value=true
}
async function startOneClickDeploy() {
  const target = oneClickTarget.value
  if (!targetReady(target)) return toast('目标环境的发布资源尚未配置完整', 'warning')
  if (target?.require_reason && !oneClickForm.value.reason.trim()) return toast('该环境要求填写发布原因', 'warning')
  oneClickDeploying.value = true
  try {
    const res = await fetch('api/pipeline/runs', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({
      service: name.value,
      branch: oneClickForm.value.branch.trim(),
      deploy_environment: oneClickForm.value.environment,
      reason: oneClickForm.value.reason.trim(),
    }) })
    const result = await res.json().catch(() => ({}))
    if (!res.ok || !result.id) return toast(result.error || '一键部署发起失败', 'error')
    deployEnv.value = oneClickForm.value.environment
    setPlatformContext({ environment: deployEnv.value })
    showOneClickDeploy.value = false
    toast(target.protected ? '构建已开始，成功后自动提交审批' : '一键部署已开始', 'success')
    router.push(`/run/${result.id}`)
  } finally { oneClickDeploying.value = false }
}
function requestDeploy() {
  if (!artifactID.value || !deployEnv.value) return toast('选择具体制品与目标环境')
  if (!targetReady(selectedTarget.value)) return toast('该应用环境尚未配置完整的发布资源', 'warning')
  if (activeDeploy.value) return toast(`该环境已有部署 #${activeDeploy.value.id} 正在运行`, 'warning')
  if (activeOrder.value) return toast(`该环境已有发布单 #${activeOrder.value.id} 正在处理`, 'warning')
  showDeployConfirm.value = true
}
function releaseTo(target) {
 showPublishForm.value=true;
  if (!artifacts.value.length) return toast('请先完成一次成功构建', 'warning')
  deployEnv.value = target.environment
  setTab('release')
}
const fmtTime = value => value ? new Date(value * 1000).toLocaleString('zh-CN', { hour12: false }) : '—'
async function deploy() {
  deploying.value = true
  try {
    const res = await fetch('api/releases', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ service: name.value, artifact_id: Number(artifactID.value), environment: deployEnv.value, reason: reason.value }) })
    const r = await res.json()
    if (!res.ok || !r.id) return toast('发起失败: ' + (r.error || '未知错误'), 'error')
    showDeployConfirm.value = false
    toast(r.status === 'pending_approval' ? `发布单 #${r.id} 已提交审批` : `发布单 #${r.id} 已创建`, 'success')
    await Promise.all([loadDeployHist(), loadReleaseOrders()])
  } finally {
    deploying.value = false
  }
}

async function loadReleaseOrders() {
  releaseOrders.value = (await fetch(`api/releases?service=${name.value}&limit=100`).then(r => r.json())) ?? []
  if (releaseOrders.value.some(r => ['queued','running','rolling_back'].includes(r.status))) {
    clearTimeout(deployTimer); deployTimer = setTimeout(() => { loadReleaseOrders(); loadDeployHist() }, 2500)
  }
}
async function releaseAction(order, action) {
  let body = {}
  if (action === 'rollback') {
    const value = prompt('填写整单回滚原因：')
    if (value === null) return
    body = { reason: value }
  }
  const res = await fetch(`api/releases/${order.id}/${action}`, { method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify(body) })
  const data = await res.json().catch(() => ({}))
  if (!res.ok) return toast(data.error || '操作失败', 'error')
  toast('操作已提交', 'success'); loadReleaseOrders(); loadDeployHist()
}
function openRelease(order) { activeRelease.value=order;showReleaseDetail.value=true }
const deployOf = id => deploys.value.find(d=>d.id===id)
function publishArtifact(a) {
 showPublishForm.value=true;artifactID.value=String(a.id)
 if(!artifacts.value.some(v=>v.id===a.id))artifacts.value.unshift(a)
 router.replace({path:'/svc/'+encodeURIComponent(name.value)+'/release',query:{...route.query,artifact:String(a.id)}})
}
function toggleDeliveryGroup(group) { const list=delivery.value.variable_groups??=[];const i=list.indexOf(group);i>=0?list.splice(i,1):list.push(group) }
async function saveDelivery() {
  if (!deliveryLoaded.value) return toast('交付配置加载中，请稍候', 'warning')
  const res=await fetch(`api/services/${name.value}/delivery`,{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify(delivery.value)})
  const data=await res.json().catch(()=>({}));if(!res.ok)return toast(data.error||'保存失败','error');delivery.value=data;rememberBuildConfiguration();toast('构建资源已保存','success')
}
const sizeText = n => !n ? '—' : n < 1024 ? `${n} B` : n < 1048576 ? `${(n/1024).toFixed(1)} KB` : `${(n/1048576).toFixed(1)} MB`
const webhookURL = computed(() => `${location.origin}${location.pathname.replace(/\/ui\/?$/, '')}/api/hooks/${encodeURIComponent(name.value)}/${delivery.value.webhook_token || ''}`)
function openPlatformResources() {
  if (window.parent !== window) window.parent.postMessage({ type:'platform.open', view:'resources' }, location.origin)
}
function editTarget(target) {
  const available = environments.value.find(e => e.name === globalEnvironment.value && !targets.value.some(t => t.environment === e.name)) || environments.value.find(e => !targets.value.some(t => t.environment === e.name))
  const defaultTarget = svc.value?.artifact_type === 'static' ? 'ssh-static' : svc.value?.artifact_type === 'image' ? 'docker-host' : 'ssh-jar'
  const base = target ? { ...target, deploy_config: target.deploy_config || {}, _existing: true } : { environment: available?.name || environments.value[0]?.name || '', _existing: false, target_type: defaultTarget, remote_dir: '', start_cmd: '', stop_cmd: '', health_url: '', deploy_config: {}, strategy: 'rolling', max_parallel: 1, batch_size: 1, pause_between_batches: false, failure_threshold: 0, host_group: '', hosts: [] }
	targetForm.value = { ...JSON.parse(JSON.stringify(base)), runtime_env_text: '', runtime_env_clear: false }
	if (!target && defaultTarget === 'ssh-jar') targetForm.value.deploy_config = { java_mode: 'managed', java_version: '21', java_jvm_options: '-Xms256m\n-Xmx1024m\n-Dfile.encoding=UTF-8', health_failure_action: 'rollback' }
	if (targetForm.value.deploy_config?.java_mode === 'managed') targetForm.value.deploy_config.health_failure_action ||= 'rollback'
  targetSection.value='basic';showTarget.value = true
}
function updateTargetEnvironment(value) {
  targetForm.value.environment = value
  targetForm.value.host_group = ''
  if (environments.value.find(environment => environment.name === value)?.protected && targetForm.value.deploy_config) targetForm.value.deploy_config.health_failure_action = 'rollback'
}
function updateJavaMode(value) {
  targetForm.value.deploy_config.java_mode = value
  targetForm.value.deploy_config.java_version ||= '21'
  if (value === 'managed') targetForm.value.deploy_config.health_failure_action ||= 'rollback'
}

const instanceStateText = state => ({ running:'运行中', stopped:'已停止', 'not-deployed':'未发布', unreachable:'无法连接', unknown:'状态异常' })[state] || state
const instanceStateClass = state => state === 'running' ? 'st-ok' : state === 'stopped' || state === 'not-deployed' ? 'st-run' : 'st-fail'
async function loadInstances() {
  if (!instanceTarget.value) return
  instancesLoading.value = true
  try {
    const res = await fetch(`api/services/${encodeURIComponent(name.value)}/environments/${encodeURIComponent(instanceTarget.value.environment)}/instances`)
    const data = await res.json().catch(() => ([]))
    if (!res.ok) return toast(data.error || '读取实例状态失败', 'error')
    instances.value = data ?? []
  } finally { instancesLoading.value = false }
}
async function openInstances(target) {
  instanceTarget.value = target
  instances.value = []
  pendingInstanceAction.value = null
  instanceReason.value = ''
  showInstances.value = true
  await loadInstances()
}
function prepareInstanceAction(instance, action) {
  if (action === 'ready') return runInstanceAction(instance, action)
  pendingInstanceAction.value = { instance, action }
  instanceReason.value = ''
}
async function runInstanceAction(instance = pendingInstanceAction.value?.instance, action = pendingInstanceAction.value?.action) {
  if (!instance || !action || !instanceTarget.value) return
  if ((instanceTarget.value.protected || instanceTarget.value.require_reason) && !instanceReason.value.trim()) return toast('请填写操作原因', 'warning')
  instanceOperating.value = true
  try {
    const res = await fetch(`api/services/${encodeURIComponent(name.value)}/environments/${encodeURIComponent(instanceTarget.value.environment)}/instances/${encodeURIComponent(instance.host)}/${action}`, {
      method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify({ reason:instanceReason.value.trim(), confirm_protected:!!instanceTarget.value.protected }),
    })
    const data = await res.json().catch(() => ({}))
    if (!res.ok) return toast(data.error || '实例操作失败', 'error')
    toast({start:'实例已启动并通过就绪检查',stop:'实例已停止',restart:'实例已重启并通过就绪检查',ready:'实例就绪检查通过'}[action] || '操作完成', 'success')
    pendingInstanceAction.value = null
    instanceReason.value = ''
    await loadInstances()
  } finally { instanceOperating.value = false }
}
function parseRuntimeEnvironment(raw) {
  const values = {}
  for (const [index, original] of String(raw || '').split(/\r?\n/).entries()) {
    const line = original.trim()
    if (!line || line.startsWith('#')) continue
    const separator = original.indexOf('=')
    if (separator < 1) throw new Error(`第 ${index + 1} 行应为 KEY=value`)
    const key = original.slice(0, separator).trim()
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(key)) throw new Error(`第 ${index + 1} 行变量名格式错误：${key}`)
    values[key] = original.slice(separator + 1)
  }
  return values
}
async function saveTarget(){if(savingTarget.value)return;savingTarget.value=true;try{await saveTargetConfiguration()}catch(e){toast('保存未完成，当前环境配置草稿仍保留。','error')}finally{savingTarget.value=false}}
async function saveTargetConfiguration() {
  const f = JSON.parse(JSON.stringify(targetForm.value))
  try {
    if (f.runtime_env_clear) f.runtime_env = {}
    else if (f.runtime_env_text?.trim()) f.runtime_env = parseRuntimeEnvironment(f.runtime_env_text)
    else delete f.runtime_env
  } catch (error) {
    return toast(error.message, 'warning')
  }
  delete f.runtime_env_text
  delete f.runtime_env_clear
  const group = hostGroups.value.find(g => g.name === f.host_group && g.environment === f.environment)
  f.hosts = f.target_type === 'aliyun-sae' ? [] : group?.hosts || []
  if (f.target_type === 'docker-host' && !f.remote_dir) f.remote_dir = '/'
  if (f.target_type === 'aliyun-sae') {
    f.host_group = ''; f.remote_dir = ''; f.start_cmd = ''; f.stop_cmd = ''; f.batch_size = 1; f.max_parallel = 1; f.failure_threshold = 0; f.pause_between_batches = false
    if (!f.environment || !f.deploy_config?.connection || !f.deploy_config?.app_id || !f.deploy_config?.namespace_id) return toast('请配置 SAE 连接、应用 ID 和命名空间 ID', 'warning')
    const connection = saeConnections.value.find(c => c.name === f.deploy_config.connection)
    f.deploy_config.region ||= connection?.config?.region || ''
    if (!f.deploy_config.region) return toast('SAE 地域不能为空', 'warning')
  } else if (!f.environment || !f.remote_dir || !f.host_group || !f.hosts.length) return toast('请选择该环境下非空的主机组', 'warning')
  const res = await fetch(`api/services/${name.value}/environments/${f.environment}`, { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(f) })
  const body = await res.json()
  if (!res.ok) return toast(body.error || '保存失败', 'error')
  showTarget.value = false; toast('环境部署配置已保存', 'success'); loadDeployData()
}
async function deleteTarget(target) {
  if (!confirm(`移除「${target.display_name}」的服务部署配置？历史部署记录不会删除。`)) return
  const res = await fetch(`api/services/${name.value}/environments/${target.environment}`, { method: 'DELETE' })
  const body = await res.json()
  if (!res.ok) return toast(body.error || '移除失败', 'error')
  if (deployEnv.value === target.environment) deployEnv.value = ''
  toast('环境部署配置已移除', 'success')
  loadDeployData()
}
async function loadDeployHist() {
  deploys.value = (await fetch(`api/deploys?service=${name.value}&limit=500`).then(r => r.json())) ?? []
  clearTimeout(deployTimer)
  if (['overview', 'environment', 'release'].includes(tab.value) && deploys.value.some(d => d.status === 'running')) deployTimer = setTimeout(loadDeployHist, 3000)
}
async function openDeployLog(d) {
  const r = await fetch(`api/deploys/${d.id}/log`).then(x => x.json())
  deployLog.value = r.log || '暂无日志'
  showDeployLog.value = true
}
const defOf = id => stepDefs.value.find(d => d.id === id)
function setParam(i, k, v) {if(savingSteps.value)return; (steps.value[i].params ??= {})[k] = v }
function move(i, d) {if(savingSteps.value)return; const j = i + d; if (j < 0 || j >= steps.value.length) return; [steps.value[i], steps.value[j]] = [steps.value[j], steps.value[i]] }
function removeBuildStep(i){if(!savingSteps.value)steps.value.splice(i,1)}
function addBuildStep(){if(savingSteps.value)return;const id=newStep.value||buildStepDefs.value[0]?.id,def=defOf(id);if(!def)return toast('请选择构建步骤','warning');steps.value.push({step:id,params:Object.fromEntries((def.params||[]).map(p=>[p.name,p.default??'']))});newStep.value=''}
async function saveSteps() {
  if(savingSteps.value||!svc.value)return
  if(steps.value.some(s=>s.step==='shell-script'&&!String(s.params?.SCRIPT??defOf(s.step)?.params.find(p=>p.name==='SCRIPT')?.default??'').trim()))return toast('脚本内容不能为空','warning')
  savingSteps.value=true
  try{
    const body = { ...svc.value, steps: JSON.parse(JSON.stringify(steps.value)) }
    const r = await fetch('api/services', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
    const data=await r.json().catch(()=>({}))
    if(!r.ok)return toast(data.error||'流水线保存失败','error')
    svc.value.steps=body.steps;toast('流水线已保存；未执行任何步骤','success')
  }catch{toast('保存失败，请重试；当前脚本仍保留','error')}finally{savingSteps.value=false}
}
const CAT_COLOR = { 源码: 'var(--info)', 构建: 'var(--brand)', 打包: 'var(--warn)', 部署: 'var(--error)' }
</script>
<template>
  <div>
    <PageHeader>
      <template #title><button class="back op-btn" @click="router.push('/')" aria-label="返回基础构建">←</button><h2 class="app-name" :title="String(name)">{{ name }}</h2><HelpTip :text="typeMeta.label + ' · ' + (svc?.git_url || '未连接代码仓库')" /></template>
      <template #navigation><nav class="app-tabs" aria-label="应用交付视图">
      <button v-for="t in [['overview','概览'],['build','构建'],['artifact','制品'],['environment','环境'],['release','发布单']]" :key="t[0]" :class="{ active: tab === t[0] }" @click="setTab(t[0])">{{ t[1] }}</button>
    </nav></template>
      <template #actions>
 <Sel v-if="['overview','environment','release'].includes(tab)&&targets.length&&!showTarget" class="header-environment" aria-label="发布环境" v-model="deployEnv" :options="targets.map(t=>({value:t.environment,label:t.display_name||t.environment}))" placeholder="环境" width="130px"/>
 <ElButton v-if="tab==='build'||tab==='overview'" type="primary" size="small" @click="runPipeline">开始构建</ElButton>
 <ElButton v-if="tab==='artifact'" size="small" @click="runPipeline">构建新制品</ElButton>
 <ElButton v-if="tab==='release'" type="primary" size="small" @click="showPublishForm=true">发起发布</ElButton>
 <ElButton v-if="tab==='environment'" type="primary" size="small" @click="showPublishForm=true;setTab('release')">发起发布</ElButton>
 <details class="app-more"><summary>更多</summary><div><button @click="openOneClickDeploy()">构建并发布</button><button @click="openBuildConfiguration">应用设置</button><button @click="settingsSection='bindings';setTab('settings')">资源绑定</button></div></details>
 </template>
    </PageHeader>

    <section v-if="tab==='resources'" class="section"><h3>应用资源配置</h3><p>声明应用使用的逻辑资源，并按环境绑定实际连接。日常构建和发布无需重复配置。</p><ElButton @click="showAppResources=true">配置应用资源</ElButton></section>
    <ApplicationResourcesEditor :show="showAppResources" :services="[{name:String(name)}]" :initial-service="String(name)" @close="showAppResources=false" />
    <!-- 应用概览：环境是主视图，构建与发布是动作。 -->
    <div v-show="!showTarget" v-if="tab === 'overview'">
      <div class="overview-strip compact-overview">
        <div><small>最新构建运行</small><b>#{{ latest?.id || '—' }} · {{ latest?.version || '尚未运行' }}</b><span :class="statusClass(latest?.status)">{{ latest ? statusText(latest.status) : '等待首次构建' }}</span></div>
        <div><small>最新可发布制品</small><b>{{ latestArtifact?.version || '尚无制品' }}</b><span>{{ latestArtifact ? `制品 #${latestArtifact.id} · ${(latestArtifact.digest || '').slice(0, 18)}…` : '成功构建后生成' }}</span></div>
        <div><small>已配置环境</small><b>{{ targets.length }}</b><span>{{ targets.filter(t => t.protected).length }} 个受保护环境</span></div>
      </div>
      <div v-if="latestArtifact&&targets.length" class="overview-next"><span>选择环境发布已有制品；实际运行状态需检测确认</span><ElButton size="small" @click="publishArtifact(latestArtifact)">发布已有制品</ElButton><ElButton v-if="deployEnv" size="small" @click="openWorkspace('logs','#/search')">查看日志</ElButton><ElButton v-if="deployEnv" size="small" @click="openWorkspace('monitor','#/')">查看监控</ElButton></div>
      <div class="section-head"><div><h3>环境</h3><p>同一份不可变制品逐级发布到测试、预发和生产</p></div><ElButton size="small" @click="editTarget()">＋ 配置环境</ElButton></div>
      <div class="env-grid">
        <article v-for="t in targets" :key="t.environment" :class="['env-card',{focused:deployEnv===t.environment}]" @click="deployEnv=t.environment">
          <div class="env-title"><span :class="['env-dot', envState[t.environment]?.current ? 'ok' : '']"></span><div><h4>{{ t.protected ? '🔒 ' : '' }}{{ t.display_name }}</h4><small>{{ targetResource(t) }} · {{ isManagedTarget(t) ? 'SAE 托管发布' : (t.strategy === 'rolling' ? '滚动发布' : t.strategy) }}</small></div><span v-if="envState[t.environment]?.latest" :class="statusClass(envState[t.environment].latest.status)">{{ statusText(envState[t.environment].latest.status) }}</span></div>
          <div class="current-version"><small>最近成功发布版本 · 运行状态未检测</small><code>{{ envState[t.environment]?.current?.version || '未发布' }}</code><span>{{ fmtTime(envState[t.environment]?.current?.ended_at) }}</span></div>
          <div class="env-actions" @click.stop><ElButton type="primary" size="small" :disabled="!targetReady(t) || envState[t.environment]?.latest?.status === 'running'" @click="releaseTo(t)">{{ !targetReady(t) ? '先配置资源' : envState[t.environment]?.latest?.status === 'running' ? '发布中' : '发布新版本' }}</ElButton><ElButton size="small" @click="editTarget(t)">配置</ElButton></div>
        </article>
        <div v-if="!targets.length" class="add-env"><span>尚未配置部署环境</span><small>使用右上角“配置环境”绑定发布资源</small></div>
      </div>
      <div v-if="releaseOrders.length" class="section-head compact"><div><h3>最近发布单</h3></div><button class="text-link op-btn" @click="setTab('release')">查看全部 →</button></div>
      <div v-if="releaseOrders.length" class="table-scroll"><table>
        <thead><tr><th>#</th><th>版本</th><th>环境</th><th>进度</th><th>状态</th><th>时间</th></tr></thead>
        <tbody><tr v-for="d in releaseOrders.slice(0,5)" :key="d.id"><td>#{{d.id}}</td><td><code>{{ d.version }}</code></td><td>{{ d.environment }}</td><td>{{d.succeeded}} / {{d.total}}</td><td><span :class="statusClass(d.status)">{{ statusText(d.status) }}</span></td><td class="dim">{{ fmtTime(d.started_at) }}</td></tr></tbody>
      </table></div>
    </div>

    <section v-if="tab==='artifact'"><BuildRecordsTable :key="'artifacts:'+name" :service="String(name)" kind="artifacts" @publish="publishArtifact"/></section>

    <!-- 构建配置与运行记录：应用级构建只保留这一个入口。 -->
    <div v-if="tab === 'build'">
      <div class="build-config-compact"><span>{{repositoryLabel}} · {{svc?.branch||'仓库默认分支'}} · {{svc?.build_tool||typeMeta.label}} · {{buildEnvironmentSummary}}</span><ElButton size="small" @click="openBuildConfiguration">编辑配置</ElButton></div>
 <BuildRecordsTable :key="'runs:'+name" :service="String(name)" kind="runs"/>
 </div>
 <section v-if="tab==='settings'||tab==='resources'" class="settings-workspace" :data-section="settingsSection">
 <nav aria-label="应用设置分组"><button v-for="s in [['source','代码来源'],['command','构建与制品'],['environment','工具链与 Runner'],['resource','凭据与变量'],['trigger','触发策略'],['bindings','应用资源绑定'],['advanced','高级流水线']]" :key="s[0]" :class="{active:settingsSection===s[0]}" @click="settingsSection=s[0]">{{s[1]}}</button></nav>
 <main><section v-if="settingsSection==='bindings'" class="settings-bindings"><h3>应用资源绑定</h3><p>按环境绑定实际连接；不影响当前构建配置草稿。</p><ElButton @click="showAppResources=true">配置应用资源</ElButton></section>

      <fieldset :disabled="savingBuildConfiguration" class="settings-editor-body"><div v-if="svc" class="build-config-grid">
        <section class="settings-card build-source-card">
          <div class="card-title"><span>1</span><div><h4>代码来源</h4><small>仓库、默认分支与应用标签</small></div></div>
          <div class="fld"><label>Git 仓库</label><ElInput v-model="svc.git_url" placeholder="https://codeup.aliyun.com/组织/项目/仓库.git" /></div>
          <div class="fld"><label>默认分支</label><GitBranchSelect v-model="svc.branch" :repositories="[{service:name,git_url:svc.git_url||'',connection:delivery.git_connection||''}]" /></div><div class="fld"><label>应用标签</label><ElInput v-model="buildTagsText" placeholder="核心、AI、Java" /></div>
          <small class="config-help">切换 HTTPS/SSH 地址时，请在下方选择相同认证方式的 Git 凭据。</small>
        </section>

        <section class="settings-card build-command-card">
          <div class="card-title"><span>2</span><div><h4>构建与制品</h4><small>可执行文件、任务与产物位置</small></div></div>
          <template v-if="svc.artifact_type === 'jar'">
            <div class="fld"><label>构建工具</label><div class="build-tool-tabs"><button :class="{active:svc.build_tool==='maven'}" @click="selectBuildTool('maven')"><b>Maven</b><small>pom.xml / mvnw / mvn</small></button><button :class="{active:svc.build_tool==='gradle'}" @click="selectBuildTool('gradle')"><b>Gradle</b><small>build.gradle / gradlew / gradle</small></button></div></div>
            <div v-if="svc.build_tool==='maven'" class="two"><div class="fld"><label>Maven 执行方式</label><Sel :model-value="['auto','./mvnw','mvn'].includes(svc.maven_executable) ? svc.maven_executable : 'custom'" :options="[{value:'auto',label:'自动（优先项目 mvnw）'},{value:'./mvnw',label:'项目 Maven Wrapper'},{value:'mvn',label:'构建环境内置 Maven（内网推荐）'},{value:'custom',label:'自定义可执行路径'}]" width="100%" @update:model-value="v=>setExecutableMode('maven',v)" /><ElInput v-if="!['auto','./mvnw','mvn'].includes(svc.maven_executable)" v-model="svc.maven_executable" placeholder="/opt/maven/bin/mvn" /></div><div class="fld"><label>Goals</label><ElInput v-model="svc.maven_goals" placeholder="clean package" /></div></div>
            <div v-else class="two"><div class="fld"><label>Gradle 执行方式</label><Sel :model-value="['auto','./gradlew','gradle'].includes(svc.gradle_executable) ? svc.gradle_executable : 'custom'" :options="[{value:'auto',label:'自动（优先项目 gradlew）'},{value:'./gradlew',label:'项目 Gradle Wrapper'},{value:'gradle',label:'构建环境内置 Gradle（内网推荐）'},{value:'custom',label:'自定义可执行路径'}]" width="100%" @update:model-value="v=>setExecutableMode('gradle',v)" /><ElInput v-if="!['auto','./gradlew','gradle'].includes(svc.gradle_executable)" v-model="svc.gradle_executable" placeholder="/opt/gradle/bin/gradle" /><small class="config-help">Wrapper 可能访问 services.gradle.org 下载 Gradle；无公网的 Runner 建议选择构建环境内置 Gradle。</small></div><div class="fld"><label>Tasks</label><ElInput v-model="svc.gradle_tasks" placeholder="clean build" /></div></div>
            <div class="fld"><label>JAR 产物位置</label><ElInput v-model="svc.artifact_glob" :placeholder="svc.build_tool==='gradle'?'build/libs/*.jar':'target/*.jar'" /></div>
            <details class="inline-advanced"><summary>构建工具高级参数</summary>
              <template v-if="svc.build_tool==='maven'"><div class="fld"><label>Maven Profiles</label><ElInput v-model="svc.maven_profiles" placeholder="ci,release" /></div><div class="fld"><label>settings.xml（可选）</label><ElInput v-model="svc.maven_settings_path" placeholder="留空使用依赖仓库连接" /></div><div class="fld"><label>-D 属性（每行 key=value）</label><ElInput v-model="svc.maven_properties" type="textarea" :rows="3" /></div></template>
              <template v-else><div class="fld"><label>排除任务</label><ElInput v-model="svc.gradle_excluded_tasks" placeholder="test integrationTest" /></div><div class="fld"><label>-P 项目属性（每行 key=value）</label><ElInput v-model="svc.gradle_project_properties" type="textarea" :rows="3" /></div><div class="fld"><label>-D 系统属性（每行 key=value）</label><ElInput v-model="svc.gradle_system_properties" type="textarea" :rows="3" /></div></template>
            </details>
          </template>
          <template v-else-if="svc.artifact_type === 'static'"><div class="fld"><label>构建命令</label><ElInput v-model="svc.build_cmd" type="textarea" :rows="2" placeholder="npm ci && npm run build" /></div><div class="fld"><label>输出目录</label><ElInput v-model="svc.artifact_glob" placeholder="dist" /></div></template>
          <template v-else><div class="fld"><label>镜像名称</label><ElInput v-model="svc.image_name" placeholder="registry.example.com/team/app" /></div><small class="config-help">使用 Docker buildx 构建并记录不可变 digest。</small></template>
        </section>

        <section class="settings-card build-environment-card">
          <div class="card-title"><span>3</span><div><h4>工具链与 Runner</h4><small>选择任务执行位置及隔离的编译环境</small></div></div>
          <div class="fld"><label>构建 Runner</label><Sel v-model="delivery.runner" :options="runners.filter(r=>r.enabled).map(r=>({value:r.name,label:`${r.name} · ${r.labels||r.kind}`}))" width="100%" /></div>
          <BuildEnvironmentEditor v-model="delivery.build_environment" :build-tool="svc.build_tool" :artifact-type="svc.artifact_type" />
        </section>

        <section class="settings-card build-resource-card">
          <div class="card-title"><span>4</span><div><h4>凭据与构建变量</h4><small>只引用资源中心已加密保存的连接</small></div><button class="text-link resource-link" @click="openPlatformResources">管理资源 ↗</button></div>
          <div v-if="svc.git_url" class="fld"><label>Git 代码凭据</label><Sel v-model="delivery.git_connection" :options="[{value:'',label:'公开仓库 / 使用 Runner 凭据'},...connections.filter(c=>c.kind==='git').map(c=>({value:c.name,label:`${c.name} · ${c.config?.auth_mode==='ssh'?'SSH':'HTTPS'}`}))]" width="100%" /></div>
          <div v-if="['maven','gradle'].includes(svc.build_tool)" class="fld"><label>依赖仓库</label><Sel v-model="delivery.maven_connection" :options="[{value:'',label:'不使用私服'},...connections.filter(c=>c.kind==='maven').map(c=>({value:c.name,label:c.name}))]" width="100%" /></div>
          <div v-if="svc.artifact_type==='image'" class="fld"><label>镜像仓库</label><Sel v-model="delivery.registry_connection" :options="[{value:'',label:'使用 Runner 的 Docker 登录'},...connections.filter(c=>c.kind==='docker').map(c=>({value:c.name,label:c.name}))]" width="100%" /></div>
          <div class="fld"><label>构建变量组</label><div class="check-list"><label v-for="g in variableGroups" :key="g.name"><input type="checkbox" :checked="delivery.variable_groups?.includes(g.name)" @change="toggleDeliveryGroup(g.name)"/> {{g.name}} <small>{{g.keys.join(', ')}}</small></label><span v-if="!variableGroups.length" class="empty-inline">暂无变量组，可从右上角进入资源中心创建</span></div></div>
        </section>

        <section class="settings-card build-trigger-card">
          <div class="card-title"><span>5</span><div><h4>触发策略</h4><small>手动、Webhook 或定时生成同一类不可变制品</small></div></div>
          <div class="fld"><label>Webhook URL</label><ElInput :model-value="webhookURL" readonly /></div>
          <div class="two"><div class="fld"><label>定时构建间隔（分钟）</label><ElInputNumber v-model="delivery.schedule_minutes" :min="0" :max="10080" /></div><div class="fld"><label>自动构建成功后</label><Sel v-model="delivery.auto_deploy_environment" :options="[{value:'',label:'只生成制品'},...targets.map(t=>({value:t.environment,label:`提交到 ${t.display_name}${t.protected?'（仍需审批）':''}`}))]" width="100%" /></div></div>
        </section>

        <section class="settings-card build-advanced-card">
          <div class="card-title"><span>⚙</span><div><h4>高级流水线</h4><small>只在需要自定义步骤顺序或参数时使用</small></div></div>
          <p>日常构建不需要进入这里。流水线只处理源码、检查、构建和制品，不混入主机发布参数。</p>
          <ElButton size="small" @click="openAdvancedBuildConfiguration">编辑高级流水线 →</ElButton>
        </section>
      </div>
      </fieldset><div class="build-config-actions"><span v-if="buildConfigurationDirty" class="unsaved-dot">● 有未保存修改</span><ElButton :disabled="savingBuildConfiguration" @click="closeBuildConfiguration">取消</ElButton><ElButton type="primary" :loading="savingBuildConfiguration" @click="commitBuildConfiguration">保存配置</ElButton></div>
 </main></section>

    <!-- 环境配置 -->
    <div v-show="!showTarget" v-if="tab === 'environment'">
      <div class="section-head"><div><h3>部署环境</h3><p>环境保存运行资源和发布策略，不保存构建参数</p></div><ElButton type="primary" size="small" @click="editTarget()">＋ 配置环境</ElButton></div>
      <div class="env-grid">
        <article v-for="t in targets" :key="t.environment" class="env-card manage">
          <div class="env-title"><span :class="['env-dot', envState[t.environment]?.current ? 'ok' : '']"></span><div><h4>{{ t.protected ? '🔒 ' : '' }}{{ t.display_name }}</h4><small>{{ t.environment }}</small></div></div>
          <dl class="env-meta"><div><dt>当前版本</dt><dd>{{ envState[t.environment]?.current?.version || '未发布' }}</dd></div><div><dt>环境资源</dt><dd>{{ targetResource(t) }}</dd></div><div><dt>运行变量</dt><dd>{{t.runtime_env_keys?.length ? `${t.runtime_env_keys.length} 项（已加密）` : '未配置'}}</dd></div><div><dt>发布策略</dt><dd>{{releasePolicyText(t)}}</dd></div><div v-if="isManagedJavaTarget(t)"><dt>检查失败</dt><dd>{{({rollback:'自动恢复旧版本',stop:'停止新版本',keep:'保留新版本运行'})[t.deploy_config?.health_failure_action||'rollback']}}</dd></div><div><dt>{{isManagedTarget(t)?'命名空间':'部署目录'}}</dt><dd><code>{{ isManagedTarget(t)?t.deploy_config?.namespace_id:t.remote_dir }}</code></dd></div></dl>
          <div class="env-actions"><ElButton size="small" type="primary" :disabled="!targetReady(t)" @click="releaseTo(t)">{{targetReady(t) ? '发布' : '先配置资源'}}</ElButton><ElButton v-if="isManagedJavaTarget(t)" size="small" @click="openInstances(t)">实例管理</ElButton><ElButton size="small" @click="editTarget(t)">编辑配置</ElButton><button class="remove op-btn" @click="deleteTarget(t)">移除</button></div>
        </article>
        <div v-if="!targets.length" class="add-env"><span>尚未配置部署环境</span><small>使用右上角“配置环境”绑定发布资源</small></div>
      </div>
    </div>

    <!-- 发布单与历史 -->
    <div v-show="!showTarget" v-if="tab === 'release'">
      <div class="section-head"><div><h3>发布记录</h3><p>选择已构建版本和目标环境，形成可审计的发布记录</p></div></div>
      <div v-if="showPublishForm" class="release-bar"><ElButton size="small" @click="showPublishForm=false">返回发布记录</ElButton>
        <div><label>不可变制品</label><Sel v-model="artifactID" :options="artifactOptions" placeholder="选择具体制品" width="310px" /></div>
        <span class="arrow">→</span>
        <div><label>目标环境</label><Sel v-model="deployEnv" :options="targets.map(t => ({ value: t.environment, label: (t.protected ? '🔒 ' : '') + t.display_name + '（' + t.hosts.length + '台）' }))" placeholder="选择环境" width="220px" /></div>
        <ElButton type="primary" :disabled="!!activeDeploy || !!activeOrder || deploying || !artifactID || !targetReady(selectedTarget)" @click="requestDeploy">{{ activeOrder ? `处理中 #${activeOrder.id}` : activeDeploy ? `发布中 #${activeDeploy.id}` : '创建发布' }}</ElButton>
        <span v-if="!artifacts.length" class="dim">请先完成构建</span><span v-else-if="!targets.length" class="dim">请先配置环境</span><button v-else-if="selectedTarget && !targetReady(selectedTarget)" class="op op-btn" @click="setTab('environment')">该环境没有可用发布目标，去配置 →</button>
      </div>
      <BuildRecordsTable v-if="!showPublishForm" :key="'releases:'+name" :service="String(name)" kind="releases" :environments="targets" :me="me" @open="openRelease" @action="releaseAction"/>
 </div>

    <!-- 高级配置（步骤编排） -->
    <div v-if="tab === 'advanced'">
      <div class="advanced-heading"><h3>高级构建流水线</h3><HelpTip text="编排源码、检查、构建、脚本和制品步骤；部署由发布单处理。脚本在所选 Runner / 隔离容器中执行，不是远程主机脚本。保存不执行。"/><ElButton size="small" @click="setTab('build')">← 返回构建</ElButton></div>
      <div v-if="svc" class="dim" style="font-size:11px;margin-bottom:8px">代码：{{ svc.git_url || '（未配 git）' }} · 分支 {{ svc.branch || '默认' }}</div>
      <fieldset :disabled="savingSteps" class="settings-editor-body"><div v-for="(inst, i) in steps" :key="i" style="border:1px solid var(--border);border-radius:8px;padding:10px 12px;margin-bottom:8px;background:var(--panel2)">
        <div style="display:flex;align-items:center;gap:8px">
          <span style="width:20px;text-align:center;color:var(--dim)">{{ i + 1 }}</span>
          <span :style="{ width:'6px', height:'16px', borderRadius:'2px', background: CAT_COLOR[defOf(inst.step)?.category] || 'var(--dim)' }"></span>
          <b>{{ defOf(inst.step)?.name || inst.step }}</b>
          <span style="color:var(--dim);font-size:11px">{{ inst.step }}</span>
          <span style="margin-left:auto;display:flex;gap:8px">
            <button type="button" class="op op-btn" :disabled="savingSteps||i===0" :aria-label="'上移第'+(i+1)+'步'" @click="move(i, -1)">↑</button><button type="button" class="op op-btn" :disabled="savingSteps||i===steps.length-1" :aria-label="'下移第'+(i+1)+'步'" @click="move(i, 1)">↓</button>
            <button type="button" class="op del op-btn" :disabled="savingSteps" :aria-label="'删除第'+(i+1)+'步'" @click="removeBuildStep(i)">✕</button>
          </span>
        </div>
        <div style="margin-top:8px;padding-top:8px;border-top:1px dashed var(--border)">
          <div v-for="p in defOf(inst.step)?.params || []" :key="p.name" class="fld" style="margin:6px 0">
            <label>{{ p.label }}</label>
            <Sel v-if="p.type === 'select'" :model-value="(inst.params || {})[p.name] ?? p.default ?? ''" :options="(p.options || []).map(o => ({ value: o.value, label: o.label || o.value }))" width="100%" @update:model-value="v => setParam(i, p.name, v)" />
            <ElInput v-else-if="p.type === 'shell'" type="textarea" :rows="inst.step==='shell-script'?6:2" style="width:100%;font-family:var(--mono);font-size:11px"
              :model-value="(inst.params || {})[p.name] ?? p.default ?? ''" @update:model-value="v => setParam(i, p.name, v)" />
            <ElInput v-else :model-value="(inst.params || {})[p.name] ?? p.default ?? ''" @update:model-value="v => setParam(i, p.name, v)" />
          </div>
        </div>
      </div>
      <div style="display:flex;gap:8px;align-items:center;margin-top:6px">
        <Sel v-model="newStep" :options="buildStepDefs.map(d => ({ value: d.id, label: d.name + '（' + d.category + '）' }))" :placeholder="buildStepDefs[0]?.id || '选择步骤'" width="220px" />
        <ElButton size="small" @click="addBuildStep">＋ 添加构建步骤</ElButton>
        <ElButton type="primary" size="small" :loading="savingSteps" style="margin-left:auto" @click="saveSteps">保存流水线</ElButton>
      </div></fieldset>
    </div>

    <Modal :show="showDeployConfirm" title="确认发布" @close="showDeployConfirm = false">
      <div class="release-summary"><span>{{ typeMeta.icon }}</span><div><b>{{ name }} → {{ selectedTarget?.display_name }}</b><small>版本 {{ selectedVersion }} · 制品 #{{ selectedArtifact?.id }} · {{targetResource(selectedTarget)}}</small></div></div>
      <div class="fld"><label>应用 / 版本</label><code>{{ name }} · {{ selectedVersion }}</code></div>
      <div class="fld"><label>制品</label><code style="word-break:break-all">#{{selectedArtifact?.id}} · {{ selectedArtifact?.uri || '—' }}</code></div>
      <div class="fld"><label>制品摘要</label><code style="font-size:10px;word-break:break-all">{{ selectedArtifact?.digest || '—' }}</code></div>
      <div class="fld"><label>目标环境</label><span>{{ selectedTarget?.protected ? '🔒 ' : '' }}{{ selectedTarget?.display_name }}</span></div>
      <div class="fld"><label>环境执行目标</label><span>{{targetResource(selectedTarget)}}；提交时自动快照目标配置</span></div>
      <div class="version-diff"><div><small>当前版本</small><code>{{envState[deployEnv]?.current?.version||'未发布'}}</code></div><span>→</span><div><small>目标版本</small><code>{{selectedVersion}} · #{{selectedArtifact?.id}}</code></div></div>
      <div class="fld"><label>发布策略</label><span>{{releasePolicyText(selectedTarget)}}</span></div>
      <div v-if="!isManagedTarget(selectedTarget)" class="fld"><label>远端目录</label><code>{{ selectedTarget?.remote_dir || '未配置' }}</code></div>
      <div v-if="selectedTarget?.target_type === 'ssh-jar'" class="fld"><label>启动命令</label><code style="word-break:break-all">{{ selectedTarget?.start_cmd || '未配置' }}</code></div>
      <div class="fld"><label>应用环境变量</label><span>{{selectedTarget?.runtime_env_keys?.length ? `${selectedTarget.runtime_env_keys.length} 项（已加密快照）` : '无'}}</span></div>
      <div v-if="!isManagedTarget(selectedTarget)" class="fld"><label>健康检查</label><span :style="{ color: selectedTarget?.health_url ? 'var(--ok)' : 'var(--warn)' }">{{ selectedTarget?.health_url || '未配置，发布后不会自动验证服务可用性' }}</span></div>
      <div class="fld"><label>构建变量组</label><span>{{delivery.variable_groups?.join('、')||'无'}}</span></div>
      <div v-if="selectedTarget?.require_reason" class="fld"><label>发布原因</label><ElInput v-model="reason" type="textarea" :rows="2" placeholder="受保护环境必填" /></div>
      <div v-if="selectedTarget?.protected" class="approval-note">🔒 提交后进入待审批；审批人：{{selectedTarget?.approvers || '未配置（当前禁止发布）'}}。发起人不能审批自己的发布单。</div>
      <div style="display:flex;gap:10px;margin-top:14px">
        <ElButton style="margin-left:auto" @click="showDeployConfirm = false">取消</ElButton>
        <ElButton type="primary" :loading="deploying" :disabled="(selectedTarget?.require_reason && !reason.trim()) || (selectedTarget?.protected && !selectedTarget?.approvers)" @click="deploy">确认发布</ElButton>
      </div>
    </Modal>

    <BuildRunDialog :show="showBuildRun" :service="svc" :delivery="delivery" :targets="targets" :mode="launchMode" :environment="launchEnvironment" @close="showBuildRun=false" @started="id=>{showBuildRun=false;router.push({path:'/run/'+id,query:{return:route.fullPath}})}"/>

    <section v-if="showTarget" class="target-workspace" aria-label="配置部署环境">
      <div class="target-editor-heading"><h3>配置部署环境 · {{targetForm.environment||'新环境'}}</h3><small>保存或取消后返回原页面，其他筛选和记录保持不变。</small></div>
      <nav aria-label="部署环境配置分组"><button v-for="s in [['basic','环境与资源'],['runtime','启动与健康检查'],['variables','应用变量'],['policy','发布策略']]" :key="s[0]" :class="{active:targetSection===s[0]}" @click="targetSection=s[0]">{{s[1]}}</button></nav>
      <fieldset :disabled="savingTarget" class="target-editor-body">
      <div class="target-config-form">
      <section v-show="targetSection==='basic'" class="target-group">
      <div class="fld"><label>应用环境</label><Sel :model-value="targetForm.environment" :disabled="targetForm._existing" :options="environments.map(e => ({ value: e.name, label: (e.protected ? '🔒 ' : '') + e.display_name }))" width="100%" @update:model-value="updateTargetEnvironment" /><small class="dim">应用无需复制；每个环境独立保存部署配置和资源绑定。</small></div>
      <div class="runtime-hint"><span>{{ targetForm.target_type==='aliyun-sae'?'☁':typeMeta.icon }}</span><div><b>{{ targetForm.target_type === 'aliyun-sae' ? '阿里云 SAE 托管发布' : targetForm.target_type === 'docker-host' ? 'Docker 主机发布' : targetForm.target_type === 'ssh-static' ? '静态站点发布' : 'Java 进程发布' }}</b><small>{{targetForm.target_type==='aliyun-sae'?'JAR 经 OSS 中转；镜像按 digest 直发':'按应用制品类型选择部署目标'}}</small></div></div>
      <div v-if="targetForm.target_type!=='aliyun-sae'" class="fld"><label>该环境的主机组</label><Sel v-model="targetForm.host_group" :options="[{value:'',label:'请选择主机组'},...hostGroups.filter(g=>g.environment===targetForm.environment).map(g=>({value:g.name,label:`${g.name}（${g.hosts.length} 台）`}))]" width="100%" /><small v-if="!hostGroups.some(g=>g.environment===targetForm.environment)" class="dim">该环境还没有主机组，请先到资源中心创建。</small></div>
      <div v-if="targetForm.target_type !== 'docker-host' && targetForm.target_type !== 'aliyun-sae'" class="fld"><label>部署目录</label><ElInput v-model="targetForm.remote_dir" placeholder="/opt/service" /></div>
      </section><section v-show="targetSection==='runtime'" class="target-group">
        <template v-if="targetForm.target_type === 'ssh-jar'">
        <div class="fld"><label>启动方式</label><Sel :model-value="targetForm.deploy_config.java_mode || 'custom'" :options="[{value:'managed',label:'标准 Java（平台管理启停和版本回滚）'},{value:'custom',label:'自定义命令（兼容已有配置）'}]" width="100%" @update:model-value="updateJavaMode" /></div>
        <template v-if="targetForm.deploy_config.java_mode === 'managed'">
          <div class="fld"><label>运行 JDK 版本</label><Sel v-model="targetForm.deploy_config.java_version" :options="['8','11','17','21','25']" width="100%" /></div>
          <div class="fld"><label>主机上的 JDK 目录</label><ElInput v-model="targetForm.deploy_config.java_home" placeholder="/home/work/runtime/jdk-21（留空使用主机 java）" /><small class="help">与构建 JDK 独立。发布前检查版本和制品字节码，不自动安装 JDK。</small></div>
          <div class="fld"><label>JVM 参数（每行一个参数，不加引号）</label><ElInput v-model="targetForm.deploy_config.java_jvm_options" type="textarea" :rows="3" placeholder="-Xms256m&#10;-Xmx1024m&#10;-Duser.timezone=Asia/Shanghai" /></div>
          <div class="fld"><label>应用参数（每行一个参数）</label><ElInput v-model="targetForm.deploy_config.java_args" type="textarea" :rows="2" placeholder="--server.port=8889" /></div>
          <div class="fld"><label>就绪检查端口</label><ElInput v-model="targetForm.deploy_config.java_port" placeholder="8889" /><small class="help">仅检查 127.0.0.1 上的监听状态，不改变应用端口；用应用参数或环境变量配置实际端口。端口和健康检查 URL 至少填一项。</small></div>
          <div class="fld"><label>健康检查失败时</label><Sel v-model="targetForm.deploy_config.health_failure_action" :disabled="environments.find(e=>e.name===targetForm.environment)?.protected" :options="[{value:'rollback',label:'停止新版本并恢复旧版本（推荐）'},{value:'stop',label:'停止新版本，不恢复旧版本'},{value:'keep',label:'保留新版本运行（仅排障）'}]" width="100%" /><small class="help">受保护环境固定自动回滚；其他环境可保留现场进行排障。无论选择哪种策略，本次发布都会标记失败并停止后续批次。</small></div>
          <p class="help">仅支持 Linux；版本存入 releases，current 指向生效版本。平台校验 PID 归属、优雅停止、检查就绪，失败恢复旧制品及旧变量。不会接管 Jpom 已启动的进程，首次切换需先停止旧服务。不提供开机自启或进程崩溃自动重启。</p>
        </template>
        <template v-else>
          <div class="fld"><label>启动命令</label><ElInput v-model="targetForm.start_cmd" type="textarea" :rows="2" placeholder="nohup java -jar /opt/service/app.jar ..." /></div>
          <div class="fld"><label>停止命令</label><ElInput v-model="targetForm.stop_cmd" type="textarea" :rows="2" /></div>
        </template>
      </template>
      <div v-if="targetForm.target_type!=='aliyun-sae'" class="fld"><label>健康检查 URL</label><ElInput v-model="targetForm.health_url" /></div>
      </section><section v-show="targetSection==='variables'" class="target-group">
      <p v-if="svc?.artifact_type==='static'" class="help">静态制品无需运行环境变量；构建变量请在应用设置中配置。</p>
      <div v-if="svc?.artifact_type!=='static'" class="fld runtime-env-editor">
        <label>应用环境变量</label>
        <ElInput v-model="targetForm.runtime_env_text" type="textarea" :rows="5" placeholder="SPRING_PROFILES_ACTIVE=test&#10;NACOS_SERVER_ADDR=192.168.0.10:8848" />
        <small v-if="targetForm.runtime_env_keys?.length" class="help">已加密保存：<code>{{targetForm.runtime_env_keys.join(', ')}}</code>。留空保持不变，填写后整体替换。</small>
        <small v-else class="help">每行 KEY=value；按应用和环境隔离，加密保存，发布日志不会显示变量值。</small>
        <label v-if="targetForm.runtime_env_keys?.length" class="clear-runtime-env"><input v-model="targetForm.runtime_env_clear" type="checkbox"/> 清空已保存的环境变量</label>
        <small v-if="targetForm.target_type==='ssh-jar'" class="help">标准 Java 模式按版本保存并加载变量快照；自定义模式写入部署目录下的 <code>.devopsplatform.env</code>。若自定义命令使用 systemctl，服务文件需声明相应 EnvironmentFile。</small>
      </div>
      </section><section v-if="['docker-host','aliyun-sae'].includes(targetForm.target_type)" v-show="targetSection==='runtime'" class="target-group">
      <template v-if="targetForm.target_type === 'docker-host'">
        <div class="fld"><label>容器名</label><ElInput v-model="targetForm.deploy_config.container_name" :placeholder="name" /></div>
        <div class="fld"><label>端口映射</label><ElInput v-model="targetForm.deploy_config.ports" placeholder="8080:8080,9090:9090" /></div>
        <div class="fld"><label>远程 env-file</label><ElInput v-model="targetForm.deploy_config.env_file" placeholder="/opt/service/app.env" /></div>
        <div class="fld"><label>额外 run 参数</label><ElInput v-model="targetForm.deploy_config.run_args" placeholder="--network app-net" /></div>
      </template>
      <template v-if="targetForm.target_type==='aliyun-sae'">
        <div class="two"><div class="fld"><label>SAE 连接</label><Sel v-model="targetForm.deploy_config.connection" :options="[{value:'',label:'请选择 SAE 连接'},...saeConnections.map(c=>({value:c.name,label:`${c.name}（${c.config?.region}）`}))]" width="100%" /></div><div class="fld"><label>地域</label><ElInput v-model="targetForm.deploy_config.region" placeholder="默认取连接地域" /></div></div>
        <div class="two"><div class="fld"><label>SAE 应用 ID</label><ElInput v-model="targetForm.deploy_config.app_id" placeholder="应用 UUID；每个环境使用独立应用" /></div><div class="fld"><label>命名空间 ID</label><ElInput v-model="targetForm.deploy_config.namespace_id" /></div></div>
        <template v-if="svc?.artifact_type==='jar'"><div class="two"><div class="fld"><label>OSS Bucket（可覆盖连接）</label><ElInput v-model="targetForm.deploy_config.oss_bucket" placeholder="默认取 SAE 连接" /></div><div class="fld"><label>JDK</label><ElInput v-model="targetForm.deploy_config.jdk" placeholder="Open JDK 8" /></div></div><div class="two"><div class="fld"><label>JVM 启动选项</label><ElInput v-model="targetForm.deploy_config.jar_start_options" placeholder="-Xms512m -Xmx512m" /></div><div class="fld"><label>JAR 参数</label><ElInput v-model="targetForm.deploy_config.jar_start_args" /></div></div></template>
        <template v-if="svc?.artifact_type==='image'"><div class="two"><div class="fld"><label>ACR 企业实例 ID（可选）</label><ElInput v-model="targetForm.deploy_config.acr_instance_id" /></div><div class="fld"><label>容器启动命令（可选）</label><ElInput v-model="targetForm.deploy_config.command" /></div></div><div class="fld"><label>命令参数 JSON（可选）</label><ElInput v-model="targetForm.deploy_config.command_args" placeholder='["--server.port=8080"]' /></div></template>
        <details class="advanced-box"><summary>SAE 高级参数</summary><div class="two"><div class="fld"><label>最小可用实例比例</label><ElInput v-model="targetForm.deploy_config.min_ready_ratio" placeholder="例如 50" /></div><div class="fld"><label>批次间隔（秒）</label><ElInput v-model="targetForm.deploy_config.batch_wait_seconds" placeholder="例如 10" /></div></div><div class="fld"><label>环境变量 JSON</label><ElInput v-model="targetForm.deploy_config.envs" type="textarea" :rows="3" placeholder='[{"name":"SPRING_PROFILES_ACTIVE","value":"prod"}]' /></div><div class="fld"><label>超时（分钟）</label><ElInput v-model="targetForm.deploy_config.timeout_minutes" placeholder="30" /></div></details>
        <small class="help">环境隔离由“环境 → 独立 SAE 应用 ID”保证；同一 SAE 应用不能重复绑定到其他环境。</small>
      </template>
      </section><section v-show="targetSection==='policy'" class="target-group">
      <ReleasePolicyEditor :target="targetForm" :disabled="savingTarget" />
      <details class="advanced-box"><summary>高级部署参数</summary>
        <div class="fld"><label>部署驱动</label><Sel v-model="targetForm.target_type" :options="[{value:'ssh-jar',label:'Java JAR / SSH'},{value:'ssh-static',label:'静态站点 / SSH'},{value:'docker-host',label:'Docker 主机 / SSH'},...(svc?.artifact_type==='static'?[]:[{value:'aliyun-sae',label:'阿里云 SAE（JAR / Docker）'}])]" width="100%" /></div>
        <div v-if="!['aliyun-sae','ssh-jar'].includes(targetForm.target_type)" class="fld"><label>停止命令</label><ElInput v-model="targetForm.stop_cmd" /></div>
      </details>
      </section></div>
      </fieldset>
      <div class="target-config-actions"><ElButton :disabled="savingTarget" @click="showTarget=false">取消</ElButton><ElButton type="primary" :loading="savingTarget" @click="saveTarget">保存环境配置</ElButton></div>
    </section>

    <Modal :show="showInstances" :title="`实例管理 · ${instanceTarget?.display_name || ''}`" extra-wide @close="showInstances=false;pendingInstanceAction=null">
      <div class="instance-toolbar"><div><b>{{name}}</b><small>{{instanceTarget?.host_group}} · 实时读取目标主机进程，不以发布记录代替运行状态</small></div><ElButton size="small" :loading="instancesLoading" @click="loadInstances">刷新状态</ElButton></div>
      <div class="table-scroll"><table class="instance-table"><thead><tr><th>主机</th><th>运行状态</th><th>当前版本</th><th>PID</th><th>检查时间</th><th>操作</th></tr></thead><tbody>
        <tr v-for="instance in instances" :key="instance.host"><td><b>{{instance.host}}</b><small class="artifact-id">{{instance.address}}</small></td><td><span :class="instanceStateClass(instance.state)">{{instanceStateText(instance.state)}}</span><small v-if="instance.message" class="instance-message" :title="instance.message">{{instance.message}}</small></td><td><code>{{instance.version || instance.release || '—'}}</code></td><td><code>{{instance.pid || '—'}}</code></td><td class="dim">{{fmtTime(instance.checked_at)}}</td><td><div class="instance-actions"><ElButton v-if="['stopped'].includes(instance.state)" size="small" type="primary" @click="prepareInstanceAction(instance,'start')">启动</ElButton><template v-if="instance.state==='running'"><ElButton size="small" @click="prepareInstanceAction(instance,'ready')">检查</ElButton><ElButton size="small" @click="prepareInstanceAction(instance,'restart')">重启</ElButton><ElButton size="small" type="danger" @click="prepareInstanceAction(instance,'stop')">停止</ElButton></template></div></td></tr>
      </tbody></table></div>
      <div v-if="instancesLoading && !instances.length" class="empty">正在连接主机并读取状态…</div><div v-else-if="!instances.length" class="empty">该环境没有可管理的主机实例</div>
      <div v-if="pendingInstanceAction" class="instance-confirm"><div><b>确认{{({start:'启动',stop:'停止',restart:'重启'})[pendingInstanceAction.action]}} {{pendingInstanceAction.instance.host}}</b><small v-if="instanceTarget?.protected">🔒 这是受保护环境，操作将记录操作者和原因。</small><small v-else>操作会直接影响该主机上的当前应用进程。</small></div><ElInput v-if="instanceTarget?.protected || instanceTarget?.require_reason" v-model="instanceReason" type="textarea" :rows="2" placeholder="填写操作原因" /><div><ElButton size="small" @click="pendingInstanceAction=null">取消</ElButton><ElButton type="primary" size="small" :loading="instanceOperating" :disabled="(instanceTarget?.protected || instanceTarget?.require_reason) && !instanceReason.trim()" @click="runInstanceAction()">确认执行</ElButton></div></div>
    </Modal>

    <Modal :show="showDeployLog" title="部署日志" wide @close="showDeployLog = false">
      <pre style="max-height:60vh;overflow:auto;white-space:pre-wrap;word-break:break-all;background:#0a0d12;color:#c9d1d9;padding:12px;border-radius:8px">{{ deployLog }}</pre>
    </Modal>

    <Modal :show="showReleaseDetail" :title="`发布单 #${activeRelease?.id || ''}`" wide @close="showReleaseDetail=false">
      <div class="release-summary"><span>🚀</span><div><b>{{activeRelease?.version}} → {{activeRelease?.environment}}</b><small>{{statusText(activeRelease?.status)}} · {{activeRelease?.succeeded||0}}/{{activeRelease?.total||0}} 个目标成功</small></div></div>
      <dl class="env-meta release-snapshot"><div><dt>目标快照</dt><dd>{{targetResource(activeRelease?.target_snapshot)}}</dd></div><div><dt>{{isManagedTarget(activeRelease?.target_snapshot)?'命名空间':'部署目录'}}</dt><dd><code>{{isManagedTarget(activeRelease?.target_snapshot)?activeRelease?.target_snapshot?.deploy_config?.namespace_id:(activeRelease?.target_snapshot?.remote_dir||'—')}}</code></dd></div><div><dt>执行策略</dt><dd>{{releasePolicyText(activeRelease?.target_snapshot)}}</dd></div><div><dt>健康检查</dt><dd>{{isManagedTarget(activeRelease?.target_snapshot)?'SAE 变更单状态':(activeRelease?.target_snapshot?.health_url||'未配置')}}</dd></div></dl>
      <table><thead><tr><th>执行目标</th><th>状态</th><th>时间</th><th></th></tr></thead><tbody><tr v-for="id in activeRelease?.deploy_ids||[]" :key="id"><td>{{deployOf(id)?.host||`任务 #${id}`}}<small v-if="deployOf(id)?.external_id" class="artifact-id">变更单 {{deployOf(id).external_id}}</small></td><td><span :class="statusClass(deployOf(id)?.status)">{{statusText(deployOf(id)?.status||'queued')}}</span></td><td>{{fmtTime(deployOf(id)?.started_at)}}</td><td><button v-if="deployOf(id)" class="op op-btn" @click="openDeployLog(deployOf(id))">日志</button></td></tr></tbody></table><div v-if="!activeRelease?.deploy_ids?.length" class="empty">审批或排队完成后生成执行任务</div>
    </Modal>

  </div>
</template>
<style scoped>
.advanced-heading{display:flex;align-items:center;gap:8px;margin-bottom:12px}.advanced-heading h3{font-size:15px;margin:0}.advanced-heading>.el-button{margin-left:auto}
.settings-editor-body{border:0;padding:0;margin:0;min-width:0}
.app-name{max-width:220px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.back{color:var(--brand);cursor:pointer;padding:6px;min-width:36px;min-height:36px}.header-environment{display:inline-block;flex-shrink:0}
.app-tabs{display:flex;gap:2px;border-bottom:1px solid var(--border);margin:14px 0 18px}.app-tabs button{padding:8px 14px;color:var(--muted);cursor:pointer;border:0;border-radius:0;background:transparent;border-bottom:2px solid transparent;margin-bottom:-1px}.app-tabs button.active{color:var(--brand);border-bottom-color:var(--brand);font-weight:600}
.more-tabs{position:relative;margin-left:auto;color:var(--muted)}.more-tabs summary{list-style:none;cursor:pointer;padding:8px 12px}.more-tabs summary::-webkit-details-marker{display:none}.more-tabs.active summary{color:var(--brand);font-weight:600}.more-tabs>div{position:absolute;right:0;top:34px;z-index:20;display:flex;flex-direction:column;min-width:170px;padding:5px;background:var(--panel);border:1px solid var(--border);border-radius:8px;box-shadow:0 8px 24px rgba(0,0,0,.18)}.more-tabs>div button{margin:0;border:0;text-align:left;border-radius:5px}.more-tabs>div button:hover,.more-tabs>div button.active{background:var(--panel2)}
.overview-strip{display:grid;grid-template-columns:repeat(3,1fr);background:var(--panel);border:1px solid var(--border);border-radius:10px;margin-bottom:20px}.overview-strip>div{display:flex;flex-direction:column;padding:14px 16px;border-right:1px solid var(--border)}.overview-strip>div:last-child{border:0}.overview-strip small{color:var(--muted)}.overview-strip b{font-size:17px;margin:3px 0}.overview-strip span{font-size:10px;color:var(--dim)}
.delivery-flow{background:var(--panel);border:1px solid var(--border);border-radius:10px;padding:14px 16px;margin-bottom:20px}.flow-head{display:flex;align-items:center;gap:12px;margin-bottom:13px}.flow-head h3{font-size:14px}.flow-head p{font-size:10px;color:var(--muted);margin-top:2px}.flow-head p b{color:var(--text)}.observe-actions{display:flex;gap:7px;margin-left:auto}.flow-steps{display:flex;align-items:stretch;gap:7px}.flow-steps>em{display:flex;align-items:center;color:var(--dim);font-style:normal}.flow-step{display:flex;align-items:center;gap:9px;min-width:0;flex:1;padding:10px;text-align:left;background:var(--panel2);border:1px solid transparent;cursor:pointer}.flow-step:hover{border-color:var(--brand)}.flow-step>i{display:grid;place-items:center;flex:0 0 23px;height:23px;border-radius:50%;background:var(--border);color:var(--muted);font-style:normal;font-size:10px}.flow-step>span{display:flex;flex-direction:column;min-width:0}.flow-step small{color:var(--dim)}.flow-step b{font-size:10px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.flow-step.done>i{background:var(--ok);color:#fff}.flow-step.active>i{background:var(--brand);color:#fff}.flow-step.error>i{background:var(--error);color:#fff}.flow-step.ready>i{background:var(--info);color:#fff}
.section-head{display:flex;align-items:center;margin:8px 0 12px}.section-head h3{font-size:15px}.section-head p{font-size:11px;color:var(--muted);margin-top:2px}.section-head button,.section-head>.text-link{margin-left:auto}.section-head.compact{margin-top:22px}.text-link{color:var(--brand);cursor:pointer;font-size:11px}
.env-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(245px,1fr));gap:12px}.env-card{background:var(--panel);border:1px solid var(--border);border-radius:10px;padding:14px;cursor:pointer}.env-card.focused{border-color:var(--brand);box-shadow:inset 0 0 0 1px color-mix(in srgb,var(--brand) 55%,transparent)}.env-title{display:flex;align-items:center;gap:8px}.env-title>div{min-width:0}.env-title h4{font-size:13px}.env-title small{color:var(--muted);font-size:10px}.env-title>span:last-child{margin-left:auto;font-size:11px}.env-dot{width:8px;height:8px;border-radius:50%;background:var(--dim)}.env-dot.ok{background:var(--ok);box-shadow:0 0 0 3px color-mix(in srgb,var(--ok) 15%,transparent)}.current-version{display:flex;flex-direction:column;background:var(--panel2);border-radius:6px;padding:9px 10px;margin:12px 0}.current-version small{color:var(--dim)}.current-version code{margin:2px 0}.current-version span{font-size:9px;color:var(--muted)}.env-actions{display:flex;align-items:center;gap:6px}.env-actions .remove{margin-left:auto;color:var(--dim);font-size:11px;cursor:pointer}.env-actions .remove:hover{color:var(--error)}
.add-env{min-height:164px;border:1px dashed var(--border);background:transparent;display:flex;flex-direction:column;align-items:center;justify-content:center;gap:4px;color:var(--muted)}.add-env span{font-weight:600}.add-env small{font-size:10px}.env-meta{margin:12px 0}.env-meta>div{display:grid;grid-template-columns:72px 1fr;padding:4px 0}.env-meta dt{color:var(--dim);font-size:10px}.env-meta dd{font-size:11px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.release-snapshot{background:var(--panel2);padding:10px;border-radius:8px;margin-bottom:12px}
.release-bar{display:flex;align-items:flex-end;gap:12px;background:var(--panel);border:1px solid var(--border);border-radius:10px;padding:14px;margin-bottom:14px}.release-bar>div{display:flex;flex-direction:column;gap:4px}.release-bar label{font-size:10px;color:var(--muted)}.release-bar .arrow{padding-bottom:6px;color:var(--dim)}.dim{color:var(--dim)}
.snapshot{font-size:10px;color:var(--muted)}.artifact-id{display:block;color:var(--dim);font-family:var(--mono)}.artifact-warning{color:var(--warn);background:color-mix(in srgb,var(--warn) 9%,var(--panel));border:1px solid color-mix(in srgb,var(--warn) 30%,var(--border));padding:9px 11px;border-radius:7px;margin-bottom:10px;font-size:11px}.progress{width:100px;height:3px;background:var(--panel2);border-radius:2px;margin-top:4px}.progress i{display:block;height:100%;background:var(--ok);border-radius:2px}.settings-grid{display:grid;grid-template-columns:1fr 1fr;gap:12px}.settings-card{background:var(--panel);border:1px solid var(--border);border-radius:9px;padding:14px}.settings-card h4{font-size:13px;margin-bottom:12px}.check-list{display:flex;flex-direction:column;gap:6px}.check-list label{display:flex;align-items:center;gap:6px;border:1px solid var(--border);border-radius:6px;padding:7px}.check-list small{margin-left:auto;color:var(--dim);font-size:9px}.help{font-size:10px;color:var(--muted);margin:-5px 0 12px}.save-row{display:flex;justify-content:flex-end;margin-top:12px}.strategy-row{display:grid;grid-template-columns:repeat(3,1fr);gap:8px}.pause-check{display:block;margin:-2px 0 12px}.version-diff{display:grid;grid-template-columns:1fr auto 1fr;gap:10px;align-items:center;background:var(--panel2);padding:9px;border-radius:7px;margin-bottom:10px}.version-diff>div{display:flex;flex-direction:column}.version-diff small{color:var(--dim)}.approval-note{color:var(--warn);background:color-mix(in srgb,var(--warn) 10%,var(--panel));padding:8px;border-radius:6px;font-size:11px}.one-click-flow{display:flex;align-items:center;justify-content:center;gap:8px;padding:11px;margin-bottom:14px;border:1px solid color-mix(in srgb,var(--brand) 25%,var(--border));border-radius:8px;background:color-mix(in srgb,var(--brand) 6%,var(--panel))}.one-click-flow span{padding:4px 7px;border-radius:5px;background:var(--panel2);font-size:11px;font-weight:600}.one-click-flow i{color:var(--dim);font-style:normal}.one-click-summary{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:8px;margin:12px 0}.one-click-summary>div{display:flex;flex-direction:column;min-width:0;padding:9px;background:var(--panel2);border-radius:7px}.one-click-summary small{color:var(--dim)}.one-click-summary b{margin-top:3px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-size:11px}.one-click-note{padding:8px 10px;border-radius:7px;background:var(--panel2);color:var(--muted);font-size:11px}.one-click-actions{display:flex;justify-content:flex-end;gap:8px;margin-top:14px}
.build-config-head{display:flex;align-items:center;gap:8px;padding:14px 16px;margin-bottom:10px;border:1px solid var(--border);border-radius:10px;background:var(--panel)}.build-config-head>div{min-width:0;margin-right:auto}.build-config-head h3{font-size:16px}.build-config-head p{margin-top:3px;color:var(--muted);font-size:11px}.build-summary-grid{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));overflow:hidden;background:var(--panel);border:1px solid var(--border);border-radius:10px}.build-summary-item{display:flex;flex-direction:column;min-width:0;padding:12px 14px;border-right:1px solid var(--border)}.build-summary-item:last-child{border-right:0}.build-summary-item>span{color:var(--dim);font-size:10px}.build-summary-item>b{margin:4px 0;font-size:12px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.build-summary-item>small{color:var(--muted);font-size:10px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.unsaved-dot{margin-right:auto;color:var(--warn);font-size:10px;white-space:nowrap}.build-config-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:12px;max-height:64vh;overflow:auto;padding-right:4px}.build-config-grid .settings-card{min-width:0}.build-config-actions{display:flex;align-items:center;gap:8px;padding-top:14px;margin-top:12px;border-top:1px solid var(--border)}.card-title{display:flex;align-items:center;gap:9px;margin-bottom:13px}.card-title>span{display:grid;place-items:center;flex:0 0 25px;height:25px;border-radius:50%;background:color-mix(in srgb,var(--brand) 12%,var(--panel));color:var(--brand);font-size:11px;font-weight:700}.card-title>div{display:flex;flex-direction:column;min-width:0}.card-title h4{margin:0;font-size:13px}.card-title small{color:var(--muted);font-size:10px}.card-title .resource-link{margin-left:auto;border:0;background:transparent;padding:4px;color:var(--brand);cursor:pointer;font-size:10px}.config-help{display:block;margin-top:4px;color:var(--muted);font-size:10px;line-height:1.55}.build-tool-tabs{display:grid;grid-template-columns:1fr 1fr;gap:8px}.build-tool-tabs button{display:flex;flex-direction:column;align-items:flex-start;gap:2px;padding:9px 11px;background:var(--panel2);border:1px solid var(--border);border-radius:7px;color:var(--text);cursor:pointer}.build-tool-tabs button.active{border-color:var(--brand);background:color-mix(in srgb,var(--brand) 8%,var(--panel));box-shadow:inset 0 0 0 1px var(--brand)}.build-tool-tabs small{color:var(--muted);font-size:9px}.inline-advanced{margin-top:8px;padding-top:8px;border-top:1px dashed var(--border)}.inline-advanced summary{color:var(--brand);font-size:10px;cursor:pointer;margin-bottom:10px}.empty-inline{padding:9px;color:var(--dim);font-size:10px;text-align:center;border:1px dashed var(--border);border-radius:6px}.build-advanced-card{display:flex;flex-direction:column}.build-advanced-card p{color:var(--muted);font-size:11px;line-height:1.6;margin-bottom:12px}.build-advanced-card>button{align-self:flex-start}.build-history-head{margin-top:24px;padding-top:18px;border-top:1px solid var(--border)}
.runtime-env-editor{padding:10px;border:1px solid var(--border);border-radius:8px;background:var(--panel2)}.runtime-env-editor .help{display:block;margin:5px 0 0}.clear-runtime-env{display:flex;align-items:center;gap:6px;margin-top:8px;color:var(--error);font-size:10px}
.release-summary,.runtime-hint{display:flex;align-items:center;gap:10px;background:var(--panel2);padding:10px;border-radius:8px;margin-bottom:14px}.release-summary>span,.runtime-hint>span{font-size:22px}.release-summary>div,.runtime-hint>div{display:flex;flex-direction:column}.release-summary small,.runtime-hint small{color:var(--muted)}.host-picker{display:grid;grid-template-columns:repeat(2,1fr);gap:7px}.host-picker label{display:grid;grid-template-columns:auto 1fr;align-items:center;column-gap:6px;border:1px solid var(--border);border-radius:7px;padding:8px;cursor:pointer}.host-picker label.checked{border-color:var(--brand);background:color-mix(in srgb,var(--brand) 7%,var(--panel))}.host-picker input{grid-row:1 / 3}.host-picker small{font-size:9px;color:var(--muted)}.advanced-box{margin:10px 0 14px}.advanced-box summary{color:var(--brand);cursor:pointer;margin-bottom:10px}
.target-config-form{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));align-items:start;gap:10px 16px;max-height:min(68vh,680px);overflow-y:auto;padding:2px 8px 4px 2px}.target-config-form>.fld{min-width:0;margin-bottom:0}.target-config-form>.runtime-hint,.target-config-form>.runtime-env-editor,.target-config-form>.strategy-row,.target-config-form>.pause-check,.target-config-form>.advanced-box,.target-config-form>p.help,.target-config-form>small.help,.target-config-form>.two{grid-column:1/-1}.target-config-form>.runtime-hint{margin-bottom:0}.target-config-form>.runtime-env-editor{margin-top:2px}.target-config-form>.advanced-box{margin:0}.target-config-form>.pause-check{margin:0}.target-config-form>p.help{margin:0;padding:8px 10px;border-radius:7px;background:var(--panel2);line-height:1.55}.target-config-actions{display:flex;justify-content:flex-end;gap:8px;padding-top:12px;margin-top:12px;border-top:1px solid var(--border)}
.instance-toolbar{display:flex;align-items:center;gap:12px;margin-bottom:12px}.instance-toolbar>div{display:flex;flex-direction:column}.instance-toolbar small{color:var(--muted)}.instance-toolbar>button{margin-left:auto}.instance-table{min-width:820px}.instance-table th:last-child,.instance-table td:last-child{min-width:210px}.instance-table td{vertical-align:middle}.instance-message{display:block;max-width:300px;margin-top:3px;color:var(--error);overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.instance-actions{display:flex;gap:5px;white-space:nowrap}.instance-confirm{margin-top:14px;padding:12px;border:1px solid color-mix(in srgb,var(--warn) 35%,var(--border));border-radius:8px;background:color-mix(in srgb,var(--warn) 8%,var(--panel))}.instance-confirm>div:first-child{display:flex;flex-direction:column;margin-bottom:10px}.instance-confirm small{color:var(--muted)}.instance-confirm>div:last-child{display:flex;justify-content:flex-end;gap:8px;margin-top:10px}
@media(max-width:800px){.overview-strip{grid-template-columns:1fr}.overview-strip>div{border-right:0;border-bottom:1px solid var(--border)}.header-environment{display:none}.flow-head{align-items:flex-start;flex-direction:column}.observe-actions{margin-left:0}.flow-steps{flex-direction:column}.flow-steps>em{display:none}.env-grid,.settings-grid,.build-config-grid,.build-summary-grid,.target-config-form,.one-click-summary{grid-template-columns:1fr}.build-summary-item{border-right:0;border-bottom:1px solid var(--border)}.build-summary-item:last-child{border-bottom:0}.build-config-head{align-items:stretch;flex-direction:column}.build-config-head>div{margin-right:0}.release-bar{align-items:stretch;flex-direction:column}.release-bar .arrow{display:none}.app-tabs{overflow:auto}.app-tabs>button{white-space:nowrap}.host-picker{grid-template-columns:1fr}.strategy-row{grid-template-columns:1fr}.one-click-flow{align-items:stretch;flex-direction:column}.one-click-flow i{display:none}}
</style>
<style scoped>
.app-more{position:relative;font-size:12px}.app-more summary{list-style:none;cursor:pointer;padding:8px;border:1px solid var(--border);border-radius:6px}.app-more[open]>div{position:absolute;right:0;top:36px;z-index:60;min-width:160px;background:var(--panel);border:1px solid var(--border);box-shadow:var(--shadow-float);padding:6px;border-radius:8px}.app-more button{display:block;width:100%;border:0;background:none;color:var(--text);text-align:left;padding:8px;cursor:pointer}
.compact-overview{margin-bottom:12px}.compact-overview>div{padding:12px 16px}.compact-overview b{font-size:17px}.overview-next,.build-config-compact{display:flex;align-items:center;gap:10px;padding:10px 12px;background:var(--panel);border:1px solid var(--border);border-radius:8px;margin-bottom:12px;font-size:12px}.overview-next>span,.build-config-compact>span{flex:1;min-width:0;overflow-wrap:anywhere;color:var(--muted)}.add-env{min-height:100px!important}.settings-workspace{display:grid;grid-template-columns:180px minmax(0,1fr);gap:20px}.settings-workspace>nav{display:flex;flex-direction:column;gap:4px}.settings-workspace>nav button{border:0;background:none;color:var(--muted);padding:10px 12px;text-align:left;border-radius:6px;cursor:pointer}.settings-workspace>nav button.active{color:var(--brand);background:var(--brand-soft)}.settings-workspace .build-config-grid{display:block}.settings-workspace .settings-card{border:1px solid var(--border);background:var(--panel);padding:18px;box-shadow:none}.settings-workspace .build-config-actions{position:sticky;bottom:0;z-index:2;background:var(--panel);padding:12px;border-top:1px solid var(--border)}.settings-workspace[data-section=source] .settings-card:not(.build-source-card),.settings-workspace[data-section=command] .settings-card:not(.build-command-card),.settings-workspace[data-section=environment] .settings-card:not(.build-environment-card),.settings-workspace[data-section=resource] .settings-card:not(.build-resource-card),.settings-workspace[data-section=trigger] .settings-card:not(.build-trigger-card),.settings-workspace[data-section=advanced] .settings-card:not(.build-advanced-card),.settings-workspace[data-section=bindings] .settings-card{display:none}.settings-bindings{background:var(--panel);border:1px solid var(--border);border-radius:8px;padding:18px}.settings-bindings p{font-size:12px;color:var(--muted)}
@media(max-width:800px){.settings-workspace{grid-template-columns:1fr}.settings-workspace>nav{flex-direction:row;flex-wrap:wrap}.overview-next,.build-config-compact{flex-wrap:wrap}}
</style>

<style scoped>
.target-workspace{max-width:1200px;margin:0 auto}.target-editor-heading{display:flex;gap:12px;align-items:center;margin:12px 0}.target-editor-heading h3{font-size:16px;margin:0}.target-editor-heading small{color:var(--muted)}.target-workspace>nav{display:flex;flex-wrap:wrap;gap:6px;margin-bottom:14px}.target-workspace>nav button{border:1px solid var(--border);background:var(--panel);color:var(--muted);padding:8px 14px;border-radius:6px;cursor:pointer}.target-workspace>nav button.active{background:var(--brand-soft);color:var(--brand)}.target-editor-body{border:0;padding:0;margin:0;min-width:0}.target-workspace .target-config-form{display:block;max-height:none;overflow:visible;padding:0}.target-group{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:12px 18px;background:var(--panel);border:1px solid var(--border);padding:18px;border-radius:8px;margin-bottom:12px}.target-group>.runtime-hint,.target-group>.runtime-env-editor,.target-group>.two,.target-group>.strategy-row,.target-group>.advanced-box,.target-group>.pause-check,.target-group>.help{grid-column:1/-1}.target-group>.fld{margin:0;min-width:0}.target-workspace .target-config-actions{position:sticky;bottom:0;background:var(--panel);padding:14px;border-top:1px solid var(--border);z-index:2}@media(max-width:760px){.target-group{grid-template-columns:1fr}.target-editor-heading{align-items:flex-start;flex-direction:column}}
</style>
