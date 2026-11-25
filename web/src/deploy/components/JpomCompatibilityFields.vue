<script setup>
import OptionPicker from '../../shared/components/OptionPicker.vue'
import HelpTip from '../../shared/components/HelpTip.vue'
import {Field,FieldDescription,FieldLabel,Input,Textarea} from '../../shared/shadcn-ui.js'
const config=defineModel({type:Object,required:true})
</script>
<template>
 <div class="two grid min-w-0 gap-4 sm:grid-cols-2">
  <Field><FieldLabel>Jpom 项目 ID<HelpTip text="使用 Jpom 原项目 ID，可能与 DevOps 应用名不同。两边按此标识及 JAR 目录识别同一实例。"/></FieldLabel><Input v-model="config.jpom_project_id" aria-label="Jpom 项目 ID" placeholder="原 Jpom 项目 ID"/></Field>
  <Field><FieldLabel>JAR 文件名</FieldLabel><Input v-model="config.jpom_jar_name" aria-label="Jpom JAR 文件名" placeholder="原目录中的 JAR 文件名"/></Field>
  <Field class="sm:col-span-2"><FieldLabel>日志文件（可选）</FieldLabel><Input v-model="config.jpom_log_file" aria-label="Jpom 日志文件" placeholder="默认沿用 Jpom 项目 ID 对应的控制台日志路径"/></Field>
  <Field><FieldLabel>运行 Java 版本</FieldLabel><OptionPicker v-model="config.java_version" :options="['8','11','17','21','25'].map(value=>({value,label:'Java '+value}))" aria-label="Jpom 运行 Java 版本"/></Field>
  <Field><FieldLabel>JAVA_HOME（可选）</FieldLabel><Input v-model="config.java_home" aria-label="Jpom JAVA_HOME" placeholder="留空使用目标主机 PATH 中的 java"/></Field>
  <Field><FieldLabel>JVM 参数</FieldLabel><Textarea v-model="config.java_jvm_options" aria-label="Jpom JVM 参数" placeholder="每行一个参数" :rows="3"/></Field>
  <Field><FieldLabel>应用参数</FieldLabel><Textarea v-model="config.java_args" aria-label="Jpom 应用参数" placeholder="每行一个参数" :rows="3"/></Field>
  <FieldDescription class="sm:col-span-2">部署目录填写原 Jpom 的 JAR 目录。此方式通过 SSH 覆盖同名 JAR，不增加跨平台锁、不自动回滚；运行结果由人工验收。</FieldDescription>
 </div>
</template>
