<script setup>
import PageHeader from "../../shared/components/PageHeader.vue"
import BuildEnvironmentEditor from '../components/BuildEnvironmentEditor.vue'
import GitBranchSelect from '../components/GitBranchSelect.vue'
import { ref, onMounted, computed, inject } from 'vue'
import { useRouter } from 'vue-router'
import Modal from '../../shared/components/Modal.vue'
import HelpTip from '../../shared/components/HelpTip.vue'
import UiIcon from '../../shared/components/UiIcon.vue'
import Sel from '../../shared/components/Sel.vue'
import { ElButton, ElInput } from '../../shared/components/ui.js'
import { toast } from '../../shared/composables/toast.js'

const api = window.api = p => fetch(p).then(r => r.json()).then(d => d ?? [])
const router = useRouter()
const globalService = inject('globalService', ref(''))
const setPlatformContext = inject('setPlatformContext', () => {})
const services = ref([]), catalogApps = ref([]), lastBuild = ref({}), lastArtifact = ref({}), serviceTargets = ref({}), serviceDelivery = ref({}), lastRelease = ref({})
const showNew = ref(false), editing = ref(null), wizardStep = ref(1)
const form = ref({}), showAdvancedBuild = ref(false)
const buildEnvironment = ref({mode:'local'})
const gitConnections = ref([])
const searchText = ref(''), category = ref('all'), activeTags = ref([])
const TYPES = {
  jar: { label: 'Java JAR', icon: '☕', hint: 'Maven / Gradle 构建，发布到 Linux 主机' },
  static: { label: '前端静态站点', icon: '🌐', hint: 'Node.js 构建，发布到 Nginx 目录' },
  image: { label: 'Docker 镜像', icon: '🐳', hint: '构建并推送镜像，按 digest 发布' },
}
const isSSHGitURL=value=>/^(?:ssh:\/\/|[^/@\s]+@[^:/\s]+:)/i.test((value||'').trim())

async function load() {
  const [svcs, runs, artifacts, deploys, catalog] = await Promise.all([api('api/services'), api('api/pipeline/runs?limit=200'), api('api/artifacts?limit=300'), api('api/deploys?limit=500'), api('/api/registry/applications').catch(() => [])])
  services.value = svcs
  gitConnections.value = (await api('api/connections')).filter(c=>c.kind==='git')
  catalogApps.value = Array.isArray(catalog) ? catalog : []
  const lb = {}, la = {}, lr = {}
  for (const b of runs) if (!lb[b.service]) lb[b.service] = b
  for (const a of artifacts) if (!la[a.service]) la[a.service] = a
  for (const d of deploys) if (!lr[d.service]) lr[d.service] = d
  lastBuild.value = lb
  lastArtifact.value = la
  lastRelease.value = lr
  const pairs = await Promise.all(svcs.map(async s => [s.name, await api(`api/services/${encodeURIComponent(s.name)}/environments`), await api(`api/services/${encodeURIComponent(s.name)}/delivery`)]))
  serviceTargets.value = Object.fromEntries(pairs.map(x=>[x[0],x[1]])); serviceDelivery.value = Object.fromEntries(pairs.map(x=>[x[0],x[2]]))
}
onMounted(load)

