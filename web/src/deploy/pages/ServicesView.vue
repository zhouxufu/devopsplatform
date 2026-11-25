<script setup>
import {buildRunLabel} from '../buildNumber.js'
import JavaBundleEditor from '../components/JavaBundleEditor.vue'
import {bundleParams,javaBundleError,bundleRelative} from '../javaBundle.js'
import {validMavenThreads} from '../mavenBuild.js'
import DataTable from "../../shared/components/DataTable.vue"

import {useTableLayout} from '../../shared/composables/tableLayout.js'
const {pinTableActions}=useTableLayout()





import BuildRunDialog from '../components/BuildRunDialog.vue'
import {deploymentTargetReady} from '../deploymentTarget.js'
import ApplicationNameEditor from '../../shared/components/ApplicationNameEditor.vue'
import {applicationName,applicationIdentifier,validApplicationIdentifier,applicationNameError,announceApplicationName} from '../../shared/applicationName.js'
import {buildState, buildScroll} from '../buildNavigation.js'
import PageHeader from "../../shared/components/PageHeader.vue"
import BuildEnvironmentEditor from '../components/BuildEnvironmentEditor.vue'
import {buildImageSourceError} from '../buildImageSource.js'
import JavaCompileFields from '../components/JavaCompileFields.vue'
import ImageBuildEditor from '../components/ImageBuildEditor.vue'
import {defaultJavaImage, imageBuildError, imageCompiler} from '../imageBuildModel.js'
import GitBranchSelect from '../components/GitBranchSelect.vue'
import {ref, onMounted, computed, inject, watch} from 'vue'
import {useRouter} from 'vue-router'
import HelpTip from '../../shared/components/HelpTip.vue'
import UiIcon from '../../shared/components/UiIcon.vue'


import {toast} from '../../shared/composables/toast.js'

const api = window.api = p => fetch(p).then(r => r.json()).then(d => d ?? [])
const router = useRouter()
const globalService = inject('globalService', ref(''))
const setPlatformContext = inject('setPlatformContext', () => {})
const services = ref([]), catalogApps = ref([]), lastBuild = ref({}), lastArtifact = ref({}), serviceTargets = ref({}), serviceDelivery = ref({}), lastRelease = ref({})
const showNew = ref(false), editing = ref(null), wizardStep = ref(1), wizardBody = ref(null)
watch(wizardStep, () => wizardBody.value?.scrollTo({top:0}), {flush:'post'})
const renamingApplication=ref(null)
function applicationRenamed(app){const existing=services.value.find(s=>s.name===app.name);if(existing)Object.assign(existing,app)}
function applicationAction(action,app){if(action==='edit')openEdit(app);else if(action==='rename')renamingApplication.value=app;else if(action==='delete')del(app)}
const form = ref({}), showAdvancedBuild = ref(false)
const buildEnvironment = ref({mode:'local'})
const gitConnections = ref([])
const me=ref({})
const ui=buildState('applications',{search:'',category:'all',tags:[],layout:'table',status:'',page:1},()=>me.value.username)
const searchText=computed({get:()=>ui.value.search,set:v=>{ui.value.search=v;ui.value.page=1}}),category=computed({get:()=>ui.value.category,set:v=>{ui.value.category=v;ui.value.page=1}}),activeTags=computed({get:()=>ui.value.tags,set:v=>ui.value.tags=v})
const scroll=buildScroll('applications'),loading=ref(true),loadError=ref(''),savingNew=ref(false)
fetch('/api/me').then(r=>{if(!r.ok)throw Error('identity unavailable');return r.json()}).then(v=>me.value=v).catch(()=>{})
const TYPES = {
  'java-bundle': {label:'Java 整包',icon:'📦',hint:'保留 JAR、脚本、工具与数据，按完整版本发布'},
  jar: { label: 'Java JAR', icon: '☕', hint: 'Maven / Gradle 构建，发布到 Linux 主机' },
  static: { label: '前端静态站点', icon: '🌐', hint: 'Node.js 构建，发布到 Nginx 目录' },
  image: { label: 'Docker 镜像', icon: '🐳', hint: '支持镜像仓库或镜像文件交付' },
}
const isSSHGitURL=value=>/^(?:ssh:\/\/|[^/@\s]+@[^:/\s]+:)/i.test((value||'').trim())

async function load(){
 loading.value=true;loadError.value=''
 try{
 const [svcs,summaries,catalog]=await Promise.all([api('api/services'),api('api/services/summary'),api('/api/registry/applications')])
 if(!Array.isArray(svcs)||!Array.isArray(summaries))throw Error('应用状态响应无效，请重试')
 services.value=svcs;catalogApps.value=Array.isArray(catalog)?catalog:[]
 const lb={},la={},targets={},deliveries={}
 for(const item of summaries){if(item.latest_run)lb[item.name]=item.latest_run;if(item.latest_artifact)la[item.name]=item.latest_artifact;targets[item.name]=item.environments||[];deliveries[item.name]=item.delivery||{}}
 lastBuild.value=lb;lastArtifact.value=la;serviceTargets.value=targets;serviceDelivery.value=deliveries
 gitConnections.value=(await api('api/connections')).filter(c=>c.kind==='git')
 await scroll.restore()
 }catch(e){loadError.value=e.message}finally{loading.value=false}
}
onMounted(load)

