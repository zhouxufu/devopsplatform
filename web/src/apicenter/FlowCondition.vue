<script setup>
import {Field,FieldLabel,Input} from '../shared/shadcn-ui.js'
import OptionPicker from '../shared/components/OptionPicker.vue'
defineProps({condition:{type:Object,required:true},disabled:Boolean})
const operators=[{value:'equals',label:'等于'},{value:'not_equals',label:'不等于'},{value:'greater',label:'大于'},{value:'less',label:'小于'},{value:'exists',label:'存在且非空值'}]
const types=[{value:'string',label:'字符串'},{value:'number',label:'数字'},{value:'boolean',label:'布尔值'},{value:'null',label:'空值'}]
</script>
<template><div class="grid gap-4 sm:grid-cols-2"><Field><FieldLabel>条件变量</FieldLabel><Input v-model="condition.variable" :disabled="disabled" placeholder="step.order_status 或 data.amount" aria-label="条件变量"/></Field><Field><FieldLabel>判断方式</FieldLabel><OptionPicker v-model="condition.operator" :disabled="disabled" :options="operators" aria-label="条件判断方式"/></Field><template v-if="condition.operator!=='exists'"><Field><FieldLabel>期望值类型</FieldLabel><OptionPicker v-model="condition.expected.type" :disabled="disabled" :options="types" aria-label="条件期望类型"/></Field><Field><FieldLabel>期望值</FieldLabel><Input v-model="condition.expected.value" :disabled="disabled" placeholder="按选定类型填写" aria-label="条件期望值"/></Field></template></div></template>