function defaults(type = 'jar') {
	const base = { name: '', application_id: '', git_url: '', git_connection: '', branch: '', artifact_type: type, tags: [], tags_text: '', remote_dir: '', start_cmd: '', stop_cmd: '', health_url: '' }
  if (type === 'jar') return { ...base, build_tool: 'maven', maven_executable: 'auto', maven_goals: 'clean package', maven_profiles: '', maven_properties: '', maven_settings_path: '', gradle_executable: 'auto', gradle_tasks: 'clean build', gradle_excluded_tasks: '', gradle_project_properties: '', gradle_system_properties: '', artifact_glob: 'target/*.jar', build_cmd: '' }
  if (type === 'static') return { ...base, build_tool: 'command', build_cmd: 'npm ci && npm run build', artifact_glob: 'dist' }
  return { ...base, build_tool: 'command', build_cmd: '', artifact_glob: '', image_name: '' }
}
function selectTemplate(type) {
	const keep = { name: form.value.name, application_id: form.value.application_id, git_url: form.value.git_url, git_connection: form.value.git_connection, branch: form.value.branch, workdir: form.value.workdir, tags: form.value.tags, tags_text: form.value.tags_text }
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
  if (app) form.value.name = app.name
}
function openEdit(s) {
  router.push(`/svc/${encodeURIComponent(s.name)}/build`)
}
function validateStep() {
  if (wizardStep.value === 1 && (!form.value.name?.trim() || (!form.value.git_url?.trim() && !form.value.workdir?.trim()))) return toast('填写应用名称和代码仓库', 'warning')
  if (wizardStep.value === 2) {
    if (buildEnvironment.value.mode === 'container' && buildEnvironment.value.preset === 'custom' && !buildEnvironment.value.image?.trim()) return toast('填写构建镜像地址和版本', 'warning')
    if (form.value.artifact_type === 'image' && !form.value.image_name?.trim()) return toast('填写镜像仓库地址', 'warning')
    if (form.value.artifact_type === 'jar' && form.value.build_tool === 'maven' && (!form.value.maven_executable?.trim() || !form.value.maven_goals?.trim())) return toast('填写 Maven 可执行文件和 Goals', 'warning')
    if (form.value.artifact_type === 'jar' && form.value.build_tool === 'gradle' && (!form.value.gradle_executable?.trim() || !form.value.gradle_tasks?.trim())) return toast('填写 Gradle 可执行文件和任务', 'warning')
    if (form.value.artifact_type !== 'image' && !['maven','gradle'].includes(form.value.build_tool) && !form.value.build_cmd?.trim()) return toast('填写构建命令', 'warning')
  }
  wizardStep.value++
}
async function save() {
	const body = { ...form.value }
	body.tags = [...new Set((body.tags_text || '').split(/[,，\n]/).map(x => x.trim()).filter(Boolean))].slice(0, 12)
	delete body.tags_text
  delete body.git_connection
  if (body.artifact_type === 'image') body.build_tool = 'command'
  delete body.steps
  const r = await fetch('api/services', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
  if (r.ok) {
    const deliveryResponse = await fetch(`api/services/${encodeURIComponent(body.name)}/delivery`, {method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({...serviceDelivery.value[body.name],git_connection:form.value.git_connection,build_environment:buildEnvironment.value})})
    if (!deliveryResponse.ok) {
      editing.value = body
      wizardStep.value = 2
      return toast(`应用配置已保存，但构建环境保存失败：${(await deliveryResponse.json().catch(()=>({}))).error || '请重试'}`, 'error')
    }
    showNew.value = false
    toast(editing.value ? '应用配置已保存' : '应用已接入，可以开始构建', 'success')
    await load()
    if (!editing.value) router.push(`/svc/${encodeURIComponent(body.name)}/overview`)
  } else toast((await r.json().catch(() => ({}))).error || '保存失败', 'error')
}
async function del(s) {
  if (!confirm(`删除应用「${s.name}」？构建和发布历史将保留。`)) return
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
function build(s) {
  bcSvc.value = s; bcBranch.value = s.branch || ''
  bcMavenProfiles.value = s.maven_profiles || ''; bcMavenProperties.value = s.maven_properties || ''
  bcGradleTasks.value = s.gradle_tasks || 'clean build'; bcGradleExcludedTasks.value = s.gradle_excluded_tasks || ''
  bcGradleProjectProperties.value = s.gradle_project_properties || ''; bcGradleSystemProperties.value = s.gradle_system_properties || ''; showBC.value = true
}
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
const buildText = s => s === 'success' ? '构建成功' : s === 'running' ? '构建中' : s ? '构建失败' : '尚未构建'
const configuredCount = name => serviceTargets.value[name]?.length || 0
const targetReady = t => t?.target_type === 'aliyun-sae' ? !!(t.deploy_config?.connection && t.deploy_config?.app_id && t.deploy_config?.namespace_id && t.deploy_config?.region) : !!(t?.host_group && t?.hosts?.length)
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
    if (!keyword) return true
    return [s.name, s.git_url, s.workdir, s.branch, ...(s.tags || [])].some(v => String(v || '').toLowerCase().includes(keyword))
  })
})
const buildSummary = s => s.artifact_type === 'jar' ? (s.build_tool === 'gradle' ? `${s.gradle_executable} ${s.gradle_tasks}` : `${s.maven_executable} ${s.maven_goals}`) : s.artifact_type === 'static' ? s.build_cmd : `Docker buildx → ${s.image_name}`
function toggleTag(tag) { const i = activeTags.value.indexOf(tag); i >= 0 ? activeTags.value.splice(i, 1) : activeTags.value.push(tag) }
function clearFilters() { searchText.value = ''; category.value = 'all'; activeTags.value = [] }
</script>

<template>
  <div class="services-page">
