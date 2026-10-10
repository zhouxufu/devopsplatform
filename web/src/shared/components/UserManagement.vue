<script setup>
import { computed, ref, watch } from 'vue'
import Modal from './Modal.vue'
import Sel from './Sel.vue'
import { ElButton, ElInput } from './ui.js'

const props = defineProps({ show: Boolean, current: { type: Object, default: () => ({}) } })
const emit = defineEmits(['close'])
const users = ref([]), editing = ref(null), editorOpen = ref(false), form = ref({}), error = ref('')
const links = ref({}), syncFilter=ref('')
const syncOpen = ref(false), connections = ref([]), connection = ref(''), directory = ref(null), selected = ref([]), syncing = ref(false), syncError = ref(''), syncResult = ref('')

const visibleCandidates=computed(()=>(directory.value?.users||[]).filter(u=>!syncFilter.value||[u.account,u.name,u.username].some(v=>String(v||'').toLowerCase().includes(syncFilter.value.toLowerCase()))))
function linkOptions(u){return users.value.filter(user=>!user.source&&!Object.entries(links.value).some(([id,username])=>id!==u.id&&username===user.username)).map(user=>({value:user.username,label:`${user.display_name}（${user.username}）${user.role==='admin'?' · 管理员':''}${user.enabled?'':' · 停用'}`}))}
function chooseLink(u,username){if(username){links.value[u.id]=username;if(!selected.value.includes(u.id))selected.value.push(u.id)}else{delete links.value[u.id];if(u.action==='skip')selected.value=selected.value.filter(id=>id!==u.id)}}
async function openSync() {
  if (syncing.value) return
  links.value={};syncFilter.value='';syncOpen.value = true; syncError.value = ''; syncResult.value = ''; directory.value = null; selected.value = []; connections.value=[];connection.value=''
  try { await load();connections.value = (await api('/api/resources/connections')).filter(c => c.kind === 'zentao'); connection.value = connections.value[0]?.name || '' }
  catch (e) { syncError.value = e.message }
}
function resetPreview() { links.value={};syncFilter.value='';directory.value = null; selected.value = []; syncResult.value = ''; syncError.value = '' }
async function syncUsers(apply = false) {
  if (syncing.value) return
  syncing.value = true; syncError.value = ''; syncResult.value = ''
  try {
    const data = await api(apply ? '/api/users/sync' : '/api/users/sync-preview', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ connection: connection.value, ids: apply ? selected.value : [], expected_source: apply ? directory.value?.source : '', links: apply ? Object.fromEntries(Object.entries(links.value).filter(([id])=>selected.value.includes(id))) : {} }) })
    directory.value = data
    if (apply) {
      const rows = data.users.filter(u => selected.value.includes(u.id))
      syncResult.value = `同步完成：新增 ${rows.filter(u => u.action === 'create').length}，更新 ${rows.filter(u => u.action === 'update').length}，关联 ${rows.filter(u => u.action === 'link').length}，跳过 ${rows.filter(u => u.action === 'skip').length}。新成员请编辑并设置平台密码后启用。`
      selected.value = []; links.value={};await load()
    } else { links.value={};selected.value = data.users.filter(u => u.action !== 'skip').map(u => u.id) }
  } catch (e) { syncError.value = e.message }
  finally { syncing.value = false }
}

async function api(url, options) {
  const response = await fetch(url, options)
  const body = await response.json().catch(() => ({}))
  if (!response.ok) throw new Error(body.error || '请求失败')
  return body
}
async function load() { users.value = await api('/api/users') }
function open(user) {
  editing.value = user || null
  form.value = user ? { ...user, password: '' } : { username: '', display_name: '', role: 'member', password: '', enabled: true }
  error.value = ''
  editorOpen.value = true
}
async function save() {
  error.value = ''
  try {
    const path = editing.value ? `/api/users/${encodeURIComponent(editing.value.username)}` : '/api/users'
    await api(path, { method: editing.value ? 'PUT' : 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(form.value) })
    editorOpen.value = false
    editing.value = null
    await load()
  } catch (e) { error.value = e.message }
}
async function remove(user) {
  if (!confirm(`删除用户「${user.display_name || user.username}」？`)) return
  try { await api(`/api/users/${encodeURIComponent(user.username)}`, { method: 'DELETE' }); await load() } catch (e) { alert(e.message) }
}
watch(() => props.show, value => {
  if (value) { editing.value = null; load().catch(e => { error.value = e.message }) }
  else { editorOpen.value = false; syncOpen.value = false; editing.value = null; form.value = {} }
})
</script>

