<script setup>
import { computed, onMounted, ref } from 'vue'
import Modal from '../../shared/components/Modal.vue'
import HelpTip from '../../shared/components/HelpTip.vue'
import Sel from '../../shared/components/Sel.vue'
import { ElButton, ElInput } from '../../shared/components/ui.js'
import { toast } from '../../shared/composables/toast.js'

const versions = ref([]), environments = ref([]), services = ref([]), me = ref({})
const selected = ref(null), statusFilter = ref(''), environmentFilter = ref('')
const showVersion = ref(false), showWork = ref(false), showChange = ref(false), saving = ref(false)
const versionForm = ref({}), workForm = ref({}), changeForm = ref({})

const statusOptions = [
  { value: 'draft', label: '草稿' }, { value: 'pending_approval', label: '待审核' },
  { value: 'approved', label: '已就绪' }, { value: 'rejected', label: '已驳回' },
  { value: 'completed', label: '已归档' }, { value: 'canceled', label: '已取消' },
]
const statusText = Object.fromEntries(statusOptions.map(v => [v.value, v.label]))
const changeKinds = [
  { value: 'artifact', label: '应用制品' }, { value: 'mysql', label: 'MySQL 变更' },
  { value: 'nacos', label: 'Nacos 配置' }, { value: 'manual', label: '人工操作' },
]
const kindText = Object.fromEntries(changeKinds.map(v => [v.value, v.label]))
const riskText = { low: '低', medium: '中', high: '高' }

