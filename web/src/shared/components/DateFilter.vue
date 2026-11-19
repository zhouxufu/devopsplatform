<script setup>
import {ref,computed,useId} from 'vue'
import {CalendarDate} from '@internationalized/date'
import {Calendar} from '@/components/ui/calendar'
import {CalendarIcon} from '@lucide/vue'
const props=defineProps({modelValue:{type:String,default:''},type:{type:String,default:'date'},placeholder:{type:String,default:'选择日期'},disabled:Boolean})
const emit=defineEmits(['update:modelValue','change'])
const open=ref(false),day=ref(),time=ref('00:00:00'),uid=useId()
const label=computed(()=>props.modelValue?.replace('T',' ')||props.placeholder)
function initialize(value){open.value=value;if(!value)return;const date=new Date(props.modelValue||Date.now());day.value=new CalendarDate(date.getFullYear(),date.getMonth()+1,date.getDate());time.value=props.modelValue?.split('T')[1]||'00:00:00'}
function apply(){const value=day.value?day.value.toString()+(props.type==='datetime'?'T'+(time.value.length===5?time.value+':00':time.value):''):'';emit('update:modelValue',value);emit('change',value);open.value=false}
function clear(){day.value=undefined;apply()}


import {Button,Field,FieldLabel,Input,Popover,PopoverContent,PopoverTrigger} from '../shadcn-ui.js'
</script>
<template><Popover :open="open" @update:open="initialize"><PopoverTrigger as-child><Button variant="outline" :disabled="disabled" class="w-full min-w-0 justify-start font-normal"><CalendarIcon class="size-4"/><span class="truncate" :class="{'text-muted-foreground':!modelValue}">{{label}}</span></Button></PopoverTrigger><PopoverContent class="w-auto max-w-[calc(100vw-2rem)] p-3"><Calendar v-model="day" locale="zh-CN"/><Field v-if="type==='datetime'" class="mt-3"><FieldLabel :for="uid">时间</FieldLabel><Input :id="uid" v-model="time" type="time" step="1"/></Field><div class="mt-3 flex justify-end gap-2"><Button variant="ghost" size="sm" @click="clear">清空</Button><Button variant="outline" size="sm" @click="open=false">取消</Button><Button size="sm" :disabled="!day" @click="apply">确定</Button></div></PopoverContent></Popover></template>
