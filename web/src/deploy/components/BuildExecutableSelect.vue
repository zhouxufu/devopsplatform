<script setup>

import {computed, ref} from 'vue'


import {executableMode, executableOptions, executableSpec} from '../buildExecutable.js'
const props=defineProps({modelValue:String,tool:{type:String,default:'gradle'},disabled:Boolean})
const emit=defineEmits(['update:modelValue'])
const customPath=ref('')
const spec=computed(()=>executableSpec(props.tool))
const mode=computed(()=>executableMode(props.tool,props.modelValue))
function select(value){
 if(mode.value==='custom')customPath.value=props.modelValue||''
 emit('update:modelValue',value==='custom'?customPath.value:value)
 customSelected.value=value==='custom'
}
// An empty custom path is a deliberate draft, not a switch back to auto.
const customSelected=ref(false)
const selected=computed(()=>customSelected.value&&!(props.modelValue||'').trim()?'custom':mode.value)

import OptionPicker from '../../shared/components/OptionPicker.vue'
import HelpTip from '../../shared/components/HelpTip.vue'
const description=computed(()=>selected.value==='auto'?'优先执行项目 Wrapper；存在 Wrapper 时，即使下载失败也不会自动换用环境工具。':selected.value===spec.value.wrapper?'使用项目指定的版本；首次运行可能下载发行包，请确保构建环境能访问下载地址。':selected.value===spec.value.system?`直接运行构建环境 PATH 中的 ${spec.value.system}，不使用项目 Wrapper；请先提供兼容版本，不会自动安装。`:'填写执行编译的环境内的完整路径；使用隔离容器时，这不是 Runner 宿主机路径。')



import {Field,FieldLabel,Input} from '../../shared/shadcn-ui.js'
</script>
<template><Field :data-build-executable="tool" class="build-executable"><FieldLabel class="min-h-6 items-center">{{spec.name}} 执行方式<HelpTip :text="description"/></FieldLabel><OptionPicker :model-value="selected" :options="executableOptions(tool)" :disabled="disabled" :aria-label="spec.name+' 执行方式'" :style="{width:'100%',maxWidth:'100%'}" @update:model-value="select"/><Input v-if="selected==='custom'" :model-value="modelValue" :disabled="disabled" :placeholder="spec.path" @update:model-value="value=>emit('update:modelValue',value)" v-bind="{&quot;aria-label&quot;:(spec.name+' 自定义可执行路径')}"/></Field></template>
<style scoped>.build-executable{min-width:0}</style>