<template>
  <Modal :show="show" title="成员管理" extra-wide @close="$emit('close')">
    <div class="head"><div><b>两个内置角色</b><p>管理员维护平台配置；成员负责日常构建、发布、日志和监控。</p></div><div class="head-actions"><ElButton size="small" @click="openSync">从禅道同步</ElButton><ElButton type="primary" size="small" @click="open(null)">＋ 添加成员</ElButton></div></div>
    <table><thead><tr><th>用户</th><th>角色</th><th>状态</th><th>操作</th></tr></thead><tbody>
      <tr v-for="user in users" :key="user.username"><td><b>{{user.display_name}}</b><small>{{user.username}} {{user.source ? '· 禅道同步' : ''}}</small></td><td>{{user.role==='admin'?'管理员':'成员'}}</td><td :class="user.enabled?'ok':'dim'">{{user.source && !user.password_configured ? '待设置密码' : user.enabled?'启用':'停用'}}</td><td><button class="op" @click="open(user)">编辑</button><button class="op danger" :disabled="user.username===current.username" @click="remove(user)">删除</button></td></tr>
    </tbody></table>
    <p v-if="!users.length" class="empty">暂无用户</p>
    <p v-if="error && !editorOpen" class="error">{{error}}</p>
  </Modal>
	<Modal :show="syncOpen" title="从禅道同步成员" extra-wide @close="syncOpen=false">
    <p class="sync-note">复用资源中心的禅道 16.0 连接。只同步账号与姓名，不同步禅道角色或密码；新成员默认停用，同名平台账号不自动覆盖，可在每行手动选择关联已有用户。关联只建立身份映射，本次不修改平台资料；后续常规同步仍仅更新姓名。每个账号只关联一个来源身份，已有关联不可在这里改绑。已关联成员仅更新姓名，保留平台权限和登录设置；不会因禅道删除用户而自动删除平台账号。</p>
    <div class="head"><Sel v-model="connection" :options="connections.map(c=>({value:c.name,label:c.name}))" width="100%" :disabled="syncing" @update:model-value="resetPreview" /><ElButton :disabled="!connection || syncing" :loading="syncing" @click="syncUsers(false)">读取用户</ElButton></div>
    <p v-if="!connections.length" class="empty">尚未配置禅道连接，请先到资源中心 → 连接添加。</p>
    <template v-if="directory"><ElInput v-model="syncFilter" placeholder="筛选来源账号、姓名或关联用户" clearable aria-label="筛选同步用户"/><div class="directory"><table><thead><tr><th>选择</th><th>来源账号</th><th>姓名</th><th>关联平台用户</th><th>处理方式</th></tr></thead><tbody><tr v-for="u in visibleCandidates" :key="u.id"><td><input v-model="selected" :value="u.id" type="checkbox" :aria-label="'选择 '+u.account" :disabled="syncing || (u.action==='skip'&&!links[u.id])" /></td><td>{{u.account}}</td><td>{{u.name || u.account}}</td><td><template v-if="u.bound"><b>{{u.username}}</b><small>已关联，不可在同步中改绑</small></template><Sel v-else :model-value="links[u.id]||''" :options="linkOptions(u)" all-label="按原规则（不关联）" :aria-label="'关联平台用户 '+u.account" filterable width="240px" :disabled="syncing" @change="chooseLink(u,$event)"/></td><td>{{links[u.id]||u.action==='link'?'关联已有用户':u.action==='create'?'新增成员':u.action==='update'?'更新姓名':'跳过'}}<small>{{links[u.id]?'仅建立关联，本次保留平台资料和登录设置':u.reason}}</small></td></tr></tbody></table><p v-if="!visibleCandidates.length" class="empty">{{directory.users.length?'没有匹配的用户':'来源平台没有可同步的内部用户'}}</p></div></template>
    <p v-if="syncError" class="error">{{syncError}}</p><p v-if="syncResult" class="ok">{{syncResult}}</p>
    <div class="actions"><ElButton @click="syncOpen=false">关闭</ElButton><ElButton type="primary" :disabled="syncing || !selected.length" :loading="syncing" @click="syncUsers(true)">同步所选（{{selected.length}}）</ElButton></div>
  </Modal>
	<Modal :show="editorOpen" :title="editing?'编辑成员':'添加成员'" @close="editorOpen=false;form={};editing=null">
    <div class="fld"><label>用户名</label><ElInput v-model="form.username" :disabled="!!editing" placeholder="如 zhangsan" /></div>
    <div class="fld"><label>姓名</label><ElInput v-model="form.display_name" placeholder="如 张三" /></div>
		<div class="fld"><label>角色</label><Sel v-model="form.role" :disabled="editing?.username===current.username" :options="[{value:'member',label:'成员'},{value:'admin',label:'管理员'}]" width="100%" /></div>
    <div class="fld"><label>{{editing?.source && !editing.password_configured ? '设置平台登录密码（启用前必填）' : editing?'新密码（留空不修改）':'初始密码'}}</label><ElInput v-model="form.password" type="password" show-password placeholder="至少 8 位" /></div>
		<label class="enabled"><input v-model="form.enabled" :disabled="editing?.username===current.username" type="checkbox" /> 启用账号</label>
    <p v-if="error" class="error">{{error}}</p>
		<div class="actions"><ElButton @click="editorOpen=false;form={};editing=null">取消</ElButton><ElButton type="primary" @click="save">保存</ElButton></div>
  </Modal>
</template>

<style scoped>
.head-actions{margin-left:auto;display:flex;gap:8px;flex-shrink:0}.sync-note{color:var(--muted);line-height:1.7;font-size:12px;margin-top:0}.directory{max-height:45vh;overflow:auto;margin:8px 0 16px}.directory table{min-width:720px}.directory td{vertical-align:top}.directory th{white-space:nowrap}.directory th{position:sticky;top:0;background:var(--surface)}
.head{display:flex;align-items:center;margin-bottom:14px}.head p{margin:4px 0 0;color:var(--muted);font-size:12px}.head>button{margin-left:auto}table{width:100%;border-collapse:collapse}th,td{padding:10px;border-bottom:1px solid var(--border);text-align:left;font-size:12px}td small{display:block;color:var(--dim);margin-top:3px}.ok{color:var(--ok)}.dim{color:var(--dim)}.op{border:0;background:transparent;color:var(--brand);cursor:pointer}.danger{color:var(--error)}.op:disabled{opacity:.35;cursor:not-allowed}.fld{margin-bottom:13px}.fld label{display:block;margin-bottom:5px;color:var(--muted);font-size:12px}.enabled{display:block;margin:4px 0 14px}.actions{display:flex;justify-content:flex-end;gap:8px}.error{color:var(--error);font-size:12px}.empty{text-align:center;color:var(--dim);padding:30px}
</style>
