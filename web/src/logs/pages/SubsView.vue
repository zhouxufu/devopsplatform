<script setup>
import PageHeader from "../../shared/components/PageHeader.vue"
import { ref, computed, onMounted, onUnmounted, inject, watch } from 'vue'
import Sel from '../../shared/components/Sel.vue'
import { ElButton, ElInput, ElInputNumber } from '../../shared/components/ui.js'
import Modal from '../../shared/components/Modal.vue'
import { toast } from '../../shared/composables/toast.js'
import { serviceNames } from '../services.js'

const rules = ref([]), st = ref({}), notifs = ref([]), services = ref([]), environments = ref([])
const loading = ref(true)
const platformContext = inject('platformContext', ref(null))
const show = ref(false), editing = ref(null)
const form = ref({})
const supArr = ref([]) // 抑制依赖：选中的规则名数组（提交时拼回逗号串）
let timer = null
const environmentOptions = computed(() => [...new Set([platformContext.value?.environment, ...environments.value].filter(e => e && e !== '*'))])

async function load() {
  loading.value = true
  const environment = platformContext.value?.environment || ''
  ;[rules.value, st.value, notifs.value] = await Promise.all([
    fetch('api/alerts?' + new URLSearchParams({ environment })).then(r => r.json()).then(d => d ?? []),
    fetch('api/alerts/status?' + new URLSearchParams({ environment })).then(r => r.json()),
    fetch('api/alert-notifications?' + new URLSearchParams({ limit: 50, environment })).then(r => r.json()).then(d => d ?? []),
  ])
  loading.value = false
}
onMounted(async () => {
  load()
  services.value = serviceNames(await fetch('api/services').then(r => r.json()).catch(() => ({})))
  const [observed, configured] = await Promise.all([
    fetch('api/environments').then(r => r.json()).catch(() => []),
    fetch('/api/plugins/deploy/api/environments').then(r => r.json()).catch(() => []),
  ])
  environments.value = [...new Set([...(configured || []).map(e => e.name), ...(observed || [])].filter(Boolean))]
  timer = setInterval(load, 15000)
})
onUnmounted(() => clearInterval(timer))
watch(() => platformContext.value?.environment, load)

// 抑制候选严格限定在相同环境作用域，避免测试规则影响生产。
const supCandidates = computed(() => rules.value.filter(r => r.id !== editing.value?.id && r.environment === form.value.environment))

function open(r) {
  editing.value = r || null
	form.value = r ? { ...r } : { name: '', service: platformContext.value?.service || '', environment: platformContext.value?.environment || environments.value[0] || '*', level: 'ERROR', keyword: '', threshold: 10, duration_min: 3, webhook: '', silence_sec: 300 }
  supArr.value = r?.suppress_by ? r.suppress_by.split(',').map(s => s.trim()).filter(Boolean) : []
  show.value = true
}
async function save() {
  if (!form.value.name || !form.value.webhook || !form.value.service || !form.value.environment) return toast('名称/服务/环境/Webhook 必填')
  const body = { ...form.value, suppress_by: supArr.value.join(',') }
  const url = editing.value ? `api/alerts/${editing.value.id}` : 'api/alerts'
  const r = await fetch(url, { method: editing.value ? 'PUT' : 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
  if (r.ok) { show.value = false; toast('已保存'); load() } else toast('保存失败')
}
async function toggle(r) {
  await fetch(`api/alerts/${r.id}`, { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ ...r, enabled: !r.enabled }) })
  load()
}
async function test(id) { const r = await fetch(`api/alerts/${id}/test`, { method: 'POST' }); toast(r.ok ? '已发送' : '失败') }
async function silence(r, ms) {
  await fetch(`api/alerts/${r.id}`, { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ ...r, silence_until: ms > 0 ? Date.now() + ms : 0 }) })
  toast(ms > 0 ? '已静默 1h' : '已解除'); load()
}
const KC = { 触发: 'var(--error)', 持续中: 'var(--warn)', 已恢复: 'var(--ok)', 汇总: 'var(--info)', 测试: 'var(--info)', 被抑制: 'var(--dim)', 静默: 'var(--dim)', 限流: 'var(--dim)' }
</script>
<template>
  <div style="padding:16px">