async function request(url, options) {
  const response = await fetch(url, options)
  const body = await response.json().catch(() => ({}))
  if (!response.ok) throw new Error(body.error || `请求失败（${response.status}）`)
  return body
}
async function loadVersions(selectID = selected.value?.id) {
  const query = new URLSearchParams({ limit: '200' })
  if (statusFilter.value) query.set('status', statusFilter.value)
  if (environmentFilter.value) query.set('environment', environmentFilter.value)
  versions.value = await request(`api/change-versions?${query}`)
  const target = versions.value.find(v => v.id === selectID) || versions.value[0]
  if (target) await selectVersion(target.id)
  else selected.value = null
}
async function selectVersion(id) { selected.value = await request(`api/change-versions/${id}`) }
function environmentName(name) { return environments.value.find(v => v.name === name)?.display_name || name }
const selectedEnvironment = computed(() => environments.value.find(v => v.name === selected.value?.environment))
const editable = computed(() => ['draft', 'rejected'].includes(selected.value?.status))
const manageable = computed(() => !!selected.value && (me.value.username === 'local' || me.value.role === 'admin' || me.value.username === selected.value.owner))
const canReview = computed(() => {
  const names = String(selectedEnvironment.value?.approvers || '').split(',').map(v => v.trim())
  return selected.value?.status === 'pending_approval' && names.includes(me.value.username) && selected.value.owner !== me.value.username
})
function statusClass(status) {
  return status === 'approved' || status === 'completed' ? 'ok' : status === 'rejected' || status === 'canceled' ? 'bad' : status === 'pending_approval' ? 'warn' : 'dim'
}
function openVersion(version) {
  versionForm.value = version ? { version: version.version, title: version.title, environment: version.environment, description: version.description } : { version: '', title: '', environment: environmentFilter.value || environments.value[0]?.name || '', description: '' }
  showVersion.value = true
}
async function saveVersion() {
  if (!versionForm.value.version?.trim() || !versionForm.value.title?.trim() || !versionForm.value.environment) return toast('版本号、标题和目标环境必填', 'warning')
  saving.value = true
  try {
    const editing = selected.value && showVersion.value && versionForm.value._id
    const body = await request(editing ? `api/change-versions/${versionForm.value._id}` : 'api/change-versions', { method: editing ? 'PUT' : 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(versionForm.value) })
    showVersion.value = false; toast(editing ? '版本已更新' : '版本已创建', 'success'); await loadVersions(body.id)
  } catch (error) { toast(error.message, 'error') } finally { saving.value = false }
}
function editVersion() {
  versionForm.value = { _id: selected.value.id, version: selected.value.version, title: selected.value.title, environment: selected.value.environment, description: selected.value.description }
  showVersion.value = true
}
async function deleteVersion() {
  if (!confirm(`删除版本「${selected.value.version}」及其全部变更内容？`)) return
  try { await request(`api/change-versions/${selected.value.id}`, { method: 'DELETE' }); toast('版本已删除', 'success'); selected.value = null; await loadVersions() } catch (error) { toast(error.message, 'error') }
}
function openWork() { workForm.value = { source: 'manual', external_id: '', kind: 'requirement', title: '', url: '', status: '' }; showWork.value = true }
async function addWork() {
  if (!workForm.value.title?.trim()) return toast('需求或缺陷标题必填', 'warning')
  try { await request(`api/change-versions/${selected.value.id}/work-items`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(workForm.value) }); showWork.value = false; toast('需求已关联', 'success'); await loadVersions(selected.value.id) } catch (error) { toast(error.message, 'error') }
}
function openChange() { changeForm.value = { kind: 'artifact', title: '', service: '', reference: '', summary: '', risk_level: 'medium', rollback_plan: '' }; showChange.value = true }
async function addChange() {
  if (!changeForm.value.title?.trim()) return toast('变更标题必填', 'warning')
  try { await request(`api/change-versions/${selected.value.id}/changes`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(changeForm.value) }); showChange.value = false; toast('变更项已添加', 'success'); await loadVersions(selected.value.id) } catch (error) { toast(error.message, 'error') }
}
async function removeItem(type, id) {
  if (!confirm('删除这条记录？')) return
  try { await request(`api/change-versions/${selected.value.id}/${type}/${id}`, { method: 'DELETE' }); await loadVersions(selected.value.id) } catch (error) { toast(error.message, 'error') }
}
async function action(name) {
  const labels = { submit: '提交审核', approve: '审核通过', reject: '驳回', reopen: '重新编辑', complete: '归档', cancel: '取消' }
  if (['reject', 'cancel', 'complete'].includes(name) && !confirm(`确认${labels[name]}当前版本？`)) return
  try { const body = await request(`api/change-versions/${selected.value.id}/${name}`, { method: 'POST' }); toast(`${labels[name]}成功`, 'success'); await loadVersions(body.id) } catch (error) { toast(error.message, 'error') }
}
async function initialize() {
  try {
    [environments.value, services.value, me.value] = await Promise.all([request('api/environments'), request('api/services'), request('/api/me')])
    await loadVersions()
  } catch (error) { toast(error.message, 'error') }
}
onMounted(initialize)
</script>

