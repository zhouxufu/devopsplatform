<script setup>
// 统一时间范围选择器：Element Plus datetimerange + 常用快捷项
// 对外契约：v-model = [startMs, endMs]（毫秒时间戳）；null/[] = 不限（全部）
import { computed } from 'vue'
import { ElConfigProvider, ElDatePicker } from 'element-plus'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import { useDatePopup } from '../composables/datePopup.js'
import 'element-plus/dist/index.css'
import 'element-plus/theme-chalk/dark/css-vars.css'

const props = defineProps({
  modelValue: { type: Array, default: null },
  size: { type: String, default: 'small' },
  width: { type: String, default: '230px' },
})
const emit = defineEmits(['update:modelValue', 'change'])
const { picker, dateVisible } = useDatePopup()

const inner = computed({
  get: () => (props.modelValue && props.modelValue.length === 2 ? [new Date(props.modelValue[0]), new Date(props.modelValue[1])] : null),
  set: v => {
    const ms = v && v.length === 2 ? [v[0].getTime(), v[1].getTime()] : null
    emit('update:modelValue', ms)
    emit('change', ms)
  },
})

const ago = m => Date.now() - m * 60000
const shortcuts = [
  { text: '最近15分钟', value: () => [new Date(ago(15)), new Date()] },
  { text: '最近1小时', value: () => [new Date(ago(60)), new Date()] },
  { text: '最近6小时', value: () => [new Date(ago(360)), new Date()] },
  { text: '最近24小时', value: () => [new Date(ago(1440)), new Date()] },
  { text: '最近3天', value: () => [new Date(ago(3 * 1440)), new Date()] },
  { text: '最近7天', value: () => [new Date(ago(7 * 1440)), new Date()] },
  {
    text: '今天',
    value: () => {
      const d = new Date()
      d.setHours(0, 0, 0, 0)
      return [d, new Date()]
    },
  },
  {
    text: '昨天',
    value: () => {
      const d = new Date()
      d.setHours(0, 0, 0, 0)
      return [new Date(d.getTime() - 86400000), d]
    },
  },
]
</script>
<template>
  <ElConfigProvider :locale="zhCn">
    <ElDatePicker ref="picker" v-model="inner" type="datetimerange" :shortcuts="shortcuts" range-separator="→" popper-class="platform-date-popper" @visible-change="dateVisible"
      start-placeholder="不限开始" end-placeholder="不限结束" :size="size" :clearable="true"
      format="MM-DD HH:mm" date-format="MM-DD" time-format="HH:mm"
      :style="{ width, '--el-date-editor-datetime-range-width': width }" />
  </ElConfigProvider>
</template>
<style scoped>
:deep(.el-range-input) { font-size: 11px }
:deep(.el-range-separator) { font-size: 11px }
</style>