<PageHeader title="日志订阅"><template #actions><ElButton type="primary" size="small" @click="open()">＋ 新建订阅报警</ElButton></template></PageHeader>
    <div class="table-scroll"><table>
      <thead><tr><th>名称</th><th>服务</th><th>条件</th><th>状态</th><th>最近评估</th><th>已发送</th><th>操作</th></tr></thead>
      <tbody>
        <tr v-for="r in rules" :key="r.id">
          <td>{{ r.name }}</td>
          <td>{{ r.service }}<div style="color:var(--brand);font-size:11px">{{ r.environment === '*' ? '全部环境' : r.environment }}</div></td>
          <td><code style="color:var(--warn)">{{ r.level }}</code> 连续 {{ r.duration_min }} 分钟 ≥ {{ r.threshold }}/分<code v-if="r.keyword" style="color:var(--info);margin-left:4px">{{ r.keyword }}</code></td>
          <td>
            <span v-if="!r.enabled" style="color:var(--dim)">已停用</span>
            <span v-else-if="st[r.id]?.firing" class="st-fail">● 报警中</span>
            <span v-else class="st-ok">● 正常</span>
            <div v-if="r.enabled && st[r.id]?.silenced" style="color:var(--dim);font-size:11px">🔕 静默至 {{ st[r.id].silence_until }}</div>
            <div v-else-if="r.enabled && st[r.id]?.suppressed_by" style="color:var(--dim);font-size:11px">⏸ 被「{{ st[r.id].suppressed_by }}」抑制</div>
          </td>
          <td style="color:var(--dim)">{{ st[r.id]?.last_eval || '—' }}</td>
          <td>{{ st[r.id]?.sent || 0 }}</td>
          <td>
            <button class="op op-btn" @click="test(r.id)">测试</button>
            <button class="op op-btn" @click="open(r)">编辑</button>
            <button class="op op-btn" style="color:var(--warn)" @click="silence(r, st[r.id]?.silenced ? 0 : 3600000)">{{ st[r.id]?.silenced ? '解除静默' : '静默1h' }}</button>
            <button class="op op-btn del" @click="toggle(r)">{{ r.enabled ? '停用' : '启用' }}</button>
          </td>
        </tr>
      </tbody>
    </table></div>
    <div v-if="loading" class="empty">加载中…</div><div v-else-if="!rules.length" class="empty">暂无订阅规则</div>

    <h3 style="font-size:14px;margin:20px 0 10px">通知历史（7 天，含被抑制/限流记录）</h3>
    <div class="table-scroll"><table>
      <thead><tr><th>时间</th><th>规则</th><th>类型</th><th>详情</th></tr></thead>
      <tbody>
        <tr v-for="(n, i) in notifs" :key="i">
          <td style="color:var(--dim);font-family:var(--mono)">{{ new Date(n.at * 1000).toLocaleString('zh-CN', { hour12: false }) }}</td>
          <td>{{ n.rule }}</td>
          <td :style="{ color: KC[n.kind] || 'var(--muted)' }">{{ n.kind }}</td>
          <td style="color:var(--dim)">{{ n.detail }}</td>
        </tr>
      </tbody>
    </table></div>
    <div v-if="!loading && !notifs.length" class="empty" style="margin-top:8px">暂无通知记录</div>

    <Modal :show="show" :title="editing ? '编辑订阅报警' : '新建订阅报警'" @close="show = false">
      <div class="fld"><label>名称</label><ElInput v-model="form.name" /></div>
      <div class="fld"><label>目标服务</label><Sel v-model="form.service" :options="services" placeholder="请选择服务" width="100%" /></div>
      <div class="fld"><label>环境</label><Sel v-model="form.environment" :options="[{ value: '*', label: '全部环境（显式）' }, ...environmentOptions.map(e => ({ value: e, label: e }))]" placeholder="选择环境" width="100%" /></div>
      <div class="fld"><label>条件（持续满足防毛刺）</label>
        <div style="display:flex;gap:6px;align-items:center">
          <Sel v-model="form.level" :options="['ERROR','WARN','INFO']" width="110px" />
          <span style="color:var(--dim)">连续</span><ElInputNumber v-model="form.duration_min" :min="1" size="small" controls-position="right" style="width:80px" />
          <span style="color:var(--dim)">分钟，每分钟 ≥</span><ElInputNumber v-model="form.threshold" :min="0" size="small" controls-position="right" style="width:90px" /><span style="color:var(--dim)">条</span>
        </div>
      </div>
      <div class="fld"><label>关键字（可选，同搜索语法）</label><ElInput v-model="form.keyword" placeholder='如 timeout -heartbeat' /></div>
      <div class="fld"><label>依赖抑制（可选）：勾选的规则报警中时，本规则不通知</label>
        <div v-if="supCandidates.length" class="supbox">
          <label v-for="c in supCandidates" :key="c.id" class="supitem">
            <input type="checkbox" :value="c.name" v-model="supArr"> {{ c.name }}
          </label>
        </div>
        <div v-else style="color:var(--dim);font-size:12px">暂无其他规则可选（先创建上游规则）</div>
      </div>
      <div class="fld"><label>Webhook（钉钉/企微/通用）</label><ElInput v-model="form.webhook" placeholder="https://..." /></div>
      <div class="fld"><label>静默期（分钟，触发后此时间内不重复通知）</label><ElInputNumber :model-value="form.silence_sec / 60" :min="1" size="small" controls-position="right" style="width:90px" @update:model-value="v => form.silence_sec = (v || 0) * 60" /></div>
      <div style="display:flex"><ElButton type="primary" style="margin-left:auto" @click="save">保存</ElButton></div>
    </Modal>
  </div>
</template>
<style scoped>
.supbox{display:flex;flex-wrap:wrap;gap:6px;max-height:120px;overflow-y:auto;border:1px solid var(--border);border-radius:8px;padding:8px}
.supitem{display:flex;align-items:center;gap:4px;font-size:12px;color:var(--muted);padding:3px 8px;border-radius:6px;background:var(--panel2);cursor:pointer;white-space:nowrap}
.supitem:has(input:checked){color:var(--text);background:var(--brand);opacity:.9}
.supitem input{accent-color:var(--brand)}
</style>
