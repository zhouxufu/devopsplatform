<script setup>
import {ref,computed} from 'vue'
import {CalendarDate} from '@internationalized/date'
import {CalendarIcon} from '@lucide/vue'
import {RangeCalendar} from '@/components/ui/range-calendar'
const props=defineProps({modelValue:Array}),emit=defineEmits(['update:modelValue','change'])
const open=ref(false),range=ref({}),startTime=ref('00:00:00'),endTime=ref('23:59:59'),error=ref('')
const formatter=new Intl.DateTimeFormat('zh-CN',{month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit',hour12:false})
const label=computed(()=>props.modelValue?.length===2?props.modelValue.map(t=>formatter.format(new Date(t))).join(' → '):'选择时间范围')
function initialize(value){open.value=value;if(!value)return;error.value='';const dates=(props.modelValue||[Date.now()-900000,Date.now()]).map(t=>new Date(t));range.value={start:new CalendarDate(dates[0].getFullYear(),dates[0].getMonth()+1,dates[0].getDate()),end:new CalendarDate(dates[1].getFullYear(),dates[1].getMonth()+1,dates[1].getDate())};startTime.value=dates[0].toTimeString().slice(0,8);endTime.value=dates[1].toTimeString().slice(0,8)}
function timestamp(day,time){if(!day||!/^\d{2}:\d{2}(:\d{2})?$/.test(time))return NaN;const [h,m,s=0]=time.split(':').map(Number);if(h>23||m>59||s>59)return NaN;return new Date(day.year,day.month-1,day.day,h,m,s).getTime()}
function apply(){const value=[timestamp(range.value.start,startTime.value),timestamp(range.value.end,endTime.value)];if(value.some(t=>!Number.isFinite(t))||value[1]<=value[0]){error.value='请选择完整时间范围，结束时间需晚于开始时间';return}emit('update:modelValue',value);emit('change',value);open.value=false}

import {Button,Field,FieldLabel,Input,Popover,PopoverContent,PopoverTrigger} from '../shared/shadcn-ui.js'
</script>
<template><Popover :open="open" @update:open="initialize"><PopoverTrigger as-child><Button variant="outline" class="h-9 min-w-0 justify-start font-normal" aria-label="当前工作区时间范围"><CalendarIcon class="size-4"/><span class="truncate">{{label}}</span></Button></PopoverTrigger><PopoverContent class="w-auto max-w-[calc(100vw-2rem)] p-3" align="end"><RangeCalendar v-model="range" locale="zh-CN" :number-of-months="1"/><div class="grid grid-cols-2 gap-3 border-t pt-3"><Field><FieldLabel for="range-start-time">开始时间</FieldLabel><Input id="range-start-time" v-model="startTime" type="time" step="1"/></Field><Field><FieldLabel for="range-end-time">结束时间</FieldLabel><Input id="range-end-time" v-model="endTime" type="time" step="1"/></Field></div><p v-if="error" role="alert" class="mt-2 max-w-72 text-xs text-destructive">{{error}}</p><div class="mt-3 flex justify-end gap-2"><Button variant="outline" size="sm" @click="open=false">取消</Button><Button size="sm" @click="apply">应用时间范围</Button></div></PopoverContent></Popover></template>
