<script setup>
import { computed, ref, watch } from 'vue'
import { ElButton, ElConfigProvider, ElPopover, ElIcon } from 'element-plus'
import { Clock } from '@element-plus/icons-vue'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import DateFilter from './DateFilter.vue'
import { historyDateRange, validDateBounds, localDateValue } from '../dateFilters.js'
const props=defineProps({start:{type:String,default:''},end:{type:String,default:''},type:{type:String,default:'datetime'},label:{type:String,default:'日期范围'},width:{type:String,default:'360px'},disabled:Boolean})
const emit=defineEmits(['update:start','update:end','change'])
const opened=ref(false),draftStart=ref(''),draftEnd=ref('')
const valid=computed(()=>validDateBounds(draftStart.value,draftEnd.value))
const hasValue=computed(()=>!!(props.start||props.end))
const short=value=>value.replace('T',' ').replace(/:00$/,'')
watch(opened,value=>{if(value){draftStart.value=props.start;draftEnd.value=props.end}})
function apply(start='',end=''){
  if(props.disabled||!validDateBounds(start,end))return
  emit('update:start',start||'');emit('update:end',end||'');emit('change',[start||'',end||'']);opened.value=false
}
function quick(days){const dates=historyDateRange(days,props.type);apply(...dates.map(date=>localDateValue(date,props.type)))}
</script>
<template>
 <ElConfigProvider :locale="zhCn">
  <div class="compact-date-filter" :style="{width}" :aria-label="label">
   <span class="date-filter-label">{{label}}</span>
   <ElPopover v-model:visible="opened" trigger="click" placement="bottom-end" :width="350" popper-class="date-bounds-popover" :disabled="disabled">
    <template #reference>
     <button type="button" class="date-range-trigger" :disabled="disabled" :aria-label="'选择'+label" :aria-expanded="opened" aria-haspopup="dialog" :title="(start||'不限开始')+' → '+(end||'不限结束')">
      <ElIcon><Clock/></ElIcon><span :class="{empty:!start}">{{start?short(start):'不限开始'}}</span><span class="date-arrow">→</span><span :class="{empty:!end}">{{end?short(end):'不限结束'}}</span>
     </button>
    </template>
    <div class="date-bounds" role="dialog" :aria-label="label+'筛选'" @keydown.esc.stop.prevent="opened=false">
     <div class="date-shortcuts"><ElButton v-for="days in [1,7,30]" :key="days" size="small" @click="quick(days)">{{days===1?'今天':'最近'+days+'天'}}</ElButton></div>
     <label><span>开始</span><DateFilter v-model="draftStart" :type="type" placeholder="不限开始时间"/></label>
     <label><span>结束</span><DateFilter v-model="draftEnd" :type="type" placeholder="不限结束时间"/></label>
     <small>任意一侧可留空；两侧留空表示不限时间。</small>
     <p v-if="!valid" role="alert">结束时间不能早于开始时间。</p>
     <div class="date-bounds-actions"><ElButton size="small" @click="opened=false">取消</ElButton><ElButton type="primary" size="small" :disabled="!valid" @click="apply(draftStart,draftEnd)">应用时间筛选</ElButton></div>
    </div>
   </ElPopover>
   <button v-if="hasValue" type="button" class="clear-date" :disabled="disabled" :aria-label="'清空'+label" title="清空日期筛选" @click="apply()">×</button>
  </div>
 </ElConfigProvider>
</template>
<style scoped>
.compact-date-filter{display:inline-flex;align-items:center;gap:6px;max-width:100%;min-width:0;flex-shrink:0;height:32px}.date-filter-label{font-size:12px;color:var(--muted);white-space:nowrap;flex-shrink:0}
.date-range-trigger{display:flex;align-items:center;gap:6px;flex:1;min-width:0;height:32px;border:1px solid var(--border);border-radius:8px;background:var(--input-bg);color:var(--text);cursor:pointer;padding:0 9px;font:inherit;font-size:12px}.date-range-trigger>span:not(.date-arrow){flex:1;min-width:0;text-align:center;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}.date-range-trigger .empty{color:var(--muted)}.date-range-trigger .el-icon{flex-shrink:0;color:var(--muted)}.date-arrow{flex-shrink:0}.date-range-trigger:disabled,.clear-date:disabled{cursor:not-allowed;opacity:.6}.date-range-trigger:hover:not(:disabled){border-color:var(--brand)}
.clear-date{border:0;background:transparent;color:var(--muted);cursor:pointer;font:inherit;font-size:18px;padding:5px 2px;flex-shrink:0}.clear-date:hover{color:var(--brand)}
.date-bounds{display:grid;gap:12px}.date-bounds label{display:flex;align-items:center;gap:10px}.date-bounds label>span{white-space:nowrap;flex-shrink:0;font-size:12px}.date-shortcuts{display:flex;gap:8px}.date-bounds small{color:var(--muted);font-size:12px}.date-bounds p{margin:0;color:var(--error);font-size:12px}.date-bounds-actions{display:flex;justify-content:flex-end;gap:8px}:focus-visible{outline:2px solid var(--brand);outline-offset:2px}
</style>
