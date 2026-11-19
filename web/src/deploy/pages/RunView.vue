<script setup>
import {buildRunLabel} from '../buildNumber.js'
import {mavenLockFailure} from '../mavenBuild.js'
import {createBuildLogStream} from '../buildLogStream.js'
import {copyText} from '../../shared/clipboard.js'
import {deploymentTargetReady} from '../deploymentTarget.js'



import ArtifactOutputs from '../components/ArtifactOutputs.vue'
import PageHeader from '../../shared/components/PageHeader.vue'
import {buildReturn} from '../buildNavigation.js'
import {buildRepairLocation, currentBuildLocation} from '../buildExecutable.js'
import {ref, computed, onMounted, onUnmounted, nextTick, watch, inject} from 'vue'
import {useRoute, useRouter} from 'vue-router'
import {useWS} from '../../shared/composables/ws.js'
import {toast} from '../../shared/composables/toast.js'



const route = useRoute(), router = useRouter()
const globalEnvironment = inject('globalEnvironment', ref(''))
const setPlatformContext = inject('setPlatformContext', () => {})
const id = computed(() => route.params.id)
const documentation=ref(null)
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
function copyLog(){return copyText(visibleLog.value,{successMessage:'已复制当前显示日志',failureMessage:'浏览器未允许复制，可选中文本后手动复制'})}
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
   run.value=v;loadError.value='';try{const dr=await fetch(`api/pipeline/runs/${requested}/documentation`);if(dr.ok){const d=await dr.json();if(!disposed&&id.value===requested&&sequence===runSequence)documentation.value=d}}catch{}
   if (run.value?.service) setPlatformContext({ service: run.value.service })
  }catch(e){if(!disposed&&requested===id.value&&sequence===runSequence)loadError.value=e.message}
}
function openDocumentation(history=false){const doc=documentation.value,snap=doc?.snapshots?.[0];const hash='#/documents?'+new URLSearchParams({service:run.value.service,environment:doc?.environment||'__unassigned',...(snap?{snapshot:String(snap.id)}:{}),...(history?{tab:'history'}:{})});if(window.parent!==window)window.parent.postMessage({type:'workspace.open',plugin:'api-center',hash},location.origin);else window.open('/api/plugins/api-center/ui'+hash,'_blank','noopener')}
const statusText = computed(() => ({ running: '运行中', waiting_deploy: '等待部署确认', success: '成功', failed: '失败', interrupted: '已中断', canceled: '已取消' })[run.value?.status] || run.value?.status || '…')
const intendedTarget = computed(() => targets.value.find(target => target.environment === run.value?.deploy_environment))
const intendedTargetLabel = computed(() => intendedTarget.value?.display_name || run.value?.deploy_environment || '')
const failureDiagnosis = computed(() => {
  if (run.value?.status !== 'failed') return null
  const text = logText.value || ''
  const mavenLock=mavenLockFailure(text)
  if(mavenLock)return mavenLock
  if (/(?:services|downloads)\.gradle\.org/.test(text)&&/UnknownHostException|Download|failed|timeout|timed out/i.test(text)) return { title: 'Gradle Wrapper 下载失败', gradle:true, detail: '项目 Wrapper 下载发行包失败，尚未完成 Java 编译。可检查构建网络；或在已提供兼容版本的前提下，选择“构建环境内置 Gradle”。保存后按当前配置新建构建，原快照重试不会使用新配置。' }
  if (/无法读取 Maven settings\.xml|Maven settings\.xml 不存在/.test(text)) return {title:'Maven 配置文件不可读取',detail:'核对 settings.xml 及父目录对平台运行账号的读取权限；含凭据的配置应使用仅该账号可读的私有文件。修复配置后按当前配置新建构建，原快照重试仍使用原路径。'}
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
  const replay = createBuildLogStream()
  const base = location.pathname.replace(/\/ui.*$/, '')
  ws = useWS(`${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}${base}/api/pipeline/runs/${id.value}/tail`, {
    onOpen: () => replay.opened(),
    onMessage: d => {
      if(disposed||epoch!==connectionEpoch)return
      logText.value = replay.receive(logText.value, d)
      scrollBottom()
      if (d.includes('⏸') || d.includes('部署结束') || d.includes('✓ 产物')) { loadRun(); loadTargets() }
    },
    onClose: () => setTimeout(async () => {
      if(disposed||epoch!==connectionEpoch)return
      await loadRun()
      if (run.value?.status !== 'running') {
        try {
          const response = await fetch(`api/pipeline/runs/${requested}/log`)
          if (response.ok) {
            const final = await response.json()
            if(disposed||epoch!==connectionEpoch||requested!==id.value)return
            logText.value = final.log || ''
            scrollBottom()
          }
        } catch {}
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
  if (!await confirmAction('放弃本次部署？制品和流水线日志仍会保留。')) return
  const res = await fetch(`api/pipeline/runs/${id.value}/cancel`, { method: 'POST' })
  const r = await res.json()
  if (!res.ok) return toast(r.error || '取消失败', 'error')
  toast('已放弃部署')
  await connect()
}
const canDeploy = computed(() => run.value?.status === 'waiting_deploy' && run.value?.artifact && targets.value.length)
const selectedTarget = computed(() => targets.value.find(t => t.environment === deployEnv.value))
const targetReady = computed(()=>deploymentTargetReady(selectedTarget.value))
const targetLabel = computed(() => selectedTarget.value?.target_type === 'aliyun-sae' ? `SAE ${selectedTarget.value.deploy_config?.app_id} · ${selectedTarget.value.deploy_config?.region}` : selectedTarget.value?.hosts?.join('、') || '未配置')
function startDeployFlow() {
 if(selectedTarget.value?.target_type==='manual'){const a=run.value.artifacts?.find(a=>a.kind===selectedTarget.value.deploy_config?.manual_artifact_kind);router.push(`/svc/${encodeURIComponent(run.value.service)}/release?artifact=${a?.id||run.value.artifact_id}&environment=${encodeURIComponent(deployEnv.value)}`);return}
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
watch(id,async()=>{run.value=null;documentation.value=null;targets.value=[];logText.value='';loadError.value='';follow.value=true;selectedStage.value=-1;logQuery.value='';capabilities.value={};await connect();await loadCapabilities();metadataPoll()})

import OptionPicker from '../../shared/components/OptionPicker.vue'



import {Accordion,AccordionItem,AccordionTrigger,AccordionContent,Badge,Button,Card,CardAction,CardContent,CardFooter,CardHeader,CardTitle,Dialog,DialogContent,DialogDescription,DialogFooter,DialogHeader,DialogTitle,Field,FieldLabel,Input,Spinner,Tabs,TabsContent,TabsList,TabsTrigger,Textarea} from '../../shared/shadcn-ui.js'

import {InputGroup,InputGroupInput,InputGroupAddon,InputGroupButton} from '../../shared/shadcn-ui.js'

import {XIcon} from '@lucide/vue'

import {confirmAction} from "../../shared/shadcnFeedback.js"
</script>
<template><div><PageHeader><template #title><Button @click="back" aria-label="返回构建记录" class="op-btn" variant="outline" size="default" type="button">←</Button><h2 class="text-2xl font-semibold leading-8 tracking-tight">构建 {{buildRunLabel(run)}}</h2><Badge :variant="(run?.status==='success'?'green':run?.status==='failed'?'red':'blue')==='red'?'destructive':'secondary'">{{statusText}}</Badge></template><template #actions><Button v-if="run?.artifact_id" @click="router.push('/svc/'+encodeURIComponent(run.service)+'/release?artifact='+run.artifact_id)" variant="default" size="sm" type="button">发布本次制品</Button><Button v-if="run&&['failed','success','canceled','interrupted'].includes(run.status)" title="读取当前已保存配置，先确认分支，不立即执行" @click="router.push(currentBuildLocation(run.service))" variant="outline" size="sm" type="button">按当前配置新建构建</Button><Button v-if="capabilities.can_retry" title="沿用原 Commit 和配置快照，不使用后来保存的配置" @click="openControl('retry')" variant="outline" size="sm" type="button">按原源码与配置重试</Button><Button v-if="capabilities.can_stop" @click="openControl('stop')" class="text-destructive" :variant="'destructive'" size="sm" type="button">取消构建</Button></template></PageHeader><Accordion v-if="run?.artifacts?.length" type="single" collapsible class="my-4 min-w-0"><AccordionItem value="artifacts"><AccordionTrigger>本次构建制品 · {{run.artifacts.length}} 项</AccordionTrigger><AccordionContent><ArtifactOutputs :artifacts="run.artifacts" :primary-id="run.artifact_id" :disabled="!['success','waiting_deploy'].includes(run.status)" publishable @publish="a=>router.push('/svc/'+encodeURIComponent(run.service)+'/release?artifact='+a.id)"/></AccordionContent></AccordionItem></Accordion><p v-if="loadError" role="alert">{{loadError}} <Button @click="loadRun" variant="outline" size="sm" type="button">重试读取</Button></p><Card v-if="run" class="build-context-card gap-0 py-4 shadow-none"><CardContent class="min-w-0"><dl class="grid min-w-0 gap-x-6 gap-y-4 sm:grid-cols-2 xl:grid-cols-3"><div class="min-w-0 space-y-1"><dt class="text-sm text-muted-foreground">应用</dt><dd class="min-w-0 break-words">{{run.service}}</dd></div><div v-if="documentation?.environment" class="min-w-0 space-y-1"><dt class="text-sm text-muted-foreground">构建环境</dt><dd class="min-w-0 break-words">{{documentation.environment}}</dd></div><div class="min-w-0 space-y-1"><dt class="text-sm text-muted-foreground">分支</dt><dd class="min-w-0 break-words">{{run.branch||'默认'}}</dd></div><div class="min-w-0 space-y-1"><dt class="text-sm text-muted-foreground">Commit</dt><dd class="min-w-0 break-words"><code>{{capabilities.commit||run.version||'尚未解析'}}</code></dd></div><div class="min-w-0 space-y-1"><dt class="text-sm text-muted-foreground">操作者</dt><dd class="min-w-0 break-words">{{run.actor||'—'}}</dd></div><div class="min-w-0 space-y-1"><dt class="text-sm text-muted-foreground">耗时</dt><dd class="min-w-0 break-words">{{elapsed}}</dd></div><div v-if="run.auto_deploy" class="min-w-0 space-y-1"><dt class="text-sm text-muted-foreground">后续发布</dt><dd class="min-w-0 break-words">{{intendedTargetLabel}}</dd></div></dl></CardContent></Card><Card v-if="documentation?.enabled" size="small" class="build-context-card gap-0 py-4 shadow-none"><CardHeader><CardTitle>接口文档</CardTitle><CardAction><Badge variant="secondary">{{documentation.provider}} · {{documentation.environment||'未区分环境'}}</Badge></CardAction></CardHeader><CardContent class="min-w-0"><template v-if="documentation.snapshots?.length"><span>快照 #{{documentation.snapshots[0].id}} · 新增 {{documentation.snapshots[0].changes.added}} · 删除 {{documentation.snapshots[0].changes.removed}} · 修改 {{documentation.snapshots[0].changes.modified}}</span><Button @click="openDocumentation()" variant="ghost" size="default" type="button">查看文档</Button><Button @click="openDocumentation(true)" variant="ghost" size="default" type="button">变更历史</Button></template><span v-else>{{run?.status==='running'?'等待文档生成阶段完成':'本次构建没有生成文档快照，请核对文档阶段日志'}}</span></CardContent></Card><div v-if="canDeploy" class="legacy-deploy"><OptionPicker v-model="deployEnv" :options="targets.map(t=>({value:t.environment,label:t.display_name}))" placeholder="选择环境"/><Button @click="cancelWaiting" variant="outline" size="sm" type="button">放弃部署</Button><Button @click="startDeployFlow" variant="default" size="sm" type="button" :disabled="(!targetReady)">{{selectedTarget?.protected?'提交发布单':'部署到该环境'}}</Button></div><Tabs class="section-tabs" :model-value="String(selectedStage)" @update:model-value="(value=>selectedStage=Number(value))($event)"><div class="flex min-w-0 items-center gap-2"><TabsList class="h-auto! max-w-full flex-wrap gap-y-1 [&>[data-slot=tabs-trigger]]:h-7"><TabsTrigger :value="&quot;-1&quot;">全部日志</TabsTrigger><TabsTrigger v-for="(s,i) in run?.stages||[]" :value="String(i)">{{s.name+' · '+(({success:'成功',failed:'失败',running:'执行中',canceled:'已取消',pending:'待执行',warning:'生成失败（未阻断）',skipped:'未执行'})[s.status]||s.status||'待执行')}}</TabsTrigger></TabsList></div><TabsContent :value="&quot;-1&quot;" force-mount v-show="!(String(selectedStage)!==&quot;-1&quot;)" class="min-w-0"></TabsContent><TabsContent v-for="(s,i) in run?.stages||[]" :value="String(i)" force-mount v-show="!(String(selectedStage)!==String(i))" class="min-w-0"></TabsContent></Tabs><div v-if="failureDiagnosis" class="failure-diagnosis"><span>!</span><div><b>{{failureDiagnosis.title}}</b><p>{{failureDiagnosis.detail}}</p></div><Button @click="router.push(buildRepairLocation(run.service,failureDiagnosis.repair || !!failureDiagnosis.gradle))" variant="outline" size="sm" type="button">打开构建配置</Button></div><div class="run-log-tools"><InputGroup  class="min-w-0 "><InputGroupInput v-model="logQuery" placeholder="搜索当前阶段日志" v-bind="{&quot;aria-label&quot;:&quot;搜索构建日志&quot;}"/><InputGroupAddon align="inline-end"><InputGroupButton v-if="logQuery" :disabled="false" size="icon-xs" aria-label="清空输入" @click="logQuery='' "><XIcon/></InputGroupButton></InputGroupAddon></InputGroup><span v-if="logQuery">{{visibleLog.split('\n').filter(Boolean).length}} 行命中</span><Button @click="toggleFollow" :variant="({primary:'default',secondary:'outline',text:'ghost',outline:'outline',dashed:'outline'})[follow?'primary':'secondary']||'outline'" size="sm" type="button">{{follow?'暂停跟随':'继续跟随'}}</Button><Button @click="copyLog" variant="outline" size="sm" type="button">复制日志</Button></div><div ref="logEl" @scroll="pauseForScroll" @mouseup="selectLog" class="run-log">{{visibleLog||(logQuery?'没有匹配日志':'正在连接日志…')}}</div><Dialog :open="!!control" @update:open="async v=>{if(!v){control=''}}"><DialogContent class="flex max-h-[92dvh] flex-col gap-0 overflow-hidden p-0 sm:max-w-[var(--editor-width)]" :class="false?'inset-0 h-dvh max-h-dvh w-screen max-w-none translate-x-0 translate-y-0 rounded-none sm:max-w-none':''" :style="{'--editor-width':typeof (&quot;min(520px, 92vw)&quot;)==='number'?(&quot;min(520px, 92vw)&quot;)+'px':(&quot;min(520px, 92vw)&quot;)}" :show-close-button="!(controlling)" @interact-outside="e=>{if(!(!(controlling)))e.preventDefault()}" @escape-key-down="e=>{if(!(!(controlling)))e.preventDefault()}"><DialogHeader class="shrink-0 border-b p-5 pr-12"><DialogTitle>{{control==='retry'?'确认重试构建':'确认取消构建'}}</DialogTitle><DialogDescription class="sr-only">详情与操作</DialogDescription></DialogHeader><div class="min-h-0 flex-1 overflow-auto p-5"><div class="run-control"><p>{{control==='retry'?'使用原 Commit、原构建配置与 Runner；凭据和变量重新授权，步骤实现与依赖按当前版本执行。只生成制品，不自动发布。':'仅停止当前构建的进程和容器，不停止部署实例；等待执行器退出后释放运行锁。'}}</p><code v-if="control==='retry'">{{capabilities.commit}}</code><Field ><FieldLabel as="div">操作原因</FieldLabel><Input v-model="controlReason" :disabled="controlling"/></Field><p v-if="controlError" role="alert">{{controlError}}</p></div></div><DialogFooter><Button @click="submitControl" variant="default" size="default" type="button" :disabled="(!controlReason.trim())||(controlling)"><Spinner v-if="controlling"/>确认{{control==='retry'?'重试':'取消'}}</Button></DialogFooter></DialogContent></Dialog><Dialog :open="showDeployConfirm" @update:open="async v=>{if(!v){showDeployConfirm = false}}"><DialogContent class="flex max-h-[92dvh] flex-col gap-0 overflow-hidden p-0 sm:max-w-[var(--editor-width)]" :class="false?'inset-0 h-dvh max-h-dvh w-screen max-w-none translate-x-0 translate-y-0 rounded-none sm:max-w-none':''" :style="{'--editor-width':typeof (&quot;min(520px, 92vw)&quot;)==='number'?(&quot;min(520px, 92vw)&quot;)+'px':(&quot;min(520px, 92vw)&quot;)}" :show-close-button="true" @interact-outside="e=>{e.preventDefault()}"><DialogHeader class="shrink-0 border-b p-5 pr-12"><DialogTitle>确认续接部署</DialogTitle><DialogDescription class="sr-only">详情与操作</DialogDescription></DialogHeader><div class="min-h-0 flex-1 overflow-auto p-5"><div class="fld"><label>服务 / 版本</label><code>{{ run?.service }} · {{ run?.version }}</code></div><div class="fld"><label>制品</label><code style="word-break:break-all">{{ run?.artifact }}</code></div><div class="fld"><label>目标环境</label><span>{{ selectedTarget?.protected ? '🔒 ' : '' }}{{ selectedTarget?.display_name || '未选择' }}</span></div><div class="fld"><label>执行目标</label><span>{{ targetLabel }}</span></div><div v-if="selectedTarget?.require_reason" class="fld"><label>发布原因</label><Textarea v-model="reason" placeholder="受保护环境必填" :rows="({minRows:(2),maxRows:Math.max((2),16)}).minRows||4"/></div><div style="color:var(--warn);font-size:.75rem;margin-top:10px">确认后将立即开始目标部署。</div></div><DialogFooter><Button @click="showDeployConfirm = false" variant="outline" size="default" type="button">取消</Button><Button @click="continueDeploy" variant="default" size="default" type="button" :disabled="(!deployEnv || (selectedTarget?.require_reason &amp;&amp; !reason.trim()))||(deploying)"><Spinner v-if="deploying"/>确认并部署</Button></DialogFooter></DialogContent></Dialog></div></template>
<style scoped>.deploy-intent{padding:3px 8px;border:1px solid color-mix(in srgb,var(--brand) 35%,var(--border));border-radius:999px;background:color-mix(in srgb,var(--brand) 8%,var(--panel));color:var(--brand);font-size:.75rem}.failure-diagnosis{display:flex;align-items:center;gap:10px;margin-bottom:10px;padding:10px 12px;border:1px solid color-mix(in srgb,var(--error) 35%,var(--border));border-radius:var(--radius);background:color-mix(in srgb,var(--error) 8%,var(--panel))}.failure-diagnosis>span{display:grid;place-items:center;flex:0 0 24px;height:24px;border-radius:50%;background:var(--error);color:#fff;font-weight:700}.failure-diagnosis>div{min-width:0;margin-right:auto}.failure-diagnosis b{font-size:.75rem}.failure-diagnosis p{margin-top:2px;color:var(--muted-foreground);font-size:.75rem;line-height:1.5}.run-log{background:#0a0d12;border:1px solid var(--border);border-radius:var(--radius);padding:12px;font-family:var(--mono);font-size:.75rem;white-space:pre-wrap;word-break:break-all;overflow-y:auto;color:#c9d1d9}</style>
<style scoped>.legacy-deploy,.run-log-tools{display:flex;align-items:center;gap:10px;flex-wrap:wrap;margin-bottom:12px;font-size:.75rem}.run-log{min-height:240px}.run-control{display:grid;gap:12px}.run-control label{display:grid;gap:6px}.run-control p{font-size:.75rem;color:var(--muted-foreground);margin:0}.run-control code{overflow-wrap:anywhere}</style>

<style scoped>.build-context-card{margin-bottom:20px}.build-context-card code{overflow-wrap:anywhere}.run-log-tools{margin:16px 0}.run-log{height:clamp(280px,calc(100dvh - 370px),900px)}.failure-diagnosis{flex-wrap:wrap}.failure-diagnosis>div{flex:1;min-width:200px}</style>

<style scoped>.run-log-tools :deep([data-slot="input-group"]){flex:1;min-width:220px;max-width:440px;width:auto}</style>
