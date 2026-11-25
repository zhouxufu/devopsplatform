<script setup>
import {supportsInstanceManagement} from '../instanceActions.js'
import {defaultDeploymentTarget,deploymentTargetOptions,switchDeploymentTarget,artifactKindMatchesTarget} from '../environmentDeployment.js'
import {artifactCreatedLabel} from '../artifactPresentation.js'
import {buildRunLabel,artifactBuildLabel,releaseOrderLabel} from '../buildNumber.js'
import ScriptPublicationEditor from '../components/ScriptPublicationEditor.vue'
import ScriptPublicationResult from '../components/ScriptPublicationResult.vue'
import {scriptPublicationError} from '../scriptPublication.js'
import DeploymentAgentOptions from '../components/DeploymentAgentOptions.vue'
import JavaBundleEditor from '../components/JavaBundleEditor.vue'
import JpomCompatibilityFields from '../components/JpomCompatibilityFields.vue'
import {jpomEnvironmentAllowed,selectJavaMode,jpomConfigurationError,createJpomDetector,shouldAutoDetectJpom} from '../jpomCompatibility.js'
import {bundleParams,javaBundleError,bundleRelative} from '../javaBundle.js'
import {validMavenThreads} from '../mavenBuild.js'
import {deploymentTargetReady} from '../deploymentTarget.js'
import {environmentHostOptions,selectedDeploymentHosts} from '../deploymentHosts.js'
import {formatUnixTime} from '../../shared/datePresentation.js'

import DataTable from "../../shared/components/DataTable.vue"

import {useTableLayout} from '../../shared/composables/tableLayout.js'
const {pinTableActions}=useTableLayout()





import ManualPublicationPanel from '../components/ManualPublicationPanel.vue'
import ReleaseRecovery from '../components/ReleaseRecovery.vue'
import InstanceManager from '../components/InstanceManager.vue'
import ReleaseNotice from '../components/ReleaseNotice.vue'
import BuildRunDialog from '../components/BuildRunDialog.vue'
import ApplicationNameEditor from '../../shared/components/ApplicationNameEditor.vue'
import {applicationName,applicationIdentifier} from '../../shared/applicationName.js'
import ArtifactRetention from '../components/ArtifactRetention.vue'
import BuildRecordsTable from '../components/BuildRecordsTable.vue'
import {buildScroll} from '../buildNavigation.js'
import {readReleaseLink} from '../releaseLink.js'
import {createReleasePolling, activeReleaseStatuses} from '../releasePolling.js'
import {onBeforeRouteLeave, onBeforeRouteUpdate} from 'vue-router'
import PageHeader from "../../shared/components/PageHeader.vue"
import HelpTip from '../../shared/components/HelpTip.vue'
import BuildEnvironmentEditor from '../components/BuildEnvironmentEditor.vue'
import {buildImageSourceError} from '../buildImageSource.js'
import JavaCompileFields from '../components/JavaCompileFields.vue'
import GitBranchSelect from '../components/GitBranchSelect.vue'
import ImageBuildEditor from '../components/ImageBuildEditor.vue'
import {imageBuildError, imageCompiler} from '../imageBuildModel.js'
import ApplicationDeliveryDefaults from '../components/ApplicationDeliveryDefaults.vue'
import ApplicationResourcesEditor from '../components/ApplicationResourcesEditor.vue'
import {ref, computed, nextTick, onUnmounted, onBeforeUnmount, watch, inject} from 'vue'
import {useRoute, useRouter} from 'vue-router'
import ReleasePolicyEditor from '../components/ReleasePolicyEditor.vue'
import DockerRuntimeFields from '../components/DockerRuntimeFields.vue'
import RuntimeEnvironmentEditor from '../components/RuntimeEnvironmentEditor.vue'
import {runtimeEnvironmentUpdate} from '../runtimeEnvironment.js'
import Stages from '../../shared/components/Stages.vue'
import {toast,dismissToast} from '../../shared/composables/toast.js'




const route = useRoute(), router = useRouter()
const globalEnvironment = inject('globalEnvironment', ref(''))
const setPlatformContext = inject('setPlatformContext', () => {})
const name = computed(() => route.params.name)
const showAppResources=ref(false)
if (name.value) setPlatformContext({ service: String(name.value) })
const tab = computed(() => ({ hist: 'build', dep: 'release', cfg: 'advanced',resources:'settings' }[route.params.tab] || route.params.tab || 'overview'))
const settingsSection=ref('source'),settingsReturn=ref(route.query.settings_return==='/'?'/':'build'),launchMode=ref('build'),launchEnvironment=ref(''),showPublishForm=ref(false),me=ref({})
const repairingBuild=ref(false)
const detailScroll=buildScroll('detail:'+name.value)
// Identity belongs to the platform, not /api/plugins/deploy/api/me.
fetch('/api/me').then(r=>{if(!r.ok)throw Error('identity unavailable');return r.json()}).then(v=>me.value=v).catch(()=>{})
const runs = ref([]), latest = ref(null)
const builds = ref([]), artifacts = ref([]), deploys = ref([]), releaseOrders = ref([]), environments = ref([]), targets = ref([])
const releaseStateReady=ref(false)
const deploymentHosts=ref([]),hostReadError=ref(''),targetHostMode=ref('direct'),targetDraftBaseline=ref('')
const targetDraftSnapshot=()=>JSON.stringify({form:targetForm.value,mode:targetHostMode.value})
const targetHostOptions=computed(()=>environmentHostOptions(deploymentHosts.value,targetForm.value.environment))
const delivery = ref({ runner: 'local', variable_groups: [] }), connections = ref([]), variableGroups = ref([]), runners = ref([]), hostGroups = ref([])
const savedBuildConfiguration = ref(''), deliveryLoaded = ref(false), savingBuildConfiguration = ref(false)
const showBuildConfiguration = ref(false), buildConfigurationBackup = ref(null)
const documentBuildEnvironments=ref([])
function openApplicationDocuments(){const hash='#/documents?service='+encodeURIComponent(name.value);if(window.parent!==window)window.parent.postMessage({type:'workspace.open',plugin:'api-center',hash},location.origin);else window.open('/api/plugins/api-center/ui'+hash,'_blank','noopener')}
const svc = ref(null), stepDefs = ref([]), steps = ref([])
const showNameEditor=ref(false),deletingApplication=ref(false)
watch(()=>svc.value?.display_name,()=>{if(svc.value?.name===name.value&&window.parent!==window)window.parent.postMessage({type:'workspace.label',label:applicationName(svc.value)},location.origin)})
async function deleteApplication(){
 if(deletingApplication.value||savingBuildConfiguration.value||savingTarget.value||savingSteps.value)return
 try{await confirmDialog.confirm(`删除应用「${applicationName(svc.value)}」？构建、制品和发布历史保留，不停止已部署的服务。当前未保存配置将放弃。`,'删除应用',{confirmButtonText:'确认删除',cancelButtonText:'取消'})}catch{return}
 deletingApplication.value=true
 try{const r=await fetch('api/services/'+encodeURIComponent(name.value),{method:'DELETE'});const body=await r.json().catch(()=>({}));if(!r.ok)return toast(body.error||'删除失败','error');showTarget.value=false;buildConfigurationBackup.value=null;savedBuildConfiguration.value='';steps.value=JSON.parse(JSON.stringify(svc.value?.steps||[]));setPlatformContext({service:''});toast(body.catalog_detached===false?'部署配置已删除，目录关联尚未同步；可到应用中心删除目录应用以重新核对。历史记录保留':'应用配置已删除，历史记录保留',body.catalog_detached===false?'warning':'success');await router.push('/')}catch{toast('删除结果未确认，请返回列表核对','error')}finally{deletingApplication.value=false}
}
function applicationRenamed(app){
  svc.value.display_name=app.display_name
  if(buildConfigurationBackup.value)buildConfigurationBackup.value.service.display_name=app.display_name
  if(savedBuildConfiguration.value){const saved=JSON.parse(savedBuildConfiguration.value);saved.service.display_name=app.display_name;savedBuildConfiguration.value=JSON.stringify(saved)}
  if(window.parent!==window)window.parent.postMessage({type:'workspace.label',label:applicationName(app)},location.origin)
}
const buildTagsText = ref('')
const newStep = ref('')
const savingSteps=ref(false),advancedNoticeShown=ref(false)
const artifactID = ref(''), deployEnv = ref(''), reason = ref('')
const showDeployConfirm = ref(false), deploying = ref(false)
const publishEnvironment=ref(''),publishLoading=ref(false),publishLoadError=ref(''),releaseRecords=ref(null)
const buildCompatibility=ref(null),checkingBuildCompatibility=ref(false)
let compatibilitySequence=0,compatibilityController
watch(()=>[showPublishForm.value,name.value,artifactID.value,publishEnvironment.value],async([show,service,id,environment])=>{
 const sequence=++compatibilitySequence;compatibilityController?.abort();buildCompatibility.value=null;checkingBuildCompatibility.value=false
 if(!show||!id||!environment)return
 compatibilityController=new AbortController();checkingBuildCompatibility.value=true
 try{const r=await fetch(`api/artifacts/${encodeURIComponent(id)}/build-compatibility?`+new URLSearchParams({environment}),{signal:compatibilityController.signal});const data=await r.json();if(!r.ok||typeof data.compatible!=='boolean')throw Error(data.error||'构建方案校验失败');if(sequence===compatibilitySequence){buildCompatibility.value=data;if(!data.compatible)toast(data.reason||'制品与目标环境不兼容，请核对构建方案','warning',{duration:8000})}}
 catch(e){if(sequence===compatibilitySequence&&e.name!=='AbortError'){buildCompatibility.value={compatible:false,reason:e.message};toast(e.message,'error',{duration:8000})}}
 finally{if(sequence===compatibilitySequence)checkingBuildCompatibility.value=false}
})
onBeforeUnmount(()=>{compatibilitySequence++;compatibilityController?.abort()})
const releaseTarget=computed(()=>targets.value.find(t=>t.environment===publishEnvironment.value))
const releaseBusyOrder=computed(()=>releaseOrders.value.find(o=>o.environment===publishEnvironment.value&&activeReleaseStatuses.includes(o.status)))
const releaseBusyDeploy=computed(()=>deploys.value.find(d=>d.environment===publishEnvironment.value&&d.status==='running'))
const manualDrafts=ref({}),manualPublicationBusy=ref(false)
const manualPublicationDirty=computed(()=>Object.values(manualDrafts.value).some(d=>d.comment&&!d.recorded))
onBeforeRouteLeave(async()=>{if(manualPublicationBusy.value)return false;if(manualPublicationDirty.value&&!await confirmAction('发布结果尚未登记，确认离开？'))return false})
function unloadManual(event){if(instanceOperating.value||manualPublicationDirty.value||manualPublicationBusy.value){event.preventDefault();event.returnValue=''}}
window.addEventListener('beforeunload',unloadManual);onBeforeUnmount(()=>window.removeEventListener('beforeunload',unloadManual))
function manualDraft(order){return manualDrafts.value[order.id]||(manualDrafts.value[order.id]={})}
const targetModeDrafts=ref({})
function changeTargetType(value){switchDeploymentTarget(targetForm.value,value,targetModeDrafts.value);targetSection.value='basic';targetForm.value.deploy_config||={};if(value==='ssh-java-bundle'){targetForm.value.deploy_config.java_mode='managed';targetForm.value.deploy_config.java_bundle_jar||=bundleParams(svc.value).MAIN_JAR;delete targetForm.value.deploy_config.java_workdir}if(value==='script'){targetForm.value.deploy_config.script_runner||=delivery.value?.runner||'local';targetForm.value.deploy_config.script_timeout_seconds||='600';targetForm.value.deploy_config.script_arguments||='[]'}if(value==='manual'){targetForm.value.deploy_config.manual_artifact_kind||=(svc.value?.artifact_type==='image'&&['maven','gradle'].includes(svc.value?.build_tool)?'jar':({'java-bundle':'java-bundle',jar:'jar',image:'oci-image',static:'static-archive'}[svc.value?.artifact_type]||'jar'));targetForm.value.deploy_config.manual_instructions||='下载审核制品，核对 SHA-256 后发布，并检查接口与日志。'}}
const targetTypeOptions=computed(()=>deploymentTargetOptions(svc.value,artifacts.value))
watch(publishEnvironment,environment=>{const selected=artifacts.value.find(a=>String(a.id)===String(artifactID.value));const target=targets.value.find(t=>t.environment===environment);if(selected&&!artifactKindMatchesTarget(selected,target,svc.value))artifactID.value=''})
const showDeployLog = ref(false), deployLog = ref('')
let deployLogID = null
let logReturnRelease = null
const showTarget = ref(false), targetForm = ref({}),targetSection=ref('basic'),savingTarget=ref(false)
const targetDeliveryDefaults=ref(null)
const runtimeVariablesEditor=ref(null)
const runtimeWorkingDirEditor = ref(null)
let invalidRuntimeWorkdir=false
let invalidBundleJar=false
const recoveryBusy=ref(false)
const showReleaseDetail = ref(false), activeRelease = ref(null)
const releaseNotice=ref(null)
async function closeReleaseDetail(){if(recoveryBusy.value)return;if(manualPublicationBusy.value)return;if(activeRelease.value&&manualDrafts.value[activeRelease.value.id]?.comment&&!manualDrafts.value[activeRelease.value.id]?.recorded&&!await confirmAction("发布结果尚未登记，关闭后保留本页草稿？"))return;if(await releaseNotice.value?.canLeave()===false)return;showReleaseDetail.value=false}
const showInstances = ref(false), instanceTarget = ref(null), instanceOperating = ref(false)
watch(()=>name.value,()=>{showInstances.value=false;instanceTarget.value=null})
const showOneClickDeploy = ref(false), oneClickDeploying = ref(false)
const showBuildRun = ref(false), runBranch = ref(''), startingBuild = ref(false)
const oneClickForm = ref({ environment: '', branch: '', reason: '' })
let timer = null, deployLogTimer = null
const releasePolling=createReleasePolling({
  key:()=>String(name.value)+':'+tab.value,
  enabled:()=>showBuildRun.value||showReleaseDetail.value||showPublishForm.value||['overview','environment','release'].includes(tab.value),
  read:()=>Promise.all([`api/releases?service=${encodeURIComponent(name.value)}&limit=100`,`api/deploys?service=${encodeURIComponent(name.value)}&limit=500`].map(async url=>{const r=await fetch(url);if(!r.ok)throw Error('发布状态读取失败，请稍后重试');return r.json()})),
  apply:(orders,items)=>{
    for(const order of orders){const previous=releaseOrders.value.find(v=>v.id===order.id);if(previous&&activeReleaseStatuses.includes(previous.status)&&!activeReleaseStatuses.includes(order.status))toast(`发布单 ${releaseOrderLabel(order)} 已结束：${statusText(order.status)}。${order.manual_publication?'可在发布记录中查看人工登记结果。':'可在发布记录中查看实例日志。'}`,order.status==='success'?'success':'warning')}
    releaseOrders.value=orders;deploys.value=items;releaseStateReady.value=true
    if(activeRelease.value)activeRelease.value=orders.find(o=>o.id===activeRelease.value.id)||activeRelease.value
  },
  error:e=>{releaseStateReady.value=false;toast(e.message,'error')},
})