function defaults(type = 'jar') {
	const base = { name: '', application_id: '', git_url: '', git_connection: '', branch: '', artifact_type: type, tags: [], tags_text: '', remote_dir: '', start_cmd: '', stop_cmd: '', health_url: '' }
  if (type === 'jar') return { ...base, build_tool: 'maven', maven_executable: 'auto', maven_goals: 'clean package', maven_profiles: '', maven_properties: '', maven_settings_path: '', gradle_executable: 'auto', gradle_tasks: 'clean build', gradle_excluded_tasks: '', gradle_project_properties: '', gradle_system_properties: '', artifact_glob: 'target/*.jar', build_cmd: '' }
  if (type === 'java-bundle') return {...base,build_tool:'command',build_cmd:'bash deploy/jpom/build-release.sh',artifact_glob:'build/jpom-release'}
  if (type === 'static') return { ...base, build_tool: 'command', build_cmd: 'npm ci && npm run build', artifact_glob: 'dist' }
  return defaultJavaImage({ ...base, build_tool: 'maven', build_cmd: '', artifact_glob: '', image_name: '' })
}
function selectTemplate(type) {
	const keep = { name: form.value.name, display_name:form.value.display_name, identifier:form.value.identifier, application_id: form.value.application_id, git_url: form.value.git_url, git_connection: form.value.git_connection, branch: form.value.branch, workdir: form.value.workdir, tags: form.value.tags, tags_text: form.value.tags_text }
  form.value = { ...defaults(type), ...keep }
  buildEnvironment.value = { mode: 'local' }
}
function selectJavaBuildTool(tool) {
  const oldTool = form.value.build_tool
  form.value.build_tool = tool
  if (buildEnvironment.value.mode === 'container' && buildEnvironment.value.preset !== 'custom') buildEnvironment.value = { ...buildEnvironment.value, preset: `${tool}-jdk${(buildEnvironment.value.preset || '').match(/jdk(\d+)/)?.[1] || '21'}` }
  if (tool === 'gradle') {
    form.value.gradle_executable ||= 'auto'
    form.value.gradle_tasks ||= 'clean build'
    if (!form.value.artifact_glob || oldTool === 'maven' && form.value.artifact_glob === 'target/*.jar') form.value.artifact_glob = 'build/libs/*.jar'
  } else {
    form.value.maven_executable ||= 'auto'
    form.value.maven_goals ||= 'clean package'
    if (!form.value.artifact_glob || oldTool === 'gradle' && form.value.artifact_glob === 'build/libs/*.jar') form.value.artifact_glob = 'target/*.jar'
  }
}
function openNew() {
  buildEnvironment.value = {mode:'local'}
  editing.value = null; wizardStep.value = 1; showAdvancedBuild.value = false
  form.value = defaults('jar'); showNew.value = true
}
const availableCatalogApps = computed(() => catalogApps.value.filter(app => !services.value.some(service => service.application_id === app.application_id)))
function openTemplate(type) { openNew(); selectTemplate(type) }
function selectCatalogApplication(applicationID) {
  form.value.application_id = applicationID || ''
  const app = catalogApps.value.find(item => item.application_id === applicationID)
  if(!app)form.value.identifier=''
  if (app) { form.value.name = app.name;form.value.display_name=applicationName(app);form.value.identifier=applicationIdentifier(app) }
}
function openEdit(s) {
  router.push({path:`/svc/${encodeURIComponent(s.name)}/settings`,query:{settings_return:'/'}})
}
function validateStep() {
  if (wizardStep.value === 1) {
    if(applicationNameError(form.value.display_name))return toast(applicationNameError(form.value.display_name),'warning')
    if(!form.value.application_id&&!validApplicationIdentifier(form.value.name))return toast('应用标识以小写英文开头，只能包含小写英文、数字和短横线，最长 63 字符','warning')
    if(!form.value.git_url?.trim()&&!form.value.workdir?.trim())return toast('填写代码仓库','warning')
  }
  if (wizardStep.value === 2) {
    if(javaBundleError(form.value))return toast(javaBundleError(form.value),'warning')
    if(form.value.build_tool==='maven'&&!validMavenThreads(form.value.maven_threads))return toast('Maven 并行线程须为正整数或 CPU 倍数，如 4、1C、2.5C','warning')
    if (buildImageSourceError(buildEnvironment.value)) return toast(buildImageSourceError(buildEnvironment.value), 'warning')
    if (form.value.artifact_type === 'image' && !form.value.image_name?.trim()) return toast('填写镜像名称', 'warning')
    if(form.value.artifact_type==='image'&&imageBuildError(form.value))return toast(imageBuildError(form.value),'warning')
    if (form.value.artifact_type === 'jar' && form.value.build_tool === 'maven' && (!form.value.maven_executable?.trim() || !form.value.maven_goals?.trim())) return toast('填写 Maven 可执行文件和 Goals', 'warning')
    if (form.value.artifact_type === 'jar' && form.value.build_tool === 'gradle' && (!form.value.gradle_executable?.trim() || !form.value.gradle_tasks?.trim())) return toast('填写 Gradle 可执行文件和任务', 'warning')
    if (form.value.artifact_type !== 'image' && !['maven','gradle'].includes(form.value.build_tool) && !form.value.build_cmd?.trim()) return toast('填写构建命令', 'warning')
  }
  wizardStep.value++
}
async function save(){if(savingNew.value)return;savingNew.value=true;try{await saveApplication()}catch(e){toast(e.message,'error')}finally{savingNew.value=false}}
async function saveApplication() {
  if (buildImageSourceError(buildEnvironment.value)) return toast(buildImageSourceError(buildEnvironment.value), 'warning')
	const body = { ...form.value, create_only:!editing.value }
	body.tags = [...new Set((body.tags_text || '').split(/[,，\n]/).map(x => x.trim()).filter(Boolean))].slice(0, 12)
	delete body.tags_text
  delete body.git_connection
  if(!['image','java-bundle'].includes(body.artifact_type))delete body.steps
  const r = await fetch('api/services', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
  if (r.ok) {
    const saved=await r.json()
    if(saved.catalog_synced===false)toast('应用已保存，但应用中心名称未同步；请稍后通过修改名称重试','warning')
    announceApplicationName(saved)
    const deliveryResponse = await fetch(`api/services/${encodeURIComponent(body.name)}/delivery`, {method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({...serviceDelivery.value[body.name],git_connection:form.value.git_connection,build_environment:buildEnvironment.value})})
    if (!deliveryResponse.ok) {
      editing.value = body
      wizardStep.value = 2
      return toast(`应用配置已保存，但构建环境保存失败：${(await deliveryResponse.json().catch(()=>({}))).error || '请重试'}`, 'error')
    }
    showNew.value = false
    toast(editing.value ? '应用配置已保存' : '应用已接入，可以开始构建', 'success')
    await load()
    if (!editing.value) router.push(`/svc/${encodeURIComponent(body.name)}/build`)
  } else toast((await r.json().catch(() => ({}))).error || '保存失败', 'error')
}
async function del(s) {
  if (!await confirmAction(`删除应用「${applicationName(s)}」？构建和发布历史保留，不停止已部署的服务。`)) return
  const response = await fetch(`api/services/${encodeURIComponent(s.name)}`, { method: 'DELETE' })
  if (!response.ok) {
    toast((await response.json().catch(() => ({}))).error || '删除失败', 'error')
    return
  }
  if (globalService.value === s.name) setPlatformContext({ service: '' })
  await load()
  toast('应用已删除', 'success')
}

const bcSvc = ref(null), bcBranch = ref(''), bcMavenProfiles = ref(''), bcMavenProperties = ref(''), bcGradleTasks = ref(''), bcGradleExcludedTasks = ref(''), bcGradleProjectProperties = ref(''), bcGradleSystemProperties = ref(''), showBC = ref(false)
const bcSubmitting = ref(false)
const bcMode=ref('build')
function build(s,mode='build'){bcMode.value=mode;bcSvc.value=s;showBC.value=true}
async function confirmBuild() {
  if (bcSubmitting.value) return
  const s = bcSvc.value; bcSubmitting.value = true
  try {
    const response = await fetch('api/pipeline/runs', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ service: s.name, branch: bcBranch.value.trim(), override_maven: s.build_tool === 'maven', maven_profiles: bcMavenProfiles.value, maven_properties: bcMavenProperties.value, override_gradle: s.build_tool === 'gradle', gradle_tasks: bcGradleTasks.value, gradle_excluded_tasks: bcGradleExcludedTasks.value, gradle_project_properties: bcGradleProjectProperties.value, gradle_system_properties: bcGradleSystemProperties.value }) })
    const r = await response.json()
    if (response.ok && r.id) { showBC.value = false; router.push(`/run/${r.id}`) }
    else toast(r.error || '构建发起失败', 'error')
  } catch(e) { toast(e.message, 'error') } finally { bcSubmitting.value = false }
}
const stCls = s => s === 'success' ? 'st-ok' : s === 'running' ? 'st-run' : s ? 'st-fail' : ''
const buildText=s=>({success:'构建成功',running:'构建中',failed:'构建失败',canceled:'已取消',interrupted:'已中断',waiting_deploy:'待部署'})[s]||(s||'尚未构建')
const configuredCount = name => serviceTargets.value[name]?.length || 0
const targetReady = deploymentTargetReady
const readyEnvironmentCount = name => (serviceTargets.value[name] || []).filter(targetReady).length
const resourceReady = name => !!serviceDelivery.value[name]?.runner && configuredCount(name) > 0 && readyEnvironmentCount(name) === configuredCount(name)
const nextAction = s => !lastArtifact.value[s.name] ? '下一步：完成首次构建' : !configuredCount(s.name) ? '下一步：配置应用环境' : !resourceReady(s.name) ? '下一步：配置环境发布资源' : '已具备发布条件'
const openDelivery = s => router.push(`/svc/${encodeURIComponent(s.name)}/${resourceReady(s.name) ? 'release' : 'environment'}`)
const template = computed(() => TYPES[form.value.artifact_type] || TYPES.jar)
const repoName = url => (url || '').replace(/\.git$/, '').split('/').pop() || '未连接仓库'
const roleOf = s => s.artifact_type === 'static' ? 'frontend' : 'backend'
const roleLabel = s => roleOf(s) === 'frontend' ? '前端' : '后端'
const displayTags = s => [roleLabel(s), ...(s.tags || []).filter(t => t !== '前端' && t !== '后端')]
const availableTags = computed(() => {
  const counts = new Map()
  for (const s of services.value) for (const tag of (s.tags || [])) counts.set(tag, (counts.get(tag) || 0) + 1)
  return [...counts].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0], 'zh-CN'))
})
const filteredServices = computed(() => {
  const keyword = searchText.value.trim().toLowerCase()
  return services.value.filter(s => {
    if (category.value !== 'all' && roleOf(s) !== category.value) return false
    if (activeTags.value.length && !activeTags.value.every(tag => (s.tags || []).includes(tag))) return false
    if(ui.value.status&&lastBuild.value[s.name]?.status!==ui.value.status)return false
    if (!keyword) return true
    return [s.name, applicationName(s), applicationIdentifier(s), s.git_url, s.workdir, s.branch, ...(s.tags || [])].some(v => String(v || '').toLowerCase().includes(keyword))
  })
})
watch(()=>filteredServices.value.length,total=>{ui.value.page=Math.min(ui.value.page,Math.max(1,Math.ceil(total/20)))})
const buildSummary = s => {
  if (s.artifact_type === 'jar') return s.build_tool === 'gradle' ? `${s.gradle_executable} ${s.gradle_tasks}` : `${s.maven_executable} ${s.maven_goals}`
  if (s.artifact_type === 'java-bundle') return `${s.build_cmd} → 完整目录归档`
  if (s.artifact_type === 'static') return s.build_cmd
  const compiler = imageCompiler(s)
  const compile = compiler === 'maven' ? 'Maven 编译 → JAR 校验' : compiler === 'gradle' ? 'Gradle 编译 → JAR 校验' : compiler === 'java-command' ? 'Java 编译 → JAR 校验' : compiler === 'node' ? 'Node.js 编译 → 产物校验' : 'Dockerfile 自行编译'
  return `${compile} → Docker 镜像 ${s.image_name}`
}
function toggleTag(tag) { ui.value.page=1;const i = activeTags.value.indexOf(tag); i >= 0 ? activeTags.value.splice(i, 1) : activeTags.value.push(tag) }
function clearFilters() { searchText.value = ''; category.value = 'all'; activeTags.value = [];ui.value.status='';ui.value.page=1 }

