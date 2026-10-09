<script setup>
import { computed, inject, onMounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import Modal from "../../shared/components/Modal.vue";
import Sel from "../../shared/components/Sel.vue";
import HelpTip from "../../shared/components/HelpTip.vue";
import SQLPlanPreview from '../components/SQLPlanPreview.vue';
import NacosPlanPreview from '../components/NacosPlanPreview.vue';
import RedisPlanPreview from '../components/RedisPlanPreview.vue';
import { ElButton, ElInput } from "../../shared/components/ui.js";
import { toast } from "../../shared/composables/toast.js";
const api = inject("api"),
  route = useRoute(),
  router = useRouter(),
  globalEnvironment = inject("globalEnvironment");
const templates = ref([]),
  runs = ref([]),
  services = ref([]),
  versions = ref([]),
  environments = ref([]),
  artifacts = ref([]),
  me = ref({});
const changes=ref([]);
const nacosExecutions=ref([]),redisExecutions=ref([]);
async function restoreNacos(changeID){
  if(busy.value||!selected.value)return;
  busy.value=true;
  try{const draft=await api(`api/delivery/runs/${selected.value.id}/nacos-restore-draft`,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({change_id:changeID})});changes.value=await api('api/changes');toast(`已创建恢复草稿 #${draft.id}；请核对、标记就绪并重新发起审核，不会立即执行`,'success')}catch(e){toast(e.message,'error')}finally{busy.value=false}
}
const area = ref("runs"),
  showTemplate = ref(false),
  showRun = ref(false),
  selected = ref(null),
  children = ref({}),
  busy = ref(false);
const template = ref({}),
  request = ref({}),
  preview = ref(null);
const showChooser=ref(false),chosenTemplate=ref('');
const availableTemplates=computed(()=>templates.value.filter(t=>!route.query.version||t.mode==='version'));
function startDelivery(){
  selected.value=null;
  const available=availableTemplates.value;
  if(available.length===1){openRun(available[0]);return}
  chosenTemplate.value='';showChooser.value=true;
}
function continueDelivery(){const t=availableTemplates.value.find(t=>t.id===chosenTemplate.value);if(!t)return toast('请选择编排模板','warning');showChooser.value=false;openRun(t)}
function createForDelivery(){showChooser.value=false;editTemplate();template.value.mode=route.query.version?'version':'fixed';creatingForDelivery.value=true}
const creatingForDelivery=ref(false);
const states = {
  pending_approval: "待审核",
  approved: "待执行",
  running: "执行中",
  success: "成功",
  failed: "失败",
  rejected: "已驳回",
  canceled: "已取消",
  pending: "待执行",
  waiting: "发布中",
  waiting_next_batch: "等待下一批",
  queued: "排队中",
  executing:'变更执行中 / 中断待核对',
};
const changeKinds = {
  code: "代码分支",
  artifact: "应用制品",
  manual: "人工操作",
  mysql: "SQL 变更",
  nacos: "Nacos 配置",
  "data-sync": "数据同步",
  redis: "Redis 命令",
};
const kinds = [
  { value: "deploy", label: "发布应用" },
  { value: "manual", label: "人工确认" },
  { value: "mysql", label: "SQL 变更" },
  { value: "nacos", label: "Nacos 配置" },
  { value: "data-sync", label: "数据同步（待接通）" },
  { value: "redis", label: "Redis 命令（强制审核）" },
];
const selectedTemplate = computed(() =>
  templates.value.find((t) => t.id === request.value.template_id),
);
const version = computed(() =>
  versions.value.find((v) => v.id === request.value.version_id),
);
const appNames = computed(() =>
  selectedTemplate.value?.mode === "fixed"
    ? selectedTemplate.value.services || []
    : [
        ...new Set(
          (version.value?.changes || [])
            .filter((c) => ["code", "artifact"].includes(c.kind))
            .map((c) => c.service)
            .filter(Boolean),
        ),
      ],
);
const activeStep = computed(() =>
  selected.value?.steps.find((s) => s.status !== "success"),
);
const canManage = (item) =>
  me.value.username === "local" ||
  me.value.role === "admin" ||
  me.value.username === item.owner;
