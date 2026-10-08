<script setup>
import { ref, inject, onMounted, watch, computed } from 'vue'
import Sel from '../../shared/components/Sel.vue'
import { ElButton, ElInput } from '../../shared/components/ui.js'
import Modal from '../../shared/components/Modal.vue'
import { toast } from '../../shared/composables/toast.js'

const rules = ref([]), services = ref([])
const curEnvironment = inject('curEnvironment', ref(''))
const environments = inject('environments', ref([]))
const show = ref(false), editing = ref(null)
const form = ref({})
const environmentOptions = computed(() => [...new Set([curEnvironment.value, ...environments.value].filter(e => e && e !== '*'))])

async function load() {
  const q = new URLSearchParams({ environment: curEnvironment.value || '' })
  ;[rules.value, services.value] = await Promise.all([
    fetch('api/avail-rules?' + q).then(r => r.json()).then(d => d ?? []),
    Promise.all([fetch('api/services').then(r => r.json()), fetch('api/otlp/services').then(r => r.json())])
      .then(([a, b]) => [...new Set([...(a ?? []), ...(b ?? [])])]),
  ])
}
onMounted(load)
watch(curEnvironment, load)

function open(r) {
  editing.value = r || null
  form.value = r ? { ...r } : { service: '', environment: curEnvironment.value || environments.value[0] || '*', name: '', ok_codes: [], ignore_codes: [] }
  show.value = true
}
async function save() {
  if (!form.value.service || !form.value.environment) return toast('请选择服务和环境')
  const r = await fetch('api/avail-rule', { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(form.value) })
  if (r.ok) { show.value = false; load(); toast('已保存') } else toast('保存失败')
}
async function del(r) {
  if (!confirm(`删除规则 ${r.service}[${r.environment === '*' ? '全部环境' : r.environment}]${r.name ? ' · ' + r.name : ''}？`)) return
  await fetch('api/avail-rule?' + new URLSearchParams({ service: r.service, environment: r.environment, name: r.name }), { method: 'DELETE' })
  load()
}
</script>
<template>
  <div style="padding:16px">
    <div style="display:flex;align-items:center;margin-bottom:12px"><h3 style="font-size:14px">可用率规则</h3><ElButton type="primary" size="small" style="margin-left:auto" @click="open()">＋ 新建规则</ElButton></div>
    <div style="color:var(--dim);font-size:11px;margin-bottom:10px">规则默认按当前环境隔离；只有显式选择“全部环境”才作为跨环境兜底。优先级：环境指标 → 环境默认 → 全环境指标 → 全环境默认 → 内置。</div>
    <div class="table-scroll"><table>
      <thead><tr><th>服务</th><th>环境</th><th>指标</th><th>成功码</th><th>忽略码</th><th>操作</th></tr></thead>
      <tbody>
        <tr v-for="r in rules" :key="r.service + ':' + r.environment + ':' + r.name">
          <td>{{ r.service }}</td>
          <td><code style="color:var(--brand)">{{ r.environment === '*' ? '全部环境' : r.environment }}</code></td>
          <td :style="{ color: r.name ? 'var(--text)' : 'var(--dim)' }">{{ r.name || '（服务默认）' }}</td>
          <td style="color:var(--ok)">{{ (r.ok_codes || []).join(', ') || '内置' }}</td>
          <td style="color:var(--warn)">{{ (r.ignore_codes || []).join(', ') || '—' }}</td>
          <td><button class="op op-btn" @click="open(r)">编辑</button><button class="op op-btn del" @click="del(r)">删除</button></td>
        </tr>
      </tbody>
    </table></div>
    <div v-if="!rules.length" class="empty">暂无规则——所有指标走内置判定</div>

    <Modal :show="show" :title="editing ? '编辑规则' : '新建规则'" @close="show = false">
      <div class="fld"><label>服务</label><Sel v-model="form.service" :options="services" width="100%" /></div>
      <div class="fld"><label>环境</label><Sel v-model="form.environment" :options="[{ value: '*', label: '全部环境（显式兜底）' }, ...environmentOptions.map(e => ({ value: e, label: e }))]" placeholder="选择环境" width="100%" /></div>
      <div class="fld"><label>指标（留空 = 服务默认）</label><ElInput v-model="form.name" placeholder="如 rpc.server.duration" /></div>
      <div class="fld"><label>算成功的 code（逗号分隔，留空=内置）</label><ElInput :model-value="(form.ok_codes || []).join(', ')" @update:model-value="v => form.ok_codes = v.split(',').map(x => x.trim()).filter(Boolean)" /></div>
      <div class="fld"><label>忽略的 code（不计入可用率）</label><ElInput :model-value="(form.ignore_codes || []).join(', ')" @update:model-value="v => form.ignore_codes = v.split(',').map(x => x.trim()).filter(Boolean)" /></div>
      <div style="display:flex"><ElButton type="primary" style="margin-left:auto" @click="save">保存</ElButton></div>
    </Modal>
  </div>
</template>
