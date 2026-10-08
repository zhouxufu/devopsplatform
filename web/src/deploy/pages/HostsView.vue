<script setup>
import { ref, onMounted } from 'vue'
import Modal from '../../shared/components/Modal.vue'
import Sel from '../../shared/components/Sel.vue'
import { ElButton, ElInput } from '../../shared/components/ui.js'
import { toast } from '../../shared/composables/toast.js'

const hosts = ref([]), environments = ref([]), testState = ref({})
const show = ref(false), editing = ref(null)
const form = ref({})

async function load() { [hosts.value, environments.value] = await Promise.all([fetch('api/hosts').then(r => r.json()), fetch('api/environments').then(r => r.json())]) }
onMounted(load)

function open(h) {
  editing.value = h || null
  form.value = h ? { ...h, password: '' } : { name: '', environment: environments.value[0]?.name || '', addr: '', user: 'root', key_path: '', password: '', host_key: '' }
  show.value = true
}
async function save() {
  if (!form.value.name || !form.value.environment || !form.value.addr) return toast('名称、环境和地址必填')
  const r = await fetch('api/hosts', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(form.value) })
  if (r.ok) { show.value = false; load() } else toast((await r.json().catch(()=>({}))).error || '保存失败', 'error')
}
async function del(h) {
  if (!confirm(`删除主机「${h.name}」？`)) return
  await fetch(`api/hosts/${h.name}`, { method: 'DELETE' }); load()
}
async function test(h) {
  testState.value[h.name] = 'testing'
  const r = await fetch(`api/hosts/${h.name}/test`, { method: 'POST' }).then(x => x.json())
  testState.value[h.name] = r.ok ? 'ok' : 'fail'
}
</script>
<template>
  <div>
    <div style="display:flex;margin-bottom:12px"><ElButton type="primary" size="small" style="margin-left:auto" @click="open()">＋ 新增主机</ElButton></div>
    <table>
      <thead><tr><th>名称</th><th>所属环境</th><th>地址</th><th>用户</th><th>凭据</th><th>连通性</th><th>操作</th></tr></thead>
      <tbody>
        <tr v-for="h in hosts" :key="h.name">
          <td>{{ h.name }}</td><td><b>{{environments.find(e=>e.name===h.environment)?.display_name||h.environment}}</b></td><td style="font-family:var(--mono)">{{ h.addr }}</td><td>{{ h.user }}</td>
          <td style="color:var(--dim)">{{ h.key_path ? '私钥' : h.credential_configured ? '密码（已保存）' : '默认私钥' }}</td>
          <td>
            <span v-if="testState[h.name] === 'ok'" class="st-ok">✓ 可达</span>
            <span v-else-if="testState[h.name] === 'fail'" class="st-fail">✗ 不通</span>
            <span v-else-if="testState[h.name] === 'testing'" class="st-run">测试中…</span>
            <span v-else style="color:var(--dim)">未测</span>
          </td>
          <td>
            <button class="op op-btn" @click="test(h)">测试</button>
            <button class="op op-btn" @click="open(h)">编辑</button>
            <button class="op op-btn del" @click="del(h)">删除</button>
          </td>
        </tr>
      </tbody>
    </table>
    <div v-if="!hosts.length" class="empty">暂无主机</div>

    <Modal :show="show" :title="editing ? '编辑主机' : '新增主机'" @close="show = false">
      <div class="fld"><label>名称</label><ElInput v-model="form.name" :disabled="!!editing" /></div>
      <div class="fld"><label>所属环境</label><Sel v-model="form.environment" :options="environments.map(e=>({value:e.name,label:(e.protected?'🔒 ':'')+e.display_name}))" width="100%" /><small style="color:var(--muted)">未被引用时可调整；加入主机组或应用环境后由服务端锁定。</small></div>
      <div class="fld"><label>地址（host[:port]）</label><ElInput v-model="form.addr" /></div>
      <div class="fld"><label>用户</label><ElInput v-model="form.user" /></div>
      <div class="fld"><label>私钥路径（留空用 ~/.ssh/id_rsa）</label><ElInput v-model="form.key_path" /></div>
      <div class="fld"><label>或密码</label><ElInput v-model="form.password" type="password" show-password /></div>
      <div class="fld"><label>SSH 主机指纹（推荐，SHA256:…）</label><ElInput v-model="form.host_key" placeholder="留空则校验 ~/.ssh/known_hosts" /></div>
      <div style="display:flex"><ElButton type="primary" style="margin-left:auto" @click="save">保存</ElButton></div>
    </Modal>
  </div>
</template>
