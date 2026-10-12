<script setup>
import { computed, ref, watch } from 'vue'
import { ElButton, ElConfigProvider, ElDatePicker, ElPopover } from 'element-plus'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import DateFilter from './DateFilter.vue'
import { historyDateRange, validDateBounds } from '../dateFilters.js'
import { useDatePopup } from '../composables/datePopup.js'
const props = defineProps({
  start: { type: String, default: '' }, end: { type: String, default: '' },
  type: { type: String, default: 'datetime' }, label: { type: String, default: '日期范围' },
  width: { type: String, default: '360px' }, disabled: Boolean,
})
const emit = defineEmits(['update:start', 'update:end', 'change'])
const { picker, dateVisible } = useDatePopup()
const advanced = ref(false), draftStart = ref(''), draftEnd = ref('')
function apply(start = '', end = '') {
  emit('update:start', start || '')
  emit('update:end', end || '')
  emit('change', [start || '', end || ''])
}
const range = computed({
  get: () => props.start && props.end ? [props.start, props.end] : null,
  set: value => apply(value?.[0], value?.[1]),
})
const partial = computed(() => !!props.start !== !!props.end)
const valid = computed(() => validDateBounds(draftStart.value, draftEnd.value))
const short = value => value.replace('T', ' ').replace(/:00$/, '')
const shortcuts = [1, 7, 30].map(days => ({
  text: days === 1 ? '今天' : '最近' + days + '天',
  value: () => historyDateRange(days, props.type),
}))
watch(advanced, visible => { if (visible) { draftStart.value = props.start; draftEnd.value = props.end } })
function applyAdvanced() { if (valid.value) { apply(draftStart.value, draftEnd.value); advanced.value = false } }
</script>
<template>
  <ElConfigProvider :locale="zhCn">
    <div class="compact-date-filter" :style="{width}" :aria-label="label">
      <span class="date-filter-label">{{label}}</span>
      <ElDatePicker ref="picker" v-model="range" :type="type==='date'?'daterange':'datetimerange'" :shortcuts="shortcuts" @visible-change="dateVisible"
        :format="type==='date'?'YYYY-MM-DD':'MM-DD HH:mm'" :value-format="type==='date'?'YYYY-MM-DD':'YYYY-MM-DDTHH:mm:ss'"
        :start-placeholder="partial&&start?'自 '+short(start):'不限开始'" :end-placeholder="partial&&end?'至 '+short(end):'不限结束'"
        :disabled="disabled" range-separator="→" clearable popper-class="platform-date-popper" />
      <button v-if="partial" type="button" class="clear-partial" :disabled="disabled" :aria-label="'清空'+label" title="清空日期筛选" @click="apply()">×</button>
      <ElPopover v-model:visible="advanced" trigger="click" placement="bottom-end" :width="330" popper-class="date-bounds-popover" :disabled="disabled">
        <template #reference><button type="button" class="date-advanced" :disabled="disabled" :aria-label="label+'单侧时间筛选'" title="仅限制开始或结束时间" :aria-expanded="advanced">单侧</button></template>
        <div class="date-bounds" :aria-label="label+'单侧时间'" @keydown.esc.stop.prevent="advanced=false">
          <label><span>开始</span><DateFilter v-model="draftStart" :type="type" placeholder="不限开始时间"/></label>
          <label><span>结束</span><DateFilter v-model="draftEnd" :type="type" placeholder="不限结束时间"/></label>
          <small>可仅填写一侧；清空两侧表示不限时间。</small>
          <p v-if="!valid" role="alert">结束时间不能早于开始时间。</p>
          <div class="date-bounds-actions"><ElButton size="small" @click="advanced=false">取消</ElButton><ElButton type="primary" size="small" :disabled="!valid" @click="applyAdvanced">应用时间筛选</ElButton></div>
        </div>
      </ElPopover>
    </div>
  </ElConfigProvider>
</template>
<style scoped>
.compact-date-filter{display:inline-flex;align-items:center;gap:6px;max-width:100%;min-width:0;flex-shrink:0;height:32px}
.date-filter-label{font-size:12px;color:var(--muted);white-space:nowrap;flex-shrink:0}
.compact-date-filter :deep(.el-date-editor){flex:1;width:0!important;min-width:0;height:32px}
.compact-date-filter :deep(.el-range-input){font-size:12px;min-width:0}.compact-date-filter :deep(.el-range-separator){padding:0 3px;flex-shrink:0}
.date-advanced,.clear-partial{border:0;background:transparent;color:var(--muted);cursor:pointer;font:inherit;font-size:12px;padding:5px 2px;white-space:nowrap;flex-shrink:0}
.date-advanced:hover,.clear-partial:hover{color:var(--brand)}.clear-partial{font-size:18px}
.date-bounds{display:grid;gap:12px}.date-bounds label{display:flex;align-items:center;gap:10px}.date-bounds label>span{white-space:nowrap;flex-shrink:0;font-size:12px}
.date-bounds small{color:var(--muted);font-size:12px}.date-bounds p{margin:0;color:var(--error);font-size:12px}.date-bounds-actions{display:flex;justify-content:flex-end;gap:8px}
:focus-visible{outline:2px solid var(--brand);outline-offset:2px}
</style>
