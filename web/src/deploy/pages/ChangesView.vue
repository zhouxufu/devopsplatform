<script setup>
import { computed, inject, onMounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import Modal from "../../shared/components/Modal.vue";
import HelpTip from "../../shared/components/HelpTip.vue";
import Sel from "../../shared/components/Sel.vue";
import { ElButton, ElInput } from "../../shared/components/ui.js";
import { toast } from "../../shared/composables/toast.js";
const api = inject("api"),
  route = useRoute(),
  router = useRouter();
const tab = computed(() =>
  ["work", "changes", "versions"].includes(route.query.tab)
    ? route.query.tab
    : "changes",
);
const work = ref([]),
  changes = ref([]),
  versions = ref([]),
  deliveries = ref([]),
  services = ref([]),
  me = ref({});
const query = ref(""),
  filter = ref(""),
  dialog = ref(""),
  form = ref({}),
  saving = ref(false);
const kinds = [
  { value: "code", label: "代码分支" },
  { value: "artifact", label: "应用制品" },
  { value: "nacos", label: "Nacos 配置" },
  { value: "mysql", label: "SQL 变更" },
  { value: "data-sync", label: "数据对比同步" },
  { value: "redis", label: "Redis 命令" },
  { value: "manual", label: "人工操作" },
];
const labels = {
  open: "进行中",
  closed: "已关闭",
  draft: "草稿",
  ready: "已就绪",
  completed: "已完成",
  canceled: "已取消",
};
const kindLabel = (value) =>
  kinds.find((v) => v.value === value)?.label || value;
const list = computed(() =>
  (tab.value === "work"
    ? work.value
    : tab.value === "versions"
      ? versions.value
      : changes.value
  ).filter(
    (v) =>
      (!filter.value || v.status === filter.value) &&
      (!query.value ||
        `${v.title} ${v.version || ""} ${v.external_id || ""} ${v.service || ""} ${v.reference || ""}`
          .toLowerCase()
          .includes(query.value.toLowerCase())),
  ),
);
const readyChanges = computed(() =>
  changes.value.filter((c) => c.status === "ready"),
);
const canManage = (item) =>
  me.value.username === "local" ||
  me.value.role === "admin" ||
  me.value.username === item.owner;
const safeURL = (value) =>
  /^https?:\/\//i.test(value || "") ? value : undefined;
async function load() {
  try {
    [
      work.value,
      changes.value,
      versions.value,
      services.value,
      deliveries.value,
    ] = await Promise.all([
      api("api/work-items?limit=500"),
      api("api/changes?limit=500"),
      api("api/release-versions?limit=500"),
      api("api/services"),
      api("api/delivery/runs"),
    ]);
  } catch (e) {
    toast(e.message, "error");
  }
}
function open(type, item) {
  dialog.value = type;
  if (type === "work")
    form.value = item
      ? { ...item }
      : {
          title: "",
          kind: "requirement",
          source: "manual",
          external_id: "",
          description: "",
          status: "open",
        };
  if (type === "changes")
    form.value = item
      ? { ...item, work_item_ids: (item.work_items || []).map((v) => v.id) }
      : {
          title: "",
          kind: "code",
          service: "",
          reference: "",
          summary: "",
          risk_level: "medium",
          rollback_plan: "",
          work_item_ids: [],
        };
  if (type === "versions")
    form.value = item
      ? {
          ...item,
          change_ids: (item.changes || []).map((v) => v.id),
          work_item_ids: [],
        }
      : {
          version: "",
          title: "",
          description: "",
          change_ids: [],
          work_item_ids: [],
        };
}
function addWorkChanges(ids) {
  const included = readyChanges.value
    .filter((c) => (c.work_items || []).some((w) => ids.includes(w.id)))
    .map((c) => c.id);
  form.value.change_ids = [...new Set([...form.value.change_ids, ...included])];
}
async function save() {
  if (!form.value.title?.trim()) return toast("请填写标题", "warning");
  if (
    dialog.value === "versions" &&
    (!form.value.version?.trim() || !form.value.change_ids?.length)
  )
    return toast("请填写版本号并选择至少一项已就绪变更", "warning");
  saving.value = true;
  const endpoint = {
    work: "work-items",
    changes: "changes",
    versions: "release-versions",
  }[dialog.value];
  try {
    await api(`api/${endpoint}${form.value.id ? "/" + form.value.id : ""}`, {
      method: form.value.id ? "PUT" : "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(form.value),
    });
    dialog.value = "";
    await load();
    toast("已保存", "success");
  } catch (e) {
    toast(e.message, "error");
  } finally {
    saving.value = false;
  }
}
async function action(type, item, op) {
  if (
    !confirm(
      `${op === "delete" ? "删除" : op === "ready" ? "标记就绪" : "退回草稿"}「${item.title}」？`,
    )
  )
    return;
  try {
    const endpoint = type === "versions" ? "release-versions" : "changes";
    await api(`api/${endpoint}/${item.id}${op === "delete" ? "" : "/" + op}`, {
      method: op === "delete" ? "DELETE" : "POST",
    });
    await load();
  } catch (e) {
    toast(e.message, "error");
  }
}
watch(tab, () => {
  query.value = "";
  filter.value = "";
});
onMounted(async () => {
  me.value = await api("/api/me").catch(() => ({
    username: "local",
    role: "admin",
  }));
  await load();
});
</script>
<template>
  <div class="change-center">
    <div class="heading">
      <div class="title">
        <h2>变更管控</h2>
        <HelpTip
          text="需求与变更独立存在；发布版本汇总本次交付范围，不绑定环境。执行时由部署编排选择环境，审核的是具体执行快照。"
        />
      </div>
      <ElButton type="primary" @click="open(tab)"
        >＋
        {{
          tab === "work"
            ? "新建需求 / 关联 BUG"
            : tab === "versions"
              ? "创建发布版本"
              : "新建变更"
        }}</ElButton
      >
    </div>
    <nav class="tabs">
      <button
        v-for="item in [
          { key: 'work', label: '需求与 BUG' },
          { key: 'changes', label: '独立变更' },
          { key: 'versions', label: '发布版本' },
        ]"
        :key="item.key"
        :class="{ active: tab === item.key }"
        @click="router.replace({ path: '/changes', query: { tab: item.key } })"
      >
        {{ item.label }}
      </button>
    </nav>
    <div class="toolbar">
      <ElInput
        v-model="query"
        placeholder="搜索标题、版本、外部编号或应用"
        clearable
        style="max-width: 420px"
      /><Sel
        v-model="filter"
        :options="
          (tab === 'work'
            ? ['open', 'closed']
            : tab === 'versions'
              ? ['draft', 'ready', 'completed']
              : ['draft', 'ready']
          ).map((v) => ({ value: v, label: labels[v] }))
        "
        all-label="全部状态"
      /><span>{{ list.length }} 条</span><ElButton @click="load">刷新</ElButton>
    </div>
    <div v-if="!list.length" class="empty">
      <h3>
        尚无{{
          tab === "work"
            ? "需求与缺陷"
            : tab === "versions"
              ? "发布版本"
              : "变更"
        }}
      </h3>
      <p>
        {{
          tab === "versions"
            ? "先将变更标记就绪，再选择需求或变更创建版本。"
            : "可以独立创建，不需要先建立发布版本。"
        }}
      </p>
      <ElButton @click="open(tab)">新建</ElButton>
    </div>
    <article v-for="item in list" :key="item.id" class="record">
      <div class="record-head">
        <div>
          <span class="tag">{{
            tab === "versions"
              ? item.version
              : tab === "work"
                ? item.kind === "bug"
                  ? "BUG"
                  : "需求"
                : kindLabel(item.kind)
          }}</span
          ><strong>{{ item.title }}</strong>
        </div>
        <span class="status">{{ labels[item.status] || item.status }}</span>
      </div>
      <p>{{ item.summary || item.description || "暂无说明" }}</p>
      <div class="meta">
        <span>#{{ item.id }} · {{ item.owner }}</span
        ><span v-if="item.service">应用 {{ item.service }}</span
        ><span v-if="item.external_id"
          >{{ item.source }} / {{ item.external_id }}</span
        ><a
          v-if="safeURL(item.url)"
          :href="safeURL(item.url)"
          target="_blank"
          rel="noopener noreferrer"
          >外部记录</a
        >
      </div>
      <template v-if="tab === 'changes'"
        ><code v-if="item.reference">{{ item.reference }}</code>
        <div class="meta">
          关联需求：{{
            (item.work_items || []).map((v) => v.title).join("、") ||
            "无（独立变更）"
          }}
          ·
          {{
            item.risk_level === "high"
              ? "高"
              : item.risk_level === "low"
                ? "低"
                : "中"
          }}风险
        </div>
      </template>
      <template v-if="tab === 'versions'"
        ><div class="chips">
          <span v-for="c in item.changes" :key="c.id"
            >{{ kindLabel(c.kind) }} · {{ c.title }}
            <small v-if="c.service">/ {{ c.service }}</small></span
          >
        </div>
        <div class="meta">
          部署范围：{{
            [
              ...new Set(
                (item.changes || [])
                  .filter((c) => ["code", "artifact"].includes(c.kind))
                  .map((c) => c.service)
                  .filter(Boolean),
              ),
            ].join("、") || "仅非代码变更，不自动发布服务"
          }}
          · 不绑定环境
        </div></template
      >
      <div v-if="tab === 'versions'" class="chips">
        <button
          v-for="run in deliveries.filter(
            (r) => r.plan.version?.id === item.id,
          )"
          :key="run.id"
          @click="
            router.push({
              path: '/orchestration',
              query: { run: String(run.id) },
            })
          "
        >
          {{ run.plan.environment.display_name }} · #{{ run.id }} ·
          {{
            {
              pending_approval: "待审核",
              approved: "待执行",
              running: "执行中",
              success: "成功",
              failed: "失败",
              rejected: "已驳回",
              canceled: "已取消",
            }[run.status] || run.status
          }}
        </button>
      </div>
      <div class="actions">
        <template v-if="canManage(item)"
          ><ElButton
            v-if="tab === 'work' || item.status === 'draft'"
            size="small"
            @click="open(tab, item)"
            >编辑</ElButton
          ><ElButton
            v-if="tab !== 'work' && item.status === 'draft'"
            size="small"
            type="primary"
            @click="action(tab, item, 'ready')"
            >标记就绪</ElButton
          ><ElButton
            v-if="tab === 'changes' && item.status === 'ready'"
            size="small"
            @click="action(tab, item, 'reopen')"
            >退回草稿</ElButton
          ><ElButton
            v-if="tab !== 'work' && item.status === 'draft'"
            size="small"
            @click="action(tab, item, 'delete')"
            >删除</ElButton
          ></template
        ><ElButton
          v-if="
            tab === 'versions' && ['ready', 'completed'].includes(item.status)
          "
          size="small"
          type="primary"
          @click="
            router.push({
              path: '/orchestration',
              query: { version: String(item.id) },
            })
          "
          >按版本发起编排</ElButton
        >
      </div>
    </article>
    <Modal
      :show="!!dialog"
      :title="
        dialog === 'work'
          ? '需求 / BUG'
          : dialog === 'versions'
            ? '发布版本'
            : '独立变更'
      "
      extra-wide
      @close="dialog = ''"
    >
      <form class="form-grid" @submit.prevent="save">
        <label class="span">标题<ElInput v-model="form.title" /></label>
        <template v-if="dialog === 'work'"
          ><label
            >类型<Sel
              v-model="form.kind"
              :options="[
                { value: 'requirement', label: '需求' },
                { value: 'bug', label: 'BUG' },
              ]"
              width="100%" /></label
          ><label
            >来源<Sel
              v-model="form.source"
              :options="[
                { value: 'manual', label: '自定义' },
                { value: 'zentao', label: '禅道（手工关联）' },
                { value: 'external', label: '其他外部平台' },
              ]"
              width="100%" /></label
          ><label v-if="form.source !== 'manual'"
            >外部编号<ElInput v-model="form.external_id" /></label
          ><label v-if="form.source !== 'manual'"
            >记录链接<ElInput v-model="form.url" /></label
          ><label v-if="form.id"
            >状态<Sel
              v-model="form.status"
              :options="[
                { value: 'open', label: '进行中' },
                { value: 'closed', label: '已关闭' },
              ]"
              width="100%" /></label
          ><label class="span"
            >说明<ElInput v-model="form.description" type="textarea" :rows="3"
          /></label>
          <p class="notice span">
            本版支持外部记录关联；禅道自动同步、日期基线及上线状态回写尚未接通。
          </p></template
        >
        <template v-if="dialog === 'changes'"
          ><label
            >变更类型<Sel
              v-model="form.kind"
              :options="kinds"
              width="100%" /></label
          ><label
            >关联应用<Sel
              v-model="form.service"
              :options="services.map((s) => ({ value: s.name, label: s.name }))"
              all-label="不关联应用"
              filterable
              width="100%" /></label
          ><label class="span"
            >{{
              form.kind === "code"
                ? "代码分支"
                : form.kind === "artifact"
                  ? "制品说明 / 标识"
                  : form.kind === "nacos"
                    ? "配置项与期望值（本版记录）"
                    : form.kind === "mysql"
                      ? "SQL 内容（本版记录）"
                      : "实施内容（本版记录）"
            }}<ElInput
              v-model="form.reference"
              type="textarea"
              :rows="3" /></label
          ><label
            >关联需求 / BUG（可选）<Sel
              v-model="form.work_item_ids"
              multiple
              :options="
                work.map((w) => ({ value: w.id, label: `#${w.id} ${w.title}` }))
              "
              filterable
              width="100%" /></label
          ><label
            >风险等级<Sel
              v-model="form.risk_level"
              :options="[
                { value: 'low', label: '低' },
                { value: 'medium', label: '中' },
                { value: 'high', label: '高' },
              ]"
              width="100%" /></label
          ><label
            >变更说明<ElInput
              v-model="form.summary"
              type="textarea"
              :rows="3" /></label
          ><label
            >回退方案<ElInput
              v-model="form.rollback_plan"
              type="textarea"
              :rows="3"
          /></label>
          <p
            v-if="!['code', 'artifact', 'manual'].includes(form.kind)"
            class="notice span"
          >
            该类变更目前可登记、关联和进入版本；执行器未接通，编排会阻止提交，不会自动执行或标记成功。
          </p></template
        >
        <template v-if="dialog === 'versions'"
          ><label
            >版本号<ElInput
              v-model="form.version"
              placeholder="例如 2026.10.1" /></label
          ><label
            >按需求 / BUG 自动加入变更<Sel
              v-model="form.work_item_ids"
              multiple
              :options="work.map((w) => ({ value: w.id, label: w.title }))"
              filterable
              width="100%"
              @change="addWorkChanges" /></label
          ><label class="span"
            >选择已就绪变更（可增删）<Sel
              v-model="form.change_ids"
              multiple
              :options="
                readyChanges.map((c) => ({
                  value: c.id,
                  label: `${kindLabel(c.kind)} · ${c.title}${c.service ? ' / ' + c.service : ''}`,
                }))
              "
              filterable
              width="100%"
          /></label>
          <div class="selection span">
            <p v-for="id in form.change_ids" :key="id">
              {{ changes.find((c) => c.id === id)?.title }}
              <button
                type="button"
                @click="
                  form.change_ids = form.change_ids.filter((v) => v !== id)
                "
              >
                移除
              </button>
            </p>
          </div>
          <label class="span"
            >版本说明<ElInput
              v-model="form.description"
              type="textarea"
              :rows="3"
          /></label>
          <p class="notice span">
            需求选择只追加关联的已就绪变更，需核对完整范围。创建版本不自动合并代码、不执行变更；推进环境与审核在部署编排中完成。
          </p></template
        >
        <div class="footer span">
          <ElButton native-type="button" @click="dialog = ''">取消</ElButton
          ><ElButton type="primary" native-type="submit" :loading="saving"
            >保存</ElButton
          >
        </div>
      </form>
    </Modal>
  </div>
