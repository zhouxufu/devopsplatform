<script setup>
// Business-value normalization around the official Combobox. No popup/list implementation.
import {computed,ref,useId,watch} from 'vue'
import {CheckIcon,ChevronsUpDownIcon} from '@lucide/vue'
import {Combobox,ComboboxAnchor,ComboboxTrigger,ComboboxList,ComboboxInput,ComboboxEmpty,ComboboxGroup,ComboboxItem,ComboboxItemIndicator} from '@/components/ui/combobox'
import {Button} from '@/components/ui/button'
defineOptions({inheritAttrs:false})
const props=defineProps({modelValue:[String,Number,Boolean,Array],options:{type:Array,default:()=>[]},retainMissing:{type:Boolean,default:true},multiple:Boolean,disabled:Boolean,loading:Boolean,placeholder:{type:String,default:'请选择'},allLabel:String,allowCreate:Boolean,remoteSearch:Boolean,id:String})
const emit=defineEmits(['update:modelValue','change','open','open-change','search'])
const open=ref(false),query=ref(''),trigger=ref(null),uid=useId()
watch(query,value=>emit('search',value))
defineExpose({focus:()=>trigger.value?.$el?.focus()})
const values=computed(()=>props.multiple?(props.modelValue||[]):[props.modelValue??''])
const options=computed(()=>{const list=props.options.map(o=>typeof o==='object'?o:{value:o,label:String(o)});if(props.allLabel&&!props.multiple&&!list.some(o=>o.value===''))list.unshift({value:'',label:props.allLabel});if(props.retainMissing)for(const value of values.value)if(value!==''&&!list.some(o=>o.value===value))list.push({value,label:String(value)});return list})
const selected=computed(()=>props.multiple?options.value.filter(o=>values.value.includes(o.value)):options.value.find(o=>o.value===(props.modelValue??'')))
const created=computed(()=>props.allowCreate&&query.value.trim()&&!options.value.some(o=>o.value===query.value.trim())?{value:query.value.trim(),label:query.value.trim()}:null)
function update(value){const next=props.multiple?(value||[]).map(o=>o.value):value?.value??'';emit('update:modelValue',next);emit('change',next);if(!props.multiple)open.value=false}
</script>
<template>
 <Combobox :ignore-filter="remoteSearch" :multiple="multiple" :model-value="selected" :disabled="disabled" :open="open" by="value" @update:model-value="update" @update:open="value=>{open=value;emit('open-change',value);if(value)emit('open',value)}">
  <ComboboxAnchor as-child><ComboboxTrigger as-child :aria-label="$attrs['aria-label']||(multiple?(selected.length?selected.map(o=>o.label).join('、'):placeholder):(selected?.label||placeholder))"><Button ref="trigger" :id="id||uid" type="button" variant="outline" :disabled="disabled" :aria-busy="loading" class="h-8 w-full min-w-0 justify-between font-normal" v-bind="$attrs"><span class="truncate text-left" :class="{'text-muted-foreground':!selected||(multiple&&!selected.length)}">{{multiple?(selected.length?selected.map(o=>o.label).join('、'):placeholder):(selected?.label||placeholder)}}</span><ChevronsUpDownIcon class="ml-2 size-3.5 shrink-0 opacity-50"/></Button></ComboboxTrigger></ComboboxAnchor>
  <ComboboxList><ComboboxInput v-model="query" placeholder="搜索…"/><ComboboxEmpty>没有匹配项</ComboboxEmpty><ComboboxGroup class="max-h-56 overflow-auto"><ComboboxItem v-for="option in options" :key="String(option.value)" :value="option" :disabled="option.disabled"><span class="truncate">{{option.label}}</span><ComboboxItemIndicator><CheckIcon class="ml-auto size-4"/></ComboboxItemIndicator></ComboboxItem><ComboboxItem v-if="created" :value="created">使用 {{created.label}}</ComboboxItem></ComboboxGroup></ComboboxList>
 </Combobox>
</template>