const statusText = s => ({ waiting_manual:'待手动发布', queued:'排队中',pending_approval:'待审批',waiting_next_batch:'等待下一批',running: '运行中', waiting_deploy: '等待部署', success: '成功', failed: '失败', needs_review:'待核对',timed_out:'超时待核对', interrupted: '已中断', canceled: '已取消', rejected:'已拒绝',rolling_back:'回滚中','rollback-failed':'回滚失败', 'rolled-back': '已回滚' })[s] || s
const statusIcon = s => s === 'success' ? '✓' : ['running','needs_review'].includes(s) ? '●' : s === 'waiting_deploy' ? '⏸' : '✗'
const statusClass = s => ['success'].includes(s) ? 'st-ok' : ['needs_review','running','queued','pending_approval','waiting_deploy','waiting_next_batch','rolling_back','waiting_manual'].includes(s) ? 'st-run' : 'st-fail'
const selectedArtifact = computed(() => artifacts.value.find(a => String(a.id) === String(artifactID.value)))
const selectedVersion = computed(() => selectedArtifact.value?.version || '')
const selectedTarget = computed(() => targets.value.find(t => t.environment === deployEnv.value))
const releaseEnvironmentOptions=computed(()=>{const options=targets.value.map(t=>({value:t.environment,label:(t.protected?'🔒 ':'')+(t.display_name||environments.value.find(e=>e.name===t.environment)?.display_name||t.environment)+(t.target_type==='script'?' · 脚本发布':t.target_type==='manual'?' · 手动发布':'（'+(t.hosts?.length||0)+'台）')}));if(deployEnv.value&&!options.some(o=>o.value===deployEnv.value))options.unshift({value:deployEnv.value,label:(environments.value.find(e=>e.name===deployEnv.value)?.display_name||deployEnv.value)+' · 未配置发布目标'});return options})
const oneClickTarget = computed(() => targets.value.find(t => t.environment === oneClickForm.value.environment))
const isManagedTarget = t => t?.target_type === 'aliyun-sae'
const isManagedJavaTarget = t => ['ssh-jar','ssh-java-bundle'].includes(t?.target_type) && t?.deploy_config?.java_mode === 'managed'
const targetReady = deploymentTargetReady
const releasePolicyText=t=>t?.target_type==='script'?'Runner 执行发布脚本':t?.target_type==='manual'?'下载后手动发布':t?.deploy_config?.release_mode==='canary'?`金丝雀 · 首批 ${t.deploy_config.canary_count||1} 实例 · 观察 ${t.deploy_config.observe_seconds??300}s · ${t.target_type==='aliyun-sae'?'剩余 '+(t.deploy_config.batch_count||2)+' 批（人工确认）':'后续每批 '+t.batch_size+' 台'}`:t?.target_type==='aliyun-sae'?t.deploy_config?.release_mode==='batch'?`SAE 分 ${t.deploy_config.batch_count||2} 批 · ${t.deploy_config.batch_mode==='auto'?'自动':'人工确认'}`:'SAE 原有发布策略':`分批 · 每批 ${t?.batch_size||1} 台 / 并发 ${t?.max_parallel||1}${t?.pause_between_batches?' / 批次暂停':''}`
const targetResource = t => t?.target_type==='script'?('脚本发布 · '+(t.deploy_config?.script_runner||'未配置 Runner')):t?.target_type==='manual'?('手动发布 · '+(t.deploy_config?.manual_assignee||'本次发起人')):isManagedTarget(t) ? `SAE ${t.deploy_config?.app_id || '未配置'} · ${t.deploy_config?.region || '未知地域'}` : t?.host_group ? `${t.host_group} · ${t.hosts?.length || 0} 台主机` : t?.hosts?.length ? `已选 ${t.hosts.length} 台主机` : '未选择发布主机'
const saeConnections = computed(() => connections.value.filter(c => c.kind === 'aliyun-sae'))
const activeDeploy = computed(() => deploys.value.find(d => d.status === 'running' && d.environment === deployEnv.value))
const activeOrder = computed(() => releaseOrders.value.find(d => activeReleaseStatuses.includes(d.status) && d.environment === deployEnv.value))
const globalEnvironmentMissing = computed(() => !!globalEnvironment.value && !targets.value.some(t => t.environment === globalEnvironment.value))
const environmentTargetsLoaded=ref(false),notifiedEnvironmentContexts=new Set()
let environmentNotice
function environmentTargetsReady(){environmentTargetsLoaded.value=true}
watch(()=>environmentTargetsLoaded.value&&globalEnvironmentMissing.value?`${name.value}:${globalEnvironment.value}`:'',key=>{
  dismissToast(environmentNotice);environmentNotice=null
  if(key&&!notifiedEnvironmentContexts.has(key)){
    notifiedEnvironmentContexts.add(key)
    environmentNotice=toast(`当前环境 ${globalEnvironment.value} 尚未配置该应用的发布目标，可在“环境”中配置。`,'warning',{duration:6000})
  }
})
onUnmounted(()=>dismissToast(environmentNotice))
const typeMeta = computed(() => ({
  'java-bundle': {icon:'📦',label:'Java 整包'},
  jar: { icon: '☕', label: 'Java JAR' },
  static: { icon: '🌐', label: '前端静态站点' },
  image: { icon: '🐳', label: 'Docker 镜像' },
})[svc.value?.artifact_type] || { icon: '📦', label: svc.value?.artifact_type || '应用' })
const latestArtifact = computed(() => artifacts.value[0])
const artifactOptions = computed(() => artifacts.value.filter(a=>artifactKindMatchesTarget(a,targets.value.find(t=>t.environment===publishEnvironment.value),svc.value)).map(a => ({
  value: String(a.id),
  label: `${({'java-bundle':'Java 整包',jar:'JAR','oci-image':'镜像','docker-image-archive':'镜像离线包','static-archive':'静态包'})[a.kind]||a.kind} · ${a.version} · 制品#${a.id} · ${a.source==='pipeline-run'?'构建 '+artifactBuildLabel(a):a.source+'#'+(a.source_id||'—')} · ${(a.digest || '').replace('sha256:', '').slice(0, 10)} · ${sizeText(a.size)} · ${artifactCreatedLabel(a)}`,
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
    { id: 'release', label: currentEnvironment.value?.display_name || deployEnv.value || '选择环境', value: release ? `${release.version} · ${statusText(release.status)}` : selectedTarget.value ? '尚未发布' : '尚未配置发布目标', state: release?.status === 'success' ? 'done' : ['needs_review','running','queued','pending_approval','waiting_next_batch'].includes(release?.status) ? 'active' : release?.status ? 'error' : targetReady(selectedTarget.value) ? 'ready' : 'todo' },
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
  const [buildData, artifactData, services, environmentData, targetData, groupData, connectionData, hostData] = await Promise.all([
    fetch(`api/builds?service=${name.value}&limit=50`).then(r => r.json()),
    fetch(`api/artifacts?service=${name.value}&limit=100`).then(r => r.json()),
    fetch('api/services').then(r => r.json()),
    fetch('api/environments').then(r => r.json()),
    fetch(`api/services/${name.value}/environments`).then(r => r.json()),
    fetch('api/host-groups').then(r => r.json()),
    fetch('api/connections').then(r => r.json()),
    fetch('api/hosts').then(async r=>{if(!r.ok)throw Error('主机列表读取失败');return {items:await r.json()}}).catch(()=>({error:'主机列表读取失败，请刷新后重试'})),
  ])
  if(requested!==name.value)return
  builds.value = (buildData ?? []).filter(b => b.status === 'success')
  artifacts.value = artifactData ?? []
  svc.value = (services ?? []).find(x => x.name === name.value)
  environments.value = environmentData ?? []
  targets.value = targetData ?? []
  environmentTargetsReady()
  hostGroups.value = groupData ?? []
  deploymentHosts.value=Array.isArray(hostData.items)?hostData.items:[];hostReadError.value=hostData.error||''
  connections.value = connectionData ?? []

  const requestedArtifact = String(route.query.artifact || '')
  if(requestedArtifact){
 let selected=artifacts.value.find(a=>String(a.id)===requestedArtifact)
 if(!selected){const r=await fetch('api/artifacts/'+encodeURIComponent(requestedArtifact));if(r.ok){selected=await r.json();if(selected.service===name.value)artifacts.value.unshift(selected)}}
 if(selected?.service===name.value&&!handledArtifactRequests.has(String(name.value)+':'+requestedArtifact)){handledArtifactRequests.add(String(name.value)+':'+requestedArtifact);artifactID.value=requestedArtifact;publishEnvironment.value=String(route.query.environment||globalEnvironment.value||targets.value[0]?.environment||'');showPublishForm.value=true}
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
  let documentation=[];try{const r=await fetch('api/services/'+encodeURIComponent(requestedName)+'/build-environments');if(r.ok)documentation=(await r.json()).documentation_environments||[]}catch{}
  if(requestedName!==name.value)return
  svc.value = all.find(x => x.name === requestedName)
  buildTagsText.value = (svc.value?.tags || []).join(', ')
  documentBuildEnvironments.value=documentation
  stepDefs.value = definitions
  steps.value = JSON.parse(JSON.stringify(svc.value?.steps ?? []))
  rememberBuildConfiguration()
}
async function setTab(t){
 if(instanceOperating.value)return toast('实例操作正在进行，请等待结果','warning')
 if(savingBuildConfiguration.value||savingTarget.value||savingSteps.value)return toast('正在保存，请稍候','warning')
 if(tab.value==='advanced'&&advancedStepsDirty.value&&t!=='advanced'){if(!await confirmAction('流水线有未保存修改，放弃修改并离开？'))return;steps.value=JSON.parse(JSON.stringify(svc.value?.steps||[]))}
 if(showTarget.value){if(!await confirmAction('环境配置尚未保存，放弃修改并离开？'))return;showTarget.value=false}
 if(tab.value==='settings'&&buildConfigurationDirty.value&&t!=='settings'){
  if(!await confirmAction('有未保存配置，放弃修改并离开？'))return
  if(buildConfigurationBackup.value){svc.value=buildConfigurationBackup.value.service;delivery.value=buildConfigurationBackup.value.delivery;buildTagsText.value=buildConfigurationBackup.value.tags}
  rememberBuildConfiguration()
 }
 detailScroll.remember();router.replace({path:'/svc/'+encodeURIComponent(name.value)+'/'+t,query:route.query})
}
onBeforeRouteLeave(async ()=>{if(instanceOperating.value||savingSteps.value||savingBuildConfiguration.value||savingTarget.value)return false;if((showTarget.value||(tab.value==='settings'&&buildConfigurationDirty.value)||(tab.value==='advanced'&&advancedStepsDirty.value))&&!await confirmAction('有未保存配置，放弃修改并离开？'))return false})
onBeforeRouteUpdate(async to=>{if(instanceOperating.value||savingSteps.value||savingBuildConfiguration.value||savingTarget.value)return false;if(showTarget.value&&!await confirmAction('环境配置尚未保存，放弃修改并离开？'))return false;if((to.params.name!==name.value||to.params.tab!=='settings')&&tab.value==='settings'&&buildConfigurationDirty.value&&!await confirmAction('有未保存配置，放弃修改并离开？'))return false;if((to.params.name!==name.value||to.params.tab!=='advanced')&&tab.value==='advanced'&&advancedStepsDirty.value&&!await confirmAction('流水线有未保存修改，放弃修改并离开？'))return false})
function unloadBuild(event){if(showTarget.value||(tab.value==='settings'&&buildConfigurationDirty.value)||(tab.value==='advanced'&&advancedStepsDirty.value)){event.preventDefault();event.returnValue=''}}
window.addEventListener('beforeunload',unloadBuild)
onUnmounted(()=>window.removeEventListener('beforeunload',unloadBuild))
function loadTab(t) {
  clearTimeout(timer); releasePolling.stop()
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
  releasePolling.stop();releaseStateReady.value=false;releaseOrders.value=[];deploys.value=[]
  repairingBuild.value=false
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
onUnmounted(() => { clearTimeout(timer); clearTimeout(deployLogTimer); releasePolling.stop() })

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
function updateDockerBuildParams(params) {
  if (!svc.value.steps?.length) svc.value.steps = [{ step: 'git-checkout', params: { GIT_URL: svc.value.git_url, BRANCH: svc.value.branch } }]
  let step = svc.value.steps.find(s => s.step === 'docker-build')
  if (!step) { step = {step:'docker-build',params:{IMAGE_NAME:svc.value.image_name,CONTEXT:'.',DOCKERFILE:'Dockerfile'}}; svc.value.steps.push(step) }
  step.params = { ...step.params, ...params }
}
const imageDeliveryMode = computed({
  get: () => svc.value?.steps?.find(s => s.step === 'docker-build')?.params?.PUSH === 'false' ? 'archive' : 'registry',
  set: mode => updateDockerBuildParams({PUSH:mode==='archive'?'false':'true'}),
})
const dockerfileSource = computed({get:()=>svc.value?.steps?.find(s=>s.step==='docker-build')?.params?.DOCKERFILE_SOURCE||'repository',set:v=>updateDockerBuildParams({DOCKERFILE_SOURCE:v})})
const dockerfilePath = computed({get:()=>svc.value?.steps?.find(s=>s.step==='docker-build')?.params?.DOCKERFILE||'Dockerfile',set:v=>updateDockerBuildParams({DOCKERFILE:v})})
const dockerfileContent = computed({get:()=>svc.value?.steps?.find(s=>s.step==='docker-build')?.params?.DOCKERFILE_CONTENT||'',set:v=>updateDockerBuildParams({DOCKERFILE_CONTENT:v})})
const dockerfileTemplateID = computed({get:()=>svc.value?.steps?.find(s=>s.step==='docker-build')?.params?.DOCKERFILE_TEMPLATE||'java21',set:v=>updateDockerBuildParams({DOCKERFILE_TEMPLATE:v,DOCKERFILE_ARTIFACT_PATH:v==='nginx'?'dist':'app.jar'})})
const dockerfileArtifactPath = computed({get:()=>svc.value?.steps?.find(s=>s.step==='docker-build')?.params?.DOCKERFILE_ARTIFACT_PATH||(dockerfileTemplateID.value==='nginx'?'dist':'app.jar'),set:v=>updateDockerBuildParams({DOCKERFILE_ARTIFACT_PATH:v})})
const dockerfileTemplatePreview = computed(()=>dockerfileTemplateID.value==='nginx'?`FROM nginx:stable\nCOPY ${JSON.stringify([dockerfileArtifactPath.value,'/usr/share/nginx/html/'])}\nEXPOSE 80\n`:`FROM eclipse-temurin:21-jre-jammy\nWORKDIR /app\nCOPY ${JSON.stringify([dockerfileArtifactPath.value,'/app/app.jar'])}\nEXPOSE 8888\nENTRYPOINT ["java","-jar","/app/app.jar"]\n`)
async function fillJavaDockerfile() {
  if(dockerfileContent.value.trim()&&!await confirmAction('替换当前 Dockerfile 草稿？未保存的内容将被示例覆盖。'))return
  dockerfileContent.value='FROM eclipse-temurin:21-jre-jammy\nWORKDIR /app\n# 替换为前置构建生成的实际 JAR 路径\nCOPY path/to/app.jar /app/app.jar\nEXPOSE 8888\nENTRYPOINT ["java", "-jar", "/app/app.jar"]\n'
}
const repositoryLabel = computed(() => {
  const url = String(svc.value?.git_url || '').replace(/\/$/, '')
  return url ? url.split(/[/:]/).pop()?.replace(/\.git$/, '') || url : (svc.value?.workdir || '未配置代码来源')
})
const buildCommandSummary = computed(() => {
  if (!svc.value) return '加载中'
  if (svc.value.artifact_type === 'java-bundle') return `${svc.value.build_cmd} → 完整目录归档`
  if (svc.value.artifact_type === 'static') return svc.value.build_cmd || '未配置构建命令'
  if (svc.value.artifact_type === 'image') return `${imageCompiler(svc.value)==='dockerfile'?'Dockerfile 自行编译':imageCompiler(svc.value)==='node'?'Node 编译':'Java '+imageCompiler(svc.value)+' 编译'} → Docker build → ${svc.value.image_name || '未配置镜像名'}`
  return svc.value.build_tool === 'maven'
    ? `${svc.value.maven_executable || 'auto'} ${svc.value.maven_goals || 'clean package'}`
    : `${svc.value.gradle_executable || 'auto'} ${svc.value.gradle_tasks || 'clean build'}`
})
const buildEnvironmentSummary = computed(() => {
  const environment = delivery.value?.build_environment || { mode: 'local' }
  return environment.mode === 'container'
    ? `隔离容器 · ${environment.preset === 'custom' ? (environment.image || '自定义镜像') : (environment.preset || '未选择工具环境')} · ${environment.network === 'host' ? '宿主机网络' : '默认网络'}${environment.preset === 'custom' && environment.image_source === 'dockerfile' ? ' · 镜像缺失时按 Dockerfile 构建' : ''}`
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
async function saveAndPrepareBuild() {
  if (savingBuildConfiguration.value || !(await saveBuildConfiguration(true))) return
  buildConfigurationBackup.value=null;showBuildConfiguration.value=false
  launchMode.value='build';showBuildRun.value=true
}
let handledBuildEntry=''
watch(()=>[name.value,tab.value,svc.value?.name,deliveryLoaded.value,route.query.settings_section,route.query.build_repair,route.query.prepare_build],async()=>{
  if(!svc.value||svc.value.name!==name.value||!deliveryLoaded.value)return
  const key=route.fullPath
  if(key===handledBuildEntry)return
  if(tab.value==='settings'&&route.query.settings_section==='command'){
    handledBuildEntry=key;settingsSection.value='command'
    repairingBuild.value=route.query.build_repair==='gradle'
    await nextTick()
    const field=document.querySelector(route.query.build_repair==='maven-arguments'?'[data-maven-arguments]':'[data-build-executable="gradle"]')
    field?.scrollIntoView({block:'center'})
    focusControl(field?.querySelector('input,textarea'),{preventScroll:true})
  }else if(tab.value==='build'&&route.query.prepare_build==='1'){
    handledBuildEntry=key
    const {prepare_build,...query}=route.query
    await router.replace({path:route.path,query})
    if(name.value!==svc.value?.name)return
    launchMode.value='build';showBuildRun.value=true
  }
},{flush:'post'})
async function openAdvancedBuildConfiguration() {
  if(buildConfigurationDirty.value)return toast('先保存配置，再进入高级流水线；当前修改仍保留。','warning')
  buildConfigurationBackup.value = null
  showBuildConfiguration.value = false
  setTab('advanced')
}
function buildValidationError() {
  if(javaBundleError(svc.value||{}))return javaBundleError(svc.value)
  if(svc.value?.build_tool==='maven'&&!validMavenThreads(svc.value.maven_threads))return 'Maven 并行线程须为正整数或 CPU 倍数，如 4、1C、2.5C'
  if (!svc.value?.git_url?.trim() && !svc.value?.workdir?.trim()) return '请填写 Git 仓库或本地工作目录'
  if (svc.value?.artifact_type === 'jar' && svc.value?.build_tool === 'maven' && (!svc.value.maven_executable?.trim() || !svc.value.maven_goals?.trim())) return '请填写 Maven 执行方式和 Goals'
  if (svc.value?.artifact_type === 'jar' && svc.value?.build_tool === 'gradle' && (!svc.value.gradle_executable?.trim() || !svc.value.gradle_tasks?.trim())) return '请填写 Gradle 执行方式和任务'
  if (svc.value?.artifact_type === 'image' && !svc.value.image_name?.trim()) return '请填写镜像名称'
  if(svc.value?.artifact_type==='image'&&imageBuildError(svc.value))return imageBuildError(svc.value)
  if (svc.value?.artifact_type === 'image' && dockerfileSource.value === 'platform' && (!dockerfileContent.value.trim() || new TextEncoder().encode(dockerfileContent.value).length > 32768)) return '平台托管 Dockerfile 必须非空，且不超过 32 KiB'
  if (buildImageSourceError(delivery.value.build_environment)) return buildImageSourceError(delivery.value.build_environment)
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
  if (validationError) {
    let selector=''
    if(javaBundleError(svc.value||{})) {
      settingsSection.value='command'
      selector=validationError.includes('发布目录')?'[aria-label="整包发布目录"]':validationError.includes('主 JAR')?'[aria-label="包内主 JAR"]':validationError.includes('必需文件')?'[aria-label="整包必需路径"]':'[aria-label="整包构建命令"]'
    } else if(svc.value?.build_tool==='maven'&&!validMavenThreads(svc.value.maven_threads)) {settingsSection.value='command';selector='[data-maven-threads] input'}
    await nextTick()
    if(selector)focusControl(document.querySelector(selector))
    toast(validationError, 'warning');return false
  }
  savingBuildConfiguration.value = true
  try {
    const body = { ...svc.value, tags: [...new Set(buildTagsText.value.split(/[,，\n]/).map(value => value.trim()).filter(Boolean))].slice(0, 12) }
    const serviceResponse = await fetch('api/services', { method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify(body) })
    const serviceResult = await serviceResponse.json().catch(() => ({}))
    if (!serviceResponse.ok) {
      const message=serviceResult.error || '应用构建配置保存失败'
      if (/Maven (附加参数|模块选择)/.test(message)) {
        settingsSection.value='command';await nextTick()
        const field=document.querySelector('[data-maven-arguments] textarea')
        field?.scrollIntoView({block:'center'});focusControl(field)
      }
      toast(message, 'error'); return false
    }
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
const handledArtifactRequests=new Set()
function requestDeploy() {
  if(!buildCompatibility.value?.compatible)return toast(checkingBuildCompatibility.value?'正在校验环境构建方案':buildCompatibility.value?.reason||'请先选择制品与环境并完成构建方案校验','warning')
  if(publishLoadError.value)return toast(publishLoadError.value,'warning')
  if(publishLoading.value||deploying.value||!releaseStateReady.value)return toast('正在核对发布数据，请稍候','warning')
  if (!artifactID.value || !publishEnvironment.value) return toast('选择具体制品与目标环境')
  if (!selectedArtifact.value) return toast('所选制品已不可用，请重新选择','warning')
  if (!targetReady(releaseTarget.value)) return toast('该应用环境尚未配置完整的发布资源', 'warning')
  if (releaseBusyDeploy.value) return toast(`该环境已有部署 #${releaseBusyDeploy.value.id} 正在运行`, 'warning')
  if (releaseBusyOrder.value) return toast(`该环境已有发布单 ${releaseOrderLabel(releaseBusyOrder.value)} 正在处理`, 'warning')
  showDeployConfirm.value = true
}
function cancelPublish(){if(deploying.value)return;showPublishForm.value=false;showDeployConfirm.value=false}
function configureReleaseBuildVariant(){if(deploying.value||!releaseTarget.value)return;const target=releaseTarget.value;cancelPublish();editTarget(target);targetSection.value='code'}
async function openPublish(artifact=null,target=null){
  if(instanceOperating.value||publishLoading.value||deploying.value)return
  const service=String(name.value)
  reason.value='';artifactID.value=artifact?String(artifact.id):''
  publishEnvironment.value=target?.environment||deployEnv.value||globalEnvironment.value||''
  publishLoadError.value='';showDeployConfirm.value=false;showPublishForm.value=true;publishLoading.value=true
  try{
    await Promise.all([loadDeployData(),loadReleaseOrders(),loadDeployHist(),loadDelivery()])
    if(String(name.value)!==service)return
    if(artifact&&!artifacts.value.some(a=>String(a.id)===String(artifact.id)))artifacts.value.unshift(artifact)
    if(!publishEnvironment.value)publishEnvironment.value=targets.value[0]?.environment||''
  }catch{publishLoadError.value='发布数据读取失败，请关闭后重试；未发起发布';toast(publishLoadError.value,'error')}
  finally{publishLoading.value=false}
}
function releaseTo(target){openPublish(null,target)}
const fmtTime = formatUnixTime
async function deploy() {
  if(deploying.value)return
  if(!buildCompatibility.value?.compatible)return toast(buildCompatibility.value?.reason||'构建方案尚未校验通过','warning')
  if(!showPublishForm.value||!showDeployConfirm.value||!releaseStateReady.value||!selectedArtifact.value||!targetReady(releaseTarget.value)||releaseBusyDeploy.value||releaseBusyOrder.value)return toast('发布条件已变化，请返回核对','warning')
  if(releaseTarget.value.require_reason&&!reason.value.trim()||releaseTarget.value.protected&&!releaseTarget.value.approvers)return toast('请补齐发布原因或审批配置','warning')
  deploying.value = true
  try {
    const res = await fetch('api/releases', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ service: name.value, artifact_id: Number(artifactID.value), environment: publishEnvironment.value, reason: reason.value }) })
    const r = await res.json()
    if (!res.ok || !r.id) return toast('发起失败: ' + (r.error || '未知错误'), 'error')
    showDeployConfirm.value = false
    showPublishForm.value = false
    toast(r.status === 'pending_approval' ? `发布单 ${releaseOrderLabel(r)} 已提交审批` : `发布单 ${releaseOrderLabel(r)} 已创建`, 'success')
    await Promise.all([loadDeployHist(), loadReleaseOrders()])
    await releaseRecords.value?.refresh()
  } catch{toast('发布结果未确认，请先查看发布记录，勿直接重复提交','error')
  } finally {
    deploying.value = false
  }
}

async function loadReleaseOrders() {
  return releasePolling.refresh()
}
async function releaseAction(order, action) {
  let body = {}
  if (action === 'rollback') {
    const value = await promptAction('填写整单回滚原因：')
    if (value === null) return
    body = { reason: value }
  }
  const res = await fetch(`api/releases/${order.id}/${action}`, { method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify(body) })
  const data = await res.json().catch(() => ({}))
  if (!res.ok) return toast(data.error || '操作失败', 'error')
  toast('操作已提交', 'success'); loadReleaseOrders(); loadDeployHist()
}
async function refreshRecovery(){await releasePolling.refresh();releaseRecords.value?.refresh()}
function openRelease(order) { activeRelease.value=order;showReleaseDetail.value=true }
let releaseLinkSequence=0,handledReleaseLink=''
watch(()=>[name.value,route.query.release,releaseStateReady.value],async([service,id,ready])=>{
 const seq=++releaseLinkSequence
 if(!id||!ready)return
 const key=String(service)+':'+String(id)
 if(handledReleaseLink===key)return
 try{
  const order=await readReleaseLink(id,service,async path=>{const r=await fetch(path);if(!r.ok)throw Error('发布单读取失败，请返回原记录重试');return r.json()})
  if(seq!==releaseLinkSequence||name.value!==service||String(route.query.release)!==String(id))return
  handledReleaseLink=key;openRelease(order)
 }catch(e){if(seq===releaseLinkSequence)toast(e.message,'error')}
})

const deployOf = id => deploys.value.find(d=>d.id===id)
function publishArtifact(a) {
 openPublish(a)
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
const detectingJpom=ref(false)
const canDetectJpom=computed(()=>targetForm.value.target_type==='ssh-jar'&&jpomEnvironmentAllowed(environments.value,targetForm.value.environment,targetForm.value.protected))
const jpomDetector=createJpomDetector({
 current:()=>showTarget.value?targetDraftSnapshot():null,
 busy:value=>detectingJpom.value=value,
 notify:toast,
 apply:next=>targetForm.value=next,
 read:async({form,mode},signal)=>{
  const selected=selectedDeploymentHosts(form,mode,hostGroups.value,deploymentHosts.value)
  const response=await fetch(`api/services/${encodeURIComponent(name.value)}/environments/${encodeURIComponent(form.environment)}/java-detection`,{method:'POST',signal,headers:{'Content-Type':'application/json'},body:JSON.stringify({...form,...selected})})
  const data=await response.json().catch(()=>({}));if(!response.ok)throw Error(data.error||(response.status===401?'登录已过期，请重新登录后识别':'检测未完成，请重试'))
  return data
 }
})
watch(showTarget,value=>{if(!value)jpomDetector.cancel()})
onBeforeUnmount(()=>jpomDetector.cancel())
function detectJpom(){if(!detectingJpom.value&&canDetectJpom.value)jpomDetector.detect()}
async function cancelTarget(){if(savingTarget.value)return;if((targetDeliveryDefaults.value?.dirty||targetDraftSnapshot()!==targetDraftBaseline.value)&&!await confirmAction('有未保存配置，放弃修改并离开？'))return;showTarget.value=false}
function editTarget(target) {
  jpomDetector.cancel()
  const available = environments.value.find(e => e.name === globalEnvironment.value && !targets.value.some(t => t.environment === e.name)) || environments.value.find(e => !targets.value.some(t => t.environment === e.name))
  const defaultTarget = defaultDeploymentTarget(svc.value)
  targetModeDrafts.value={}
  const base = target ? { ...target, deploy_config: target.deploy_config || {}, _existing: true } : { environment: available?.name || environments.value[0]?.name || '', _existing: false, target_type: defaultTarget, remote_dir: '', start_cmd: '', stop_cmd: '', health_url: '', deploy_config: {}, strategy: 'rolling', max_parallel: 1, batch_size: 1, pause_between_batches: false, failure_threshold: 0, host_group: '', hosts: [] }
	targetForm.value = { ...JSON.parse(JSON.stringify(base)), runtime_env_text: '', runtime_env_clear: false, runtime_env_delete_keys: [] }
	targetHostMode.value=target?.host_group?'group':'direct'
	if(!target&&targetHostOptions.value.length===1)targetForm.value.hosts=[targetHostOptions.value[0].value]
	if (!target && ['ssh-jar','ssh-java-bundle'].includes(defaultTarget)) targetForm.value.deploy_config = { java_mode: 'managed', java_version: '21', java_jvm_options: '-Xms256m\n-Xmx1024m\n-Dfile.encoding=UTF-8', health_failure_action: 'rollback' }
	if(defaultTarget==='ssh-java-bundle'&&!target)targetForm.value.deploy_config.java_bundle_jar=bundleParams(svc.value).MAIN_JAR
	if (targetForm.value.deploy_config?.java_mode === 'managed') targetForm.value.deploy_config.health_failure_action ||= 'rollback'
  targetDraftBaseline.value=targetDraftSnapshot();targetSection.value='basic';showTarget.value = true
  if(target&&canDetectJpom.value&&shouldAutoDetectJpom(targetForm.value,String(name.value)))nextTick(()=>detectJpom())
}
async function updateTargetEnvironment(value) {
  if(targetDeliveryDefaults.value?.dirty&&!await confirmAction('当前环境的代码与制品策略尚未保存，放弃这部分修改并切换环境？'))return
  targetForm.value.environment = value
  targetForm.value.host_group = '';targetForm.value.hosts=[]
  if (environments.value.find(environment => environment.name === value)?.protected && targetForm.value.deploy_config) targetForm.value.deploy_config.health_failure_action = 'rollback'
}
function updateJavaMode(value) {
  jpomDetector.cancel()
  targetForm.value.deploy_config={...selectJavaMode(targetForm.value.deploy_config,value),java_detection:'manual'}
}

function openInstances(target) {instanceTarget.value=target;showInstances.value=true}
const supportsInstances=supportsInstanceManagement
let invalidJpomField=''
async function saveTarget(){if(savingTarget.value||detectingJpom.value)return;invalidJpomField='';invalidRuntimeWorkdir=false;invalidBundleJar=false;savingTarget.value=true;try{await saveTargetConfiguration()}catch(e){toast('保存未完成，当前环境配置草稿仍保留。','error')}finally{savingTarget.value=false;await nextTick();targetDeliveryDefaults.value?.focusInvalid();if(invalidJpomField)focusControl(document.querySelector('[aria-label="'+invalidJpomField+'" ]'));if(invalidBundleJar)focusControl(document.querySelector('[aria-label="部署包内主 JAR"]'));if(invalidRuntimeWorkdir)focusControl(runtimeWorkingDirEditor.value?.$el?.querySelector('input'))}}
async function saveTargetConfiguration() {
  const f = JSON.parse(JSON.stringify(targetForm.value))
 if(f.target_type==='script'){const error=scriptPublicationError(f.deploy_config);if(error){targetSection.value='basic';toast(error,'warning');await nextTick();const label=error.includes('Runner')?'脚本执行 Runner':error.includes('超时')?'脚本发布超时':error.includes('参数')?'发布脚本参数':'发布脚本';focusControl(document.querySelector('[aria-label="'+label+'"]'));return}}
  if(f.target_type==='ssh-java-bundle'&&(!bundleRelative(f.deploy_config?.java_bundle_jar)||!f.deploy_config.java_bundle_jar.endsWith('.jar'))){targetSection.value='runtime';invalidBundleJar=true;return toast('请填写包内主 JAR 的相对路径','warning')}
  if(f.target_type==='ssh-jar'&&f.deploy_config?.java_mode==='jpom'){
   const error=jpomConfigurationError(f.deploy_config)
   if(error){targetSection.value='runtime';invalidJpomField=error.field;toast(error.message,'warning');return}
  }
  const workdir=String(f.deploy_config?.java_workdir||'').trim()
  if(f.target_type==='ssh-jar'&&f.deploy_config?.java_mode==='managed'&&workdir&&(!workdir.startsWith('/')||workdir.includes('\0'))){targetSection.value='runtime';invalidRuntimeWorkdir=true;return toast('运行工作目录必须是绝对路径','warning')}
  try{f.delivery_default=targetDeliveryDefaults.value?.patch()}catch(e){targetSection.value='code';return toast(e.message,'warning')}
  try {
    delete f.runtime_env
    delete f.runtime_env_patch
    Object.assign(f, runtimeEnvironmentUpdate(f))
  } catch (error) {
    targetSection.value='variables'
    await nextTick();focusControl(runtimeVariablesEditor.value)
    return toast(error.message, 'warning')
  }
  if (f.runtime_env_clear || f.runtime_env_delete_keys?.length) {
    try {
      await confirmDialog.confirm(f.runtime_env_clear ? `确定清空「${f.environment}」全部已保存的运行变量？此操作无法撤销。` : `确定删除以下运行变量？${f.runtime_env_delete_keys.join('、')}。其他变量保留。`, f.runtime_env_clear ? '清空运行变量' : '删除运行变量', {type:'warning', confirmButtonText:'确认删除并保存', cancelButtonText:'返回编辑', closeOnClickModal:false})
    } catch { return }
  }
  delete f.runtime_env_text
  delete f.runtime_env_clear
  delete f.runtime_env_delete_keys
  try{Object.assign(f,selectedDeploymentHosts(f,targetHostMode.value,hostGroups.value,deploymentHosts.value))}catch(error){targetSection.value='basic';await nextTick();toast(error.message,'warning');return}
  if (f.target_type === 'docker-host' && !f.remote_dir) f.remote_dir = '/'
  if (f.target_type === 'aliyun-sae') {
    f.host_group = ''; f.remote_dir = ''; f.start_cmd = ''; f.stop_cmd = ''; f.batch_size = 1; f.max_parallel = 1; f.failure_threshold = 0; f.pause_between_batches = false
    if (!f.environment || !f.deploy_config?.connection || !f.deploy_config?.app_id || !f.deploy_config?.namespace_id) return toast('请配置 SAE 连接、应用 ID 和命名空间 ID', 'warning')
    const connection = saeConnections.value.find(c => c.name === f.deploy_config.connection)
    f.deploy_config.region ||= connection?.config?.region || ''
    if (!f.deploy_config.region) return toast('SAE 地域不能为空', 'warning')
  } else if (f.target_type==='script'){if(!f.environment)return toast('请选择发布环境','warning')} else if (f.target_type==='manual'){if(!f.environment||!f.deploy_config?.manual_instructions?.trim())return toast('请选择环境并填写手动发布说明','warning')} else if (!f.environment || !f.remote_dir || !f.hosts.length) return toast('请选择发布主机并完善部署目录', 'warning')
  const res = await fetch(`api/services/${name.value}/environments/${f.environment}`, { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(f) })
  const body = await res.json()
  if (!res.ok) {targetSection.value=f.target_type==='script'?'basic':'runtime';return toast(body.error || '保存失败', 'error')}
  showTarget.value = false; toast('环境部署配置已保存', 'success'); loadDeployData()
}
async function deleteTarget(target) {
  if (!await confirmAction(`移除「${target.display_name}」的服务部署配置？历史部署记录不会删除。`)) return
  const res = await fetch(`api/services/${name.value}/environments/${target.environment}`, { method: 'DELETE' })
  const body = await res.json()
  if (!res.ok) return toast(body.error || '移除失败', 'error')
  if (deployEnv.value === target.environment) deployEnv.value = ''
  toast('环境部署配置已移除', 'success')
  loadDeployData()
}
async function loadDeployHist() {
  return releasePolling.refresh()
}
async function openDeployLog(d) {
  if(showReleaseDetail.value){
    if(await releaseNotice.value?.canLeave()===false)return
    logReturnRelease=activeRelease.value?.id
    showReleaseDetail.value=false
  }
  showDeployLog.value = true
  deployLogID=d.id
  return refreshDeployLog(d.id)
}
watch(showDeployLog,value=>{if(!value){clearTimeout(deployLogTimer);deployLogID=null;if(logReturnRelease){const order=releaseOrders.value.find(v=>v.id===logReturnRelease&&v.service===name.value);logReturnRelease=null;if(order)openRelease(order)}}})
async function refreshDeployLog(id) {
  clearTimeout(deployLogTimer)
  try {
    const response=await fetch(`api/deploys/${id}/log`)
    if(!response.ok)throw Error('发布日志读取失败')
    const r=await response.json()
    if(!showDeployLog.value||deployLogID!==id)return
    deployLog.value=r.log||'暂无日志'
    const current=deploys.value.find(v=>v.id===id)
    if(current?.status==='running')deployLogTimer=setTimeout(()=>refreshDeployLog(id),2500)
  }catch(e){toast(e.message,'error')}
}
const defOf = id => stepDefs.value.find(d => d.id === id)
function setParam(i, k, v) {if(savingSteps.value)return; (steps.value[i].params ??= {})[k] = v }
function move(i, d) {if(savingSteps.value)return; const j = i + d; if (j < 0 || j >= steps.value.length) return; [steps.value[i], steps.value[j]] = [steps.value[j], steps.value[i]] }
function removeBuildStep(i){if(!savingSteps.value)steps.value.splice(i,1)}
function addBuildStep(){if(savingSteps.value)return;const id=newStep.value||buildStepDefs.value[0]?.id,def=defOf(id);if(!def)return toast('请选择构建步骤','warning');const params=Object.fromEntries((def.params||[]).map(p=>[p.name,p.default??'']));if(id==='docker-build'){params.DOCKERFILE_SOURCE='template';params.DOCKERFILE_TEMPLATE='java21';params.DOCKERFILE_ARTIFACT_PATH='app.jar'}steps.value.push({step:id,params});newStep.value=''}
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

import OptionPicker from '../../shared/components/OptionPicker.vue'

import {confirmDialog} from '../../shared/shadcnFeedback.js'

import {ref as _viewRef} from 'vue'
const _view1=_viewRef([])
const _view2=_viewRef([])
const _view3=_viewRef([])
const _view4=_viewRef([])

import {copyText} from '../../shared/clipboard.js'

import {focusControl} from '../../shared/focusControl.js'


import {Accordion,AccordionContent,AccordionItem,AccordionTrigger,Badge,Button,Card,CardAction,CardContent,CardFooter,CardHeader,CardTitle,Checkbox,Dialog,DialogContent,DialogDescription,DialogFooter,DialogHeader,DialogTitle,DropdownMenu,DropdownMenuContent,DropdownMenuItem,DropdownMenuTrigger,Empty,EmptyContent,EmptyDescription,EmptyHeader,Field,FieldGroup,FieldDescription,FieldError,FieldLabel,Input,NumberField,NumberFieldContent,NumberFieldDecrement,NumberFieldIncrement,NumberFieldInput,SidebarMenu,SidebarMenuButton,SidebarMenuItem,Spinner,Table,TableBody,TableCell,TableHead,TableHeader,TableRow,Tabs,TabsContent,TabsList,TabsTrigger,Textarea} from '../../shared/shadcn-ui.js'

import {confirmAction,promptAction} from "../../shared/shadcnFeedback.js"
</script>
<template><div class="application-detail"><PageHeader><template #title><Button @click="router.push('/')" aria-label="返回基础构建" class="back op-btn" variant="outline" size="default" type="button">←</Button><h2 :title="applicationIdentifier(svc)||String(name)" class="app-name text-2xl font-semibold leading-8 tracking-tight">{{ applicationName(svc)||name }}</h2><HelpTip :text="typeMeta.label + ' · ' + (svc?.git_url || '未连接代码仓库')"/></template><template #subtitle><span class="font-mono text-xs">{{applicationIdentifier(svc)||String(name)}}</span><span v-if="svc?.branch">{{svc.branch}}</span><span>{{typeMeta.label}}</span></template><template #navigation><Tabs aria-label="应用交付视图" class="app-view-tabs" :model-value="tab" @update:model-value="setTab($event)"><div class="flex min-w-0 items-center gap-2"><TabsList variant="line" class="h-10 max-w-full"><TabsTrigger v-for="t in [['overview','概览'],['build','构建'],['artifact','制品'],['environment','环境'],['release','发布单']]" :value="t[0]">{{t[1]}}</TabsTrigger></TabsList></div><TabsContent v-for="t in [['overview','概览'],['build','构建'],['artifact','制品'],['environment','环境'],['release','发布单']]" :value="t[0]" force-mount v-show="!(tab!==t[0])" class="min-w-0"></TabsContent></Tabs></template><template #actions><OptionPicker v-if="['overview','environment','release'].includes(tab)&&targets.length&&!showTarget&&!showInstances" aria-label="发布环境" v-model="deployEnv" :options="targets.map(t=>({value:t.environment,label:t.display_name||t.environment}))" placeholder="环境" :style="{width:'130px',maxWidth:'100%'}" class="header-environment"/><Button v-if="tab==='build'||tab==='overview'" @click="runPipeline" variant="default" size="sm" type="button">开始构建</Button><Button v-if="['build','overview','release','environment'].includes(tab)" @click="openOneClickDeploy()" variant="outline" size="sm" type="button">构建并发布</Button><Button v-if="tab==='artifact'" @click="runPipeline" variant="outline" size="sm" type="button">构建新制品</Button><Button v-if="tab==='release'||tab==='environment'" @click="openPublish()" :disabled="instanceOperating" variant="default" size="sm" type="button">发起发布</Button><DropdownMenu><DropdownMenuTrigger as-child><Button variant="outline" size="sm" type="button">更多</Button></DropdownMenuTrigger><DropdownMenuContent align="end"><DropdownMenuItem @select="openBuildConfiguration">应用设置</DropdownMenuItem><DropdownMenuItem v-if="me.role==='admin'" @select="showNameEditor=true">修改应用名称</DropdownMenuItem><DropdownMenuItem @select="settingsSection='bindings';setTab('settings')">资源绑定</DropdownMenuItem><DropdownMenuItem v-if="me.role==='admin'" @select="deleteApplication" :disabled="deletingApplication||savingBuildConfiguration||savingTarget||savingSteps">删除应用</DropdownMenuItem></DropdownMenuContent></DropdownMenu></template></PageHeader><section v-if="tab==='resources'" class="section"><h3>应用资源配置<HelpTip text="声明应用使用的逻辑资源，并按环境绑定实际连接。日常构建和发布无需重复配置。"/></h3><Button @click="showAppResources=true" variant="outline" size="default" type="button">配置应用资源</Button></section><ApplicationResourcesEditor :show="showAppResources" :services="svc?[svc]:[{name:String(name)}]" :initial-service="String(name)" @close="showAppResources=false"/><ApplicationNameEditor :show="showNameEditor" :service="svc" @close="showNameEditor=false" @saved="applicationRenamed"/><!-- 应用概览：环境是主视图，构建与发布是动作。 --><div v-show="!showTarget" v-if="tab === 'overview'"><div class="overview-strip compact-overview"><div><small>最新构建运行</small><b>{{buildRunLabel(latest)}} · {{ latest?.version || '尚未运行' }}</b><span :class="statusClass(latest?.status)">{{ latest ? statusText(latest.status) : '等待首次构建' }}</span></div><div><small>最新可发布制品</small><b>{{ latestArtifact?.version || '尚无制品' }}</b><span>{{ latestArtifact ? `制品 #${latestArtifact.id} · ${(latestArtifact.digest || '').slice(0, 18)}…` : '成功构建后生成' }}</span></div><div><small>已配置环境</small><b>{{ targets.length }}</b><span>{{ targets.filter(t => t.protected).length }} 个受保护环境</span></div></div><div v-if="latestArtifact&&targets.length" class="overview-next"><span>选择环境发布已有制品；实际运行状态需检测确认</span><Button @click="publishArtifact(latestArtifact)" variant="outline" size="sm" type="button">发布已有制品</Button><Button v-if="deployEnv" @click="openWorkspace('logs','#/search')" variant="outline" size="sm" type="button">查看日志</Button><Button v-if="deployEnv" @click="openWorkspace('monitor','#/')" variant="outline" size="sm" type="button">查看监控</Button></div><div class="section-head"><div><h3>环境<HelpTip text="按环境构建方案选择兼容制品；仅通用方案允许同一制品跨环境发布"/></h3></div><Button @click="editTarget()" variant="outline" size="sm" type="button">＋ 配置环境</Button></div><Card v-if="!targets.length" class="environment-empty"><CardContent class="min-w-0"><Empty><EmptyHeader><EmptyDescription><p>尚未配置部署环境</p><Button @click="editTarget()" variant="default" size="default" type="button">配置环境</Button></EmptyDescription></EmptyHeader></Empty></CardContent></Card><div v-else class="env-grid"><Card v-for="t in targets" :key="t.environment" size="small" :class="{focused:deployEnv===t.environment}" @click="deployEnv=t.environment"><CardHeader><CardTitle>{{t.display_name||t.environment}}</CardTitle><CardAction><Badge v-if="envState[t.environment]?.latest" variant="secondary">{{statusText(envState[t.environment].latest.status)}}</Badge><Badge v-else variant="secondary">未发布</Badge></CardAction></CardHeader><CardContent class="min-w-0"><dl class="grid min-w-0 gap-4 sm:grid-cols-2"><div class="min-w-0 space-y-1"><dt class="text-sm text-muted-foreground">发布资源</dt><dd class="min-w-0 break-words">{{targetResource(t)}}</dd></div><div class="min-w-0 space-y-1"><dt class="text-sm text-muted-foreground">发布方式</dt><dd class="min-w-0 break-words">{{t.target_type==='script'?'脚本发布':isManagedTarget(t)?'SAE 托管发布':t.strategy==='rolling'?'滚动发布':t.strategy}}</dd></div><div class="min-w-0 space-y-1"><dt class="text-sm text-muted-foreground">最近成功版本</dt><dd class="min-w-0 break-words">{{envState[t.environment]?.current?.version||'未发布'}}</dd></div><div class="min-w-0 space-y-1"><dt class="text-sm text-muted-foreground">发布时间</dt><dd class="min-w-0 break-words">{{fmtTime(envState[t.environment]?.current?.ended_at)}}</dd></div></dl></CardContent><CardFooter><Button @click.stop="releaseTo(t)" variant="ghost" size="default" type="button" :disabled="(!targetReady(t)||envState[t.environment]?.latest?.status==='running')">{{!targetReady(t)?'先配置资源':envState[t.environment]?.latest?.status==='running'?'发布中':'发布新版本'}}</Button><Button @click.stop="editTarget(t)" variant="ghost" size="default" type="button">配置</Button></CardFooter></Card></div><div v-if="releaseOrders.length" class="section-head compact"><div><h3>最近发布单</h3></div><Button @click="setTab('release')" class="text-link op-btn" variant="outline" size="default" type="button">查看全部 →</Button></div><div v-if="releaseOrders.length" class="table-scroll"><div class="console-table min-w-0 max-w-full"><div class="min-w-0 max-w-full" :style="{maxHeight:({x:900}).y?({x:900}).y+'px':undefined}"><DataTable class="[&_[data-slot=table-container]]:rounded-lg [&_[data-slot=table-container]]:border" :rows="(((releaseOrders.slice(0,5))||[]).map((d,rowIndex)=>({...d,__rowKey:d.id}))||[])" :columns="[{id:'c0',label:&quot;#&quot;,size:100,minSize:64,required:true,defaultHidden:false,flex:false,selection:false,pin:'left'},{id:'c1',label:&quot;版本&quot;,size:200,minSize:64,required:false,defaultHidden:false,flex:true,selection:false,pin:false},{id:'c2',label:&quot;环境&quot;,size:180,minSize:64,required:false,defaultHidden:false,flex:false,selection:false,pin:false},{id:'c3',label:&quot;进度&quot;,size:120,minSize:64,required:false,defaultHidden:false,flex:false,selection:false,pin:false},{id:'c4',label:&quot;状态&quot;,size:88,minSize:64,required:false,defaultHidden:false,flex:false,selection:false,pin:false},{id:'c5',label:&quot;时间&quot;,size:180,minSize:64,required:false,defaultHidden:false,flex:false,selection:false,pin:false}]" table-key="deploy-pages-DetailView-0" :row-key="(record,rowIndex)=>typeof (&quot;__rowKey&quot;)==='function'?(&quot;__rowKey&quot;)(record):record[&quot;__rowKey&quot;]??rowIndex"><template #header-c0>#</template><template #cell-c0="{record,rowIndex}"><div class="min-w-0 break-words" :class="{truncate:!!(true)}"><template v-for="{record:d,rowIndex:rowIndex} in [{record,rowIndex}]"><div>{{releaseOrderLabel(d)}}</div></template></div></template><template #header-c1>版本</template><template #cell-c1="{record,rowIndex}"><div class="min-w-0 break-words" :class="{truncate:!!(true)}"><template v-for="{record:d,rowIndex:rowIndex} in [{record,rowIndex}]"><div><code>{{ d.version }}</code></div></template></div></template><template #header-c2>环境</template><template #cell-c2="{record,rowIndex}"><div class="min-w-0 break-words" :class="{truncate:!!(true)}"><template v-for="{record:d,rowIndex:rowIndex} in [{record,rowIndex}]"><div>{{ d.environment }}</div></template></div></template><template #header-c3>进度</template><template #cell-c3="{record,rowIndex}"><div class="min-w-0 break-words" :class="{truncate:!!(true)}"><template v-for="{record:d,rowIndex:rowIndex} in [{record,rowIndex}]"><div>{{d.succeeded}} / {{d.total}}</div></template></div></template><template #header-c4>状态</template><template #cell-c4="{record,rowIndex}"><div class="min-w-0 break-words" :class="{truncate:!!(true)}"><template v-for="{record:d,rowIndex:rowIndex} in [{record,rowIndex}]"><div><span :class="statusClass(d.status)">{{ statusText(d.status) }}</span></div></template></div></template><template #header-c5>时间</template><template #cell-c5="{record,rowIndex}"><div class="min-w-0 break-words" :class="{truncate:!!(true)}"><template v-for="{record:d,rowIndex:rowIndex} in [{record,rowIndex}]"><div class="dim">{{ fmtTime(d.started_at) }}</div></template></div></template><template #empty><Empty><EmptyHeader><EmptyDescription>暂无数据</EmptyDescription></EmptyHeader></Empty></template></DataTable></div></div></div></div><section v-if="tab==='artifact'"><ArtifactRetention :service="String(name)" :can-manage="me?.role==='admin'||me?.username==='local'"/><BuildRecordsTable :key="'artifacts:'+name" :service="String(name)" kind="artifacts" :me="me" @publish="publishArtifact"/></section><!-- 构建配置与运行记录：应用级构建只保留这一个入口。 --><div v-if="tab === 'build'"><BuildRecordsTable :key="'runs:'+name" :service="String(name)" kind="runs" :me="me" @publish="publishArtifact"><template #actions><Button @click="openBuildConfiguration" variant="outline" size="sm" type="button">编辑配置</Button></template><template #context><span class="break-words">{{repositoryLabel}} · {{svc?.branch||'仓库默认分支'}} · {{svc?.build_tool||typeMeta.label}} · {{buildEnvironmentSummary}}</span></template></BuildRecordsTable></div><section v-if="tab==='settings'||tab==='resources'" :data-section="settingsSection" class="settings-workspace"><SidebarMenu class="settings-section-navigation" aria-label="应用设置分组"><SidebarMenuItem v-for="s in [['source','代码来源'],['command','构建与制品'],['environment','工具链与 Runner'],['resource','凭据与变量'],['trigger','触发策略'],['bindings','应用资源绑定'],['advanced','高级流水线']]"><SidebarMenuButton class="h-10 text-sm" :is-active="([settingsSection]).includes(s[0])" @click="settingsSection=s[0]"><span>{{s[1]}}</span></SidebarMenuButton></SidebarMenuItem></SidebarMenu><main><section v-if="settingsSection==='bindings'" class="settings-bindings"><h3>应用资源绑定<HelpTip text="按环境绑定实际连接；不影响当前构建配置草稿。"/></h3><Button @click="showAppResources=true" variant="outline" size="default" type="button">配置应用资源</Button></section><fieldset :disabled="savingBuildConfiguration" class="settings-editor-body min-w-0 space-y-5" ><div v-if="svc" class="build-config-grid"><section class="settings-card build-source-card"><div class="card-title"><span>1</span><div><h4>代码来源</h4><small>应用名称、固定标识与代码来源</small></div></div><Field><FieldLabel>应用名称</FieldLabel><div class="flex flex-wrap items-center gap-2"><span bold class="min-w-0 break-words">{{applicationName(svc)}}</span><Button @click="showNameEditor=true" variant="outline" size="sm" type="button" :disabled="(me.role!=='admin')">修改名称</Button></div><FieldDescription>支持中文，可修改；仅改变显示名称。{{me.role==='member'?'仅管理员可修改。':!me.role?'操作权限尚未确认，请刷新页面重试。':''}}</FieldDescription></Field><Field><FieldLabel>应用标识（固定）</FieldLabel><span class="min-w-0 break-words"><code>{{applicationIdentifier(svc)}}</code><Button variant="ghost" size="xs" aria-label="复制内容" @click="copyText(applicationIdentifier(svc))">复制</Button></span><FieldDescription>不可修改；用于关联历史记录、资源和部署，不随名称变化。</FieldDescription></Field><Field class="fld" data-validation-field="git_url"><FieldLabel>Git 仓库</FieldLabel><Input v-model="svc.git_url" placeholder="https://codeup.aliyun.com/组织/项目/仓库.git"/></Field><Field class="fld"><FieldLabel>默认分支</FieldLabel><GitBranchSelect v-model="svc.branch" :repositories="[{service:name,git_url:svc.git_url||'',connection:delivery.git_connection||''}]"/></Field><Field class="fld"><FieldLabel>应用标签</FieldLabel><Input v-model="buildTagsText" placeholder="核心、AI、Java"/></Field><small class="config-help">切换 HTTPS/SSH 地址时，请在下方选择相同认证方式的 Git 凭据。</small><div class="application-delete-action"><div><strong>删除应用配置</strong><p>保留历史记录，不停止已部署的服务。{{me.role==='member'?'仅管理员可删除。':!me.role?'操作权限尚未确认，请刷新页面重试。':''}}</p></div><Button @click="deleteApplication" class="text-destructive" :variant="'destructive'" size="default" type="button" :disabled="(me.role!=='admin'||deletingApplication||savingBuildConfiguration||savingTarget||savingSteps)||(deletingApplication)"><Spinner v-if="deletingApplication"/>删除应用</Button></div></section><section class="settings-card build-command-card"><div class="card-title"><span>2</span><div><h4>构建与制品</h4><small>可执行文件、任务与产物位置</small></div></div><JavaBundleEditor v-if="svc.artifact_type==='java-bundle'" :service="svc"/><FieldGroup v-else-if="svc.artifact_type === 'jar'" class="gap-5"><Field data-validation-field="build_tool"><FieldLabel>构建工具</FieldLabel><Tabs :model-value="svc.build_tool" @update:model-value="selectBuildTool($event)"><TabsList class="h-auto! flex-wrap gap-y-1 [&>[data-slot=tabs-trigger]]:h-7"><TabsTrigger v-for="option in [{value:'maven',label:'Maven'},{value:'gradle',label:'Gradle'}]" :key="option.value??option" :value="option.value??option">{{option.label??option}}</TabsTrigger></TabsList></Tabs></Field><JavaCompileFields :service="svc" :tool="svc.build_tool" :disabled="savingBuildConfiguration"/><Field class="fld" data-validation-field="artifact_glob"><FieldLabel>JAR 产物位置</FieldLabel><Input v-model="svc.artifact_glob" :placeholder="svc.build_tool==='gradle'?'build/libs/*.jar':'target/*.jar'"/></Field><Accordion class="inline-advanced platform-collapse" type="multiple" :model-value="(_view1||[]).map(String)" @update:model-value="_view1=$event"><AccordionItem :value="String(&quot;panel&quot;)"><AccordionTrigger>构建工具高级参数</AccordionTrigger><AccordionContent force-mount v-show="!(!(_view1||[]).map(String).includes(String(&quot;panel&quot;)))"><template v-if="svc.build_tool==='maven'"><Field class="fld" data-validation-field="maven_profiles"><FieldLabel>Maven Profiles</FieldLabel><Input v-model="svc.maven_profiles" placeholder="ci,release"/></Field><Field class="fld" data-validation-field="maven_settings_path"><FieldLabel>settings.xml（可选）</FieldLabel><Input v-model="svc.maven_settings_path" placeholder="留空使用依赖仓库连接"/></Field><Field class="fld" data-validation-field="maven_properties"><FieldLabel>-D 属性（每行 key=value）</FieldLabel><Textarea v-model="svc.maven_properties" :rows="({minRows:(3),maxRows:Math.max((3),16)}).minRows||4"/></Field></template><template v-else><Field class="fld" data-validation-field="gradle_excluded_tasks"><FieldLabel>排除任务</FieldLabel><Input v-model="svc.gradle_excluded_tasks" placeholder="test integrationTest"/></Field><Field class="fld" data-validation-field="gradle_project_properties"><FieldLabel>-P 项目属性（每行 key=value）</FieldLabel><Textarea v-model="svc.gradle_project_properties" :rows="({minRows:(3),maxRows:Math.max((3),16)}).minRows||4"/></Field><Field class="fld" data-validation-field="gradle_system_properties"><FieldLabel>-D 系统属性（每行 key=value）</FieldLabel><Textarea v-model="svc.gradle_system_properties" :rows="({minRows:(3),maxRows:Math.max((3),16)}).minRows||4"/></Field></template></AccordionContent></AccordionItem></Accordion></FieldGroup><template v-else-if="svc.artifact_type === 'static'"><Field class="fld" data-validation-field="build_cmd"><FieldLabel>构建命令</FieldLabel><Textarea v-model="svc.build_cmd" placeholder="npm ci && npm run build" :rows="({minRows:(2),maxRows:Math.max((2),16)}).minRows||4"/></Field><Field class="fld" data-validation-field="artifact_glob"><FieldLabel>输出目录</FieldLabel><Input v-model="svc.artifact_glob" placeholder="dist"/></Field></template><ImageBuildEditor v-else :service="svc"/></section><section class="settings-card build-environment-card"><div class="card-title"><span>3</span><div><h4>工具链与 Runner</h4><small>选择任务执行位置及隔离的编译环境</small></div></div><Field class="fld"><FieldLabel>构建 Runner</FieldLabel><OptionPicker v-model="delivery.runner" :options="runners.filter(r=>r.enabled).map(r=>({value:r.name,label:`${r.name} · ${r.labels||r.kind}`}))" :style="{width:'100%',maxWidth:'100%'}"/></Field><BuildEnvironmentEditor v-model="delivery.build_environment" :build-tool="svc.artifact_type==='image'?imageCompiler(svc):svc.build_tool" :artifact-type="svc.artifact_type"/></section><section class="settings-card build-resource-card"><div class="card-title"><span>4</span><div><h4>凭据与构建变量</h4><small>只引用资源中心已加密保存的连接</small></div><Button @click="openPlatformResources" class="text-link resource-link" variant="outline" size="default" type="button">管理资源 ↗</Button></div><Field v-if="svc.git_url" class="fld"><FieldLabel>Git 代码凭据</FieldLabel><OptionPicker v-model="delivery.git_connection" :options="[{value:'',label:'公开仓库 / 使用 Runner 凭据'},...connections.filter(c=>c.kind==='git').map(c=>({value:c.name,label:`${c.name} · ${c.config?.auth_mode==='ssh'?'SSH':'HTTPS'}`}))]" :style="{width:'100%',maxWidth:'100%'}"/></Field><Field v-if="['maven','gradle'].includes(svc.build_tool)" class="fld"><FieldLabel>依赖仓库</FieldLabel><OptionPicker v-model="delivery.maven_connection" :options="[{value:'',label:'不使用私服'},...connections.filter(c=>c.kind==='maven').map(c=>({value:c.name,label:c.name}))]" :style="{width:'100%',maxWidth:'100%'}"/></Field><Field v-if="svc.artifact_type==='image'&&imageDeliveryMode==='registry'" class="fld"><FieldLabel>镜像仓库</FieldLabel><OptionPicker v-model="delivery.registry_connection" :options="[{value:'',label:'使用 Runner 的 Docker 登录'},...connections.filter(c=>c.kind==='docker').map(c=>({value:c.name,label:c.name}))]" :style="{width:'100%',maxWidth:'100%'}"/></Field><Field class="fld"><FieldLabel>构建变量组</FieldLabel><div class="check-list"><label v-for="g in variableGroups" :key="g.name"><Checkbox :model-value="delivery.variable_groups?.includes(g.name)" @update:model-value="toggleDeliveryGroup(g.name)"/> {{g.name}} <small>{{g.keys.join(', ')}}</small></label><span v-if="!variableGroups.length" class="empty-inline">暂无变量组，可从右上角进入资源中心创建</span></div></Field></section><section class="settings-card build-trigger-card"><div class="card-title"><span>5</span><div><h4>触发策略</h4><small>手动、Webhook 或定时生成同一类不可变制品</small></div></div><Field class="fld"><FieldLabel>Webhook URL</FieldLabel><Input :model-value="webhookURL" readonly/></Field><div class="two"><Field class="fld"><FieldLabel>定时构建间隔（分钟）</FieldLabel><NumberField v-model="delivery.schedule_minutes" :min="0" :max="10080"><NumberFieldContent><NumberFieldDecrement/><NumberFieldInput/><NumberFieldIncrement/></NumberFieldContent></NumberField></Field><Field class="fld"><FieldLabel>自动构建成功后</FieldLabel><OptionPicker v-model="delivery.auto_deploy_environment" :options="[{value:'',label:'只生成制品'},...targets.map(t=>({value:t.environment,label:`提交到 ${t.display_name}${t.protected?'（仍需审批）':''}`}))]" :style="{width:'100%',maxWidth:'100%'}"/></Field></div></section><section class="settings-card build-advanced-card"><div class="card-title"><span>⚙</span><div><h4>高级流水线</h4><small>只在需要自定义步骤顺序或参数时使用</small></div></div><p>日常构建不需要进入这里。流水线只处理源码、检查、构建和制品，不混入主机发布参数。</p><Button @click="openAdvancedBuildConfiguration" variant="outline" size="sm" type="button">编辑高级流水线 →</Button></section></div></fieldset><div v-if="settingsSection!=='bindings'" class="build-config-actions"><span v-if="buildConfigurationDirty" class="unsaved-dot">● 有未保存修改</span><Button @click="closeBuildConfiguration" variant="outline" size="default" type="button" :disabled="(savingBuildConfiguration)">取消</Button><Button @click="commitBuildConfiguration" :variant="({primary:'default',secondary:'outline',text:'ghost',outline:'outline',dashed:'outline'})[repairingBuild?'secondary':'primary']||'outline'" size="default" type="button" :disabled="(savingBuildConfiguration)"><Spinner v-if="savingBuildConfiguration"/>保存配置</Button><Button v-if="repairingBuild" @click="saveAndPrepareBuild" variant="default" size="default" type="button" :disabled="(savingBuildConfiguration)"><Spinner v-if="savingBuildConfiguration"/>保存并准备新构建</Button></div></main></section><!-- 环境配置 --><InstanceManager :releases="releaseOrders" :deploys="deploys" @refresh="refreshRecovery" v-if="showInstances&&instanceTarget" v-show="tab==='environment'" :service="String(name)" :target="instanceTarget" :me="me" @busy="instanceOperating=$event" @close="showInstances=false"/><div v-show="!showTarget&&!showInstances" v-if="tab === 'environment'"><div class="section-head"><div><h3>部署环境<HelpTip text="运行资源按环境隔离；在“代码与制品策略”中绑定环境构建方案"/></h3></div><Button @click="editTarget()" variant="default" size="sm" type="button">＋ 配置环境</Button></div><Card v-if="!targets.length" class="environment-empty"><CardContent class="min-w-0"><Empty><EmptyHeader><EmptyDescription><p>尚未配置部署环境</p><Button @click="editTarget()" variant="default" size="default" type="button">配置环境</Button></EmptyDescription></EmptyHeader></Empty></CardContent></Card><div v-else class="env-grid"><Card v-for="t in targets" :key="t.environment" size="small"><CardHeader><CardTitle>{{t.display_name||t.environment}}</CardTitle><CardAction><Badge v-if="t.protected" variant="secondary">受保护</Badge><Badge v-else variant="secondary">{{t.environment}}</Badge></CardAction></CardHeader><CardContent class="min-w-0"><dl class="grid min-w-0 gap-4 sm:grid-cols-2"><div class="min-w-0 space-y-1"><dt class="text-sm text-muted-foreground">当前版本</dt><dd class="min-w-0 break-words">{{envState[t.environment]?.current?.version||'未发布'}}</dd></div><div class="min-w-0 space-y-1"><dt class="text-sm text-muted-foreground">环境资源</dt><dd class="min-w-0 break-words">{{targetResource(t)}}</dd></div><div class="min-w-0 space-y-1"><dt class="text-sm text-muted-foreground">运行变量</dt><dd class="min-w-0 break-words">{{t.runtime_env_keys?.length?t.runtime_env_keys.length+' 项（已加密）':'未配置'}}</dd></div><div class="min-w-0 space-y-1"><dt class="text-sm text-muted-foreground">发布策略</dt><dd class="min-w-0 break-words">{{releasePolicyText(t)}}</dd></div><div class="min-w-0 space-y-1"><dt class="text-sm text-muted-foreground">{{t.target_type==='manual'?'交付制品':t.target_type==='script'?'执行位置':isManagedTarget(t)?'命名空间':'部署目录'}}</dt><dd class="min-w-0 break-words">{{t.target_type==='manual'?({jar:'Java JAR','oci-image':'Docker 镜像','docker-image-archive':'镜像离线包','static-archive':'静态资源包'}[t.deploy_config?.manual_artifact_kind]||'待配置'):t.target_type==='script'?t.deploy_config?.script_runner:isManagedTarget(t)?t.deploy_config?.namespace_id:t.remote_dir}}</dd></div><div v-if="t.deploy_config?.java_mode==='jpom'" class="min-w-0 space-y-1"><dt class="text-sm text-muted-foreground">发布方式</dt><dd class="min-w-0 break-words">Jpom 兼容 · 覆盖发布，人工验收</dd></div><div v-if="isManagedJavaTarget(t)" class="min-w-0 space-y-1"><dt class="text-sm text-muted-foreground">检查失败</dt><dd class="min-w-0 break-words">{{({rollback:'自动恢复旧版本',stop:'停止新版本',keep:'保留新版本运行'})[t.deploy_config?.health_failure_action||'rollback']}}</dd></div></dl></CardContent><CardFooter><Button @click="releaseTo(t)" variant="ghost" size="default" type="button" :disabled="(!targetReady(t))">{{targetReady(t)?'发布':'先配置资源'}}</Button><Button v-if="supportsInstances(t)" @click="openInstances(t)" variant="ghost" size="default" type="button">实例管理</Button><Button @click="editTarget(t)" variant="ghost" size="default" type="button">编辑配置</Button><Button @click="deleteTarget(t)" class="text-destructive" :variant="'destructive'" size="default" type="button">移除</Button></CardFooter></Card></div></div><!-- 发布单与历史 --><div v-show="!showTarget&&(!showReleaseDetail||!activeRelease?.manual_publication)" v-if="tab === 'release'"><BuildRecordsTable ref="releaseRecords" :key="'releases:'+name" :service="String(name)" kind="releases" :environments="targets" :me="me" @open="openRelease" @action="releaseAction"><template #heading-extra><HelpTip text="选择已构建版本和目标环境，形成可审计的发布记录"/></template></BuildRecordsTable></div><!-- 高级配置（步骤编排） --><div v-if="tab === 'advanced'"><div class="advanced-heading"><h3>高级构建流水线<HelpTip text="编排源码、检查、构建、脚本和制品步骤；部署由发布单处理。脚本在所选 Runner / 隔离容器中执行，不是远程主机脚本。保存不执行。"/></h3><Button @click="setTab('build')" variant="outline" size="sm" type="button">← 返回构建</Button></div><div v-if="svc" style="font-size:.75rem;margin-bottom:8px" class="dim">代码：{{ svc.git_url || '（未配 git）' }} · 分支 {{ svc.branch || '默认' }}</div><fieldset :disabled="savingSteps" class="settings-editor-body"><div v-for="(inst, i) in steps" :key="i" style="border:1px solid var(--border);border-radius:8px;padding:10px 12px;margin-bottom:8px;background:var(--panel2)"><div style="display:flex;align-items:center;gap:8px"><span style="width:20px;text-align:center;color:var(--dim)">{{ i + 1 }}</span><span :style="{ width:'6px', height:'16px', borderRadius:'2px', background: CAT_COLOR[defOf(inst.step)?.category] || 'var(--dim)' }"></span><b>{{ defOf(inst.step)?.name || inst.step }}</b><span style="color:var(--dim);font-size:.75rem">{{ inst.step }}</span><span style="margin-left:auto;display:flex;gap:8px"><Button :aria-label="'上移第'+(i+1)+'步'" @click="move(i, -1)" class="op op-btn" variant="outline" size="default" type="button" :disabled="(savingSteps||i===0)">↑</Button><Button :aria-label="'下移第'+(i+1)+'步'" @click="move(i, 1)" class="op op-btn" variant="outline" size="default" type="button" :disabled="(savingSteps||i===steps.length-1)">↓</Button><Button :aria-label="'删除第'+(i+1)+'步'" @click="removeBuildStep(i)" class="op del op-btn" variant="outline" size="default" type="button" :disabled="(savingSteps)">✕</Button></span></div><div style="margin-top:8px;padding-top:8px;border-top:1px dashed var(--border)"><Field v-for="p in defOf(inst.step)?.params || []" v-show="inst.step!=='docker-build'||(p.name!=='DOCKERFILE_CONTENT'||inst.params?.DOCKERFILE_SOURCE==='platform')&&(p.name!=='DOCKERFILE'||!inst.params?.DOCKERFILE_SOURCE||inst.params?.DOCKERFILE_SOURCE==='repository')&&(!['DOCKERFILE_TEMPLATE','DOCKERFILE_ARTIFACT_PATH'].includes(p.name)||inst.params?.DOCKERFILE_SOURCE==='template')" :key="p.name" style="margin:6px 0" class="fld"><FieldLabel>{{ p.label }}</FieldLabel><OptionPicker v-if="p.type === 'select'" :model-value="(inst.params || {})[p.name] ?? p.default ?? ''" :options="(p.options || []).map(o => ({ value: o.value, label: o.label || o.value }))" :style="{width:'100%',maxWidth:'100%'}" @update:model-value="v => setParam(i, p.name, v)"/><Textarea v-else-if="p.type === 'shell'" style="width:100%;font-family:var(--mono);font-size:.75rem" :model-value="(inst.params || {})[p.name] ?? p.default ?? ''" @update:model-value="v => setParam(i, p.name, v)" :rows="({minRows:(p.name==='DOCKERFILE_CONTENT'?10:inst.step==='shell-script'?6:2),maxRows:Math.max((p.name==='DOCKERFILE_CONTENT'?10:inst.step==='shell-script'?6:2),16)}).minRows||4"/><Input v-else :model-value="(inst.params || {})[p.name] ?? p.default ?? ''" @update:model-value="v => setParam(i, p.name, v)"/></Field></div></div><Card size="small" style="margin:12px 0"><CardHeader><CardTitle>生成接口文档 · 插件节点</CardTitle><CardAction><Button @click="openApplicationDocuments" variant="ghost" size="default" type="button">配置环境文档来源</Button></CardAction></CardHeader><CardContent class="min-w-0"><div v-if="documentBuildEnvironments.length" class="flex flex-wrap items-center gap-2"><Badge v-for="env in documentBuildEnvironments" :key="env" :variant="(&quot;blue&quot;)==='red'?'destructive':'secondary'">{{env}} · 构建后自动生成并对比</Badge></div><Empty v-else><EmptyHeader><EmptyDescription>{{"尚未配置自动生成环境"}}</EmptyDescription></EmptyHeader></Empty></CardContent></Card><div style="display:flex;gap:8px;align-items:center;margin-top:6px"><OptionPicker v-model="newStep" :options="buildStepDefs.map(d => ({ value: d.id, label: d.name + '（' + d.category + '）' }))" :placeholder="buildStepDefs[0]?.id || '选择步骤'" :style="{width:'220px',maxWidth:'100%'}"/><Button @click="addBuildStep" variant="outline" size="sm" type="button">＋ 添加构建步骤</Button><Button style="margin-left:auto" @click="saveSteps" variant="default" size="sm" type="button" :disabled="(savingSteps)"><Spinner v-if="savingSteps"/>保存流水线</Button></div></fieldset></div><Dialog :open="showPublishForm" @update:open="async v=>{if(!v){cancelPublish()}}"><DialogContent class="flex max-h-[92dvh] flex-col gap-0 overflow-hidden p-0 sm:max-w-[var(--editor-width)]" :class="false?'inset-0 h-dvh max-h-dvh w-screen max-w-none translate-x-0 translate-y-0 rounded-none sm:max-w-none':''" :style="{'--editor-width':typeof (&quot;min(720px, 92vw)&quot;)==='number'?(&quot;min(720px, 92vw)&quot;)+'px':(&quot;min(720px, 92vw)&quot;)}" :show-close-button="!(deploying)" @interact-outside="e=>{if(!(!(deploying)))e.preventDefault()}" @escape-key-down="e=>{if(!(!(deploying)))e.preventDefault()}"><DialogHeader class="shrink-0 border-b p-5 pr-12"><DialogTitle>{{showDeployConfirm?'确认发布':'发起发布'}}</DialogTitle><DialogDescription class="sr-only">详情与操作</DialogDescription></DialogHeader><div class="min-h-0 flex-1 overflow-auto p-5"><div :aria-busy="publishLoading" class="publish-dialog"><b>{{name}}</b><p v-if="publishLoading" role="status">正在读取制品并核对发布状态…</p><Field ><FieldLabel as="div">不可变制品</FieldLabel><OptionPicker v-model="artifactID" :options="artifactOptions"  :disabled="publishLoading||deploying||showDeployConfirm" placeholder="搜索并选择具体制品" :style="{width:'100%',maxWidth:'100%'}"/></Field><Field ><FieldLabel as="div">目标环境</FieldLabel><OptionPicker v-model="publishEnvironment" :options="targets.map(t=>({value:t.environment,label:(t.protected?'🔒 ':'')+(t.display_name||t.environment)}))" :disabled="publishLoading||deploying||showDeployConfirm" placeholder="选择环境" :style="{width:'100%',maxWidth:'100%'}"/></Field><small v-if="checkingBuildCompatibility" role="status">正在校验制品构建方案…</small><template v-else-if="buildCompatibility"><small v-if="buildCompatibility.compatible">构建方案与目标环境兼容</small><div v-else role="alert" class="publish-compatibility-error"><span>{{buildCompatibility.reason}}</span><Button v-if="releaseTarget" @click="configureReleaseBuildVariant" variant="ghost" size="default" type="button" :disabled="(deploying)">配置目标环境构建方案</Button></div></template><small v-if="!publishLoading&&!artifacts.length">暂无可发布制品，请先完成构建。</small><small v-if="!publishLoading&&!targets.length">尚未配置应用发布环境。</small><template v-if="releaseTarget"><div class="publish-brief"><span>目标：{{targetResource(releaseTarget)}}</span><span>策略：{{releasePolicyText(releaseTarget)}}</span></div><Accordion class="platform-collapse" type="multiple" :model-value="(_view2||[]).map(String)" @update:model-value="_view2=$event"><AccordionItem :value="String(&quot;panel&quot;)"><AccordionTrigger>制品与运行配置详情</AccordionTrigger><AccordionContent force-mount v-show="!(!(_view2||[]).map(String).includes(String(&quot;panel&quot;)))"><div class="publish-brief"><code>制品 #{{selectedArtifact?.id||'—'}} · {{selectedArtifact?.digest||'未选择'}}</code><span>最近成功版本：{{envState[publishEnvironment]?.current?.version||'暂无记录（不代表实时运行状态）'}}</span><span>应用变量：{{releaseTarget.runtime_env_keys?.length||0}} 项；提交时冻结目标配置</span><span>健康检查：{{releaseTarget.health_url||'未配置，无法据此确认业务可用性'}}</span><span v-if="!isManagedTarget(releaseTarget)">远端目录：{{releaseTarget.remote_dir||'未配置'}}</span><code v-if="releaseTarget.target_type==='ssh-jar'">启动命令：{{releaseTarget.start_cmd||'未配置'}}</code></div></AccordionContent></AccordionItem></Accordion><Field ><FieldLabel as="div">发布原因{{releaseTarget.require_reason?'（必填）':'（选填）'}}</FieldLabel><Textarea v-model="reason" :disabled="deploying" :rows="({minRows:(2),maxRows:Math.max((2),16)}).minRows||4"/></Field><small v-if="releaseTarget.protected">提交后进入审批；审批人：{{releaseTarget.approvers||'未配置，禁止发布'}}。发起人不能自审。</small><ReleaseRecovery :order="releaseBusyOrder" :deploys="deploys" :me="me" @updated="refreshRecovery" @refresh="refreshRecovery"/><small v-if="!releaseBusyOrder&&releaseBusyDeploy">该环境已有处理中的部署，请核对原部署记录。</small><small v-if="!targetReady(releaseTarget)">目标资源不完整，请先关闭弹窗，再到“环境”配置。</small></template><small v-if="showDeployConfirm">{{releaseTarget?.protected?'确认后创建发布单并提交审批，不直接发布。':'确认后创建发布单并开始执行。'}} 未确认或取消不会发布。</small></div></div><DialogFooter class="shrink-0 flex-wrap border-t p-4"><Button @click="cancelPublish" variant="outline" size="default" type="button" :disabled="(deploying)">取消</Button><Button v-if="showDeployConfirm" @click="showDeployConfirm=false" variant="outline" size="default" type="button" :disabled="(deploying)">上一步</Button><Button @click="showDeployConfirm?deploy():requestDeploy()" variant="default" size="default" type="button" :disabled="(!!publishLoadError||!buildCompatibility?.compatible||!releaseStateReady||!artifactID||!targetReady(releaseTarget)||!!releaseBusyOrder||!!releaseBusyDeploy||(releaseTarget?.require_reason&amp;&amp;!reason.trim())||(releaseTarget?.protected&amp;&amp;!releaseTarget?.approvers))||(publishLoading||deploying||checkingBuildCompatibility)"><Spinner v-if="publishLoading||deploying||checkingBuildCompatibility"/>{{showDeployConfirm?(releaseTarget?.protected?'确认提交审批':'确认发布'):'下一步：核对发布'}}</Button></DialogFooter></DialogContent></Dialog><BuildRunDialog :releases="releaseOrders" :deploys="deploys" :me="me" @refresh="refreshRecovery" :show="showBuildRun" :service="svc" :delivery="delivery" :targets="targets" :mode="launchMode" :environment="launchEnvironment" @close="showBuildRun=false" @started="id=>{showBuildRun=false;router.push({path:'/run/'+id,query:{return:route.fullPath}})}"/><section v-if="showTarget" aria-label="配置部署环境" class="target-workspace"><div class="target-editor-heading"><div><h3>环境配置</h3></div><div class="target-editor-context"><OptionPicker :model-value="targetForm.environment" :disabled="targetForm._existing" :options="environments.map(e => ({ value: e.name, label: (e.protected ? '🔒 ' : '') + e.display_name }))" :style="{width:'200px',maxWidth:'100%'}" @update:model-value="updateTargetEnvironment"/><Badge variant="secondary">{{targetForm.target_type==='script'?'脚本发布':targetForm.target_type==='manual'?'手动发布':targetForm.target_type==='aliyun-sae'?'阿里云 SAE':targetForm.target_type==='docker-host'?'Docker':targetForm.target_type==='ssh-static'?'静态站点':'Java 进程'}}</Badge></div></div><Tabs class="target-editor-layout" orientation="vertical" :model-value="String(targetSection)" @update:model-value="(value=>targetSection=value)($event)"><div class="target-editor-navigation"><TabsList variant="line" class="h-auto w-full flex-col items-stretch gap-1"><TabsTrigger class="justify-start px-3 py-2" v-for="s in (targetForm.target_type==='script'?[['basic','脚本与执行'],['code','代码与制品策略'],['variables','应用变量']]:targetForm.target_type==='manual'?[['basic','手动发布'],['code','代码与制品策略']]:[['basic','环境与资源'],['code','代码与制品策略'],['runtime','启动与健康检查'],['variables','应用变量'],['policy','发布策略']])" :value="s[0]">{{s[1]}}</TabsTrigger></TabsList></div><fieldset :disabled="savingTarget" class="target-editor-body"><div class="target-config-form"><section v-show="targetSection==='basic'" class="target-group"><Field class="fld"><FieldLabel>发布方式</FieldLabel><OptionPicker :model-value="targetForm.target_type" :options="targetTypeOptions" @update:model-value="changeTargetType"/></Field><ScriptPublicationEditor v-if="targetForm.target_type==='script'" :config="targetForm.deploy_config" :runners="runners" :disabled="savingTarget"/><template v-if="targetForm.target_type==='manual'"><Field class="fld"><FieldLabel>交付制品类型</FieldLabel><OptionPicker v-model="targetForm.deploy_config.manual_artifact_kind" :options="svc.artifact_type==='image'?[{value:'jar',label:'本次镜像构建关联的 JAR'},{value:'oci-image',label:'Docker 镜像地址'},{value:'docker-image-archive',label:'Docker 镜像离线包'}]:[{value:svc.artifact_type==='static'?'static-archive':'jar',label:svc.artifact_type==='static'?'静态资源包':'Java JAR'}]"/></Field><Field class="fld"><FieldLabel>发布负责人账号（留空使用本次发起人）</FieldLabel><Input v-model="targetForm.deploy_config.manual_assignee" placeholder="平台登录账号"/></Field><Field class="fld"><FieldLabel>发布操作说明</FieldLabel><Textarea v-model="targetForm.deploy_config.manual_instructions" rows="4" placeholder="填写发布步骤、核对方法与失败处理方式；不要填写密码"/></Field></template><Field v-if="!['aliyun-sae','manual','script'].includes(targetForm.target_type)" class="fld"><div class="flex flex-wrap items-center justify-between gap-2"><FieldLabel>发布主机</FieldLabel><Button variant="ghost" size="sm" @click="targetHostMode=targetHostMode==='direct'?'group':'direct'">{{targetHostMode==='direct'?'复用主机组':'直接选择主机'}}</Button></div><template v-if="targetHostMode==='direct'"><OptionPicker v-model="targetForm.hosts" multiple :disabled="!!hostReadError" :options="targetHostOptions" placeholder="选择同环境主机，可多选" aria-label="发布主机" width="100%"/><span v-if="hostReadError" class="text-sm text-destructive">{{hostReadError}}</span><span v-else-if="!targetHostOptions.length" class="text-sm text-muted-foreground">该环境还没有登记主机。</span></template><OptionPicker v-else v-model="targetForm.host_group" :options="[{value:'',label:'请选择主机组'},...hostGroups.filter(g=>g.environment===targetForm.environment).map(g=>({value:g.name,label:`${g.name}（${g.hosts.length} 台）`}))]" aria-label="复用主机组" width="100%"/></Field><Field v-if="!['docker-host','aliyun-sae','manual','script'].includes(targetForm.target_type)" class="fld"><FieldLabel class="flex items-center gap-1">部署目录</FieldLabel><Input v-model="targetForm.remote_dir" placeholder="/opt/service"/></Field></section><section v-show="targetSection==='code'" class="target-group code-policy-group"><ApplicationDeliveryDefaults ref="targetDeliveryDefaults" :service="String(name)" :environment="targetForm.environment" :disabled="savingTarget||me.role==='member'"/></section><section v-show="targetSection==='runtime'" class="target-group"><template v-if="['ssh-jar','ssh-java-bundle'].includes(targetForm.target_type)"><Field v-if="targetForm.target_type==='ssh-jar'" class="fld" :class="{two:targetForm.deploy_config.java_mode==='jpom'}"><FieldLabel class="flex items-center justify-between gap-2">启动方式<Button v-if="canDetectJpom" variant="ghost" size="sm" type="button" :disabled="detectingJpom||savingTarget" @click="detectJpom">{{detectingJpom?'正在识别…':'识别 Jpom 配置'}}</Button></FieldLabel><OptionPicker :model-value="targetForm.deploy_config.java_mode || 'custom'" :options="[{value:'managed',label:'标准 Java（平台管理启停和版本回滚）'},{value:'custom',label:'自定义命令（兼容已有配置）'},{value:'jpom',label:'Jpom 兼容（覆盖发布，人工兜底）',disabled:!jpomEnvironmentAllowed(environments,targetForm.environment,targetForm.protected)}]" :style="{width:'100%',maxWidth:'100%'}" @update:model-value="updateJavaMode"/></Field><template v-if="targetForm.deploy_config.java_mode === 'managed'"><DeploymentAgentOptions v-if="showTarget" v-model="targetForm.deploy_config" :service="String(name)" :environment="targetForm.environment" :hosts="targetForm.hosts||[]"/><Field v-if="targetForm.target_type==='ssh-java-bundle'" class="fld"><FieldLabel>包内主 JAR<HelpTip text="必须与制品清单中的主 JAR 一致。运行目录为当前完整版本的包根目录，工具和数据随版本回滚；包内启停脚本仅保留，不自动执行。"/></FieldLabel><Input v-model="targetForm.deploy_config.java_bundle_jar" aria-label="部署包内主 JAR" placeholder="lib/estate-gis-service.jar"/></Field><Field class="fld"><FieldLabel class="flex items-center gap-1">运行 JDK 版本</FieldLabel><OptionPicker v-model="targetForm.deploy_config.java_version" :options="['8','11','17','21','25']" :style="{width:'100%',maxWidth:'100%'}"/></Field><Field class="fld"><FieldLabel class="flex items-center gap-1">主机上的 JDK 目录<HelpTip text="与构建 JDK 独立。发布前检查版本和制品字节码，不自动安装 JDK。"/></FieldLabel><Input v-model="targetForm.deploy_config.java_home" placeholder="/home/work/runtime/jdk-21（留空使用主机 java）"/></Field><Field v-if="targetForm.target_type!=='ssh-java-bundle'" ref="runtimeWorkingDirEditor" class="fld"><FieldLabel :for="`${name}-java-workdir`" class="flex items-center gap-1">运行工作目录（可选）<HelpTip text="留空使用本次发布版本目录。迁移旧应用时可填写原工作目录，保留相对文件路径；发布前检查目录存在，不会自动创建。"/></FieldLabel><Input :id="`${name}-java-workdir`" v-model="targetForm.deploy_config.java_workdir" placeholder="绝对路径；留空沿用发布版本目录"/></Field><Field class="fld"><FieldLabel class="flex items-center gap-1">JVM 参数（每行一个参数，不加引号）</FieldLabel><Textarea v-model="targetForm.deploy_config.java_jvm_options" placeholder="-Xms256m&#10;-Xmx1024m&#10;-Duser.timezone=Asia/Shanghai" :rows="({minRows:(3),maxRows:Math.max((3),16)}).minRows||4"/></Field><Field class="fld"><FieldLabel class="flex items-center gap-1">应用参数（每行一个参数）</FieldLabel><Textarea v-model="targetForm.deploy_config.java_args" placeholder="--server.port=8889" :rows="({minRows:(2),maxRows:Math.max((2),16)}).minRows||4"/></Field><Field class="fld"><FieldLabel class="flex items-center gap-1">就绪检查端口<HelpTip text="仅检查 127.0.0.1 上的监听状态，不改变应用端口；用应用参数或环境变量配置实际端口。端口和健康检查 URL 至少填一项。"/></FieldLabel><Input v-model="targetForm.deploy_config.java_port" placeholder="8889"/></Field><Field class="fld"><FieldLabel class="flex items-center gap-1">健康检查失败时<HelpTip text="受保护环境固定自动回滚。只有明确启动失败才按策略处理；启动慢、断连或结果不明进入待核对，保留进程并暂停后续任务。"/></FieldLabel><OptionPicker v-model="targetForm.deploy_config.health_failure_action" :disabled="environments.find(e=>e.name===targetForm.environment)?.protected" :options="[{value:'rollback',label:'明确启动失败时恢复旧版本（推荐）'},{value:'stop',label:'停止新版本，不恢复旧版本'},{value:'keep',label:'保留新版本运行（仅排障）'}]" :style="{width:'100%',maxWidth:'100%'}"/></Field><p class="help">仅支持 Linux；版本存入 releases，current 指向生效版本。平台校验 PID 归属、优雅停止和就绪条件；明确失败时按策略处理，启动慢或结果不明只待核对。不会接管 Jpom 已启动的进程，首次切换需先停止旧服务。不提供开机自启或进程崩溃自动重启。</p></template><template v-else-if="targetForm.deploy_config.java_mode==='jpom'"><JpomCompatibilityFields v-model="targetForm.deploy_config"/></template><template v-else><Field class="fld"><FieldLabel class="flex items-center gap-1">启动命令</FieldLabel><Textarea v-model="targetForm.start_cmd" placeholder="nohup java -jar /opt/service/app.jar ..." :rows="({minRows:(2),maxRows:Math.max((2),16)}).minRows||4"/></Field><Field class="fld"><FieldLabel class="flex items-center gap-1">停止命令</FieldLabel><Textarea v-model="targetForm.stop_cmd" :rows="({minRows:(2),maxRows:Math.max((2),16)}).minRows||4"/></Field></template></template><Field v-if="targetForm.target_type!=='aliyun-sae'" class="fld"><FieldLabel class="flex items-center gap-1">健康检查 URL</FieldLabel><Input v-model="targetForm.health_url"/></Field></section><section v-show="targetSection==='variables'" class="target-group"><p v-if="svc?.artifact_type==='static'&&targetForm.target_type!=='script'" class="help">静态制品无需运行环境变量；构建变量请在应用设置中配置。</p><div v-if="svc?.artifact_type!=='static'||targetForm.target_type==='script'" class="fld runtime-env-editor"><RuntimeEnvironmentEditor ref="runtimeVariablesEditor" :draft="targetForm"/><small v-if="targetForm.target_type==='docker-host'" class="help">仅在部署时注入，不传给镜像构建。API Key 等密钥在这里配置；JVM 参数可使用 JAVA_TOOL_OPTIONS=-Xms256m -Xmx1024m。查询只返回已保存变量名，不返回值。</small><small v-if="['ssh-jar','ssh-java-bundle'].includes(targetForm.target_type)" class="help">标准 Java 模式按版本保存并加载变量快照；自定义模式写入部署目录下的 <code>.devopsplatform.env</code>。若自定义命令使用 systemctl，服务文件需声明相应 EnvironmentFile。</small></div></section><section v-if="['docker-host','aliyun-sae'].includes(targetForm.target_type)" v-show="targetSection==='runtime'" class="target-group"><template v-if="targetForm.target_type === 'docker-host'"><Field class="fld"><FieldLabel class="flex items-center gap-1">容器名</FieldLabel><Input v-model="targetForm.deploy_config.container_name" :placeholder="name"/></Field><Field class="fld"><FieldLabel class="flex items-center gap-1">端口映射</FieldLabel><Input v-model="targetForm.deploy_config.ports" placeholder="8080:8080,9090:9090"/></Field><DockerRuntimeFields :config="targetForm.deploy_config"/><Field class="fld"><FieldLabel class="flex items-center gap-1">远程 env-file</FieldLabel><Input v-model="targetForm.deploy_config.env_file" placeholder="/opt/service/app.env"/></Field><Field class="fld"><FieldLabel class="flex items-center gap-1">额外 run 参数（高级）<HelpTip text="仅支持受控 Docker 参数，不执行 Shell。密钥不能填在此处，请使用“应用变量”。旧网络与 bind 挂载参数仍会解析、校验。"/></FieldLabel><Input v-model="targetForm.deploy_config.run_args" placeholder="--memory 2g --cpus 2"/></Field></template><template v-if="targetForm.target_type==='aliyun-sae'"><div class="two"><Field class="fld"><FieldLabel class="flex items-center gap-1">SAE 连接</FieldLabel><OptionPicker v-model="targetForm.deploy_config.connection" :options="[{value:'',label:'请选择 SAE 连接'},...saeConnections.map(c=>({value:c.name,label:`${c.name}（${c.config?.region}）`}))]" :style="{width:'100%',maxWidth:'100%'}"/></Field><Field class="fld"><FieldLabel class="flex items-center gap-1">地域</FieldLabel><Input v-model="targetForm.deploy_config.region" placeholder="默认取连接地域"/></Field></div><div class="two"><Field class="fld"><FieldLabel class="flex items-center gap-1">SAE 应用 ID</FieldLabel><Input v-model="targetForm.deploy_config.app_id" placeholder="应用 UUID；每个环境使用独立应用"/></Field><Field class="fld"><FieldLabel class="flex items-center gap-1">命名空间 ID</FieldLabel><Input v-model="targetForm.deploy_config.namespace_id"/></Field></div><template v-if="svc?.artifact_type==='jar'"><div class="two"><Field class="fld"><FieldLabel class="flex items-center gap-1">OSS Bucket（可覆盖连接）</FieldLabel><Input v-model="targetForm.deploy_config.oss_bucket" placeholder="默认取 SAE 连接"/></Field><Field class="fld"><FieldLabel class="flex items-center gap-1">JDK</FieldLabel><Input v-model="targetForm.deploy_config.jdk" placeholder="Open JDK 8"/></Field></div><div class="two"><Field class="fld"><FieldLabel class="flex items-center gap-1">JVM 启动选项</FieldLabel><Input v-model="targetForm.deploy_config.jar_start_options" placeholder="-Xms512m -Xmx512m"/></Field><Field class="fld"><FieldLabel class="flex items-center gap-1">JAR 参数</FieldLabel><Input v-model="targetForm.deploy_config.jar_start_args"/></Field></div></template><template v-if="svc?.artifact_type==='image'"><div class="two"><Field class="fld"><FieldLabel class="flex items-center gap-1">ACR 企业实例 ID（可选）</FieldLabel><Input v-model="targetForm.deploy_config.acr_instance_id"/></Field><Field class="fld"><FieldLabel class="flex items-center gap-1">容器启动命令（可选）</FieldLabel><Input v-model="targetForm.deploy_config.command"/></Field></div><Field class="fld"><FieldLabel class="flex items-center gap-1">命令参数 JSON（可选）</FieldLabel><Input v-model="targetForm.deploy_config.command_args" placeholder='["--server.port=8080"]'/></Field></template><Accordion class="advanced-box platform-collapse" type="multiple" :model-value="(_view3||[]).map(String)" @update:model-value="_view3=$event"><AccordionItem :value="String(&quot;panel&quot;)"><AccordionTrigger>SAE 高级参数</AccordionTrigger><AccordionContent force-mount v-show="!(!(_view3||[]).map(String).includes(String(&quot;panel&quot;)))"><div class="two"><Field class="fld"><FieldLabel class="flex items-center gap-1">最小可用实例比例</FieldLabel><Input v-model="targetForm.deploy_config.min_ready_ratio" placeholder="例如 50"/></Field><Field class="fld"><FieldLabel class="flex items-center gap-1">批次间隔（秒）</FieldLabel><Input v-model="targetForm.deploy_config.batch_wait_seconds" placeholder="例如 10"/></Field></div><Field class="fld"><FieldLabel class="flex items-center gap-1">环境变量 JSON</FieldLabel><Textarea v-model="targetForm.deploy_config.envs" placeholder='[{"name":"SPRING_PROFILES_ACTIVE","value":"prod"}]' :rows="({minRows:(3),maxRows:Math.max((3),16)}).minRows||4"/></Field><Field class="fld"><FieldLabel class="flex items-center gap-1">超时（分钟）</FieldLabel><Input v-model="targetForm.deploy_config.timeout_minutes" placeholder="30"/></Field></AccordionContent></AccordionItem></Accordion><small class="help">环境隔离由“环境 → 独立 SAE 应用 ID”保证；同一 SAE 应用不能重复绑定到其他环境。</small></template></section><section v-show="targetSection==='policy'" class="target-group"><ReleasePolicyEditor :target="targetForm" :disabled="savingTarget"/><Accordion class="advanced-box platform-collapse" type="multiple" :model-value="(_view4||[]).map(String)" @update:model-value="_view4=$event"><AccordionItem :value="String(&quot;panel&quot;)"><AccordionTrigger>高级部署参数</AccordionTrigger><AccordionContent force-mount v-show="!(!(_view4||[]).map(String).includes(String(&quot;panel&quot;)))"><Field class="fld"><FieldLabel class="flex items-center gap-1">部署驱动</FieldLabel><OptionPicker v-model="targetForm.target_type" :options="svc?.artifact_type==='java-bundle'?[{value:'ssh-java-bundle',label:'Java 整包 / SSH'}]:[{value:'ssh-jar',label:'Java JAR / SSH'},{value:'ssh-static',label:'静态站点 / SSH'},{value:'docker-host',label:'Docker 主机 / SSH'},...(svc?.artifact_type==='static'?[]:[{value:'aliyun-sae',label:'阿里云 SAE（JAR / Docker）'}])]" :style="{width:'100%',maxWidth:'100%'}"/></Field><Field v-if="!['aliyun-sae','ssh-jar','ssh-java-bundle'].includes(targetForm.target_type)" class="fld"><FieldLabel class="flex items-center gap-1">停止命令</FieldLabel><Input v-model="targetForm.stop_cmd"/></Field></AccordionContent></AccordionItem></Accordion></section></div></fieldset></Tabs><div class="target-config-actions"><Button @click="cancelTarget" variant="outline" size="default" type="button" :disabled="(savingTarget)">取消</Button><Button @click="saveTarget" variant="default" size="default" type="button" :disabled="savingTarget||detectingJpom"><Spinner v-if="savingTarget||detectingJpom"/>保存环境配置</Button></div></section><Dialog :open="showDeployLog" @update:open="async v=>{if(!v){showDeployLog = false}}"><DialogContent class="flex max-h-[92dvh] flex-col gap-0 overflow-hidden p-0 sm:max-w-[var(--editor-width)]" :class="false?'inset-0 h-dvh max-h-dvh w-screen max-w-none translate-x-0 translate-y-0 rounded-none sm:max-w-none':''" :style="{'--editor-width':typeof (&quot;min(720px, 92vw)&quot;)==='number'?(&quot;min(720px, 92vw)&quot;)+'px':(&quot;min(720px, 92vw)&quot;)}" :show-close-button="true" @interact-outside="e=>{e.preventDefault()}"><DialogHeader class="shrink-0 border-b p-5 pr-12"><DialogTitle>部署日志</DialogTitle><DialogDescription class="sr-only">详情与操作</DialogDescription></DialogHeader><div class="min-h-0 flex-1 overflow-auto p-5"><pre style="max-height:60vh;overflow:auto;white-space:pre-wrap;word-break:break-all;background:#0a0d12;color:#c9d1d9;padding:12px;border-radius:8px">{{ deployLog }}</pre></div></DialogContent></Dialog><section v-if="showReleaseDetail&&activeRelease?.manual_publication" class="my-5 min-w-0"><div class="mb-4 flex flex-wrap items-center justify-between gap-3"><h3 class="text-lg font-semibold">手动发布 {{releaseOrderLabel(activeRelease)}} · {{activeRelease.environment}}</h3><Button variant="outline" :disabled="manualPublicationBusy" @click="closeReleaseDetail">返回发布列表</Button></div><ManualPublicationPanel :key="activeRelease.id" :order="activeRelease" :me="me" :draft="manualDraft(activeRelease)" @updated="activeRelease=$event;loadReleaseOrders();releaseRecords?.refresh()" @state="manualPublicationBusy=$event.busy"/></section><Dialog :open="showReleaseDetail&&!activeRelease?.manual_publication" @update:open="async v=>{if(!v){closeReleaseDetail()}}"><DialogContent class="flex max-h-[92dvh] flex-col gap-0 overflow-hidden p-0 sm:max-w-[var(--editor-width)]" :class="false?'inset-0 h-dvh max-h-dvh w-screen max-w-none translate-x-0 translate-y-0 rounded-none sm:max-w-none':''" :style="{'--editor-width':typeof (&quot;min(720px, 92vw)&quot;)==='number'?(&quot;min(720px, 92vw)&quot;)+'px':(&quot;min(720px, 92vw)&quot;)}" :show-close-button="true" @interact-outside="e=>{e.preventDefault()}"><DialogHeader class="shrink-0 border-b p-5 pr-12"><DialogTitle>{{`发布单 ${releaseOrderLabel(activeRelease)}`}}</DialogTitle><DialogDescription>全局记录 ID {{activeRelease?.id}} · {{activeRelease?.service}} · {{activeRelease?.environment}}</DialogDescription></DialogHeader><div class="min-h-0 flex-1 overflow-auto p-5"><ScriptPublicationResult v-if="activeRelease?.script_publication" :key="activeRelease.id" :order="activeRelease" :me="me" :draft="manualDraft(activeRelease)" @updated="activeRelease=$event;loadReleaseOrders();releaseRecords?.refresh()" @state="manualPublicationBusy=$event.busy"/><ReleaseNotice v-if="showReleaseDetail&&activeRelease" ref="releaseNotice" :key="activeRelease.id" kind="release" :record-id="activeRelease.id" :context-revision="activeRelease.status" :editable="me.role==='admin'||me.username===activeRelease.actor"/><ReleaseRecovery v-if="activeRelease?.status==='needs_review'" :order="activeRelease" :deploys="deploys" :me="me" @busy="recoveryBusy=$event" @updated="refreshRecovery" @refresh="refreshRecovery"/><dl class="release-detail-meta grid min-w-0 gap-4 sm:grid-cols-2"><div class="min-w-0 space-y-1"><dt class="text-sm text-muted-foreground">制品版本</dt><dd class="min-w-0 break-words">{{activeRelease?.version||'—'}}</dd></div><div class="min-w-0 space-y-1"><dt class="text-sm text-muted-foreground">目标环境</dt><dd class="min-w-0 break-words">{{activeRelease?.environment||'—'}}</dd></div><div class="min-w-0 space-y-1"><dt class="text-sm text-muted-foreground">发布状态</dt><dd class="min-w-0 break-words"><Badge :variant="(activeRelease?.status==='success'?'green':activeRelease?.status==='failed'?'red':'blue')==='red'?'destructive':'secondary'">{{statusText(activeRelease?.status)}}</Badge></dd></div><div class="min-w-0 space-y-1"><dt class="text-sm text-muted-foreground">执行结果</dt><dd class="min-w-0 break-words">成功 {{activeRelease?.succeeded||0}} / {{activeRelease?.total||0}} · 未成功 {{activeRelease?.failed||0}}<span v-if="activeRelease?.rollback_failed_targets" class="text-destructive"> · {{activeRelease.rollback_failed_targets}} 个目标回滚失败</span></dd></div><div class="min-w-0 space-y-1"><dt class="text-sm text-muted-foreground">目标快照</dt><dd class="min-w-0 break-words">{{targetResource(activeRelease?.target_snapshot)}}</dd></div><div class="min-w-0 space-y-1"><dt class="text-sm text-muted-foreground">{{activeRelease?.target_snapshot?.target_type==='script'?'执行位置':isManagedTarget(activeRelease?.target_snapshot)?'命名空间':'部署目录'}}</dt><dd class="min-w-0 break-words"><code>{{activeRelease?.target_snapshot?.target_type==='script'?activeRelease?.target_snapshot?.deploy_config?.script_runner:isManagedTarget(activeRelease?.target_snapshot)?activeRelease?.target_snapshot?.deploy_config?.namespace_id:(activeRelease?.target_snapshot?.remote_dir||'—')}}</code></dd></div><div class="min-w-0 space-y-1"><dt class="text-sm text-muted-foreground">执行策略</dt><dd class="min-w-0 break-words">{{releasePolicyText(activeRelease?.target_snapshot)}}</dd></div><div class="min-w-0 space-y-1"><dt class="text-sm text-muted-foreground">健康检查</dt><dd class="min-w-0 break-words">{{activeRelease?.target_snapshot?.target_type==='script'?'脚本验证及结构化结果':isManagedTarget(activeRelease?.target_snapshot)?'SAE 变更单状态':(activeRelease?.target_snapshot?.health_url||'未配置')}}</dd></div></dl><div class="console-table min-w-0 max-w-full"><div class="min-w-0 max-w-full" :style="{maxHeight:({x:592}).y?({x:592}).y+'px':undefined}"><DataTable class="[&_[data-slot=table-container]]:rounded-lg [&_[data-slot=table-container]]:border" :rows="(((activeRelease?.deploy_ids||[])||[]).map((id,rowIndex)=>({id,__rowKey:id}))||[])" :columns="[{id:'c0',label:&quot;执行目标&quot;,size:180,minSize:64,required:true,defaultHidden:false,flex:true,selection:false,pin:'left'},{id:'c1',label:&quot;状态&quot;,size:88,minSize:64,required:false,defaultHidden:false,flex:false,selection:false,pin:false},{id:'c2',label:&quot;时间&quot;,size:180,minSize:64,required:false,defaultHidden:false,flex:false,selection:false,pin:false},{id:'c3',label:&quot;操作&quot;,size:152,minSize:64,required:true,defaultHidden:false,flex:false,selection:false,pin:'right'}]" table-key="deploy-pages-DetailView-2" :row-key="(record,rowIndex)=>typeof (&quot;__rowKey&quot;)==='function'?(&quot;__rowKey&quot;)(record):record[&quot;__rowKey&quot;]??rowIndex"><template #header-c0>执行目标</template><template #cell-c0="{record,rowIndex}"><div class="min-w-0 break-words" :class="{truncate:!!(true)}"><template v-for="{record:{id},rowIndex:rowIndex} in [{record,rowIndex}]"><div>{{deployOf(id)?.host||`任务 #${id}`}}<small v-if="deployOf(id)?.external_id" class="artifact-id">变更单 {{deployOf(id).external_id}}</small></div></template></div></template><template #header-c1>状态</template><template #cell-c1="{record,rowIndex}"><div class="min-w-0 break-words" :class="{truncate:!!(true)}"><template v-for="{record:{id},rowIndex:rowIndex} in [{record,rowIndex}]"><div><span :class="statusClass(deployOf(id)?.status)">{{statusText(deployOf(id)?.status||'queued')}}</span></div></template></div></template><template #header-c2>时间</template><template #cell-c2="{record,rowIndex}"><div class="min-w-0 break-words" :class="{truncate:!!(true)}"><template v-for="{record:{id},rowIndex:rowIndex} in [{record,rowIndex}]"><div>{{fmtTime(deployOf(id)?.started_at)}}</div></template></div></template><template #header-c3>操作</template><template #cell-c3="{record,rowIndex}"><div class="min-w-0 break-words" :class="{truncate:!!(false)}"><template v-for="{record:{id},rowIndex:rowIndex} in [{record,rowIndex}]"><div class="cell-actions"><Button v-if="deployOf(id)" @click="openDeployLog(deployOf(id))" variant="ghost" size="sm" type="button">日志</Button></div></template></div></template><template #empty><Empty><EmptyHeader><EmptyDescription>审批或排队完成后生成执行任务</EmptyDescription></EmptyHeader></Empty></template></DataTable></div></div></div></DialogContent></Dialog></div></template>
<style scoped>.publish-compatibility-error{display:flex;align-items:center;flex-wrap:wrap;gap:8px;color:var(--error);font-size:.75rem;line-height:1.6}.publish-compatibility-error>span{flex:1;min-width:180px}.publish-dialog{display:grid;gap:14px;min-width:0}.publish-dialog>label{display:grid;gap:6px;font-size:.875rem}.publish-dialog small{color:var(--muted-foreground);font-size:.75rem;line-height:1.6}.publish-brief{display:grid;gap:8px;padding:12px;background:var(--panel2);border-radius:var(--radius);font-size:.75rem;line-height:1.6}.publish-brief code{overflow-wrap:anywhere;white-space:pre-wrap}</style>
<style scoped>.code-policy-group{display:block}
.advanced-heading{display:flex;align-items:center;gap:8px;margin-bottom:12px}.advanced-heading h3{font-size:1rem;margin:0}
.settings-editor-body{border:0;padding:0;margin:0;min-width:0}
.app-name{min-width:0;overflow-wrap:anywhere}.back{min-width:36px}.header-environment{display:inline-block;flex-shrink:0}
.app-tabs{display:flex;gap:2px;border-bottom:1px solid var(--border);margin:14px 0 18px}
.more-tabs{position:relative;margin-left:auto;color:var(--muted-foreground)}.more-tabs>div{position:absolute;right:0;top:34px;z-index:20;display:flex;flex-direction:column;min-width:170px;padding:5px;background:var(--panel);border:1px solid var(--border);border-radius:var(--radius);box-shadow:0 8px 24px rgba(0,0,0,.18)}
.overview-strip{display:grid;grid-template-columns:repeat(3,1fr);background:var(--panel);border:1px solid var(--border);border-radius:var(--radius);margin-bottom:20px}.overview-strip>div{display:flex;flex-direction:column;padding:14px 16px;border-right:1px solid var(--border)}.overview-strip>div:last-child{border:0}.overview-strip small{color:var(--muted-foreground)}.overview-strip b{font-size:1.125rem;margin:3px 0}.overview-strip span{font-size:.75rem;color:var(--dim)}
.delivery-flow{background:var(--panel);border:1px solid var(--border);border-radius:var(--radius);padding:14px 16px;margin-bottom:20px}.flow-head{display:flex;align-items:center;gap:12px;margin-bottom:13px}.flow-head h3{font-size:1rem}.flow-head p{font-size:.75rem;color:var(--muted-foreground);margin-top:2px}.flow-head p b{color:var(--text)}.observe-actions{display:flex;gap:7px;margin-left:auto}.flow-steps{display:flex;align-items:stretch;gap:7px}.flow-steps>em{display:flex;align-items:center;color:var(--dim);font-style:normal}.flow-step{display:flex;align-items:center;gap:9px;min-width:0;flex:1;padding:10px;text-align:left;background:var(--panel2);border:1px solid transparent;cursor:pointer}.flow-step:hover{border-color:var(--brand)}.flow-step>i{display:grid;place-items:center;flex:0 0 23px;height:23px;border-radius:50%;background:var(--border);color:var(--muted-foreground);font-style:normal;font-size:.75rem}.flow-step>span{display:flex;flex-direction:column;min-width:0}.flow-step small{color:var(--dim)}.flow-step b{font-size:.75rem;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.flow-step.done>i{background:var(--ok);color:#fff}.flow-step.active>i{background:var(--brand);color:#fff}.flow-step.error>i{background:var(--error);color:#fff}.flow-step.ready>i{background:var(--info);color:#fff}
.section-head{display:flex;align-items:center;margin:8px 0 12px}.section-head h3{font-size:1rem}.section-head p{font-size:.75rem;color:var(--muted-foreground);margin-top:2px}.section-head>.text-link{margin-left:auto}.section-head.compact{margin-top:22px}
.env-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(245px,1fr));gap:12px}.env-card{background:var(--panel);border:1px solid var(--border);border-radius:var(--radius);padding:14px;cursor:pointer}.env-card.focused{border-color:var(--brand);box-shadow:inset 0 0 0 1px color-mix(in srgb,var(--brand) 55%,transparent)}.env-title{display:flex;align-items:center;gap:8px}.env-title>div{min-width:0}.env-title h4{font-size:.875rem}.env-title small{color:var(--muted-foreground);font-size:.75rem}.env-title>span:last-child{margin-left:auto;font-size:.75rem}.env-dot{width:8px;height:8px;border-radius:50%;background:var(--dim)}.env-dot.ok{background:var(--ok);box-shadow:0 0 0 3px color-mix(in srgb,var(--ok) 15%,transparent)}.current-version{display:flex;flex-direction:column;background:var(--panel2);border-radius:var(--radius);padding:9px 10px;margin:12px 0}.current-version small{color:var(--dim)}.current-version code{margin:2px 0}.current-version span{font-size:.75rem;color:var(--muted-foreground)}.env-actions{display:flex;align-items:center;gap:6px}.env-actions .remove{margin-left:auto;color:var(--dim);font-size:.75rem;cursor:pointer}.env-actions .remove:hover{color:var(--error)}
.add-env{min-height:164px;border:1px dashed var(--border);background:transparent;display:flex;flex-direction:column;align-items:center;justify-content:center;gap:4px;color:var(--muted-foreground)}.add-env span{font-weight:600}.add-env small{font-size:.75rem}.env-meta{margin:12px 0}.env-meta>div{display:grid;grid-template-columns:72px 1fr;padding:4px 0}.env-meta dt{color:var(--dim);font-size:.75rem}.env-meta dd{font-size:.75rem;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.release-snapshot{background:var(--panel2);padding:10px;border-radius:var(--radius);margin-bottom:12px}
.release-bar{display:flex;align-items:flex-end;gap:12px;background:var(--panel);border:1px solid var(--border);border-radius:var(--radius);padding:14px;margin-bottom:14px}.release-bar>div{display:flex;flex-direction:column;gap:4px}.release-bar label{font-size:.75rem;color:var(--muted-foreground)}.release-bar .arrow{padding-bottom:6px;color:var(--dim)}.dim{color:var(--dim)}
.snapshot{font-size:.75rem;color:var(--muted-foreground)}.artifact-id{display:block;color:var(--dim);font-family:var(--mono)}.artifact-warning{color:var(--warn);background:color-mix(in srgb,var(--warn) 9%,var(--panel));border:1px solid color-mix(in srgb,var(--warn) 30%,var(--border));padding:9px 11px;border-radius:var(--radius);margin-bottom:10px;font-size:.75rem}.progress{width:100px;height:3px;background:var(--panel2);border-radius:var(--radius);margin-top:4px}.progress i{display:block;height:100%;background:var(--ok);border-radius:var(--radius)}.settings-grid{display:grid;grid-template-columns:1fr 1fr;gap:12px}.settings-card{background:var(--panel);border:1px solid var(--border);border-radius:var(--radius);padding:14px}.settings-card h4{font-size:.875rem;margin-bottom:12px}.check-list{display:flex;flex-direction:column;gap:6px}.check-list label{display:flex;align-items:center;gap:6px;border:1px solid var(--border);border-radius:var(--radius);padding:7px}.check-list small{margin-left:auto;color:var(--dim);font-size:.75rem}.help{font-size:.75rem;color:var(--muted-foreground);margin:-5px 0 12px}.save-row{display:flex;justify-content:flex-end;margin-top:12px}.strategy-row{display:grid;grid-template-columns:repeat(3,1fr);gap:8px}.pause-check{display:block;margin:-2px 0 12px}.version-diff{display:grid;grid-template-columns:1fr auto 1fr;gap:10px;align-items:center;background:var(--panel2);padding:9px;border-radius:var(--radius);margin-bottom:10px}.version-diff>div{display:flex;flex-direction:column}.version-diff small{color:var(--dim)}.approval-note{color:var(--warn);background:color-mix(in srgb,var(--warn) 10%,var(--panel));padding:8px;border-radius:var(--radius);font-size:.75rem}.one-click-flow{display:flex;align-items:center;justify-content:center;gap:8px;padding:11px;margin-bottom:14px;border:1px solid color-mix(in srgb,var(--brand) 25%,var(--border));border-radius:var(--radius);background:color-mix(in srgb,var(--brand) 6%,var(--panel))}.one-click-flow span{padding:4px 7px;border-radius:var(--radius);background:var(--panel2);font-size:.75rem;font-weight:600}.one-click-flow i{color:var(--dim);font-style:normal}.one-click-note{padding:8px 10px;border-radius:var(--radius);background:var(--panel2);color:var(--muted-foreground);font-size:.75rem}.one-click-actions{display:flex;justify-content:flex-end;gap:8px;margin-top:14px}
.build-config-head{display:flex;align-items:center;gap:8px;padding:14px 16px;margin-bottom:10px;border:1px solid var(--border);border-radius:var(--radius);background:var(--panel)}.build-config-head>div{min-width:0;margin-right:auto}.build-config-head h3{font-size:1.125rem}.build-config-head p{margin-top:3px;color:var(--muted-foreground);font-size:.75rem}.unsaved-dot{margin-right:auto;color:var(--warn);font-size:.75rem;white-space:nowrap}.build-config-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:12px;max-height:64vh;overflow:auto;padding-right:4px}.build-config-grid .settings-card{min-width:0}.build-config-actions{display:flex;align-items:center;gap:8px;padding-top:14px;margin-top:12px;border-top:1px solid var(--border)}.card-title{display:flex;align-items:center;gap:9px;margin-bottom:13px}.card-title>span{display:grid;place-items:center;flex:0 0 25px;height:25px;border-radius:50%;background:color-mix(in srgb,var(--brand) 12%,var(--panel));color:var(--brand);font-size:.75rem;font-weight:700}.card-title>div{display:flex;flex-direction:column;min-width:0}.card-title h4{margin:0;font-size:.875rem}.card-title small{color:var(--muted-foreground);font-size:.75rem}.card-title .resource-link{margin-left:auto}.config-help{display:block;margin-top:4px;color:var(--muted-foreground);font-size:.75rem;line-height:1.55}.build-tool-tabs{display:grid;grid-template-columns:1fr 1fr;gap:8px}.build-tool-tabs small{color:var(--muted-foreground);font-size:.75rem}.inline-advanced{margin-top:8px;padding-top:8px;border-top:1px dashed var(--border)}.empty-inline{padding:9px;color:var(--dim);font-size:.75rem;text-align:center;border:1px dashed var(--border);border-radius:var(--radius)}.build-advanced-card{display:flex;flex-direction:column}.build-advanced-card p{color:var(--muted-foreground);font-size:.75rem;line-height:1.6;margin-bottom:12px}.build-history-head{margin-top:24px;padding-top:18px;border-top:1px solid var(--border)}
.runtime-env-editor{padding:10px;border:1px solid var(--border);border-radius:var(--radius);background:var(--panel2)}.runtime-env-editor .help{display:block;margin:5px 0 0}.clear-runtime-env{display:flex;align-items:center;gap:6px;margin-top:8px;color:var(--error);font-size:.75rem}
.runtime-hint{display:flex;align-items:center;gap:10px;background:var(--panel2);padding:10px;border-radius:var(--radius);margin-bottom:14px}.runtime-hint>span{font-size:1.25rem}.runtime-hint>div{display:flex;flex-direction:column}.runtime-hint small{color:var(--muted-foreground)}.host-picker{display:grid;grid-template-columns:repeat(2,1fr);gap:7px}.host-picker label{display:grid;grid-template-columns:auto 1fr;align-items:center;column-gap:6px;border:1px solid var(--border);border-radius:var(--radius);padding:8px;cursor:pointer}.host-picker label.checked{border-color:var(--brand);background:color-mix(in srgb,var(--brand) 7%,var(--panel))}.host-picker small{font-size:.75rem;color:var(--muted-foreground)}.advanced-box{margin:10px 0 14px}
.target-config-form{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));align-items:start;gap:10px 16px;max-height:min(68vh,680px);overflow-y:auto;padding:2px 8px 4px 2px}.target-config-form>.fld{min-width:0;margin-bottom:0}.target-config-form>.runtime-hint,.target-config-form>.runtime-env-editor,.target-config-form>.strategy-row,.target-config-form>.pause-check,.target-config-form>.advanced-box,.target-config-form>p.help,.target-config-form>small.help,.target-config-form>.two{grid-column:1/-1}.target-config-form>.runtime-hint{margin-bottom:0}.target-config-form>.runtime-env-editor{margin-top:2px}.target-config-form>.advanced-box{margin:0}.target-config-form>.pause-check{margin:0}.target-config-form>p.help{margin:0;padding:8px 10px;border-radius:var(--radius);background:var(--panel2);line-height:1.55}.target-config-actions{display:flex;justify-content:flex-end;gap:8px;padding-top:12px;margin-top:12px;border-top:1px solid var(--border)}
.instance-toolbar{display:flex;align-items:center;gap:12px;margin-bottom:12px}.instance-toolbar>div{display:flex;flex-direction:column}.instance-toolbar small{color:var(--muted-foreground)}.instance-message{display:block;max-width:300px;margin-top:3px;color:var(--error);overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.instance-actions{display:flex;gap:5px;white-space:nowrap}.instance-confirm{margin-top:14px;padding:12px;border:1px solid color-mix(in srgb,var(--warn) 35%,var(--border));border-radius:var(--radius);background:color-mix(in srgb,var(--warn) 8%,var(--panel))}.instance-confirm>div:first-child{display:flex;flex-direction:column;margin-bottom:10px}.instance-confirm small{color:var(--muted-foreground)}.instance-confirm>div:last-child{display:flex;justify-content:flex-end;gap:8px;margin-top:10px}
@media(max-width:800px){.overview-strip{grid-template-columns:1fr}.overview-strip>div{border-right:0;border-bottom:1px solid var(--border)}.header-environment{display:none}.flow-head{align-items:flex-start;flex-direction:column}.observe-actions{margin-left:0}.flow-steps{flex-direction:column}.flow-steps>em{display:none}.env-grid,.settings-grid,.build-config-grid,.target-config-form{grid-template-columns:1fr}.build-config-head{align-items:stretch;flex-direction:column}.build-config-head>div{margin-right:0}.release-bar{align-items:stretch;flex-direction:column}.release-bar .arrow{display:none}.app-tabs{overflow:auto}.host-picker{grid-template-columns:1fr}.strategy-row{grid-template-columns:1fr}.one-click-flow{align-items:stretch;flex-direction:column}.one-click-flow i{display:none}}</style>
<style scoped>.app-more{position:relative;font-size:.75rem}
.compact-overview{margin-bottom:12px}.compact-overview>div{padding:12px 16px}.compact-overview b{font-size:1.125rem}.overview-next,.build-config-compact{display:flex;align-items:center;gap:10px;padding:10px 12px;background:var(--panel);border:1px solid var(--border);border-radius:var(--radius);margin-bottom:12px;font-size:.75rem}.overview-next>span,.build-config-compact>span{flex:1;min-width:0;overflow-wrap:anywhere;color:var(--muted-foreground)}.add-env{min-height:100px!important}.settings-workspace{display:grid;grid-template-columns:180px minmax(0,1fr);gap:20px}.settings-workspace>nav{display:flex;flex-direction:column;gap:4px}.settings-workspace .build-config-grid{display:block;max-height:none;overflow:visible;padding-right:0}.settings-workspace .inline-advanced{margin:0;padding-top:0;border-top-style:solid}.settings-workspace .settings-card{border:1px solid var(--border);background:var(--panel);padding:18px;box-shadow:none}.settings-workspace .build-config-actions{justify-content:flex-end;gap:8px;position:sticky;bottom:0;z-index:2;background:var(--panel);padding:12px;border-top:1px solid var(--border)}.settings-workspace[data-section=source] .settings-card:not(.build-source-card),.settings-workspace[data-section=command] .settings-card:not(.build-command-card),.settings-workspace[data-section=environment] .settings-card:not(.build-environment-card),.settings-workspace[data-section=resource] .settings-card:not(.build-resource-card),.settings-workspace[data-section=trigger] .settings-card:not(.build-trigger-card),.settings-workspace[data-section=advanced] .settings-card:not(.build-advanced-card),.settings-workspace[data-section=bindings] .settings-card{display:none}.settings-bindings{background:var(--panel);border:1px solid var(--border);border-radius:var(--radius);padding:18px}.settings-bindings p{font-size:.75rem;color:var(--muted-foreground)}
@media(max-width:800px){.settings-workspace{grid-template-columns:1fr}.settings-workspace>.settings-section-navigation{flex-direction:row;flex-wrap:nowrap;overflow-x:auto;max-width:100%;padding-bottom:4px}.settings-section-navigation :deep([data-slot=sidebar-menu-item]){flex-shrink:0;width:auto}.overview-next,.build-config-compact{flex-wrap:wrap}}</style>

<style scoped>
.target-workspace{max-width:1120px;margin:12px auto 0}
.target-editor-heading{display:flex;align-items:center;justify-content:space-between;gap:16px;margin:0 0 20px;padding-bottom:16px;border-bottom:1px solid var(--border)}
.target-editor-heading h3{font-size:1.125rem;font-weight:600;margin:0}.target-editor-context{display:flex;align-items:center;gap:10px;min-width:0}
.target-editor-layout{display:grid;grid-template-columns:180px minmax(0,1fr);gap:28px;align-items:start}
.target-editor-navigation{position:sticky;top:12px;min-width:0}.target-editor-body{border:0;padding:0;margin:0;min-width:0}
.target-workspace .target-config-form{display:block;max-height:none;overflow:visible;padding:0}
.target-group{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));align-items:start;gap:24px;background:var(--background);border:0;padding:0 0 24px;margin:0 0 24px;border-bottom:1px solid var(--border);min-width:0}
.target-group>.runtime-env-editor,.target-group>.two,.target-group>.strategy-row,.target-group>.advanced-box,.target-group>.pause-check,.target-group>.help{grid-column:1/-1}
.target-group>.fld{margin:0;min-width:0}.target-group .fld>label{display:block;font-size:.875rem;font-weight:500;line-height:1.4;margin-bottom:8px}.target-group .fld>small{display:block;margin-top:6px;font-size:.75rem}.target-group.code-policy-group{display:block}
.target-workspace .target-config-actions{position:sticky;bottom:0;background:var(--background);padding:16px 0;margin:0;border-top:1px solid var(--border);z-index:2;min-height:68px}
@media(max-width:900px){.target-editor-layout{grid-template-columns:150px minmax(0,1fr);gap:20px}.target-group{grid-template-columns:1fr}}
@media(max-width:640px){.target-editor-heading{align-items:flex-start;flex-direction:column}.target-editor-layout{display:flex;flex-direction:column;gap:20px}.target-editor-navigation{position:static;width:100%;overflow:auto}.target-editor-navigation :deep([data-slot=tabs-list]){flex-direction:row;width:max-content}.target-editor-body{width:100%}}
</style>

<style scoped>.app-view-tabs{min-width:0}.environment-empty{margin:8px 0 20px}@media(max-width:760px){}</style>

<style scoped>.release-detail-meta{margin:16px 0 20px}.release-detail-meta code{overflow-wrap:anywhere;font:.75rem/1.6 var(--mono)}.target-editor-heading{gap:8px}.settings-workspace{align-items:start}.application-delete-action{display:flex;align-items:center;justify-content:space-between;gap:16px;border-top:1px solid var(--border);margin-top:20px;padding-top:16px}.application-delete-action p{margin:6px 0 0;color:var(--muted-foreground);font-size:.75rem}@media(max-width:600px){.application-delete-action{align-items:flex-start;flex-direction:column}}</style>