import OptionPicker from '../../shared/components/OptionPicker.vue'



import {Accordion,AccordionContent,AccordionItem,AccordionTrigger,Badge,Button,Card,CardAction,CardContent,CardFooter,CardHeader,CardTitle,Dialog,DialogContent,DialogDescription,DialogFooter,DialogHeader,DialogTitle,DropdownMenu,DropdownMenuContent,DropdownMenuItem,DropdownMenuTrigger,Empty,EmptyContent,EmptyDescription,EmptyHeader,Field,FieldGroup,FieldDescription,FieldError,FieldLabel,Input,Pagination,PaginationContent,PaginationNext,PaginationPrevious,Spinner,Stepper,StepperIndicator,StepperItem,StepperSeparator,StepperTitle,StepperTrigger,Table,TableBody,TableCell,TableHead,TableHeader,TableRow,Tabs,TabsList,TabsTrigger,Textarea,Checkbox} from '../../shared/shadcn-ui.js'

import {InputGroup,InputGroupInput,InputGroupAddon,InputGroupButton} from '../../shared/shadcn-ui.js'

import {XIcon} from '@lucide/vue'

import {confirmAction} from "../../shared/shadcnFeedback.js"
</script>

<template><section class="services-page "><PageHeader title="应用与构建" help="管理应用的代码来源、构建配置和不可变制品；单应用可直接发布，多应用交付使用部署编排。"><template #actions><Button @click="openNew" variant="default" size="default" type="button">＋ 接入应用</Button></template><template #navigation><Tabs :model-value="category" @update:model-value="category=$event;"><TabsList variant="line" class="h-auto! max-w-full flex-wrap gap-y-1 [&>[data-slot=tabs-trigger]]:h-9"><TabsTrigger v-for="item in [['all','全部'],['frontend','前端'],['backend','后端']]" :key="item[0]" :value="item[0]">{{item[1]}} {{item[0]==='all'?services.length:services.filter(s=>roleOf(s)===item[0]).length}}</TabsTrigger></TabsList></Tabs></template></PageHeader><section v-if="services.length" class="filter-panel mb-5 space-y-3"><div class="filter-main flex min-w-0 flex-wrap items-center gap-2"><InputGroup  class="min-w-0 max-w-sm flex-1 basis-60"><InputGroupInput v-model="searchText" placeholder="搜索应用名、仓库、分支或标签"/><InputGroupAddon align="inline-end"><InputGroupButton v-if="searchText" :disabled="false" size="icon-xs" aria-label="清空输入" @click="searchText='' "><XIcon/></InputGroupButton></InputGroupAddon></InputGroup><span class="result-count text-xs text-muted-foreground">{{filteredServices.length}} 个应用</span><div v-if="services.length" class="application-view-tools ml-auto flex min-w-0 flex-wrap items-center gap-2"><OptionPicker v-model="ui.status" :options="[{value:'running',label:'构建中'},{value:'success',label:'成功'},{value:'failed',label:'失败'},{value:'canceled',label:'已取消'}]" all-label="全部构建状态" @change="ui.page=1"/><span v-if="loading" role="status">读取状态中…</span><Button @click="ui.layout='table'" :variant="({primary:'default',secondary:'outline',text:'ghost',outline:'outline',dashed:'outline'})[ui.layout==='table'?'primary':'secondary']||'outline'" size="sm" type="button">表格</Button><Button @click="ui.layout='cards'" :variant="({primary:'default',secondary:'outline',text:'ghost',outline:'outline',dashed:'outline'})[ui.layout==='cards'?'primary':'secondary']||'outline'" size="sm" type="button">卡片</Button></div></div><div v-if="availableTags.length" class="tag-filters flex min-w-0 flex-wrap items-center gap-2 text-xs text-muted-foreground"><span>标签</span><Button v-for="[tag,count] in availableTags" :key="tag" :variant="activeTags.includes(tag)?'default':'secondary'" :aria-pressed="activeTags.includes(tag)" @click="toggleTag(tag)" size="sm" type="button">{{tag}} <small>{{count}}</small></Button><Button v-if="activeTags.length" @click="activeTags=[]" class="clear-filter" variant="ghost" size="default" type="button">清除标签</Button></div></section><p v-if="loadError" role="alert" class="load-error">{{loadError}} <Button @click="load" variant="outline" size="sm" type="button">重试</Button></p><div v-if="ui.layout==='table'&&services.length" class="application-table-scroll"><div class="application-table console-table min-w-0 max-w-full"><div v-if="loading" class="flex items-center gap-2 py-2" role="status"><Spinner/>正在读取</div><div class="min-w-0 max-w-full" :style="{maxHeight:({x:1110}).y?({x:1110}).y+'px':undefined}"><DataTable class="[&_[data-slot=table-container]]:rounded-lg [&_[data-slot=table-container]]:border" :rows="(((filteredServices.slice((ui.page-1)*20,ui.page*20))||[]).map((s,rowIndex)=>({...s,__rowKey:s.name}))||[])" :columns="[{id:'c0',label:&quot;应用 / 类型&quot;,size:220,minSize:160,required:true,defaultHidden:false,flex:true,selection:false,pin:'left'},{id:'c1',label:&quot;最新构建&quot;,size:128,minSize:112,required:false,defaultHidden:false,flex:false,selection:false,pin:false},{id:'c2',label:&quot;分支 / 代码版本&quot;,size:176,minSize:160,required:false,defaultHidden:false,flex:true,selection:false,pin:false},{id:'c3',label:&quot;最新制品&quot;,size:144,minSize:120,required:false,defaultHidden:false,flex:true,selection:false,pin:false},{id:'c4',label:&quot;环境就绪&quot;,size:90,minSize:64,required:false,defaultHidden:true,flex:false,selection:false,pin:false},{id:'c5',label:&quot;更新时间&quot;,size:170,minSize:64,required:false,defaultHidden:true,flex:false,selection:false,pin:false},{id:'c6',label:&quot;操作&quot;,size:296,minSize:264,required:true,defaultHidden:false,flex:false,selection:false,pin:'right'}]" table-key="deploy-pages-ServicesView-0" :row-key="(record,rowIndex)=>typeof (&quot;__rowKey&quot;)==='function'?(&quot;__rowKey&quot;)(record):record[&quot;__rowKey&quot;]??rowIndex"><template #header-c0>应用 / 类型</template><template #cell-c0="{record,rowIndex}"><div class="min-w-0 break-words" :class="{truncate:!!(true)}"><template v-for="{record:s,rowIndex:rowIndex} in [{record,rowIndex}]"><div><Button @click="router.push('/svc/'+encodeURIComponent(s.name)+'/overview')" variant="link" class="h-auto max-w-full justify-start whitespace-normal p-0 text-left font-semibold text-foreground" size="sm" type="button">{{applicationName(s)}}</Button><small>{{applicationIdentifier(s)}} · {{TYPES[s.artifact_type]?.label}} · {{(s.tags||[]).join('、')}}</small></div></template></div></template><template #header-c1>最新构建</template><template #cell-c1="{record,rowIndex}"><div class="min-w-0 break-words" :class="{truncate:!!(true)}"><template v-for="{record:s,rowIndex:rowIndex} in [{record,rowIndex}]"><div><Badge :variant="(lastBuild[s.name]?.status==='success'?'green':lastBuild[s.name]?.status==='failed'?'red':lastBuild[s.name]?.status==='running'?'blue':'gray')==='red'?'destructive':'secondary'">{{buildText(lastBuild[s.name]?.status)}}</Badge><small v-if="lastBuild[s.name]">{{buildRunLabel(lastBuild[s.name])}}</small></div></template></div></template><template #header-c2>分支 / 代码版本</template><template #cell-c2="{record,rowIndex}"><div class="min-w-0 break-words" :class="{truncate:!!(true)}"><template v-for="{record:s,rowIndex:rowIndex} in [{record,rowIndex}]"><div>{{lastBuild[s.name]?.branch||s.branch||'默认分支'}}<small>{{lastBuild[s.name]?.version||'—'}}</small></div></template></div></template><template #header-c3>最新制品</template><template #cell-c3="{record,rowIndex}"><div class="min-w-0 break-words" :class="{truncate:!!(true)}"><template v-for="{record:s,rowIndex:rowIndex} in [{record,rowIndex}]"><div>{{lastArtifact[s.name]?.version||'尚无制品'}}</div></template></div></template><template #header-c4>环境就绪</template><template #cell-c4="{record,rowIndex}"><div class="min-w-0 break-words" :class="{truncate:!!(true)}"><template v-for="{record:s,rowIndex:rowIndex} in [{record,rowIndex}]"><div>{{readyEnvironmentCount(s.name)}} / {{configuredCount(s.name)}}</div></template></div></template><template #header-c5>更新时间</template><template #cell-c5="{record,rowIndex}"><div class="min-w-0 break-words" :class="{truncate:!!(true)}"><template v-for="{record:s,rowIndex:rowIndex} in [{record,rowIndex}]"><div>{{lastBuild[s.name]?.started_at?new Date(lastBuild[s.name].started_at*1000).toLocaleString('zh-CN',{hour12:false}):'—'}}</div></template></div></template><template #header-c6>操作</template><template #cell-c6="{record,rowIndex}"><div class="min-w-0 break-words" :class="{truncate:!!(false)}"><template v-for="{record:s,rowIndex:rowIndex} in [{record,rowIndex}]"><div class="cell-actions flex-nowrap!"><Button @click="build(s)" variant="ghost" size="sm" type="button" :disabled="(loading)">构建</Button><Button @click="build(s,'deploy')" variant="ghost" size="sm" type="button" :disabled="loading">构建并发布</Button><Button @click="openDelivery(s)" variant="ghost" size="sm" type="button">{{resourceReady(s.name)?'发布':'配置环境'}}</Button><DropdownMenu><DropdownMenuTrigger as-child><Button variant="ghost" size="sm" type="button">更多</Button></DropdownMenuTrigger><DropdownMenuContent align="end"><DropdownMenuItem @select="(v=>applicationAction(v,s))(&quot;edit&quot;)">构建配置</DropdownMenuItem><DropdownMenuItem v-if="me.role==='admin'" @select="(v=>applicationAction(v,s))(&quot;rename&quot;)">修改名称</DropdownMenuItem><DropdownMenuItem v-if="me.role==='admin'" @select="(v=>applicationAction(v,s))(&quot;delete&quot;)">删除应用</DropdownMenuItem></DropdownMenuContent></DropdownMenu></div></template></div></template><template #empty><Empty><EmptyHeader><EmptyDescription>{{"没有匹配的应用，可调整筛选条件"}}</EmptyDescription></EmptyHeader></Empty></template></DataTable></div></div></div><footer v-if="ui.layout==='table'&&services.length" class="table-footer"><div class="flex flex-wrap items-center justify-end gap-3 py-4"><span class="mr-auto text-sm text-muted-foreground">共 {{filteredServices.length}} 项</span><Pagination class="mx-0 w-auto" :disabled="loading" :page="ui.page" :total="filteredServices.length" :items-per-page="20" @update:page="ui.page=$event;"><PaginationContent><PaginationPrevious /><span class="px-2 text-sm">{{ui.page}} / {{Math.max(1,Math.ceil((filteredServices.length)/(20)))}}</span><PaginationNext /></PaginationContent></Pagination></div></footer><div v-if="ui.layout==='cards'" class="app-grid"><article v-for="s in filteredServices" :key="s.name" role="link" tabindex="0" :aria-label="`查看应用 ${s.name}`" @click="router.push(`/svc/${encodeURIComponent(s.name)}/overview`)" @keydown.enter.self="router.push(`/svc/${encodeURIComponent(s.name)}/overview`)" class="app-card"><div class="app-top"><span class="app-icon"><UiIcon :name="s.artifact_type === 'jar' ? 'java' : s.artifact_type === 'image' ? 'docker' : s.artifact_type" :size="22"/></span><div><h3>{{ applicationName(s) }}</h3><span>{{applicationIdentifier(s)}} · {{ TYPES[s.artifact_type]?.label || s.artifact_type }}</span></div><DropdownMenu><DropdownMenuTrigger as-child><Button aria-label="应用操作" @click.stop variant="ghost" size="sm" type="button">更多</Button></DropdownMenuTrigger><DropdownMenuContent align="end"><DropdownMenuItem @select="(v=>applicationAction(v,s))(&quot;edit&quot;)">构建配置</DropdownMenuItem><DropdownMenuItem v-if="me.role==='admin'" @select="(v=>applicationAction(v,s))(&quot;rename&quot;)">修改名称</DropdownMenuItem><DropdownMenuItem v-if="me.role==='admin'" @select="(v=>applicationAction(v,s))(&quot;delete&quot;)">删除应用</DropdownMenuItem></DropdownMenuContent></DropdownMenu></div><div class="app-tags"><span v-for="tag in displayTags(s)" :key="tag" :class="{role:tag==='前端'||tag==='后端'}">{{tag}}</span></div><div class="repo">⌘ {{ repoName(s.git_url || s.workdir) }}<span>{{ s.git_url ? (s.branch || '默认分支') : '本地目录' }}</span></div><div class="app-state"><div><small>最新运行</small><b :class="stCls(lastBuild[s.name]?.status)">{{ buildText(lastBuild[s.name]?.status) }}</b><code>{{buildRunLabel(lastBuild[s.name])}} · {{ lastBuild[s.name]?.version || '—' }}</code></div><div><small>可发布制品 / 环境</small><b>{{ lastArtifact[s.name]?.version || '尚无制品' }}</b><span>{{ readyEnvironmentCount(s.name) }}/{{configuredCount(s.name)}} 个环境资源就绪</span></div></div><div :class="['next-action',{ready:resourceReady(s.name)}]">{{resourceReady(s.name)?'✓':'→'}} {{nextAction(s)}}</div><div @click.stop class="app-actions"><Button @click="build(s)" variant="default" size="sm" type="button">构建</Button><Button @click="build(s,'deploy')" variant="outline" size="sm" type="button">构建并发布</Button><Button @click="openDelivery(s)" variant="outline" size="sm" type="button">{{resourceReady(s.name)?'发布':'配置环境'}}</Button></div></article></div><div v-if="services.length && !filteredServices.length && ui.layout==='cards'" class="empty-state compact"><div>⌕</div><h3>没有匹配的应用<HelpTip text="换个关键词或清除筛选条件。"/></h3><Button @click="clearFilters" variant="outline" size="default" type="button">清除筛选</Button></div><div v-if="loading&&!services.length" role="status" class="flex min-h-48 items-center justify-center gap-2 rounded-lg border text-muted-foreground"><Spinner/>正在读取应用与构建列表</div><div v-else-if="!loadError&&!services.length" class="empty-state"><div class="empty-symbol"><UiIcon name="build" :size="28"/></div><h3>接入第一个应用<HelpTip text="连接代码仓库，选择构建环境，生成可发布制品。"/></h3><div class="onboarding-templates [&>button]:h-auto [&>button]:min-w-0 [&>button]:flex-col [&>button]:items-start [&>button]:whitespace-normal [&>button]:p-5 [&>button]:text-left"><Button v-for="(type, key) in TYPES" :key="key" @click="openTemplate(key)" variant="outline" size="default" type="button"><UiIcon :name="['jar','java-bundle'].includes(key) ? 'java' : key === 'image' ? 'docker' : key" :size="24"/><strong>{{type.label}}</strong><small>{{type.hint}}</small><span>接入应用 →</span></Button></div></div><BuildRunDialog :show="showBC" :service="bcSvc" :delivery="serviceDelivery[bcSvc?.name]" :mode="bcMode" :targets="serviceTargets[bcSvc?.name]||[]" @close="showBC=false" @started="id=>{showBC=false;router.push({path:'/run/'+id,query:{return:'/'}})}"/><ApplicationNameEditor :show="!!renamingApplication" :service="renamingApplication" @close="renamingApplication=null" @saved="applicationRenamed"/><Dialog :open="showNew" @update:open="async v=>{if(!v){showNew = false}}"><DialogContent class="flex max-h-[92dvh] flex-col gap-0 overflow-hidden p-0 sm:max-w-[var(--editor-width)]" :class="false?'inset-0 h-dvh max-h-dvh w-screen max-w-none translate-x-0 translate-y-0 rounded-none sm:max-w-none':''" :style="{'--editor-width':typeof (&quot;min(720px, 92vw)&quot;)==='number'?(&quot;min(720px, 92vw)&quot;)+'px':(&quot;min(720px, 92vw)&quot;)}" :show-close-button="!(savingNew)" @interact-outside="e=>{if(!(!(savingNew)))e.preventDefault()}" @escape-key-down="e=>{if(!(!(savingNew)))e.preventDefault()}"><DialogHeader class="shrink-0 border-b p-5 pr-12"><DialogTitle>{{editing ? '编辑应用' : '接入应用'}}</DialogTitle><DialogDescription class="sr-only">详情与操作</DialogDescription></DialogHeader><div ref="wizardBody" class="min-h-0 flex-1 overflow-auto p-5"><fieldset :disabled="savingNew" class="application-wizard"><fieldset :disabled="savingNew" class="min-w-0 space-y-5" @submit=""><Stepper class="application-steps" :model-value="wizardStep"><StepperItem v-for="(x,index) in ['代码','构建方式','确认']" :key="x" :step="index+1" class="relative flex flex-1 items-center gap-2"><StepperTrigger disabled><StepperIndicator>{{index+1}}</StepperIndicator><StepperTitle>{{x}}</StepperTitle></StepperTrigger></StepperItem></Stepper><div v-if="wizardStep === 1"><h3 class="wizard-title">连接代码仓库</h3><div v-if="!editing && availableCatalogApps.length" class="discovered-app"><div class="fld"><label>关联已发现应用（可选）</label><OptionPicker :model-value="form.application_id" :options="availableCatalogApps.map(app=>({value:app.application_id,label:`${applicationName(app)} · ${(app.sources||[]).length?(app.sources||[]).map(s=>s==='otel'?'OTel':s==='logs'?'日志':s).join(' + '):'历史数据'}`}))" all-label="创建全新应用"  :style="{width:'100%',maxWidth:'100%'}" @update:model-value="selectCatalogApplication"/></div><small>日志或 OTel 先上报时会生成待接入应用；选中后沿用同一个 application_id。</small></div><div class="fld"><label>应用名称</label><Input v-model="form.display_name" placeholder="支持中文，例如订单服务" :maxlength="100"/></div><div class="fld"><label>应用标识</label><Input v-if="form.application_id" :model-value="form.identifier" readonly/><Input v-else v-model="form.name" :disabled="!!editing" placeholder="例如 order-api，创建后不可修改"/><small class="field-help">小写英文开头，可含数字和短横线，最长 63 字符。</small></div><div class="fld"><label>Git 仓库</label><Input v-model="form.git_url" placeholder="https://.../repo.git 或 git@...:repo.git"/><small v-if="isSSHGitURL(form.git_url)" class="git-url-warning">SSH 仓库请选择 SSH 私钥凭据，并在资源中心获取、确认主机指纹。</small></div><div v-if="form.git_url" class="fld"><label>Git 代码凭据</label><OptionPicker v-model="form.git_connection" :options="[{value:'',label:'公开仓库 / 使用执行环境凭据'},...gitConnections.map(c=>({value:c.name,label:`${c.name} · ${c.config?.auth_mode==='ssh'?'SSH':'HTTPS'}`}))]"  :style="{width:'100%',maxWidth:'100%'}"/></div><div v-if="form.workdir && !form.git_url" class="local-source">当前使用本地工作目录：<code>{{ form.workdir }}</code></div><div class="fld"><label>默认分支</label><GitBranchSelect v-model="form.branch" :repositories="[{git_url:form.git_url||'',connection:form.git_connection||''}]"/></div><div class="fld"><label>应用标签</label><Input v-model="form.tags_text" placeholder="例如：核心、交易、Java（逗号分隔，最多 12 个）"/><small class="field-help">前端/后端会根据制品类型自动分类，这里填写业务、团队或技术标签。</small></div></div><div v-else-if="wizardStep === 2" class="space-y-5"><h3 class="wizard-title">选择构建模板</h3><Tabs aria-label="选择构建模板" class="w-full" :model-value="form.artifact_type" @update:model-value="selectTemplate($event)"><TabsList class="grid h-auto! w-full grid-cols-1 items-stretch gap-2 sm:grid-cols-2 xl:grid-cols-4"><TabsTrigger v-for="(v, k) in TYPES" :key="k" class="h-auto! min-w-0 justify-start whitespace-normal" :value="k"><div class="flex min-w-0 flex-col items-start gap-2 p-3 text-left"><UiIcon :name="['jar','java-bundle'].includes(k)?'java':k==='image'?'docker':k" :size="22"/><b>{{v.label}}</b><span class="text-xs font-normal text-muted-foreground whitespace-normal">{{v.hint}}</span></div></TabsTrigger></TabsList></Tabs><JavaBundleEditor v-if="form.artifact_type==='java-bundle'" :service="form"/><FieldGroup v-else-if="form.artifact_type === 'jar'" class="gap-5"><Field><FieldLabel>构建工具</FieldLabel><Tabs class="wizard-build-tools w-full" :model-value="form.build_tool" @update:model-value="selectJavaBuildTool($event)"><TabsList class="grid h-auto! w-full grid-cols-1 gap-1 sm:grid-cols-2"><TabsTrigger class="h-auto! min-w-0 whitespace-normal px-3 py-2" :value="&quot;maven&quot;">Maven · pom.xml / mvnw</TabsTrigger><TabsTrigger class="h-auto! min-w-0 whitespace-normal px-3 py-2" :value="&quot;gradle&quot;">Gradle · build.gradle / gradlew</TabsTrigger></TabsList></Tabs></Field><JavaCompileFields :service="form" :tool="form.build_tool"/><Field><FieldLabel>产物位置</FieldLabel><Input v-model="form.artifact_glob" :placeholder="form.build_tool === 'gradle' ? 'build/libs/*.jar' : 'target/*.jar'"/></Field></FieldGroup><template v-else-if="form.artifact_type === 'static'"><div class="fld"><label>构建命令</label><Input v-model="form.build_cmd" placeholder="npm ci && npm run build"/></div><div class="fld"><label>输出目录</label><Input v-model="form.artifact_glob" placeholder="dist"/></div></template><ImageBuildEditor v-else :service="form"/><div style="margin:16px 0"><BuildEnvironmentEditor v-model="buildEnvironment" :build-tool="form.artifact_type==='image'?imageCompiler(form):form.build_tool" :artifact-type="form.artifact_type"/></div><Accordion class="platform-collapse" type="multiple" :model-value="(showAdvancedBuild?['advanced']:[]||[]).map(String)" @update:model-value="(keys=>showAdvancedBuild=keys.length>0)($event)"><AccordionItem :value="String(&quot;advanced&quot;)"><AccordionTrigger>高级构建参数</AccordionTrigger><AccordionContent force-mount v-show="!(!(showAdvancedBuild?['advanced']:[]||[]).map(String).includes(String(&quot;advanced&quot;)))"><div class="advanced-panel"><template v-if="form.artifact_type === 'jar'"><template v-if="form.build_tool === 'maven'"><div class="fld"><label>Maven Profiles</label><Input v-model="form.maven_profiles" placeholder="ci,release"/></div><div class="fld"><label>Maven settings.xml</label><Input v-model="form.maven_settings_path" placeholder="/opt/logplatform/settings.xml"/></div><div class="fld"><label>-D 属性（每行 key=value）</label><Textarea v-model="form.maven_properties" :rows="({minRows:(3),maxRows:Math.max((3),16)}).minRows||4"/></div></template><template v-else><div class="fld"><label>排除任务（空格或逗号分隔）</label><Input v-model="form.gradle_excluded_tasks" placeholder="test integrationTest"/></div><div class="fld"><label>-P 项目属性（每行 key=value）</label><Textarea v-model="form.gradle_project_properties" placeholder="profile=prod" :rows="({minRows:(3),maxRows:Math.max((3),16)}).minRows||4"/></div><div class="fld"><label>-D 系统属性（每行 key=value）</label><Textarea v-model="form.gradle_system_properties" :rows="({minRows:(3),maxRows:Math.max((3),16)}).minRows||4"/></div><small class="field-help">敏感值请写成 ${env.NAME}，由构建变量组在运行时注入。</small></template></template><div v-else-if="!['image','java-bundle'].includes(form.artifact_type)" class="fld"><label>自定义构建命令</label><Textarea v-model="form.build_cmd" :rows="({minRows:(2),maxRows:Math.max((2),16)}).minRows||4"/></div></div></AccordionContent></AccordionItem></Accordion></div><div v-else class="confirm-card"><span class="confirm-icon">{{ template.icon }}</span><h3>{{ applicationName(form) }}</h3><p>{{ form.name }} · {{ template.label }} · {{ form.artifact_type==='static'?'前端':'后端' }}</p><dl><div><dt>代码来源</dt><dd>{{ form.git_url || form.workdir }}</dd></div><div><dt>分支</dt><dd>{{ form.git_url ? (form.branch || '仓库默认分支') : '本地工作目录' }}</dd></div><div><dt>构建</dt><dd>{{ buildSummary(form) }}</dd></div><div v-if="form.artifact_type==='jar'"><dt>产物</dt><dd>{{form.artifact_glob}}</dd></div></dl><p class="next-hint">构建环境：{{buildEnvironment.mode==='container' ? (buildEnvironment.preset==='custom' ? buildEnvironment.image : buildEnvironment.preset) : '本机工具'}}。保存后先构建制品，再为测试、预发或生产环境配置部署目标。</p></div></fieldset></fieldset></div><DialogFooter class="mx-0! mb-0! shrink-0 flex-wrap border-t p-4"><Button class="sm:mr-auto" v-if="wizardStep > 1" @click="wizardStep--" variant="outline" size="default" type="button" :disabled="(savingNew)">上一步</Button><span class="spacer"></span><Button @click="showNew = false" variant="outline" size="default" type="button" :disabled="(savingNew)">取消</Button><Button v-if="wizardStep < 3" @click="validateStep" variant="default" size="default" type="button">下一步</Button><Button v-else @click="save" variant="default" size="default" type="button" :disabled="(savingNew)"><Spinner v-if="savingNew"/>{{ editing ? '保存修改' : '完成接入' }}</Button></DialogFooter></DialogContent></Dialog></section></template>