const canReview = computed(
  () =>
    selected.value?.status === "pending_approval" &&
    selected.value.owner !== me.value.username &&
    String(selected.value.plan.environment.approvers || "")
      .split(",")
      .map((v) => v.trim())
      .includes(me.value.username),
);
const post = (path, body = {}) =>
  api(path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
async function load() {
  try {
    [
      templates.value,
      runs.value,
      services.value,
      versions.value,
      environments.value,
      artifacts.value,
      changes.value,
      nacosExecutions.value,redisExecutions.value,
    ] = await Promise.all([
      api("api/delivery/templates"),
      api("api/delivery/runs"),
      api("api/services"),
      api("api/release-versions?limit=500"),
      api("api/environments"),
      api("api/artifacts?limit=500"),
      api('api/changes?status=ready&limit=500'),
      api('api/nacos-executions'),api('api/redis-executions'),
    ]);
  } catch (e) {
    toast(e.message, "error");
  }
}
function addNode(list) {
  list.push({
    id: `step-${list.length + 1}-${Date.now().toString(36)}`,
    name: "发布应用",
    kind: "deploy",
    service: "",
    instructions: "",
  });
}
function move(list, index, delta) {
  const other = index + delta;
  if (other >= 0 && other < list.length)
    [list[index], list[other]] = [list[other], list[index]];
}
function editTemplate(item) {
  creatingForDelivery.value=false;
  template.value = item
    ? JSON.parse(JSON.stringify(item))
    : {
        name: "",
        mode: "fixed",
        services: [],
        nodes: [
          { id: "deploy", kind: "deploy", name: "发布应用", service: "" },
        ],
      };
  showTemplate.value = true;
}
async function saveTemplate() {
  busy.value = true;
  const origin=route.fullPath;
  try {
    const saved=await api(
      `api/delivery/templates${template.value.id ? "/" + template.value.id : ""}`,
      {
        method: template.value.id ? "PUT" : "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(template.value),
      },
    );
    const continueInContext=creatingForDelivery.value&&showTemplate.value&&route.fullPath===origin;
    showTemplate.value = false;
    await load();
    if(continueInContext&&route.fullPath===origin)openRun(saved);
    toast("模板已保存", "success");
  } catch (e) {
    toast(e.message, "error");
  } finally {
    busy.value = false;
  }
}
function openRun(item) {
  request.value = {
    template_id: item.id,
    version_id:
      item.mode === "version" ? Number(route.query.version) || "" : "",
    environment: globalEnvironment?.value || environments.value[0]?.name || "",
    reason: "",
    artifacts: {},
    nodes: JSON.parse(JSON.stringify(item.nodes)),
  };
  preview.value = null;
  showRun.value = true;
}
async function resolve() {
  busy.value = true;
  try {
    preview.value = await post("api/delivery/preview", request.value);
  } catch (e) {
    toast(e.message, "error");
  } finally {
    busy.value = false;
  }
}
async function submit() {
  busy.value = true;
  try {
    const result = await post("api/delivery/runs", request.value);
    showRun.value = false;
    area.value = "runs";
    await load();
    await openDetail(result);
    toast("已生成不可变执行单；提交不会自动部署", "success");
  } catch (e) {
    toast(e.message, "error");
  } finally {
    busy.value = false;
  }
}
async function openDetail(run) {
  try {
    selected.value = await api(`api/delivery/runs/${run.id}`);
    nacosExecutions.value = await api(`api/nacos-executions?run_id=${run.id}`);
    redisExecutions.value = await api(`api/redis-executions?run_id=${run.id}`);
    router.replace({path:'/orchestration',query:{run:String(run.id)}});
    const entries = await Promise.all(
      selected.value.steps
        .filter((s) => s.release_id)
        .map(async (s) => [
          s.release_id,
          await api(`api/releases/${s.release_id}`),
        ]),
    );
    children.value = Object.fromEntries(entries);
  } catch (e) {
    toast(e.message, "error");
  }
}
async function action(op) {
  if (
    op === "advance" &&
    !confirm(
      activeStep.value?.kind === "manual"
        ? `确认已完成「${activeStep.value.name}」说明中的人工操作？`
        : "继续执行当前节点？发布应用会连接真实目标主机。",
    )
  )
    return;
  busy.value = true;
  try {
    const result = await post(`api/delivery/runs/${selected.value.id}/${op}`, {
      digest: selected.value.digest,
      confirm_manual: activeStep.value?.kind === "manual",
    });
    await load();
    await openDetail(result);
  } catch (e) {
    toast(e.message, "error");
  } finally {
    busy.value = false;
  }
}
async function continueBatch(id) {
  if (!confirm("继续该应用的下一批发布？")) return;
  try {
    await post(`api/releases/${id}/continue`);
    await openDetail(selected.value);
  } catch (e) {
    toast(e.message, "error");
  }
}
function viewRelease(service) {
  router.push(`/svc/${encodeURIComponent(service)}/release`);
  selected.value = null;
}
watch(
  request,
  () => {
    preview.value = null;
  },
  { deep: true },
);
onMounted(async () => {
  me.value = await api("/api/me").catch(() => ({
    username: "local",
    role: "admin",
  }));
  await load();
  if (route.query.run) {
    area.value = "runs";
    await openDetail({ id: Number(route.query.run) });
  } else if (route.query.version) {
    startDelivery();
  }
});
</script>
<template>
  <div class="delivery-page">
    <div class="heading">
      <div class="title">
        <h2>部署编排</h2>
        <HelpTip
          text="模板定义顺序节点；执行单选择环境、版本和制品并冻结快照。生产需要指定审核人审核；本版按节点显式推进，多服务不会因多个 BUG 重复发布。"
        />
      </div>
      <ElButton v-if="area==='templates'" type="primary" @click="editTemplate()">＋ 新建编排</ElButton><ElButton v-else type="primary" @click="startDelivery">发起交付</ElButton>
    </div>
    <nav class="tabs">
      <button :class="{ active: area === 'runs' }" @click="area = 'runs'">执行与审核 <small>{{runs.filter(r=>r.status==='pending_approval').length||''}}</small></button>
      <button
        :class="{ active: area === 'templates' }"
        @click="area = 'templates'"
      >
        编排模板</button
      ><ElButton size="small" @click="load">刷新</ElButton>
    </nav>
    <div v-if="route.query.version" class="notice"><ElButton size="small" @click="router.push({path:'/changes',query:{tab:'versions',item:String(route.query.version)}})">← 返回版本</ElButton> 当前交付版本：{{versions.find(v=>v.id===Number(route.query.version))?.version||route.query.version}}</div>
    <template v-if="area === 'templates'"
      ><div v-if="!templates.length" class="empty">
        <h3>组织多个应用的一次交付</h3>
        <p>选择固定服务，或由发布版本中的代码变更自动推导服务范围。</p>
        <ElButton @click="editTemplate()">创建第一个编排</ElButton>
      </div>
      <article v-for="t in templates" :key="t.id" class="card">
        <div class="heading">
          <h3>{{ t.name }}</h3>
          <span class="tag">{{
            t.mode === "fixed" ? "固定服务" : "按发布版本"
          }}</span>
        </div>
        <p>
          {{
            t.mode === "fixed"
              ? (t.services || []).join("、")
              : "根据版本关联变更推导应用范围，同一应用只发布一个制品"
          }}
        </p>
        <div class="flow">
          <span v-for="(n, index) in t.nodes" :key="n.id"
            >{{ index + 1 }}. {{ n.name }}
            {{ n.service ? "/ " + n.service : "" }}</span
          >
        </div>
        <div class="actions">
          <ElButton v-if="canManage(t)" @click="editTemplate(t)"
            >编辑模板</ElButton
          ><ElButton type="primary" @click="openRun(t)">发起交付</ElButton>
        </div>
      </article></template
    >
    <template v-else
      ><div v-if="!runs.length" class="empty">
        <h3>尚无执行单</h3><p>选择已有编排，核对版本、环境和制品后提交；提交不会自动执行。</p><ElButton @click="startDelivery">发起第一次交付</ElButton>
      </div>
      <article v-for="r in runs" :key="r.id" class="card">
        <div class="heading">
          <h3>#{{ r.id }} · {{ r.plan.template.name }}</h3>
          <span class="tag">{{ states[r.status] || r.status }}</span>
        </div>
        <p>
          {{ r.plan.environment.display_name }} ·
          {{ r.plan.version?.version || "固定服务" }} · {{ r.owner }} ·
          {{ r.plan.services.length }} 个应用
        </p>
        <div class="flow">
          <span v-for="s in r.steps" :key="s.node_id + s.service"
            >{{ s.name }} · {{ states[s.status] || s.status }}</span
          >
        </div>
        <ElButton @click="openDetail(r)">查看 / 审核 / 执行</ElButton>
      </article></template
    >
    <Modal :show="showChooser" title="选择交付编排" wide @close="showChooser=false"><div class="form-grid"><p class="span">{{route.query.version?'当前版本：'+(versions.find(v=>v.id===Number(route.query.version))?.version||route.query.version):'选择固定服务编排，或按版本推导服务范围'}}。不会自动选择第一个模板。</p><label v-if="availableTemplates.length" class="span">编排模板<Sel v-model="chosenTemplate" :options="availableTemplates.map(t=>({value:t.id,label:t.name+' · '+(t.mode==='version'?'按版本':'固定服务')}))" width="100%" /></label><p v-else class="span">暂无适用模板。先配置交付步骤，保存后继续选择环境和制品。</p><div class="actions span"><ElButton @click="createForDelivery">新建适用编排</ElButton><ElButton @click="showChooser=false">取消</ElButton><ElButton v-if="availableTemplates.length" type="primary" @click="continueDelivery">下一步</ElButton></div></div></Modal>
    <Modal
      :show="showTemplate"
      title="编排模板"
      extra-wide
      @close="showTemplate = false"
      ><form class="form" @submit.prevent="saveTemplate">
        <div class="grid">
          <label>名称<ElInput v-model="template.name" /></label
          ><label
            >交付范围<Sel
              v-model="template.mode"
              :options="[
                { value: 'fixed', label: '固定服务' },
                { value: 'version', label: '由发布版本推导' },
              ]"
              width="100%"
          /></label>
        </div>
        <label v-if="template.mode === 'fixed'"
          >应用（可多选）<Sel
            v-model="template.services"
            multiple
            :options="services.map((s) => s.name)"
            filterable
            width="100%"
        /></label>
        <p v-else class="notice">
          执行时选择版本，仅代码/制品变更触发应用发布；配置变更不会把相关应用全部重启。
        </p>
        <h4>
          顺序节点
          <HelpTip
            text="可增删、排序；部署节点不选应用表示交付范围内的全部应用。每个应用只能安排一次。人工节点必须填写确认说明。"
          />
        </h4>
        <div v-for="(n, index) in template.nodes" :key="n.id" class="node">
          <div class="node-head">
            <b>{{ index + 1 }}</b
            ><ElInput v-model="n.name" placeholder="节点名称" /><Sel
              v-model="n.kind"
              :options="kinds"
              width="230px"
            /><button type="button" @click="move(template.nodes, index, -1)">
              ↑</button
            ><button type="button" @click="move(template.nodes, index, 1)">
              ↓</button
            ><button type="button" @click="template.nodes.splice(index, 1)">
              删除
            </button>
          </div>
          <label v-if="n.kind === 'deploy'"
            >指定应用<Sel
              v-model="n.service"
              :options="services.map((s) => s.name)"
              all-label="范围内全部应用"
              width="100%" /></label
          ><label v-else
            >操作 / 确认说明<ElInput
              v-model="n.instructions"
              type="textarea"
              :rows="2"
          /></label>
          <label v-if="n.kind==='mysql'">指定 SQL 变更（不选时执行版本内全部 SQL）<Sel v-model="n.change_ids" multiple :options="changes.filter(c=>c.kind==='mysql').map(c=>({value:c.id,label:`#${c.id} ${c.title}`}))" width="100%" /></label>
          <label v-if="n.kind==='nacos'">指定配置变更（不选时执行版本内全部 Nacos 变更）<Sel v-model="n.change_ids" multiple filterable :options="changes.filter(c=>c.kind==='nacos').map(c=>({value:c.id,label:`#${c.id} ${c.title}`}))" width="100%" /></label>
          <label v-if="n.kind==='redis'">指定 Redis 变更（不选时执行版本内全部 Redis 变更）<Sel v-model="n.change_ids" multiple filterable :options="changes.filter(c=>c.kind==='redis').map(c=>({value:c.id,label:`#${c.id} ${c.title}`}))" width="100%" /></label>
          <p v-if="!['deploy', 'manual','mysql','nacos','redis'].includes(n.kind)" class="notice">
            可保存节点定义；执行器未接通，会阻止提交。
          </p>
        </div>
        <ElButton native-type="button" @click="addNode(template.nodes)"
          >＋ 添加节点</ElButton
        >
        <div class="footer">
          <ElButton native-type="button" @click="showTemplate = false"
            >取消</ElButton
          ><ElButton native-type="submit" type="primary" :loading="busy"
            >保存模板</ElButton
          >
        </div>
      </form></Modal
    >
    <Modal
      :show="showRun"
      title="发起交付 · 范围与执行预览"
      extra-wide
      @close="showRun = false"
      ><div class="form">
        <div class="grid">
          <label
            >目标环境<Sel
              v-model="request.environment"
              :options="
                environments.map((e) => ({
                  value: e.name,
                  label: e.display_name,
                }))
              "
              width="100%" /></label
          ><label v-if="selectedTemplate?.mode === 'version'"
            >发布版本<Sel
              v-model="request.version_id"
              :options="
                versions
                  .filter((v) => ['ready', 'completed'].includes(v.status))
                  .map((v) => ({
                    value: v.id,
                    label: v.version + ' · ' + v.title,
                  }))
              "
              width="100%"
          /></label>
        </div>
        <label
          >发布原因<ElInput
            v-model="request.reason"
            placeholder="说明本次交付目的，生产环境按策略必填"
        /></label>
        <h4>候选制品（每应用一个，不自动选择最新制品）</h4>
        <label v-for="name in appNames" :key="name"
          >{{ name
          }}<Sel
            v-model="request.artifacts[name]"
            :options="
              artifacts
                .filter((a) => a.service === name)
                .map((a) => ({
                  value: a.id,
                  label: `#${a.id} · ${a.version} · ${a.kind} · ${a.digest?.slice(0, 23)}`,
                }))
            "
            filterable
            width="100%"
        /></label>
        <p v-if="!appNames.length" class="notice">
          当前范围没有代码应用；仅配置/数据变更不会产生部署服务。
        </p>
        <h4>本次执行节点（不修改模板）</h4>
        <div v-for="(n, index) in request.nodes" :key="n.id" class="node">
          <div class="node-head">
            <b>{{ index + 1 }}</b
            ><ElInput v-model="n.name" /><Sel
              v-model="n.kind"
              :options="kinds"
              width="230px"
            /><button @click="move(request.nodes, index, -1)">↑</button
            ><button @click="move(request.nodes, index, 1)">↓</button
            ><button @click="request.nodes.splice(index, 1)">删除</button>
          </div>
          <Sel
            v-if="n.kind === 'deploy'"
            v-model="n.service"
            :options="appNames"
            all-label="范围内全部应用"
            width="100%"
          /><ElInput
            v-else
            v-model="n.instructions"
            type="textarea"
            :rows="2"
            placeholder="操作与确认说明"
          />
          <label v-if="n.kind==='mysql'">本次 SQL 变更（版本模式仅允许版本内变更）<Sel v-model="n.change_ids" multiple :options="(version?.changes||changes).filter(c=>c.kind==='mysql').map(c=>({value:c.id,label:`#${c.id} ${c.title}`}))" all-label="版本内全部 SQL" width="100%" /></label>
          <label v-if="n.kind==='redis'">本次 Redis 变更（版本模式仅允许版本内变更）<Sel v-model="n.change_ids" multiple filterable :options="(version?.changes||changes).filter(c=>c.kind==='redis').map(c=>({value:c.id,label:`#${c.id} ${c.title}`}))" all-label="版本内全部 Redis 变更" width="100%" /></label>
          <label v-if="n.kind==='nacos'">本次配置变更（版本模式仅允许版本内变更）<Sel v-model="n.change_ids" multiple filterable :options="(version?.changes||changes).filter(c=>c.kind==='nacos').map(c=>({value:c.id,label:`#${c.id} ${c.title}`}))" all-label="版本内全部 Nacos 变更" width="100%" /></label>
        </div>
        <ElButton @click="addNode(request.nodes)">＋ 增加本次节点</ElButton
        ><ElButton @click="resolve" :loading="busy">检查范围与配置</ElButton>
        <section v-if="preview" class="preview">
          <h4>
            执行预览 ·
            {{
              preview.environment.protected || preview.redis_plans?.length || preview.sql_plans?.length || preview.template.workbench ? "需要审核（发起人不可自审）" : "无需额外审核"
            }}
          </h4>
          <p v-for="(b, i) in preview.blockers" :key="'b' + i" class="blocker">
            ✗ {{ b }}
          </p>
          <p v-for="(w, i) in preview.warnings" :key="'w' + i" class="notice">
            {{ w }}
          </p>
          <article v-for="app in preview.services" :key="app.service">
            <b
              >{{ app.service }} → {{ app.artifact.version }} / #{{
                app.artifact.id
              }}</b
            >
            <p>
              {{ app.target.host_group || app.target.target_type }} ·
              {{ app.target.hosts?.length || 1 }} 个目标 · 每批
              {{ app.target.batch_size || 1 }} 台
            </p>
            <code>{{ app.artifact.digest }}</code>
          </article>
          <p v-if="!preview.blockers.length">
            ✓ 校验通过。提交后冻结制品、目标和节点；修改需另建执行单。
          </p>
          <SQLPlanPreview :plans="preview.sql_plans" />
          <NacosPlanPreview :plans="preview.nacos_plans" />
          <RedisPlanPreview :plans="preview.redis_plans" />
        </section>
        <div class="footer">
          <ElButton @click="showRun = false">取消</ElButton
          ><ElButton
            type="primary"
            :loading="busy"
            :disabled="!preview || !!preview.blockers.length"
            @click="submit"
            >提交执行单（不立即部署）</ElButton
          >
        </div>
      </div></Modal
    >
    <Modal
      :show="!!selected"
      :title="`执行单 #${selected?.id || ''}`"
      extra-wide
      @close="selected = null"
      ><template v-if="selected"
        ><div class="heading">
          <h3>{{ selected.plan.template.name }}</h3>
          <ElButton @click="openDetail(selected)">刷新状态</ElButton>
        </div>
        <p>
          {{ selected.plan.environment.display_name }} ·
          {{ selected.plan.version?.version || "固定服务" }} ·
          {{ states[selected.status] }} · 发起人 {{ selected.owner }}
          <span v-if="selected.approver">· 审核人 {{ selected.approver }}</span>
        </p>
        <code class="digest">{{ selected.digest }}</code>
        <p>{{ selected.plan.reason || "未填写发布原因" }}</p>
        <section class="preview">
          <h4>审核快照：应用、制品与发布目标</h4>
          <p v-if="!selected.plan.services.length">
            本次没有应用部署，执行所选变更或人工确认节点。
          </p>
          <article v-for="app in selected.plan.services" :key="app.service">
            <b
              >{{ app.service }} → {{ app.artifact.version }} / #{{
                app.artifact.id
              }}</b
            >
            <code class="digest">{{ app.artifact.digest }}</code>
            <p>
              目标：{{ app.target.host_group || app.target.target_type }} ·
              {{
                app.target.hosts?.join("、") || app.target.deploy_config?.app_id
              }}
              · 每批 {{ app.target.batch_size || 1 }} ·
              {{
                app.target.pause_between_batches ? "批间暂停" : "自动连续批次"
              }}
            </p>
            <p>
              部署目录：{{ app.target.remote_dir || "云资源"
              }}<br />启动命令：{{ app.target.start_cmd || "平台生成"
              }}<br />健康检查：{{ app.target.health_url || "未配置"
              }}<br />环境变量：{{
                app.target.runtime_env_keys?.join("、") || "无"
              }}
            </p>
          </article>
          <template v-if="selected.plan.version">
            <h4>关联变更</h4>
            <article
              v-for="change in selected.plan.version.changes"
              :key="change.id"
            >
              <b
                >{{ change.title }} ·
                {{ changeKinds[change.kind] || change.kind }} ·
                {{ change.service || "无应用部署" }}</b
              >
              <code class="change-content">{{ change.reference }}</code>
              <p>
                {{ change.summary }}<br />回退：{{
                  change.rollback_plan || "未填写"
                }}
              </p>
            </article>
          </template>
          <SQLPlanPreview :plans="selected.plan.sql_plans" />
          <RedisPlanPreview :plans="selected.plan.redis_plans" :executions="redisExecutions.filter(e=>e.run_id===selected.id)" />
          <NacosPlanPreview :plans="selected.plan.nacos_plans" :executions="nacosExecutions.filter(e=>e.run_id===selected.id)" :can-restore="canManage(selected)&&['success','failed'].includes(selected.status)" @restore="restoreNacos" />
        </section>
        <div
          v-for="s in selected.steps"
          :key="s.node_id + s.service"
          class="node"
        >
          <div class="heading">
            <b>{{ s.name }}</b
            ><span>{{
              states[
                s.release_id
                  ? children[s.release_id]?.status || s.status
                  : s.status
              ] || s.status
            }}</span>
          </div>
          <p v-if="s.kind === 'manual'">
            {{
              selected.plan.nodes.find((n) => n.id === s.node_id)?.instructions
            }}
          </p>
          <p v-if="s.detail">{{s.detail}}</p>
          <template v-if="s.release_id"
            ><p>
              子发布单 #{{ s.release_id }} · 成功
              {{ children[s.release_id]?.succeeded || 0 }} /
              {{ children[s.release_id]?.total || 0 }}
            </p>
            <div class="actions">
              <ElButton
                v-if="
                  children[s.release_id]?.status === 'waiting_next_batch' &&
                  canManage(selected)
                "
                size="small"
                @click="continueBatch(s.release_id)"
                >继续下一批</ElButton
              ><ElButton size="small" @click="viewRelease(s.service)"
                >查看应用发布与日志</ElButton
              >
            </div></template
          >
        </div>
        <p class="notice">
          本版逐节点显式推进。子发布失败会中止后续应用，不自动回滚已经成功的其他应用；恢复需重新生成交付单。
        </p>
        <div class="footer">
          <template v-if="canReview"
            ><ElButton @click="action('reject')" :loading="busy">驳回</ElButton
            ><ElButton type="primary" @click="action('approve')" :loading="busy"
              >审核通过</ElButton
            ></template
          ><template v-if="canManage(selected)"
            ><ElButton
              v-if="['pending_approval', 'approved'].includes(selected.status)"
              @click="action('cancel')"
              >取消</ElButton
            ><ElButton
              v-if="['approved', 'running'].includes(selected.status)"
              type="primary"
              :loading="busy"
              @click="action('advance')"
              >{{
                !activeStep
                  ? "确认编排完成"
                  : activeStep.kind === "manual"
                    ? "确认人工节点"
                    : "继续执行 / 核对节点结果"
              }}</ElButton
            ></template
          >
        </div></template
      ></Modal
    >
  </div>
