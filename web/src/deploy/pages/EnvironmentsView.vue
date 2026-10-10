<script setup>
import PageHeader from "../../shared/components/PageHeader.vue"
import { ref, onMounted } from 'vue'
import Modal from '../../shared/components/Modal.vue'
import HelpTip from '../../shared/components/HelpTip.vue'
import Sel from '../../shared/components/Sel.vue'
import { ElButton, ElInput, ElInputNumber } from '../../shared/components/ui.js'
import { toast } from '../../shared/composables/toast.js'

defineProps({ embedded: { type: Boolean, default: false } })
const environments = ref([]), users = ref([]), show = ref(false), editing = ref(null), form = ref({})
const resourceURL = path => `/api/resources/${path}`
async function load() {
  const [envRows, userRows] = await Promise.all([fetch(resourceURL('environments')).then(r => r.json()), fetch('/api/users').then(r => r.ok ? r.json() : [])])
  environments.value = envRows ?? []
  users.value = (userRows ?? []).filter(user => user.enabled)
}
function open(env) {
  editing.value = env || null
	form.value = env ? { ...env, approver_list: String(env.approvers || '').split(',').map(v => v.trim()).filter(Boolean) } : { name: '', display_name: '', protected: false, require_reason: false, approvers: '', approver_list: [], sort_order: 50 }
  show.value = true
}
async function save() {
  if (!form.value.name || !form.value.display_name) return toast('环境标识和名称必填')
	form.value.approvers = (form.value.approver_list || []).join(',')
  if (form.value.protected && !form.value.approvers) return toast('受保护环境必须配置至少一名审批人', 'warning')
	const { approver_list, ...payload } = form.value
	const res = await fetch(resourceURL('environments'), { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(payload) })
  const body = await res.json()
  if (!res.ok) return toast(body.error || '保存失败', 'error')
  show.value = false; toast('环境已保存', 'success'); load()
}
async function remove(env) {
  if (!confirm(`删除环境「${env.display_name}」？`)) return
  const res = await fetch(resourceURL(`environments/${env.name}`), { method: 'DELETE' })
  const body = await res.json()
  if (!res.ok) return toast(body.error || '删除失败', 'error')
  load()
}
onMounted(load)
</script>

<template>
  <div>
<PageHeader title="环境管理" :heading-tag="embedded ? 'h3' : 'h2'" help="环境是运行资源和发布策略的隔离边界；同一个应用可以配置多个环境，无需复制应用。"><template #actions><ElButton type="primary" native-type="button" size="small" @click="open()">＋ 新增环境</ElButton></template></PageHeader>
    <div class="scope-note">主机、主机组和应用部署配置只能属于同一环境；受保护环境必须审批后才能发布。</div>
    <table>
      <thead><tr><th>环境</th><th>标识</th><th>保护策略</th><th>审批人</th><th>顺序</th><th>操作</th></tr></thead>
      <tbody><tr v-for="env in environments" :key="env.name">
        <td><b>{{ env.display_name }}</b></td><td><code>{{ env.name }}</code></td>
        <td><span :class="env.protected ? 'st-run' : 'st-ok'">{{ env.protected ? '🔒 受保护' : '普通' }}</span><span v-if="env.require_reason" style="color:var(--dim)"> · 必填原因</span></td><td :class="env.protected && !env.approvers ? 'st-fail' : ''">{{env.approvers || (env.protected ? '未配置（禁止发布）' : '无需审批')}}</td>
        <td>{{ env.sort_order }}</td>
        <td><button class="op op-btn" @click="open(env)">编辑</button><button class="op op-btn del" :disabled="environments.length <= 1" :title="environments.length <= 1 ? '至少保留一个环境' : '删除环境'" @click="remove(env)">删除</button></td>
      </tr></tbody>
    </table>
    <Modal :show="show" :title="editing ? '编辑环境' : '新增环境'" @close="show = false">
      <div class="fld"><label>环境标识</label><ElInput v-model="form.name" :disabled="!!editing" placeholder="如 uat" /></div>
      <div class="fld"><label>显示名称</label><ElInput v-model="form.display_name" placeholder="如 用户验收" /></div>
      <div class="fld"><label>排序</label><ElInputNumber v-model="form.sort_order" :min="0" :max="999" /></div>
      <label style="display:block;margin:10px 0"><input v-model="form.protected" type="checkbox" /> 受保护环境，需要审批后发布</label>
      <label style="display:block;margin:10px 0"><input v-model="form.require_reason" type="checkbox" /> 发布时必须填写原因</label>
			<div class="fld"><label>发布 / 操作审核人员</label><Sel v-model="form.approver_list" :options="users.map(user=>({value:user.username,label:user.display_name+' · '+user.username}))" multiple filterable width="100%" placeholder="选择审核人员" /><small style="color:var(--dim)">从平台成员中选择；发起人不能审核自己的执行单。普通环境的服务发布仍无需审核，但 SQL 写操作和 Redis 操作始终需要另一名审核人。</small></div>
      <div style="display:flex"><ElButton type="primary" style="margin-left:auto" @click="save">保存</ElButton></div>
    </Modal>
  </div>
</template>
<style scoped>
.page-head{display:flex;align-items:center;margin-bottom:12px}.page-head h2{font-size:20px}.page-head>button{margin-left:auto}.title-row{display:flex;align-items:center;gap:7px}.scope-note{padding:9px 11px;margin-bottom:12px;border:1px solid var(--border);border-radius:7px;background:var(--panel);color:var(--muted);font-size:12px}
</style>