<template>
  <div class="changes-page">
    <div class="page-head">
      <div><div class="title-row"><h2>变更中心</h2><HelpTip text="一个版本聚合需求、应用制品、SQL、配置和人工操作；审核通过后才进入后续发布编排。" /></div><p>先确认“本次为什么改、具体改什么、失败怎么退”，再组织发布。</p></div>
      <ElButton type="primary" @click="openVersion()">＋ 新建版本</ElButton>
    </div>
    <div class="toolbar">
      <Sel v-model="statusFilter" :options="statusOptions" all-label="全部状态" width="150px" @change="loadVersions()" />
      <Sel v-model="environmentFilter" :options="environments.map(v=>({value:v.name,label:v.display_name}))" all-label="全部环境" width="150px" @change="loadVersions()" />
      <span>共 {{ versions.length }} 个版本</span>
    </div>
    <div v-if="versions.length" class="workspace">
      <aside class="version-list">
        <button v-for="version in versions" :key="version.id" :class="{ active:selected?.id===version.id }" @click="selectVersion(version.id)">
          <div><b>{{ version.version }}</b><span :class="['badge',statusClass(version.status)]">{{ statusText[version.status] || version.status }}</span></div>
          <strong>{{ version.title }}</strong>
          <small>{{ environmentName(version.environment) }} · {{ version.owner || '未知发起人' }}</small>
        </button>
      </aside>
      <section v-if="selected" class="detail">
        <div class="detail-head">
          <div><div class="eyebrow">{{ environmentName(selected.environment) }} · #{{ selected.id }}</div><h3>{{ selected.version }} · {{ selected.title }}</h3><p>{{ selected.description || '暂无版本说明' }}</p></div>
          <span :class="['state',statusClass(selected.status)]">{{ statusText[selected.status] || selected.status }}</span>
        </div>
        <div class="meta"><span>发起人 <b>{{ selected.owner }}</b></span><span v-if="selected.approver">审核人 <b>{{ selected.approver }}</b></span><span>更新 {{ new Date(selected.updated_at*1000).toLocaleString() }}</span></div>
        <div class="actions">
          <template v-if="manageable">
            <ElButton v-if="editable" @click="editVersion">编辑版本</ElButton>
            <ElButton v-if="editable" type="primary" @click="action('submit')">提交审核</ElButton>
            <ElButton v-if="['rejected','approved'].includes(selected.status)" @click="action('reopen')">重新编辑</ElButton>
            <ElButton v-if="selected.status==='approved'" type="primary" @click="action('complete')">确认完成并归档</ElButton>
            <ElButton v-if="['draft','rejected','pending_approval','approved'].includes(selected.status)" @click="action('cancel')">取消版本</ElButton>
            <ElButton v-if="['draft','rejected','canceled'].includes(selected.status)" @click="deleteVersion">删除</ElButton>
          </template>
          <template v-if="canReview"><ElButton type="primary" @click="action('approve')">审核通过</ElButton><ElButton @click="action('reject')">驳回</ElButton></template>
        </div>

        <article class="block">
          <header><div><h4>需求与缺陷</h4><p>可以手工新建，也可以填写外部系统编号和链接。</p></div><ElButton v-if="editable && manageable" size="small" @click="openWork">＋ 添加</ElButton></header>
          <div v-if="selected.work_items?.length" class="rows">
            <div v-for="item in selected.work_items" :key="item.id" class="row">
              <span class="type">{{ item.kind==='bug'?'缺陷':'需求' }}</span><div><b>{{ item.title }}</b><small>{{ item.source==='manual'?'手工录入':item.source }}<template v-if="item.external_id"> · {{ item.external_id }}</template><template v-if="item.status"> · {{ item.status }}</template></small></div>
              <a v-if="item.url" :href="item.url" target="_blank">查看</a><button v-if="editable && manageable" class="op del" @click="removeItem('work-items',item.id)">删除</button>
            </div>
          </div><div v-else class="empty-mini">尚未关联需求；没有外部 Bug 时可直接添加手工需求。</div>
        </article>

        <article class="block">
          <header><div><h4>实施变更</h4><p>制品、数据库、配置和人工操作统一进入本版本。</p></div><ElButton v-if="editable && manageable" size="small" @click="openChange">＋ 添加</ElButton></header>
          <div v-if="selected.changes?.length" class="change-grid">
            <div v-for="item in selected.changes" :key="item.id" class="change-card">
              <div><span class="type">{{ kindText[item.kind] || item.kind }}</span><span :class="['risk',item.risk_level]">{{ riskText[item.risk_level] || item.risk_level }}风险</span></div>
              <b>{{ item.title }}</b><small v-if="item.service">应用：{{ item.service }}</small><code v-if="item.reference">{{ item.reference }}</code><p>{{ item.summary || '暂无实施说明' }}</p><p class="rollback">回退：{{ item.rollback_plan || '未填写' }}</p>
              <button v-if="editable && manageable" class="op del" @click="removeItem('changes',item.id)">删除</button>
            </div>
          </div><div v-else class="empty-mini">尚未添加实施变更，提交前至少需要一项。</div>
        </article>
      </section>
    </div>
    <div v-else class="empty-state"><span>🧾</span><h3>建立第一个发布版本</h3><p>把分散的需求、制品、SQL 和配置归入同一个可审核版本。</p><ElButton type="primary" @click="openVersion()">新建版本</ElButton></div>

    <Modal :show="showVersion" :title="versionForm._id ? '编辑版本' : '新建版本'" wide @close="showVersion=false">
      <div class="form-grid"><div class="fld"><label>版本号</label><ElInput v-model="versionForm.version" placeholder="如 2026.10.1" /></div><div class="fld"><label>目标环境</label><Sel v-model="versionForm.environment" :options="environments.map(v=>({value:v.name,label:v.display_name}))" width="100%" /></div></div>
      <div class="fld"><label>版本标题</label><ElInput v-model="versionForm.title" placeholder="说明本次版本的业务目标" /></div>
      <div class="fld"><label>版本说明</label><textarea v-model="versionForm.description" rows="4" placeholder="范围、窗口和注意事项"></textarea></div>
      <div class="dialog-actions"><ElButton @click="showVersion=false">取消</ElButton><ElButton type="primary" :loading="saving" @click="saveVersion">保存</ElButton></div>
    </Modal>

    <Modal :show="showWork" title="添加需求或缺陷" wide @close="showWork=false">
      <div class="form-grid"><div class="fld"><label>来源</label><select v-model="workForm.source"><option value="manual">手工录入</option><option value="external">外部工作项</option></select></div><div class="fld"><label>类型</label><select v-model="workForm.kind"><option value="requirement">需求</option><option value="bug">缺陷</option></select></div></div>
      <div class="fld"><label>标题</label><ElInput v-model="workForm.title" placeholder="需求或缺陷标题" /></div>
      <div class="form-grid"><div class="fld"><label>外部编号（可选）</label><ElInput v-model="workForm.external_id" placeholder="如 ZT-1024" /></div><div class="fld"><label>外部状态（可选）</label><ElInput v-model="workForm.status" placeholder="如 已关闭/待上线" /></div></div>
      <div class="fld"><label>链接（可选）</label><ElInput v-model="workForm.url" placeholder="https://..." /></div>
      <div class="dialog-actions"><ElButton @click="showWork=false">取消</ElButton><ElButton type="primary" @click="addWork">添加</ElButton></div>
    </Modal>

    <Modal :show="showChange" title="添加实施变更" wide @close="showChange=false">
      <div class="form-grid"><div class="fld"><label>变更类型</label><select v-model="changeForm.kind"><option v-for="item in changeKinds" :key="item.value" :value="item.value">{{ item.label }}</option></select></div><div class="fld"><label>风险等级</label><select v-model="changeForm.risk_level"><option value="low">低</option><option value="medium">中</option><option value="high">高</option></select></div></div>
      <div class="fld"><label>标题</label><ElInput v-model="changeForm.title" placeholder="例如：发布 AI 服务、执行用户表索引" /></div>
      <div class="form-grid"><div class="fld"><label>关联应用（可选）</label><Sel v-model="changeForm.service" :options="services.map(v=>({value:v.name,label:v.name}))" all-label="不关联应用" width="100%" /></div><div class="fld"><label>制品 / SQL / 配置引用</label><ElInput v-model="changeForm.reference" placeholder="artifact:24、sql:v3、nacos:dataId@v2" /></div></div>
      <div class="fld"><label>实施说明</label><textarea v-model="changeForm.summary" rows="3" placeholder="执行内容和前置条件"></textarea></div>
      <div class="fld"><label>回退方案</label><textarea v-model="changeForm.rollback_plan" rows="3" placeholder="失败后如何恢复"></textarea></div>
      <div class="dialog-actions"><ElButton @click="showChange=false">取消</ElButton><ElButton type="primary" @click="addChange">添加</ElButton></div>
    </Modal>
  </div>
