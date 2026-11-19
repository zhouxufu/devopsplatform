<script setup>
import {computed,ref,watch} from 'vue'
import {CalendarIcon} from '@lucide/vue'
import DateFilter from './DateFilter.vue'
import {historyDateRange,validDateBounds,localDateValue} from '../dateFilters.js'
const props=defineProps({start:{type:String,default:''},end:{type:String,default:''},type:{type:String,default:'datetime'},label:{type:String,default:'日期范围'},width:{type:String,default:'360px'},disabled:Boolean})
const emit=defineEmits(['update:start','update:end','change'])
const opened=ref(false),draftStart=ref(''),draftEnd=ref('')
const valid=computed(()=>validDateBounds(draftStart.value,draftEnd.value))
watch(opened,value=>{if(value){draftStart.value=props.start;draftEnd.value=props.end}})
function apply(start='',end=''){if(props.disabled||!validDateBounds(start,end))return;emit('update:start',start);emit('update:end',end);emit('change',[start,end]);opened.value=false}
function quick(days){apply(...historyDateRange(days,props.type).map(date=>localDateValue(date,props.type)))}


import {Button,Field,FieldDescription,FieldLabel,Popover,PopoverContent,PopoverTrigger} from '../shadcn-ui.js'
</script>
<template><div class="flex min-w-0 max-w-full items-center gap-2" :style="{width}"><span class="shrink-0 text-sm text-muted-foreground">{{label}}</span><Popover v-model:open="opened"><PopoverTrigger as-child><Button variant="outline" :disabled="disabled" class="min-w-0 flex-1 justify-start font-normal" :aria-label="'选择'+label"><CalendarIcon class="size-4"/><span class="truncate">{{start?start.replace('T',' '):'不限开始'}} → {{end?end.replace('T',' '):'不限结束'}}</span></Button></PopoverTrigger><PopoverContent class="w-96 max-w-[calc(100vw-2rem)] space-y-4" align="end"><div class="flex gap-2"><Button v-for="days in [1,7,30]" :key="days" variant="outline" size="sm" @click="quick(days)">{{days===1?'今天':'最近'+days+'天'}}</Button></div><Field><FieldLabel>开始</FieldLabel><DateFilter v-model="draftStart" :type="type" placeholder="不限开始时间"/></Field><Field><FieldLabel>结束</FieldLabel><DateFilter v-model="draftEnd" :type="type" placeholder="不限结束时间"/></Field><p v-if="!valid" role="alert" class="text-sm text-destructive">结束时间不能早于开始时间</p><div class="flex justify-end gap-2"><Button variant="ghost" size="sm" @click="apply()">清空</Button><Button variant="outline" size="sm" @click="opened=false">取消</Button><Button size="sm" :disabled="!valid" @click="apply(draftStart,draftEnd)">应用</Button></div></PopoverContent></Popover></div></template>
