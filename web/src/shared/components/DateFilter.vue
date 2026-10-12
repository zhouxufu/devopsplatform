<script setup>
import { computed } from 'vue'
import { ElConfigProvider, ElDatePicker } from 'element-plus'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import { useDatePopup } from '../composables/datePopup.js'
import 'element-plus/dist/index.css'
import 'element-plus/theme-chalk/dark/css-vars.css'

// Keep the API contract as a local date string. Clearing means no boundary,
// not today's date, and does not convert a day baseline into a UTC timestamp.
const props = defineProps({ modelValue: { type: String, default: '' }, type: { type: String, default: 'date' }, placeholder: { type: String, default: '选择日期' }, disabled: Boolean })
const emit = defineEmits(['update:modelValue', 'change'])
const { picker, dateVisible } = useDatePopup()
const value = computed({ get: () => props.modelValue || '', set: v => emit('update:modelValue', v || '') })
</script>
<template>
  <ElConfigProvider :locale="zhCn">
    <ElDatePicker ref="picker" v-model="value" :type="type" :disabled="disabled" :placeholder="placeholder" clearable popper-class="platform-date-popper" @visible-change="dateVisible"
      :format="type==='datetime' ? 'YYYY-MM-DD HH:mm:ss' : 'YYYY-MM-DD'"
      :value-format="type==='datetime' ? 'YYYY-MM-DDTHH:mm:ss' : 'YYYY-MM-DD'"
      style="width:100%;min-width:0" @change="emit('change', $event || '')" />
  </ElConfigProvider>
</template>
