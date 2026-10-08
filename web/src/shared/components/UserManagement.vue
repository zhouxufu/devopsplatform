<script setup>
import { ref, watch } from 'vue'
import Modal from './Modal.vue'
import Sel from './Sel.vue'
import { ElButton, ElInput } from './ui.js'

const props = defineProps({ show: Boolean, current: { type: Object, default: () => ({}) } })
const emit = defineEmits(['close'])
const users = ref([]), editing = ref(null), editorOpen = ref(false), form = ref({}), error = ref('')

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
  else { editorOpen.value = false; editing.value = null; form.value = {} }
})
</script>

<template>
  <Modal :show="show" title="成员管理" extra-wide @close="$emit('close')">
    <div class="head"><div><b>两个内置角色</b><p>管理员维护平台配置；成员负责日常构建、发布、日志和监控。</p></div><ElButton type="primary" size="small" @click="open(null)">＋ 添加成员</ElButton></div>
    <table><thead><tr><th>用户</th><th>角色</th><th>状态</th><th>操作</th></tr></thead><tbody>
      <tr v-for="user in users" :key="user.username"><td><b>{{user.display_name}}</b><small>{{user.username}}</small></td><td>{{user.role==='admin'?'管理员':'成员'}}</td><td :class="user.enabled?'ok':'dim'">{{user.enabled?'启用':'停用'}}</td><td><button class="op" @click="open(user)">编辑</button><button class="op danger" :disabled="user.username===current.username" @click="remove(user)">删除</button></td></tr>
    </tbody></table>
    <p v-if="!users.length" class="empty">暂无用户</p>
  </Modal>
	<Modal :show="editorOpen" :title="editing?'编辑成员':'添加成员'" @close="editorOpen=false;form={};editing=null">
    <div class="fld"><label>用户名</label><ElInput v-model="form.username" :disabled="!!editing" placeholder="如 zhangsan" /></div>
    <div class="fld"><label>姓名</label><ElInput v-model="form.display_name" placeholder="如 张三" /></div>
		<div class="fld"><label>角色</label><Sel v-model="form.role" :disabled="editing?.username===current.username" :options="[{value:'member',label:'成员'},{value:'admin',label:'管理员'}]" width="100%" /></div>
    <div class="fld"><label>{{editing?'新密码（留空不修改）':'初始密码'}}</label><ElInput v-model="form.password" type="password" show-password placeholder="至少 8 位" /></div>
		<label class="enabled"><input v-model="form.enabled" :disabled="editing?.username===current.username" type="checkbox" /> 启用账号</label>
    <p v-if="error" class="error">{{error}}</p>
		<div class="actions"><ElButton @click="editorOpen=false;form={};editing=null">取消</ElButton><ElButton type="primary" @click="save">保存</ElButton></div>
  </Modal>
</template>

<style scoped>
.head{display:flex;align-items:center;margin-bottom:14px}.head p{margin:4px 0 0;color:var(--muted);font-size:12px}.head>button{margin-left:auto}table{width:100%;border-collapse:collapse}th,td{padding:10px;border-bottom:1px solid var(--border);text-align:left;font-size:12px}td small{display:block;color:var(--dim);margin-top:3px}.ok{color:var(--ok)}.dim{color:var(--dim)}.op{border:0;background:transparent;color:var(--brand);cursor:pointer}.danger{color:var(--error)}.op:disabled{opacity:.35;cursor:not-allowed}.fld{margin-bottom:13px}.fld label{display:block;margin-bottom:5px;color:var(--muted);font-size:12px}.enabled{display:block;margin:4px 0 14px}.actions{display:flex;justify-content:flex-end;gap:8px}.error{color:var(--error);font-size:12px}.empty{text-align:center;color:var(--dim);padding:30px}
</style>
