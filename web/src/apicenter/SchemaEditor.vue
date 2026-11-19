<script setup>
import {computed,ref,nextTick,watch,inject,onBeforeUnmount} from 'vue'
import SchemaEditor from './SchemaEditor.vue'
import {resolveLocal,formatValue,parseValue} from './documentEditModel.js'
import {toast} from '../shared/composables/toast.js'
import OptionPicker from '../shared/components/OptionPicker.vue'
import {Accordion,AccordionItem,AccordionTrigger,AccordionContent,Button,Checkbox,Field,FieldLabel,Input,Textarea} from '../shared/shadcn-ui.js'
const props=defineProps({modelValue:{type:Object,default:()=>({})},document:Object,label:{type:String,default:'字段'},depth:{type:Number,default:0}}),emit=defineEmits(['update:modelValue'])
const root=ref(null),opened=ref({}),exampleDraft=ref(formatValue(props.modelValue.example)),examplePending=ref(false),exampleInvalid=ref(false),pendingKey=Symbol('example'),reportPending=inject('apiDocumentPending',()=>{})
watch(()=>props.modelValue.example,v=>{if(!examplePending.value)exampleDraft.value=formatValue(v)})
onBeforeUnmount(()=>reportPending(pendingKey,false))
function draftExample(value){exampleDraft.value=value;examplePending.value=true;reportPending(pendingKey,true)}
const types=['object','array','string','integer','number','boolean'].map(value=>({value,label:({object:'对象',array:'数组',string:'字符串',integer:'整数',number:'数字',boolean:'布尔值'})[value]}))
const kind=computed(()=>props.modelValue.type||(props.modelValue.properties?'object':props.modelValue.items?'array':'string'))
function patch(key,value){emit('update:modelValue',{...props.modelValue,[key]:value})}
function materialize(){try{emit('update:modelValue',resolveLocal(props.modelValue,props.document))}catch(e){toast(e.message,'error')}}
function child(name,value){patch('properties',{...props.modelValue.properties,[name]:value})}
function required(name,on){patch('required',on?[...new Set([...(props.modelValue.required||[]),name])]:(props.modelValue.required||[]).filter(n=>n!==name))}
function add(){let name='field';let i=1;while(Object.hasOwn(props.modelValue.properties||{},name))name='field'+i++;child(name,{type:'string'});opened.value[name]=name;nextTick(()=>{const input=[...(root.value?.querySelectorAll('input')||[])].find(e=>e.getAttribute('aria-label')==='字段名 '+name&&e.closest('[data-schema-editor]')===root.value);input?.focus();input?.select()})}
function remove(name){const next={...props.modelValue.properties};delete next[name];emit('update:modelValue',{...props.modelValue,properties:next,required:(props.modelValue.required||[]).filter(n=>n!==name)})}
function rename(name,event){const nextName=event.target.value.trim();if(!nextName||nextName!==name&&Object.hasOwn(props.modelValue.properties,nextName)){event.target.value=name;toast('字段名不能为空或重复','error');return}const next={};for(const [k,v]of Object.entries(props.modelValue.properties))next[k===name?nextName:k]=v;emit('update:modelValue',{...props.modelValue,properties:next,required:(props.modelValue.required||[]).map(n=>n===name?nextName:n)})}
function example(event){try{const next={...props.modelValue};if(exampleDraft.value.trim())next.example=parseValue(exampleDraft.value);else delete next.example;examplePending.value=false;exampleInvalid.value=false;reportPending(pendingKey,false);emit('update:modelValue',next)}catch{exampleInvalid.value=true;toast('示例需要有效 JSON；字符串请加双引号','error');event.target.focus()}}
</script>
<template>
 <div ref="root" data-schema-editor class="min-w-0 space-y-3" :aria-label="label+'结构编辑'">
  <div v-if="modelValue.$ref" class="flex flex-wrap items-center gap-2 rounded-md border p-3"><code class="min-w-0 flex-1 break-all text-xs">{{modelValue.$ref}}</code><Button variant="outline" size="sm" @click="materialize">编辑当前接口副本</Button></div>
  <template v-else-if="depth<16">
   <div class="grid min-w-0 gap-3 sm:grid-cols-[10rem_minmax(0,1fr)]"><Field><FieldLabel>类型</FieldLabel><OptionPicker :model-value="kind" :options="types" :aria-label="label+'类型'" @update:model-value="patch('type',$event)"/></Field><Field><FieldLabel>说明</FieldLabel><Input :model-value="modelValue.description||''" :aria-label="label+'字段说明'" @update:model-value="patch('description',$event)"/></Field></div>
   <template v-if="kind==='object'"><div v-for="(value,name) in modelValue.properties||{}" :key="name" class="min-w-0 rounded-md border px-3"><Accordion type="single" collapsible :model-value="opened[name]||''" @update:model-value="opened[name]=$event"><AccordionItem :value="name"><div class="flex min-w-0 items-center gap-2"><AccordionTrigger class="min-w-0 flex-1 truncate">{{name}} · {{value.type|| (value.$ref?'引用':'对象')}}</AccordionTrigger><Button variant="ghost" size="sm" :aria-label="'移除字段 '+name" @click="remove(name)">移除</Button></div><AccordionContent class="space-y-3"><div class="flex min-w-0 flex-wrap items-center gap-3"><Field class="min-w-0 flex-1"><FieldLabel>字段名</FieldLabel><Input :model-value="name" :aria-label="'字段名 '+name" @change="rename(name,$event)"/></Field><label class="flex items-center gap-2 text-sm"><Checkbox :model-value="(modelValue.required||[]).includes(name)" @update:model-value="required(name,$event)"/>必填</label></div><SchemaEditor :model-value="value" :document="document" :label="label+'.'+name" :depth="depth+1" @update:model-value="child(name,$event)"/></AccordionContent></AccordionItem></Accordion></div><Button variant="outline" size="sm" :aria-label="'添加'+label+'字段'" @click="add">＋ 添加字段</Button></template>
   <div v-if="kind==='array'" class="min-w-0 border-l-2 pl-3"><p class="mb-3 text-sm font-medium">数组元素</p><SchemaEditor :model-value="modelValue.items||{type:'string'}" :document="document" :label="label+'[]'" :depth="depth+1" @update:model-value="patch('items',$event)"/></div>
   <Accordion type="single" collapsible><AccordionItem value="example"><AccordionTrigger>示例与复杂定义</AccordionTrigger><AccordionContent class="space-y-2"><Field><FieldLabel>JSON 示例</FieldLabel><Textarea :model-value="exampleDraft" @update:model-value="draftExample" :aria-invalid="exampleInvalid" :data-example-pending="examplePending" rows="3" :aria-label="label+'示例'" spellcheck="false" class="font-mono text-xs" @change="example"/></Field><p v-if="modelValue.allOf||modelValue.oneOf||modelValue.anyOf" class="text-xs text-muted-foreground">组合结构保留原定义，可在高级 JSON 中调整。</p></AccordionContent></AccordionItem></Accordion>
  </template><p v-else class="text-sm text-muted-foreground">更深结构请使用高级 JSON。</p>
 </div>
</template>