<PageHeader title="基础构建" help="管理应用的代码来源、构建配置和不可变制品；单应用可直接发布，多应用交付使用部署编排。"><template #actions><ElButton type="primary" native-type="button" @click="openNew">＋ 接入应用</ElButton></template></PageHeader>

    <section v-if="services.length" class="filter-panel">
      <div class="filter-main"><ElInput v-model="searchText" clearable placeholder="搜索应用名、仓库、分支或标签" /><div class="category-tabs"><button v-for="item in [['all','全部'],['frontend','前端'],['backend','后端']]" :key="item[0]" :class="{active:category===item[0]}" @click="category=item[0]">{{item[1]}}<small>{{item[0]==='all'?services.length:services.filter(s=>roleOf(s)===item[0]).length}}</small></button></div><span class="result-count">{{filteredServices.length}} 个应用</span></div>
      <div v-if="availableTags.length" class="tag-filters"><span>标签</span><button v-for="[tag,count] in availableTags" :key="tag" :class="{active:activeTags.includes(tag)}" @click="toggleTag(tag)">{{tag}} <small>{{count}}</small></button><button v-if="activeTags.length" class="clear-filter" @click="activeTags=[]">清除标签</button></div>
    </section>

    <div class="app-grid">
	  <article v-for="s in filteredServices" :key="s.name" class="app-card" role="link" tabindex="0" :aria-label="`查看应用 ${s.name}`" @click="router.push(`/svc/${encodeURIComponent(s.name)}/overview`)" @keydown.enter.self="router.push(`/svc/${encodeURIComponent(s.name)}/overview`)">
        <div class="app-top">
          <span class="app-icon"><UiIcon :name="s.artifact_type === 'jar' ? 'java' : s.artifact_type" :size="22"/></span>
          <div><h3>{{ s.name }}</h3><span>{{ TYPES[s.artifact_type]?.label || s.artifact_type }}</span></div>
          <details class="card-menu" @click.stop><summary title="应用操作">···</summary><div><button @click="openEdit(s)">构建配置</button><button class="danger" @click="del(s)">删除应用</button></div></details>
        </div>
		<div class="app-tags"><span v-for="tag in displayTags(s)" :key="tag" :class="{role:tag==='前端'||tag==='后端'}">{{tag}}</span></div>
        <div class="repo">⌘ {{ repoName(s.git_url || s.workdir) }}<span>{{ s.git_url ? (s.branch || '默认分支') : '本地目录' }}</span></div>
        <div class="app-state">
          <div><small>最新运行</small><b :class="stCls(lastBuild[s.name]?.status)">{{ buildText(lastBuild[s.name]?.status) }}</b><code>#{{lastBuild[s.name]?.id || '—'}} · {{ lastBuild[s.name]?.version || '—' }}</code></div>
          <div><small>可发布制品 / 环境</small><b>{{ lastArtifact[s.name]?.version || '尚无制品' }}</b><span>{{ readyEnvironmentCount(s.name) }}/{{configuredCount(s.name)}} 个环境资源就绪</span></div>
        </div>
        <div :class="['next-action',{ready:resourceReady(s.name)}]">{{resourceReady(s.name)?'✓':'→'}} {{nextAction(s)}}</div>
        <div class="app-actions" @click.stop>
          <ElButton size="small" type="primary" @click="build(s)">构建</ElButton>
          <ElButton size="small" @click="openDelivery(s)">{{resourceReady(s.name)?'发布':'配置环境'}}</ElButton>
        </div>
      </article>
    </div>
	<div v-if="services.length && !filteredServices.length" class="empty-state compact"><div>⌕</div><h3>没有匹配的应用</h3><p>换个关键词或清除筛选条件。</p><ElButton @click="clearFilters">清除筛选</ElButton></div>
    <div v-if="!services.length" class="empty-state">
      <div class="empty-symbol"><UiIcon name="build" :size="28"/></div><h3>接入第一个应用</h3><p>连接代码仓库，选择构建环境，生成可发布制品。</p>
      <div class="onboarding-templates"><button v-for="(type, key) in TYPES" :key="key" type="button" @click="openTemplate(key)"><UiIcon :name="key === 'jar' ? 'java' : key" :size="24"/><strong>{{type.label}}</strong><small>{{type.hint}}</small><span>接入应用 →</span></button></div>
    </div>

    <Modal :show="showBC" :title="`构建 ${bcSvc?.name}`" @close="!bcSubmitting && (showBC = false)">
      <div class="summary-line"><span>{{ TYPES[bcSvc?.artifact_type]?.icon }}</span><div><b>{{ repoName(bcSvc?.git_url) }}</b><small>{{ TYPES[bcSvc?.artifact_type]?.label }}</small></div></div>
      <div class="fld"><label>本次构建分支</label><GitBranchSelect v-model="bcBranch" :repositories="bcSvc?.git_url?[{service:bcSvc.name}]:[]" :disabled="bcSubmitting" /></div>
      <details v-if="bcSvc?.build_tool === 'maven'" class="advanced-box">
        <summary>本次覆盖 Maven 参数（可选）</summary>
        <div class="fld"><label>Profiles</label><ElInput v-model="bcMavenProfiles" placeholder="ci,release" /></div>
        <div class="fld"><label>-D 属性（每行 key=value）</label><ElInput v-model="bcMavenProperties" type="textarea" :rows="3" /></div>
      </details>
      <details v-if="bcSvc?.build_tool === 'gradle'" class="advanced-box" open>
        <summary>本次覆盖 Gradle 参数（可选）</summary>
        <div class="fld"><label>任务</label><ElInput v-model="bcGradleTasks" placeholder="clean build" /></div>
        <div class="fld"><label>排除任务（空格或逗号分隔）</label><ElInput v-model="bcGradleExcludedTasks" placeholder="test integrationTest" /></div>
        <div class="fld"><label>-P 项目属性（每行 key=value）</label><ElInput v-model="bcGradleProjectProperties" type="textarea" :rows="3" /></div>
        <div class="fld"><label>-D 系统属性（每行 key=value）</label><ElInput v-model="bcGradleSystemProperties" type="textarea" :rows="3" /></div>
      </details>
      <div class="modal-actions"><ElButton :disabled="bcSubmitting" @click="showBC = false">取消</ElButton><ElButton type="primary" :loading="bcSubmitting" @click="confirmBuild">开始构建</ElButton></div>
    </Modal>

    <Modal :show="showNew" :title="editing ? '编辑应用' : '接入应用'" wide @close="showNew = false">
      <div class="wizard-steps">
        <span v-for="(x, i) in ['代码', '构建方式', '确认']" :key="x" :class="{ active: wizardStep === i + 1, done: wizardStep > i + 1 }"><i>{{ wizardStep > i + 1 ? '✓' : i + 1 }}</i>{{ x }}</span>
      </div>

      <div v-if="wizardStep === 1">
        <h3 class="wizard-title">连接代码仓库</h3>
        <div v-if="!editing && availableCatalogApps.length" class="discovered-app"><div class="fld"><label>关联已发现应用（可选）</label><Sel :model-value="form.application_id" :options="availableCatalogApps.map(app=>({value:app.application_id,label:`${app.name} · ${(app.sources||[]).length?(app.sources||[]).map(s=>s==='otel'?'OTel':s==='logs'?'日志':s).join(' + '):'历史数据'}`}))" all-label="创建全新应用" filterable width="100%" @update:model-value="selectCatalogApplication" /></div><small>日志或 OTel 先上报时会生成待接入应用；选中后沿用同一个 application_id。</small></div>
        <div class="fld"><label>应用名称</label><ElInput v-model="form.name" :disabled="!!editing" placeholder="例如 order-api" /></div>
        <div class="fld"><label>Git 仓库</label><ElInput v-model="form.git_url" placeholder="https://.../repo.git 或 git@...:repo.git" /><small v-if="isSSHGitURL(form.git_url)" class="git-url-warning">SSH 仓库请选择 SSH 私钥凭据，并在资源中心获取、确认主机指纹。</small></div>
        <div v-if="form.git_url" class="fld"><label>Git 代码凭据</label><Sel v-model="form.git_connection" :options="[{value:'',label:'公开仓库 / 使用执行环境凭据'},...gitConnections.map(c=>({value:c.name,label:`${c.name} · ${c.config?.auth_mode==='ssh'?'SSH':'HTTPS'}`}))]" filterable width="100%" /></div>
        <div v-if="form.workdir && !form.git_url" class="local-source">当前使用本地工作目录：<code>{{ form.workdir }}</code></div>
        <div class="fld"><label>默认分支</label><GitBranchSelect v-model="form.branch" :repositories="[{git_url:form.git_url||'',connection:form.git_connection||''}]" /></div>
		<div class="fld"><label>应用标签</label><ElInput v-model="form.tags_text" placeholder="例如：核心、交易、Java（逗号分隔，最多 12 个）" /><small class="field-help">前端/后端会根据制品类型自动分类，这里填写业务、团队或技术标签。</small></div>
      </div>

      <div v-else-if="wizardStep === 2">
        <h3 class="wizard-title">选择构建模板</h3>
        <div class="template-grid">
          <button v-for="(v, k) in TYPES" :key="k" :class="['template-card', { selected: form.artifact_type === k }]" @click="selectTemplate(k)">
            <span>{{ v.icon }}</span><b>{{ v.label }}</b><small>{{ v.hint }}</small>
          </button>
        </div>
        <template v-if="form.artifact_type === 'jar'">
          <div class="fld"><label>构建工具</label><div class="build-tool-tabs"><button :class="{active:form.build_tool==='maven'}" @click="selectJavaBuildTool('maven')"><b>Maven</b><small>pom.xml / mvnw</small></button><button :class="{active:form.build_tool==='gradle'}" @click="selectJavaBuildTool('gradle')"><b>Gradle</b><small>build.gradle(.kts) / gradlew</small></button></div></div>
          <div v-if="form.build_tool === 'maven'" class="two-col"><div class="fld"><label>Maven 执行方式</label><ElInput v-model="form.maven_executable" placeholder="auto / mvn / /opt/maven/bin/mvn" /><small class="field-help">auto：优先 ./mvnw，没有则使用 Runner 的 mvn。</small></div><div class="fld"><label>Goals</label><ElInput v-model="form.maven_goals" placeholder="clean package" /></div></div>
          <div v-else class="two-col"><div class="fld"><label>Gradle 执行方式</label><ElInput v-model="form.gradle_executable" placeholder="auto / gradle / /opt/gradle/bin/gradle" /><small class="field-help">auto：优先 ./gradlew，没有则使用 Runner 的 gradle。</small></div><div class="fld"><label>任务</label><ElInput v-model="form.gradle_tasks" placeholder="clean build" /></div></div>
          <div class="fld"><label>产物位置</label><ElInput v-model="form.artifact_glob" :placeholder="form.build_tool === 'gradle' ? 'build/libs/*.jar' : 'target/*.jar'" /></div>
        </template>
        <template v-else-if="form.artifact_type === 'static'">
          <div class="fld"><label>构建命令</label><ElInput v-model="form.build_cmd" placeholder="npm ci && npm run build" /></div>
          <div class="fld"><label>输出目录</label><ElInput v-model="form.artifact_glob" placeholder="dist" /></div>
        </template>
        <template v-else>
          <div class="fld"><label>镜像仓库 / 镜像名</label><ElInput v-model="form.image_name" placeholder="registry.example.com/team/order-api" /></div>
          <p class="field-help">平台会使用 Docker buildx 推送镜像，并记录不可变 digest。</p>
        </template>
        <div style="margin:16px 0"><BuildEnvironmentEditor v-model="buildEnvironment" :build-tool="form.build_tool" :artifact-type="form.artifact_type" /></div>
        <button class="advanced-toggle" @click="showAdvancedBuild = !showAdvancedBuild">{{ showAdvancedBuild ? '收起高级参数' : '高级构建参数' }}⌄</button>
        <div v-if="showAdvancedBuild" class="advanced-panel">
          <template v-if="form.artifact_type === 'jar'">
            <template v-if="form.build_tool === 'maven'">
              <div class="fld"><label>Maven Profiles</label><ElInput v-model="form.maven_profiles" placeholder="ci,release" /></div>
              <div class="fld"><label>Maven settings.xml</label><ElInput v-model="form.maven_settings_path" placeholder="/opt/logplatform/settings.xml" /></div>
              <div class="fld"><label>-D 属性（每行 key=value）</label><ElInput v-model="form.maven_properties" type="textarea" :rows="3" /></div>
            </template>
            <template v-else>
              <div class="fld"><label>排除任务（空格或逗号分隔）</label><ElInput v-model="form.gradle_excluded_tasks" placeholder="test integrationTest" /></div>
              <div class="fld"><label>-P 项目属性（每行 key=value）</label><ElInput v-model="form.gradle_project_properties" type="textarea" :rows="3" placeholder="profile=prod" /></div>
              <div class="fld"><label>-D 系统属性（每行 key=value）</label><ElInput v-model="form.gradle_system_properties" type="textarea" :rows="3" /></div>
              <small class="field-help">敏感值请写成 ${env.NAME}，由构建变量组在运行时注入。</small>
            </template>
          </template>
          <div v-else-if="form.artifact_type !== 'image'" class="fld"><label>自定义构建命令</label><ElInput v-model="form.build_cmd" type="textarea" :rows="2" /></div>
        </div>
      </div>

      <div v-else class="confirm-card">
        <span class="confirm-icon">{{ template.icon }}</span>
		<h3>{{ form.name }}</h3><p>{{ template.label }} · {{ form.artifact_type==='static'?'前端':'后端' }}</p>
        <dl><div><dt>代码来源</dt><dd>{{ form.git_url || form.workdir }}</dd></div><div><dt>分支</dt><dd>{{ form.git_url ? (form.branch || '仓库默认分支') : '本地工作目录' }}</dd></div><div><dt>构建</dt><dd>{{ buildSummary(form) }}</dd></div><div v-if="form.artifact_type==='jar'"><dt>产物</dt><dd>{{form.artifact_glob}}</dd></div></dl>
        <p class="next-hint">构建环境：{{buildEnvironment.mode==='container' ? (buildEnvironment.preset==='custom' ? buildEnvironment.image : buildEnvironment.preset) : '本机工具'}}。保存后先构建制品，再为测试、预发或生产环境配置部署目标。</p>
      </div>

      <div class="modal-actions">
        <ElButton v-if="wizardStep > 1" @click="wizardStep--">上一步</ElButton><span class="spacer"></span>
        <ElButton @click="showNew = false">取消</ElButton>
        <ElButton v-if="wizardStep < 3" type="primary" @click="validateStep">下一步</ElButton>
        <ElButton v-else type="primary" @click="save">{{ editing ? '保存修改' : '完成接入' }}</ElButton>
      </div>
    </Modal>
  </div>
