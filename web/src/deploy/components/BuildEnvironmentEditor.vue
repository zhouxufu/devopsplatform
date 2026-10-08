<script setup>
import { computed } from 'vue'
import Sel from '../../shared/components/Sel.vue'
import HelpTip from '../../shared/components/HelpTip.vue'
import { ElInput } from '../../shared/components/ui.js'
const props = defineProps({ modelValue: Object, buildTool: String, artifactType: String })
const emit = defineEmits(['update:modelValue'])
const config = computed(() => props.modelValue || { mode: 'local' })
const recommended = computed(() => props.artifactType === 'static' ? 'node22' : props.buildTool === 'gradle' ? 'gradle-jdk21' : 'maven-jdk21')
const presets = computed(() => [
  ...['maven', 'gradle'].flatMap(tool => [8, 17, 21].map(jdk => ({ value: `${tool}-jdk${jdk}`, label: `JDK ${jdk} + ${tool === 'maven' ? 'Maven 3.9.9' : 'Gradle 8.14.3'}` }))),
  { value: 'node20', label: 'Node.js 20 + npm' }, { value: 'node22', label: 'Node.js 22 + npm' },
  { value: 'custom', label: '自定义镜像（组合工具 / 内网镜像）' },
].filter(p => p.value === 'custom' || !['maven','gradle'].includes(props.buildTool) || p.value.startsWith(`${props.buildTool}-`)))
function update(patch) { emit('update:modelValue', { ...config.value, ...patch }) }
</script>

<template>
  <div class="build-environment">
    <div class="field">
      <label>构建环境 <HelpTip text="仅影响编译：拉取代码和制品归档仍在 Runner 上执行。构建容器按次创建并清理，缓存按应用和镜像隔离；部署服务器的运行 JDK 在应用环境中另行配置。只构建可信代码，这不是多租户安全沙箱。" /></label>
      <Sel :model-value="config.mode || 'local'" :options="[{value:'local',label:'本机工具（使用 Runner 已安装版本）'},...(artifactType === 'image' ? [] : [{value:'container',label:'隔离容器（选择工具版本）'}])]" width="100%" @update:model-value="mode => update({mode, preset: config.preset || recommended})" />
    </div>
    <template v-if="config.mode === 'container'">
      <div class="field"><label>工具环境</label><Sel :model-value="config.preset" :options="presets" width="100%" @update:model-value="preset => update({preset})" /></div>
      <div class="field"><label>构建网络 <HelpTip text="默认隔离网络安全边界更清晰；如果内网 DNS、HTTP 代理或私服只能从 Runner 宿主机访问，可显式使用宿主机网络。仅适用于可信代码和自托管 Linux Runner。" /></label><Sel :model-value="config.network || 'bridge'" :options="[{value:'bridge',label:'默认隔离网络'},{value:'host',label:'宿主机网络（内网 DNS / 私服）'}]" width="100%" @update:model-value="network => update({network})" /></div>
      <div v-if="config.preset === 'custom'" class="field"><label>构建镜像</label><ElInput :model-value="config.image" placeholder="内网仓库/builder:java21-node22" @update:model-value="image => update({image})" /></div>
      <small>Runner 需运行 Docker。构建配置可选择项目 Wrapper 或镜像内置 Maven / Gradle；内网环境建议使用镜像内工具，避免 Wrapper 从公网下载发行包。</small>
      <small v-if="config.preset === 'custom'">镜像需包含 bash 和构建工具，并允许以 Runner 的 UID/GID 运行。无须填写 docker run；不自动挂载宿主机密钥或其他目录。</small>
      <small v-if="buildTool === 'gradle'">选择 Wrapper 时由项目决定 Gradle 版本；选择环境工具时使用上方镜像内置版本。JDK 21 运行 Gradle 需 8.5 及以上。</small>
      <small v-if="config.preset?.startsWith('node')">Node 模板内置 npm，其他包管理器需项目自行安装或使用自定义镜像。</small>
    </template>
    <small v-else>沿用 Runner 的 PATH / JAVA_HOME，不自动安装工具。不同项目需要不同版本时，可切换到隔离容器。</small>
  </div>
</template>

<style scoped>
.build-environment{display:flex;flex-direction:column;gap:10px;min-width:0}.field{display:flex;flex-direction:column;gap:5px}.field>label{display:flex;align-items:center;gap:6px;font-size:12px;color:var(--muted)}small{display:block;font-size:11px;color:var(--muted);line-height:1.6}
</style>
