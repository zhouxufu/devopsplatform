<script setup>
import {computed} from 'vue'
import {Field,FieldLabel,Input,Textarea,Checkbox} from '../../shared/shadcn-ui.js'
import HelpTip from '../../shared/components/HelpTip.vue'
import OptionPicker from '../../shared/components/OptionPicker.vue'
import JavaCompileFields from './JavaCompileFields.vue'
import {bundleParams,updateBundleParams} from '../javaBundle.js'
const props=defineProps({service:{type:Object,required:true}})
const mainJar=computed({get:()=>bundleParams(props.service).MAIN_JAR,set:v=>updateBundleParams(props.service,{MAIN_JAR:v})})
const requiredPaths=computed({get:()=>bundleParams(props.service).REQUIRED_PATHS,set:v=>updateBundleParams(props.service,{REQUIRED_PATHS:v})})
function selectTool(tool){
 props.service.build_tool=tool
 if(tool==='maven'){props.service.maven_executable||='auto';props.service.maven_goals||='clean package'}
 if(tool==='gradle'){props.service.gradle_executable||='auto';props.service.gradle_tasks||='clean build'}
}
</script>
<template><div class="space-y-4" aria-label="Java 整包构建配置">
 <Field><FieldLabel>构建方式</FieldLabel><OptionPicker :model-value="service.build_tool||'command'" :options="[{value:'command',label:'组合构建脚本'},{value:'maven',label:'Maven'},{value:'gradle',label:'Gradle'}]" @update:model-value="selectTool"/></Field>
 <Field v-if="!['maven','gradle'].includes(service.build_tool)"><FieldLabel>整包构建命令<HelpTip text="执行可信仓库的组合构建脚本，产出完整发布目录。归档步骤保留目录、文件和执行权限，不运行包内的启动脚本。"/></FieldLabel><Textarea v-model="service.build_cmd" aria-label="整包构建命令" rows="3" placeholder="bash deploy/jpom/build-release.sh"/></Field>
 <JavaCompileFields v-if="['maven','gradle'].includes(service.build_tool)" :service="service" :tool="service.build_tool"/>
 <template v-if="service.build_tool==='maven'"><Field><FieldLabel>Maven Profiles</FieldLabel><Input v-model="service.maven_profiles"/></Field><Field><FieldLabel>-D 属性（每行 key=value）</FieldLabel><Textarea v-model="service.maven_properties" rows="3"/></Field></template>
 <template v-if="service.build_tool==='gradle'"><Field><FieldLabel>排除任务</FieldLabel><Input v-model="service.gradle_excluded_tasks"/></Field><Field><FieldLabel>-P 项目属性（每行 key=value）</FieldLabel><Textarea v-model="service.gradle_project_properties" rows="2"/></Field><Field><FieldLabel>-D 系统属性（每行 key=value）</FieldLabel><Textarea v-model="service.gradle_system_properties" rows="2"/></Field></template>
 <div class="grid grid-cols-1 gap-3 md:grid-cols-2"><Field><FieldLabel>发布目录</FieldLabel><Input v-model="service.artifact_glob" aria-label="整包发布目录" placeholder="build/jpom-release"/></Field><Field><FieldLabel>包内主 JAR</FieldLabel><Input v-model="mainJar" aria-label="包内主 JAR" placeholder="lib/estate-gis-service.jar"/></Field></div>
 <Field><FieldLabel>必需文件或目录<HelpTip text="每行一个包内相对路径，缺少任意项则归档失败。可填写启动脚本、工具、配置或数据路径。"/></FieldLabel><Textarea v-model="requiredPaths" aria-label="整包必需路径" rows="4" placeholder="bin/estate-gis-service.sh&#10;tools/changchun-traffic"/></Field>
</div></template>