<style scoped>.page-head{display:flex;align-items:center;margin-bottom:12px}.page-head h2{font-size:1.25rem}.title-row{display:flex;align-items:center;gap:7px}.category-tabs{display:flex;align-items:center;background:var(--panel2);padding:3px;border-radius:var(--radius)}.category-tabs small{margin-left:5px;color:var(--dim)}
.app-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(310px,1fr));gap:14px}.app-card{background:var(--panel);border:1px solid var(--border);border-radius:var(--radius);padding:16px;cursor:pointer;transition:.15s}.app-card:hover{border-color:var(--brand);box-shadow:0 3px 12px rgba(0,0,0,.06);transform:translateY(-1px)}
.app-top{display:flex;align-items:center;gap:10px}.app-icon{display:grid;place-items:center;width:38px;height:38px;border-radius:var(--radius);background:var(--panel2);font-size:1.25rem}.app-top h3{font-size:1rem}.app-top div span{color:var(--muted-foreground);font-size:.75rem}.card-menu>div{position:absolute;right:0;top:28px;z-index:10;display:flex;flex-direction:column;min-width:110px;padding:4px;background:var(--panel);border:1px solid var(--border);border-radius:var(--radius);box-shadow:0 8px 20px rgba(0,0,0,.2)}
.app-tags{display:flex;gap:5px;flex-wrap:wrap;margin:10px 0 -5px}.app-tags span{font-size:.75rem;padding:2px 6px;border-radius:999px;background:var(--panel2);color:var(--muted-foreground)}.app-tags span.role{color:var(--brand);background:color-mix(in srgb,var(--brand) 10%,var(--panel))}
.repo{margin:14px 0 12px;padding:8px 10px;background:var(--panel2);border-radius:var(--radius);font-family:var(--mono);font-size:.75rem;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.repo span{float:right;color:var(--muted-foreground);font-family:inherit}
.app-state{display:grid;grid-template-columns:1fr 1fr;gap:12px}.app-state>div{display:flex;flex-direction:column;min-width:0}.app-state small{color:var(--dim)}.app-state b{font-size:.75rem;margin:2px 0}.app-state code,.app-state span{font-size:.75rem;color:var(--muted-foreground);overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.app-actions{display:flex;align-items:center;border-top:1px solid var(--border);margin-top:12px;padding-top:12px}.next-action{margin-top:11px;padding:6px 8px;border-radius:var(--radius);background:color-mix(in srgb,var(--warn) 9%,var(--panel));color:var(--warn);font-size:.75rem}.next-action.ready{background:color-mix(in srgb,var(--ok) 9%,var(--panel));color:var(--ok)}
.empty-state{text-align:center;padding:70px 20px;border:1px dashed var(--border);border-radius:var(--radius);color:var(--muted-foreground)}.empty-state.compact{padding:36px 20px}.empty-state>div{font-size:2rem}.empty-state h3{color:var(--text);margin:8px}.empty-state p{margin-bottom:16px}
.wizard-steps{display:flex;justify-content:center;gap:56px;margin:-4px 0 24px}.wizard-steps span{position:relative;display:flex;align-items:center;gap:6px;color:var(--dim);font-size:.75rem}.wizard-steps span:not(:last-child):after{content:'';position:absolute;width:40px;height:1px;background:var(--border);left:calc(100% + 8px)}.wizard-steps i{font-style:normal;display:grid;place-items:center;width:22px;height:22px;border:1px solid var(--border);border-radius:50%}.wizard-steps .active{color:var(--brand);font-weight:600}.wizard-steps .active i,.wizard-steps .done i{background:var(--brand);border-color:var(--brand);color:#fff}.wizard-title{font-size:1rem;margin-bottom:14px}.template-grid{display:grid;grid-template-columns:repeat(3,1fr);gap:8px;margin-bottom:16px}.template-card{display:flex;flex-direction:column;align-items:flex-start;gap:4px;padding:12px;text-align:left;background:var(--panel);cursor:pointer}.template-card>span{font-size:1.25rem}.template-card small{color:var(--muted-foreground);font-size:.75rem;line-height:1.35}.template-card.selected{border-color:var(--brand);box-shadow:inset 0 0 0 1px var(--brand)}.two-col{display:grid;grid-template-columns:1fr 1.4fr;gap:10px}.field-help{color:var(--muted-foreground);font-size:.75rem;margin-top:-4px}.advanced-toggle{border:0;background:transparent;padding:5px 0;color:var(--brand)}.advanced-panel{border-left:2px solid var(--border);padding:8px 0 0 12px}.confirm-card{text-align:center;padding:8px 10px}.confirm-icon{font-size:2rem}.confirm-card h3{font-size:1.125rem;margin-top:6px}.confirm-card>p{color:var(--muted-foreground)}.confirm-card dl{text-align:left;margin:18px 0;background:var(--panel2);border-radius:var(--radius);padding:6px 14px}.confirm-card dl div{display:grid;grid-template-columns:90px 1fr;padding:8px 0;border-bottom:1px solid var(--border)}.confirm-card dl div:last-child{border:0}.confirm-card dt{color:var(--muted-foreground)}.confirm-card dd{font-family:var(--mono);word-break:break-all}.next-hint{font-size:.75rem}.advanced-box{margin-top:12px}
.build-tool-tabs{display:grid;grid-template-columns:1fr 1fr;gap:8px}.build-tool-tabs small{color:var(--muted-foreground);font-size:.75rem}
.local-source{font-size:.75rem;color:var(--muted-foreground);padding:8px 10px;background:var(--panel2);border-radius:var(--radius);margin:-2px 0 10px}.local-source code{word-break:break-all}
.discovered-app{padding:10px 11px;margin-bottom:12px;border:1px solid color-mix(in srgb,var(--brand) 35%,var(--border));border-radius:var(--radius);background:color-mix(in srgb,var(--brand) 5%,var(--panel))}.discovered-app .fld{margin-bottom:4px}.discovered-app>small{color:var(--muted-foreground);font-size:.75rem}
.git-url-warning{display:block;margin-top:5px;color:var(--warn);font-size:.75rem;line-height:1.45}
.application-steps{margin-bottom:24px}.application-wizard .template-grid{width:100%;grid-template-columns:repeat(3,minmax(0,1fr));gap:12px;margin-bottom:20px}.template-option{width:100%;min-width:0;margin:0;align-items:stretch}.template-card{width:100%;min-height:118px;box-sizing:border-box;gap:7px;border:1px solid var(--border);border-radius:var(--radius);padding:14px;white-space:normal}.template-card.selected{background:color-mix(in srgb,var(--brand) 6%,var(--panel))}.application-wizard .two-col{grid-template-columns:repeat(2,minmax(0,1fr));gap:16px;align-items:start}.two-col>*{min-width:0}.wizard-build-tools{display:flex;flex-wrap:wrap;max-width:100%}
@media(max-width:600px){.application-wizard .template-grid,.application-wizard .two-col{grid-template-columns:1fr}.template-card{min-height:0}.wizard-build-tools{gap:4px}}
@media(max-width:760px){.category-tabs{align-self:flex-start}.app-grid{grid-template-columns:1fr}.template-grid{grid-template-columns:1fr}.wizard-steps{gap:24px}.wizard-steps span:not(:last-child):after{display:none}}</style>

<style scoped>.services-page{width:100%;min-width:0}.page-head{margin-bottom:24px;gap:12px}.page-head h2{font-size:1.25rem}.app-grid{gap:20px;grid-template-columns:repeat(auto-fill,minmax(min(100%,330px),1fr))}.app-card{padding:22px;border-radius:var(--radius);box-shadow:var(--shadow-sm)}.app-card:hover{transform:none;border-color:var(--brand);box-shadow:0 6px 20px rgb(29 41 61 / 8%)}.app-top>div{min-width:0}.app-top h3{font-size:1.125rem;overflow-wrap:anywhere}.app-top div span{font-size:.75rem}.app-icon{width:44px;height:44px;border-radius:var(--radius);background:var(--brand-soft);color:var(--brand-ink);flex-shrink:0}.app-tags{margin:14px 0 0}.app-tags span{font-size:.75rem;padding:2px 8px}.repo{margin:16px 0;padding:10px;font-size:.75rem}.app-state b{font-size:1rem;margin:4px 0}.app-state small,.app-state code,.app-state span{font-size:.75rem}.next-action{padding:8px 10px;margin-top:18px;font-size:.75rem}.app-actions{margin-top:18px;padding-top:16px}.empty-state{padding:48px 24px;background:var(--panel);border:1px solid var(--border);border-radius:var(--radius);box-shadow:var(--shadow-sm)}.empty-state .empty-symbol{display:grid;place-items:center;margin:0 auto 16px;width:60px;height:60px;border-radius:var(--radius);background:var(--brand-soft);color:var(--brand-ink)}.empty-state h3{font-size:1.25rem;margin:0 0 8px}.empty-state p{font-size:.875rem;margin-bottom:28px}.onboarding-templates{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:16px;max-width:860px;margin:auto;text-align:left}.onboarding-templates .ui-icon{color:var(--brand-ink)}.onboarding-templates strong{font-size:1rem}.onboarding-templates small{font-size:.75rem;line-height:1.7;color:var(--muted-foreground)}.onboarding-templates span{font-size:.75rem;font-weight:600;color:var(--brand-ink);margin-top:8px}.empty-state.compact{padding:36px 20px}@media(max-width:760px){.page-head{margin-bottom:18px}.onboarding-templates{grid-template-columns:1fr}.onboarding-templates .ui-icon{grid-row:1/4}.onboarding-templates small,.onboarding-templates span{grid-column:2}.onboarding-templates span{margin:0}.empty-state{padding:32px 20px}.app-card{padding:18px}}</style>
<style scoped>.application-pages{display:flex;align-items:center;gap:8px;margin:10px 0;font-size:.75rem}.application-pages>span:first-child{margin-right:auto}.application-table-scroll{overflow:auto}.application-table{width:100%}.application-table-actions{white-space:nowrap}.application-wizard{border:0;padding:0;margin:0;min-width:0}.load-error{color:var(--error);font-size:.875rem}</style>

<style scoped></style>
