<script setup>
import PageHeader from "../../shared/components/PageHeader.vue"
import { ref, computed, inject, watch, onMounted, onUnmounted } from 'vue'
import Sel from '../../shared/components/Sel.vue'
import { ElButton, ElInput, ElInputNumber } from '../../shared/components/ui.js'
import Modal from '../../shared/components/Modal.vue'
import { toast } from '../../shared/composables/toast.js'

const rules = ref([]), st = ref({}), notifs = ref([])
const loading = ref(true)
const curEnvironment = inject('curEnvironment', ref(''))
const environments = inject('environments', ref([]))
const services = ref([]), instances = ref([]), metrics = ref([]), ifaces = ref([])
const show = ref(false), editing = ref(null)
const form = ref({})
const supArr = ref([])
let timer = null

const TYPES = [
  { v: 'error_rate', label: '错误率' },
  { v: 'latency', label: '响应时间' },
  { v: 'traffic_up', label: '调用量激增' },
  { v: 'traffic_down', label: '调用量骤降' },
]
const environmentOptions = computed(() => [...new Set([curEnvironment.value, ...environments.value].filter(e => e && e !== '*'))])

async function load() {
  loading.value = true
  const q = new URLSearchParams({ environment: curEnvironment.value || '' })
  ;[rules.value, st.value, notifs.value] = await Promise.all([
    fetch('api/alerts?' + q).then(r => r.json()).then(d => d ?? []),
    fetch('api/alerts/status?' + q).then(r => r.json()),
    fetch('api/alert-notifications?' + new URLSearchParams({ limit: 50, environment: curEnvironment.value || '' })).then(r => r.json()).then(d => d ?? []),
  ])
  loading.value = false
}
onMounted(async () => {
  load()
  services.value = (await fetch('api/otlp/services').then(r => r.json()).catch(() => [])) ?? []
  timer = setInterval(load, 15000)
})
onUnmounted(() => clearInterval(timer))
watch(curEnvironment, load)

const supCandidates = computed(() => rules.value.filter(r => r.id !== editing.value?.id && (r.environment || '') === (form.value.environment || '') && (r.instance || '') === (form.value.instance || '')))

watch([() => form.value.service, () => form.value.environment], async ([svc]) => {
  metrics.value = []
  ifaces.value = []
  if (!svc) return
  const environment = form.value.environment === '*' ? '' : (form.value.environment || '')
  instances.value = (await fetch('api/instances?' + new URLSearchParams({ service: svc, environment })).then(r => r.json()).catch(() => [])) ?? []
  if (form.value.instance && !instances.value.includes(form.value.instance)) form.value.instance = ''
  metrics.value = (await fetch('api/otlp/names?' + new URLSearchParams({ service: svc, environment, instance: form.value.instance || '' })).then(r => r.json()).catch(() => [])) ?? []
})
watch([() => form.value.metric, () => form.value.instance], async ([m]) => {
  ifaces.value = []
  if (!m || !form.value.service) return
  const environment = form.value.environment === '*' ? '' : (form.value.environment || '')
  ifaces.value = (await fetch('api/otlp/interfaces?' + new URLSearchParams({ service: form.value.service, environment, instance: form.value.instance || '', name: m })).then(r => r.json()).catch(() => [])) ?? []
})