</template>
<style scoped>
.change-center {
  max-width: 1500px;
  margin: auto;
}
.heading,
.title,
.toolbar,
.record-head,
.actions,
.meta,
.chips {
  display: flex;
  align-items: center;
  gap: 12px;
}
.heading,
.record-head {
  justify-content: space-between;
}
.title h2 {
  margin: 0;
}
.tabs {
  display: flex;
  gap: 20px;
  border-bottom: 1px solid var(--border);
  margin: 20px 0;
}
.tabs button {
  border: 0;
  border-bottom: 2px solid transparent;
  background: none;
  color: var(--muted);
  padding: 12px 4px;
  cursor: pointer;
}
.tabs button.active {
  color: var(--brand);
  border-color: var(--brand);
}
.toolbar {
  margin-bottom: 16px;
  flex-wrap: wrap;
}
.toolbar span,
.meta,
p {
  color: var(--muted);
  font-size: 12px;
}
.record {
  padding: 20px;
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: 12px;
  margin-bottom: 12px;
}
.record-head > div {
  display: flex;
  gap: 12px;
  align-items: center;
}
.tag,
.status,
.chips span {
  padding: 4px 8px;
  border-radius: 5px;
  background: var(--panel2);
  font-size: 12px;
}
.meta,
.chips {
  flex-wrap: wrap;
  margin: 12px 0;
}
.record code {
  display: block;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  background: var(--panel2);
  padding: 12px;
  font-size: 12px;
  max-height: 150px;
  overflow: auto;
}
.actions {
  margin-top: 12px;
}
.empty {
  text-align: center;
  padding: 70px;
  border: 1px dashed var(--border);
  border-radius: 12px;
}
.form-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 16px;
}
.form-grid label {
  display: grid;
  gap: 8px;
  font-size: 13px;
  min-width: 0;
}
.span {
  grid-column: 1/-1;
}
.notice {
  background: var(--panel2);
  padding: 12px;
  border-radius: 8px;
  line-height: 1.6;
  margin: 0;
}
.footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
}
.selection p {
  margin: 5px 0;
  display: flex;
  justify-content: space-between;
}
.selection button {
  background: none;
  border: 0;
  color: var(--brand);
  cursor: pointer;
}
@media (max-width: 700px) {
  .form-grid {
    grid-template-columns: 1fr;
  }
  .heading {
    align-items: flex-start;
    gap: 12px;
  }
  .record-head {
    align-items: flex-start;
  }
  .record-head > div {
    flex-wrap: wrap;
  }
}
</style>
