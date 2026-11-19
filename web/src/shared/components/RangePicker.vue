<script setup>
import {ref,computed,useId} from 'vue'
import {CalendarDate} from '@internationalized/date'
import {CalendarIcon} from '@lucide/vue'
import {RangeCalendar} from '@/components/ui/range-calendar'
const props=defineProps({modelValue:Array,width:{type:String,default:'280px'},size:String,label:{type:String,default:'时间范围'}}),emit=defineEmits(['update:modelValue','change'])
const open=ref(false),range=ref({}),startTime=ref('00:00:00'),endTime=ref('23:59:59'),error=ref(''),uid=useId()
const formatter=new Intl.DateTimeFormat('zh-CN',{month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit',hour12:false})
const valueLabel=computed(()=>props.modelValue?.length===2?props.modelValue.map(t=>formatter.format(new Date(t))).join(' → '):'不限时间')
function initialize(value){open.value=value;if(!value)return;error.value='';const dates=(props.modelValue||[Date.now()-900000,Date.now()]).map(t=>new Date(t));range.value={start:new CalendarDate(dates[0].getFullYear(),dates[0].getMonth()+1,dates[0].getDate()),end:new CalendarDate(dates[1].getFullYear(),dates[1].getMonth()+1,dates[1].getDate())};startTime.value=dates[0].toTimeString().slice(0,8);endTime.value=dates[1].toTimeString().slice(0,8)}
function timestamp(day,time){if(!day||!/^\d{2}:\d{2}(:\d{2})?$/.test(time))return NaN;const [h,m,s=0]=time.split(':').map(Number);if(h>23||m>59||s>59)return NaN;return new Date(day.year,day.month-1,day.day,h,m,s).getTime()}
function update(value){emit('update:modelValue',value);emit('change',value);open.value=false}
function apply(){const value=[timestamp(range.value.start,startTime.value),timestamp(range.value.end,endTime.value)];if(value.some(t=>!Number.isFinite(t))||value[1]<value[0]){error.value='请选择完整范围，结束时间不能早于开始时间';return}update(value)}
function recent(minutes){update([Date.now()-minutes*60000,Date.now()])}


import {Button,Field,FieldLabel,Input,Popover,PopoverContent,PopoverTrigger} from '../shadcn-ui.js'
</script>
<template><Popover :open="open" @update:open="initialize"><PopoverTrigger as-child><Button variant="outline" class="max-w-full min-w-0 justify-start font-normal" :style="{width}" :aria-label="label"><CalendarIcon class="size-4"/><span class="truncate">{{valueLabel}}</span></Button></PopoverTrigger><PopoverContent class="w-auto max-w-[calc(100vw-2rem)] p-3" align="end"><div class="mb-3 flex max-w-72 flex-wrap gap-1"><Button v-for="minutes in [15,60,360,1440,4320,10080]" :key="minutes" variant="ghost" size="xs" @click="recent(minutes)">{{minutes<60?'最近15分钟':minutes<1440?'最近'+minutes/60+'小时':'最近'+minutes/1440+'天'}}</Button></div><RangeCalendar v-model="range" locale="zh-CN" :number-of-months="1"/><div class="grid grid-cols-2 gap-3 border-t pt-3"><Field><FieldLabel :for="uid+'-start'">开始时间</FieldLabel><Input :id="uid+'-start'" v-model="startTime" type="time" step="1"/></Field><Field><FieldLabel :for="uid+'-end'">结束时间</FieldLabel><Input :id="uid+'-end'" v-model="endTime" type="time" step="1"/></Field></div><p v-if="error" role="alert" class="mt-2 max-w-72 text-xs text-destructive">{{error}}</p><div class="mt-3 flex justify-end gap-2"><Button variant="ghost" size="sm" @click="update(null)">清空</Button><Button variant="outline" size="sm" @click="open=false">取消</Button><Button size="sm" @click="apply">应用</Button></div></PopoverContent></Popover></template>