function open(r) {
  editing.value = r || null
  form.value = r ? { ...r } : { name: '', service: '', environment: curEnvironment.value || environments.value[0] || '*', instance: '', metric: '', iface: '', type: 'error_rate', quantile: 0.99, window_min: 5, threshold: 1, min_sample: 10, webhook: '', silence_sec: 300 }
  supArr.value = r?.suppress_by ? r.suppress_by.split(',').map(s => s.trim()).filter(Boolean) : []
  if (r) { // 编辑时回填级联下拉
    metrics.value = [{ iface: '' }].slice(0, 0)
    const environment = r.environment === '*' ? '' : (r.environment || '')
    Promise.all([
      fetch('api/instances?' + new URLSearchParams({ service: r.service, environment })).then(x => x.json()).catch(() => []),
      fetch('api/otlp/names?' + new URLSearchParams({ service: r.service, environment, instance: r.instance || '' })).then(x => x.json()).catch(() => []),
      fetch('api/otlp/interfaces?' + new URLSearchParams({ service: r.service, environment, instance: r.instance || '', name: r.metric })).then(x => x.json()).catch(() => []),
    ]).then(([ins, ns, ifs]) => { instances.value = ins ?? []; metrics.value = ns ?? []; ifaces.value = ifs ?? [] })
  }
  show.value = true
}
async function save() {
  if (!form.value.name || !form.value.webhook || !form.value.service || !form.value.environment || !form.value.metric) return toast('名称/服务/环境/指标/Webhook 必填')
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

const typeLabel = t => TYPES.find(x => x.v === t)?.label || t
const qName = q => q === 0.5 ? 'TP50' : q === 0.95 ? 'TP95' : 'TP99'
function condText(r) {
  switch (r.type) {
    case 'error_rate': return `${r.window_min}分钟错误率 ≥ ${r.threshold}%（样本≥${r.min_sample}）`
    case 'latency': return `${r.window_min}分钟 ${qName(r.quantile)} ≥ ${r.threshold}ms`
    case 'traffic_up': return `环比前${r.window_min}分钟激增 ≥ ${r.threshold}%（基线≥${r.min_sample}）`
    case 'traffic_down': return `环比前${r.window_min}分钟骤降 ≥ ${r.threshold}%（基线≥${r.min_sample}）`
  }
}
const KC = { 触发: 'var(--error)', 持续中: 'var(--warn)', 已恢复: 'var(--ok)', 汇总: 'var(--info)', 测试: 'var(--info)', 被抑制: 'var(--dim)', 静默: 'var(--dim)', 限流: 'var(--dim)' }
</script>
<template>
  <div style="padding:16px">
<PageHeader title="指标报警"><template #actions><ElButton type="primary" size="small" @click="open()">＋ 新建指标报警</ElButton></template></PageHeader>
    <div class="table-scroll"><table>
      <thead><tr><th>名称</th><th>对象</th><th>条件</th><th>状态</th><th>最近评估</th><th>已发送</th><th>操作</th></tr></thead>
      <tbody>
        <tr v-for="r in rules" :key="r.id">
          <td>{{ r.name }}</td>
          <td><code style="color:var(--info)">{{ r.service }}</code> <code style="color:var(--brand)">[{{ r.environment === '*' ? '全部环境' : r.environment }}/{{ r.instance || '全部实例' }}]</code><div style="color:var(--dim);font-size:11px">{{ r.metric }}{{ r.iface ? ' · ' + r.iface : ' · 全部接口' }}</div></td>
          <td><code style="color:var(--warn)">{{ typeLabel(r.type) }}</code> {{ condText(r) }}
            <div v-if="st[r.id]?.recent" style="color:var(--dim);font-size:11px">{{ Object.entries(st[r.id].recent).map(([k, v]) => k + '=' + v).join(' ') }}</div>
          </td>
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
    <div v-if="loading" class="empty">加载中…</div><div v-else-if="!rules.length" class="empty">暂无指标报警规则（错误率 / 响应时间 / 调用量突变）</div>

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

    <Modal :show="show" :title="editing ? '编辑指标报警' : '新建指标报警'" @close="show = false">
      <div class="fld"><label>名称</label><ElInput v-model="form.name" /></div>
      <div class="fld"><label>监控对象</label>
        <div style="display:flex;gap:6px">
          <Sel v-model="form.service" :options="services" placeholder="选服务" filterable style="flex:1" width="auto" />
          <Sel v-model="form.environment" :options="[{ value: '*', label: '全部环境（显式）' }, ...environmentOptions.map(e => ({ value: e, label: e }))]" placeholder="选择环境" style="flex:.9" width="auto" />
          <Sel v-model="form.instance" :options="instances" all-label="全部实例" style="flex:1" width="auto" />
          <Sel v-model="form.metric" :options="metrics" placeholder="选指标" filterable style="flex:1.4" width="auto" />
        </div>
      </div>
      <div class="fld"><label>接口（可选）</label><Sel v-model="form.iface" :options="ifaces.map(i => ({ value: i.iface, label: i.iface + '（' + Math.round(i.cnt) + ' 次）' }))" all-label="全部接口" filterable width="100%" /></div>
      <div class="fld"><label>报警类型</label>
        <Sel v-model="form.type" :options="TYPES.map(t => ({ value: t.v, label: t.label }))" width="130px" />
      </div>

      <div class="fld" v-if="form.type === 'error_rate'"><label>条件（持续满足防毛刺）</label>
        <div style="display:flex;gap:6px;align-items:center;flex-wrap:wrap">
          <span style="color:var(--dim)">窗口</span><ElInputNumber v-model="form.window_min" :min="1" size="small" controls-position="right" style="width:80px" /><span style="color:var(--dim)">分钟，错误率 ≥</span>
          <ElInputNumber v-model="form.threshold" :min="0" size="small" controls-position="right" style="width:90px" /><span style="color:var(--dim)">%，样本 ≥</span>
          <ElInputNumber v-model="form.min_sample" :min="0" size="small" controls-position="right" style="width:80px" />
        </div>
      </div>
      <div class="fld" v-else-if="form.type === 'latency'"><label>条件（持续满足防毛刺）</label>
        <div style="display:flex;gap:6px;align-items:center;flex-wrap:wrap">
          <Sel v-model="form.quantile" :options="[{ value: 0.5, label: 'TP50' }, { value: 0.95, label: 'TP95' }, { value: 0.99, label: 'TP99' }]" width="100px" />
          <span style="color:var(--dim)">≥</span><ElInputNumber v-model="form.threshold" :min="0" size="small" controls-position="right" style="width:95px" /><span style="color:var(--dim)">ms，窗口</span>
          <ElInputNumber v-model="form.window_min" :min="1" size="small" controls-position="right" style="width:80px" /><span style="color:var(--dim)">分钟</span>
        </div>
      </div>
      <div class="fld" v-else><label>条件（环比前一个等长窗口）</label>
        <div style="display:flex;gap:6px;align-items:center;flex-wrap:wrap">
          <span style="color:var(--dim)">窗口</span><ElInputNumber v-model="form.window_min" :min="1" size="small" controls-position="right" style="width:80px" />
          <span style="color:var(--dim)">分钟，{{ form.type === 'traffic_up' ? '激增' : '骤降' }} ≥</span>
          <ElInputNumber v-model="form.threshold" :min="0" size="small" controls-position="right" style="width:90px" /><span style="color:var(--dim)">%，基线样本 ≥</span>
          <ElInputNumber v-model="form.min_sample" :min="0" size="small" controls-position="right" style="width:80px" />
        </div>
      </div>

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