</template>
<style scoped>
.delivery-page {
  max-width: 1500px;
  margin: auto;
}
.heading,
.title,
.actions,
.node-head,
.footer {
  display: flex;
  align-items: center;
  gap: 12px;
}
.heading {
  justify-content: space-between;
  flex-wrap:wrap;
}
.title h2,
h3 {
  margin: 0;
}
.tabs {
  display: flex;
  align-items: center;
  gap: 4px;
  border-bottom: 1px solid var(--border);
  margin: 20px 0;
}
.tabs > button {
  background: none;
  border: 0;
  border-bottom: 2px solid transparent;
  color: var(--muted);
  padding: 12px 16px;
  font-size:13px;
  border-radius:8px 8px 0 0;
  cursor: pointer;
}
.tabs > button.active {
  border-color: var(--brand);
  color: var(--brand-ink);
  background:var(--brand-soft);
  font-weight:600;
}
.tabs > button:hover:not(.active){background:var(--panel2);color:var(--text)}
.card {
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: 12px;
  padding: 20px;
  margin-bottom: 14px;
  box-shadow:var(--shadow-sm);
}
.tag,
.flow span {
  background: var(--panel2);
  padding: 6px 10px;
  border-radius: 6px;
  font-size: 12px;
}
.flow {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
  margin: 18px 0;
}
.actions {
  margin-top: 12px;
}
p {
  color: var(--muted);
  font-size: 12px;
  line-height: 1.7;
}
.empty {
  text-align: center;
  border: 1px solid var(--border);
  background:var(--panel);
  padding: 80px 20px;
  border-radius: 12px;
}
.empty h3{font-size:20px;margin-bottom:10px}.empty p{margin:0 auto 20px;max-width:560px}.flow span{border:1px solid var(--border)}
.form {
  display: grid;
  gap: 16px;
}
.form label {
  display: grid;
  gap: 8px;
  font-size: 13px;
  min-width: 0;
}
.grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 16px;
}
.node {
  border: 1px solid var(--border);
  background: var(--panel2);
  border-radius: 8px;
  padding: 14px;
  margin-bottom: 10px;
}
.node-head {
  margin-bottom: 12px;
}
.node-head > .el-input {
  flex: 1;
  min-width: 100px;
}
.node-head button {
  white-space: nowrap;
  background: none;
  border: 0;
  color: var(--brand);
  cursor: pointer;
}
.notice {
  padding: 12px;
  background: var(--panel2);
  border-radius: 8px;
  margin: 0;
}
.footer {
  justify-content: flex-end;
  margin-top: 12px;
}
.preview {
  padding: 16px;
  border: 1px solid var(--border);
  border-radius: 8px;
}
.preview article {
  padding: 12px 0;
  border-bottom: 1px solid var(--border);
}
.blocker {
  color: var(--error);
}
code {
  font-size: 11px;
  overflow-wrap: anywhere;
}
.digest {
  display: block;
  background: var(--panel2);
  padding: 10px;
}
.change-content {
  display: block;
  white-space: pre-wrap;
  padding: 10px 0;
  max-height: 180px;
  overflow: auto;
}
.form h4 {
  margin: 0;
}
@media (max-width: 700px) {
  .grid {
    grid-template-columns: 1fr;
  }
  .node-head {
    flex-wrap: wrap;
  }
  .heading {
    gap: 12px;
    align-items: flex-start;
  }
  .footer {
    flex-wrap: wrap;
  }
}
</style>
