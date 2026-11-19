<script setup>
import {Field,FieldLabel,Input,Checkbox,Textarea} from '../../shared/shadcn-ui.js'
import HelpTip from '../../shared/components/HelpTip.vue'
import BuildExecutableSelect from './BuildExecutableSelect.vue'
defineProps({service:{type:Object,required:true},tool:{type:String,required:true},disabled:Boolean})
</script>

<template>
 <div class="@container/java-build min-w-0" aria-label="Java 编译参数">
  <div class="grid min-w-0 items-start gap-x-4 gap-y-5 @min-[32rem]/java-build:grid-cols-2">
   <BuildExecutableSelect :key="tool" v-model="service[tool==='maven'?'maven_executable':'gradle_executable']" :tool="tool" :disabled="disabled"/>
   <Field class="min-w-0" :data-validation-field="tool==='maven'?'maven_goals':'gradle_tasks'">
    <FieldLabel class="min-h-6 items-center">{{tool==='maven'?'Goals':'任务（Tasks）'}}</FieldLabel>
    <Input v-model="service[tool==='maven'?'maven_goals':'gradle_tasks']" :aria-label="tool==='maven'?'Maven Goals':'Gradle 任务'" :placeholder="tool==='maven'?'clean package':'clean build'" :disabled="disabled"/>
   </Field>
   <template v-if="tool==='maven'">
    <Field class="min-w-0" data-maven-threads>
     <FieldLabel class="min-h-6 items-center">并行线程（-T）<HelpTip text="1 为单线程；1C 表示每个 CPU 核一个线程，例如 4 核会用 4 个线程。支持 4、1C、2.5C；留空使用 Maven 默认。所有环境共用。"/></FieldLabel>
     <Input v-model="service.maven_threads" aria-label="Maven 并行线程" placeholder="默认；如 1C 或 4" :disabled="disabled"/>
    </Field>
    <Field class="min-w-0">
     <FieldLabel class="min-h-6 items-center">依赖更新（-U）<HelpTip text="所有环境共用。启用后每次构建检查更新的快照依赖和缺失的发行依赖；不改变环境构建参数。"/></FieldLabel>
     <Field orientation="horizontal" class="min-h-8 gap-2">
      <Checkbox :id="`maven-update-${service.name||'new'}`" v-model="service.maven_update_snapshots" :disabled="disabled"/>
      <FieldLabel :for="`maven-update-${service.name||'new'}`" class="font-normal">每次构建刷新依赖</FieldLabel>
     </Field>
    </Field>
    <Field class="min-w-0 @min-[32rem]/java-build:col-span-2" data-maven-arguments>
     <FieldLabel class="min-h-6 items-center">命令行附加参数<HelpTip text="空格或换行分隔，例如 -e 输出异常堆栈、-X 调试日志、-o 离线、-fae 收集模块失败，或 -pl :service-bus -am 选择模块。Profiles、-D、线程数、依赖更新和 settings 请用对应字段；不执行 Shell。调试日志可能包含项目敏感配置，分享前请核对。"/></FieldLabel>
     <Textarea v-model="service.maven_arguments" aria-label="Maven 命令行附加参数" placeholder="例如 -e，或 -pl :service-bus -am -e" :rows="2" :maxlength="4096" :disabled="disabled"/>
    </Field>
   </template>
  </div>
 </div>
</template>
