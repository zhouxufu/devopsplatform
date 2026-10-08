<script setup>
// 统一下拉：ElSelect 包装。options 支持 string[] 或 {value,label}[]；
// 单选 allLabel 自动补 value='' 的首项；多选清空为 []，表示不限。
import { computed } from 'vue'
import { ElSelect, ElOption } from 'element-plus'
import 'element-plus/dist/index.css'
import 'element-plus/theme-chalk/dark/css-vars.css'
const props = defineProps({
  modelValue: { type: [String, Number, Array], default: '' },
  multiple: { type: Boolean, default: false },
  options: { type: Array, default: () => [] },
  allLabel: { type: String, default: '' },
  placeholder: { type: String, default: '' },
  filterable: { type: Boolean, default: false },
  width: { type: String, default: '140px' },
  disabled: { type: Boolean, default: false },
})
const emit = defineEmits(['update:modelValue', 'change'])
const normalizedModelValue = computed(() => props.multiple
  ? (Array.isArray(props.modelValue) ? props.modelValue : [])
  : (props.modelValue ?? ''))
const all = computed(() => {
  const list = (Array.isArray(props.options) ? props.options : []).map(o => (typeof o === 'object' && o !== null ? o : { value: o, label: String(o) }))
  return props.allLabel && !props.multiple ? [{ value: '', label: props.allLabel }, ...list] : list
})
function update(value) {
  const next = props.multiple ? (Array.isArray(value) ? value : []) : (value ?? '')
  emit('update:modelValue', next)
  emit('change', next)
}
</script>
<template>
  <ElSelect :model-value="normalizedModelValue" :clearable="!!allLabel" :filterable="filterable" :disabled="disabled" :placeholder="placeholder || allLabel || '请选择'"
    :multiple="multiple" :collapse-tags="multiple" :collapse-tags-tooltip="multiple" :max-collapse-tags="2"
    :style="{ width }" size="default"
    @update:model-value="update">
    <ElOption v-for="o in all" :key="String(o.value)" :value="o.value" :label="o.label" />
  </ElSelect>
</template>