</template>

<style scoped>
.changes-page{max-width:1500px;margin:0 auto}.page-head{display:flex;align-items:flex-start;gap:16px;margin-bottom:12px}.page-head>button{margin-left:auto}.page-head h2{font-size:20px}.page-head p,.block header p{color:var(--muted);font-size:12px}.title-row{display:flex;align-items:center;gap:7px}.toolbar{display:flex;align-items:center;gap:8px;background:var(--panel);border:1px solid var(--border);border-radius:8px;padding:9px;margin-bottom:12px}.toolbar>span{margin-left:auto;color:var(--dim);font-size:11px}.workspace{display:grid;grid-template-columns:270px minmax(0,1fr);gap:12px;min-height:calc(100vh - 145px)}.version-list{background:var(--panel);border:1px solid var(--border);border-radius:9px;padding:6px;overflow:auto}.version-list button{display:block;width:100%;border:0;background:transparent;text-align:left;padding:10px;cursor:pointer}.version-list button+button{border-top:1px solid var(--border)}.version-list button.active{background:var(--panel2)}.version-list button>div{display:flex;align-items:center;gap:6px}.version-list strong{display:block;font-size:12px;margin:4px 0}.version-list small,.row small,.change-card small{display:block;color:var(--dim)}.badge,.state{border-radius:10px;padding:1px 7px;font-size:10px}.badge{margin-left:auto}.ok{color:var(--ok);background:color-mix(in srgb,var(--ok) 12%,transparent)}.bad{color:var(--error);background:color-mix(in srgb,var(--error) 10%,transparent)}.warn{color:var(--warn);background:color-mix(in srgb,var(--warn) 12%,transparent)}.dim{color:var(--muted);background:var(--panel2)}.detail{min-width:0}.detail-head{display:flex;align-items:flex-start;background:var(--panel);border:1px solid var(--border);border-radius:9px 9px 0 0;padding:15px}.detail-head h3{font-size:17px;margin:2px 0}.detail-head p{color:var(--muted)}.detail-head .state{margin-left:auto;font-size:11px}.eyebrow{color:var(--brand);font-size:10px}.meta{display:flex;gap:20px;padding:8px 15px;background:var(--panel2);color:var(--muted);font-size:11px}.actions{display:flex;justify-content:flex-end;gap:7px;padding:10px;background:var(--panel);border:1px solid var(--border);border-top:0;border-radius:0 0 9px 9px}.block{margin-top:12px;background:var(--panel);border:1px solid var(--border);border-radius:9px;padding:13px}.block header{display:flex;align-items:center;margin-bottom:10px}.block header h4{font-size:13px}.block header button{margin-left:auto}.rows{border-top:1px solid var(--border)}.row{display:flex;align-items:center;gap:10px;padding:9px 2px;border-bottom:1px solid var(--border)}.row>div{flex:1}.row>a{color:var(--brand)}.type{font-size:10px;color:var(--brand);background:var(--panel2);padding:2px 6px;border-radius:5px;white-space:nowrap}.empty-mini{padding:18px;text-align:center;color:var(--dim);background:var(--panel2);border-radius:6px}.change-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(250px,1fr));gap:8px}.change-card{position:relative;border:1px solid var(--border);border-radius:7px;padding:10px;min-width:0}.change-card>div{display:flex;gap:6px;margin-bottom:7px}.change-card>b,.change-card code{display:block;margin-bottom:5px}.change-card code{font-size:10px;word-break:break-all;color:var(--brand)}.change-card p{font-size:11px;color:var(--muted);white-space:pre-wrap}.change-card .rollback{margin-top:7px;padding-top:7px;border-top:1px dashed var(--border)}.change-card .op{position:absolute;right:2px;top:8px}.risk{font-size:9px}.risk.low{color:var(--ok)}.risk.medium{color:var(--warn)}.risk.high{color:var(--error)}.empty-state{height:55vh;display:flex;flex-direction:column;align-items:center;justify-content:center;border:1px dashed var(--border);border-radius:9px;color:var(--muted)}.empty-state span{font-size:32px}.empty-state h3{color:var(--text);margin:10px}.empty-state button{margin-top:14px}.form-grid{display:grid;grid-template-columns:1fr 1fr;gap:10px}.fld select,.fld textarea{width:100%}.dialog-actions{display:flex;justify-content:flex-end;gap:8px;margin-top:14px}@media(max-width:900px){.workspace{grid-template-columns:1fr}.version-list{max-height:230px}.form-grid{grid-template-columns:1fr}.meta{flex-wrap:wrap}.actions{flex-wrap:wrap}}
</style>