</template>

<style scoped>
.page-head{display:flex;align-items:center;margin-bottom:12px}.page-head h2{font-size:20px}.page-head>button{margin-left:auto}.title-row{display:flex;align-items:center;gap:7px}
.filter-panel{background:var(--panel);border:1px solid var(--border);border-radius:10px;padding:11px 12px;margin-bottom:14px}.filter-main{display:flex;align-items:center;gap:10px}.filter-main>:first-child{max-width:360px}.category-tabs{display:flex;align-items:center;background:var(--panel2);padding:3px;border-radius:7px}.category-tabs button{border:0;background:transparent;color:var(--muted);padding:5px 10px;border-radius:5px;cursor:pointer}.category-tabs button.active{background:var(--panel);color:var(--brand);box-shadow:0 1px 4px rgba(0,0,0,.12)}.category-tabs small{margin-left:5px;color:var(--dim)}.result-count{margin-left:auto;color:var(--muted);font-size:10px;white-space:nowrap}.tag-filters{display:flex;align-items:center;gap:6px;flex-wrap:wrap;border-top:1px solid var(--border);margin-top:10px;padding-top:9px}.tag-filters>span{color:var(--dim);font-size:10px;margin-right:2px}.tag-filters button{border:1px solid var(--border);background:transparent;color:var(--muted);padding:3px 8px;border-radius:999px;font-size:10px;cursor:pointer}.tag-filters button.active{border-color:var(--brand);background:color-mix(in srgb,var(--brand) 10%,var(--panel));color:var(--brand)}.tag-filters small{opacity:.7;margin-left:3px}.tag-filters .clear-filter{border:0;color:var(--brand)}
.app-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(310px,1fr));gap:14px}.app-card{background:var(--panel);border:1px solid var(--border);border-radius:10px;padding:16px;cursor:pointer;transition:.15s}.app-card:hover{border-color:var(--brand);box-shadow:0 3px 12px rgba(0,0,0,.06);transform:translateY(-1px)}
.app-top{display:flex;align-items:center;gap:10px}.app-icon{display:grid;place-items:center;width:38px;height:38px;border-radius:9px;background:var(--panel2);font-size:20px}.app-top h3{font-size:15px}.app-top div span{color:var(--muted);font-size:11px}.card-menu{position:relative;margin-left:auto}.card-menu summary{list-style:none;border:0;background:transparent;font-size:18px;padding:2px 7px;color:var(--muted);cursor:pointer}.card-menu summary::-webkit-details-marker{display:none}.card-menu>div{position:absolute;right:0;top:28px;z-index:10;display:flex;flex-direction:column;min-width:110px;padding:4px;background:var(--panel);border:1px solid var(--border);border-radius:7px;box-shadow:0 8px 20px rgba(0,0,0,.2)}.card-menu button{border:0;text-align:left;background:transparent;padding:6px 8px}.card-menu button:hover{background:var(--panel2)}.card-menu button.danger{color:var(--error)}
.app-tags{display:flex;gap:5px;flex-wrap:wrap;margin:10px 0 -5px}.app-tags span{font-size:9px;padding:2px 6px;border-radius:999px;background:var(--panel2);color:var(--muted)}.app-tags span.role{color:var(--brand);background:color-mix(in srgb,var(--brand) 10%,var(--panel))}
.repo{margin:14px 0 12px;padding:8px 10px;background:var(--panel2);border-radius:6px;font-family:var(--mono);font-size:11px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.repo span{float:right;color:var(--muted);font-family:inherit}
.app-state{display:grid;grid-template-columns:1fr 1fr;gap:12px}.app-state>div{display:flex;flex-direction:column;min-width:0}.app-state small{color:var(--dim)}.app-state b{font-size:12px;margin:2px 0}.app-state code,.app-state span{font-size:10px;color:var(--muted);overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.app-actions{display:flex;align-items:center;border-top:1px solid var(--border);margin-top:12px;padding-top:12px}.next-action{margin-top:11px;padding:6px 8px;border-radius:6px;background:color-mix(in srgb,var(--warn) 9%,var(--panel));color:var(--warn);font-size:11px}.next-action.ready{background:color-mix(in srgb,var(--ok) 9%,var(--panel));color:var(--ok)}
.empty-state{text-align:center;padding:70px 20px;border:1px dashed var(--border);border-radius:10px;color:var(--muted)}.empty-state.compact{padding:36px 20px}.empty-state>div{font-size:36px}.empty-state h3{color:var(--text);margin:8px}.empty-state p{margin-bottom:16px}
.wizard-steps{display:flex;justify-content:center;gap:56px;margin:-4px 0 24px}.wizard-steps span{position:relative;display:flex;align-items:center;gap:6px;color:var(--dim);font-size:12px}.wizard-steps span:not(:last-child):after{content:'';position:absolute;width:40px;height:1px;background:var(--border);left:calc(100% + 8px)}.wizard-steps i{font-style:normal;display:grid;place-items:center;width:22px;height:22px;border:1px solid var(--border);border-radius:50%}.wizard-steps .active{color:var(--brand);font-weight:600}.wizard-steps .active i,.wizard-steps .done i{background:var(--brand);border-color:var(--brand);color:#fff}.wizard-title{font-size:15px;margin-bottom:14px}.template-grid{display:grid;grid-template-columns:repeat(3,1fr);gap:8px;margin-bottom:16px}.template-card{display:flex;flex-direction:column;align-items:flex-start;gap:4px;padding:12px;text-align:left;background:var(--panel);cursor:pointer}.template-card>span{font-size:20px}.template-card small{color:var(--muted);font-size:10px;line-height:1.35}.template-card.selected{border-color:var(--brand);box-shadow:inset 0 0 0 1px var(--brand)}.two-col{display:grid;grid-template-columns:1fr 1.4fr;gap:10px}.field-help{color:var(--muted);font-size:11px;margin-top:-4px}.advanced-toggle{border:0;background:transparent;padding:5px 0;color:var(--brand)}.advanced-panel{border-left:2px solid var(--border);padding:8px 0 0 12px}.confirm-card{text-align:center;padding:8px 10px}.confirm-icon{font-size:34px}.confirm-card h3{font-size:18px;margin-top:6px}.confirm-card>p{color:var(--muted)}.confirm-card dl{text-align:left;margin:18px 0;background:var(--panel2);border-radius:8px;padding:6px 14px}.confirm-card dl div{display:grid;grid-template-columns:90px 1fr;padding:8px 0;border-bottom:1px solid var(--border)}.confirm-card dl div:last-child{border:0}.confirm-card dt{color:var(--muted)}.confirm-card dd{font-family:var(--mono);word-break:break-all}.next-hint{font-size:11px}.modal-actions{display:flex;gap:8px;align-items:center;margin-top:18px}.modal-actions .spacer{flex:1}.summary-line{display:flex;align-items:center;gap:10px;background:var(--panel2);padding:10px;border-radius:8px;margin-bottom:14px}.summary-line>span{font-size:22px}.summary-line div{display:flex;flex-direction:column}.summary-line small{color:var(--muted)}.advanced-box{margin-top:12px}.advanced-box summary{cursor:pointer;color:var(--brand);margin-bottom:10px}
.build-tool-tabs{display:grid;grid-template-columns:1fr 1fr;gap:8px}.build-tool-tabs button{display:flex;flex-direction:column;align-items:flex-start;gap:2px;padding:9px 11px;background:var(--panel2);border:1px solid var(--border);border-radius:7px;color:var(--text);cursor:pointer}.build-tool-tabs button.active{border-color:var(--brand);background:color-mix(in srgb,var(--brand) 8%,var(--panel));box-shadow:inset 0 0 0 1px var(--brand)}.build-tool-tabs small{color:var(--muted);font-size:10px}
.local-source{font-size:10px;color:var(--muted);padding:8px 10px;background:var(--panel2);border-radius:6px;margin:-2px 0 10px}.local-source code{word-break:break-all}
.discovered-app{padding:10px 11px;margin-bottom:12px;border:1px solid color-mix(in srgb,var(--brand) 35%,var(--border));border-radius:8px;background:color-mix(in srgb,var(--brand) 5%,var(--panel))}.discovered-app .fld{margin-bottom:4px}.discovered-app>small{color:var(--muted);font-size:10px}
.git-url-warning{display:block;margin-top:5px;color:var(--warn);font-size:10px;line-height:1.45}
@media(max-width:760px){.filter-main{align-items:stretch;flex-direction:column}.filter-main>:first-child{max-width:none}.result-count{margin-left:0}.category-tabs{align-self:flex-start}.app-grid{grid-template-columns:1fr}.template-grid{grid-template-columns:1fr}.wizard-steps{gap:24px}.wizard-steps span:not(:last-child):after{display:none}}
</style>

<style scoped>
.services-page{max-width:1500px;margin:0 auto}.page-head{margin-bottom:24px;gap:12px}.page-head h2{font-size:22px}.filter-panel{padding:16px;margin-bottom:20px;box-shadow:var(--shadow-sm)}.result-count{font-size:12px}.app-grid{gap:20px;grid-template-columns:repeat(auto-fill,minmax(min(100%,330px),1fr))}.app-card{padding:22px;border-radius:14px;box-shadow:var(--shadow-sm)}.app-card:hover{transform:none;border-color:var(--brand);box-shadow:0 6px 20px rgb(29 41 61 / 8%)}.app-top>div{min-width:0}.app-top h3{font-size:17px;overflow-wrap:anywhere}.app-top div span{font-size:12px}.app-icon{width:44px;height:44px;border-radius:12px;background:var(--brand-soft);color:var(--brand-ink);flex-shrink:0}.app-tags{margin:14px 0 0}.app-tags span{font-size:11px;padding:2px 8px}.repo{margin:16px 0;padding:10px;font-size:12px}.app-state b{font-size:14px;margin:4px 0}.app-state small,.app-state code,.app-state span{font-size:12px}.next-action{padding:8px 10px;margin-top:18px;font-size:12px}.app-actions{margin-top:18px;padding-top:16px}.empty-state{padding:48px 24px;background:var(--panel);border:1px solid var(--border);border-radius:16px;box-shadow:var(--shadow-sm)}.empty-state .empty-symbol{display:grid;place-items:center;margin:0 auto 16px;width:60px;height:60px;border-radius:18px;background:var(--brand-soft);color:var(--brand-ink)}.empty-state h3{font-size:20px;margin:0 0 8px}.empty-state p{font-size:13px;margin-bottom:28px}.onboarding-templates{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:16px;max-width:860px;margin:auto;text-align:left}.onboarding-templates button{display:grid;justify-items:start;gap:10px;padding:24px;background:var(--panel);border:1px solid var(--border);border-radius:12px;color:var(--text);transition:border-color .15s,background .15s}.onboarding-templates button:hover{border-color:var(--brand);background:var(--brand-soft)}.onboarding-templates .ui-icon{color:var(--brand-ink)}.onboarding-templates strong{font-size:15px}.onboarding-templates small{font-size:12px;line-height:1.7;color:var(--muted)}.onboarding-templates span{font-size:12px;font-weight:600;color:var(--brand-ink);margin-top:8px}.empty-state.compact{padding:36px 20px}@media(max-width:760px){.page-head{margin-bottom:18px}.onboarding-templates{grid-template-columns:1fr}.onboarding-templates button{grid-template-columns:32px 1fr;gap:6px 12px;padding:16px}.onboarding-templates .ui-icon{grid-row:1/4}.onboarding-templates small,.onboarding-templates span{grid-column:2}.onboarding-templates span{margin:0}.empty-state{padding:32px 20px}.filter-main{flex-wrap:wrap}.filter-main>:first-child{max-width:100%;flex:1 0 100%}.app-card{padding:18px}}
</style>
