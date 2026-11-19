<script setup>
import {computed,nextTick,ref} from 'vue'
import MySQLDatabaseSelect from '../../shared/components/MySQLDatabaseSelect.vue'
import OptionPicker from '../../shared/components/OptionPicker.vue'
import {newResourceBinding} from '../applicationResourceDraft.js'
import {resourceCheckKey,resourceCheckLabels,resourceSourceLabels} from '../resourceExperience.js'
import {focusControl} from '../../shared/focusControl.js'
import {Accordion,AccordionContent,AccordionItem,AccordionTrigger,Badge,Button,Empty,EmptyHeader,EmptyDescription,Field,FieldLabel,Input} from '../../shared/shadcn-ui.js'
const props=defineProps({resources:Object,connections:Array,environments:Array,disabled:Boolean,catalogNacos:Boolean,minResources:{type:Number,default:0}})
const root=ref(null),panels=ref([]),environment=ref(''),kind=ref(''),query=ref(''),onlyProblems=ref(false)
const checks=computed(()=>new Map((props.resources.checks||[]).map(c=>[resourceCheckKey(c),c])))
const bindingCheck=b=>checks.value.get(resourceCheckKey(b))
const rowsFor=r=>props.resources.bindings.map((b,index)=>({b,index})).filter(({b})=>b.alias===r.alias&&(!environment.value||b.environment===environment.value))
const filtered=computed(()=>props.resources.requirements.map((r,index)=>({r,index})).filter(({r})=>{
 if(kind.value&&r.kind!==kind.value)return false
 const bindings=rowsFor(r),term=query.value.trim().toLocaleLowerCase()
 if(term&&![r.alias,r.description,...bindings.flatMap(({b})=>[b.connection,...Object.values(b.config||{})])].some(v=>String(v||'').toLocaleLowerCase().includes(term)))return false
 return !onlyProblems.value||!bindings.length||bindings.some(({b})=>['failed','stale'].includes(bindingCheck(b)?.status))
}))
function rename(r,value){const old=r.alias;r.alias=value;for(const b of props.resources.bindings)if(b.alias===old)b.alias=value}
function changeKind(r,value){r.kind=value;for(const b of props.resources.bindings)if(b.alias===r.alias){b.connection='';b.config={}}}
function changeConnection(b,value){b.connection=value;b.config={}}
function addResource(){const index=props.resources.requirements.length;props.resources.requirements.push({alias:'',kind:'mysql',required:false,description:''});panels.value=[...panels.value,String(index)];query.value='';kind.value='';onlyProblems.value=false}
function addBinding(r){const b=newResourceBinding(r,props.resources.bindings,props.connections,props.environments);if(environment.value&&!props.resources.bindings.some(v=>v.alias===r.alias&&v.environment===environment.value))b.environment=environment.value;props.resources.bindings.push(b)}
function removeResource(r){props.resources.requirements.splice(props.resources.requirements.indexOf(r),1);props.resources.bindings=props.resources.bindings.filter(b=>b.alias!==r.alias)}
function validate(){
 if(props.resources.requirements.length<props.minResources)return {selector:'.add-resource',message:'请至少添加一项资源后再保存'}
 const seen=new Set()
 for(const [i,r]of props.resources.requirements.entries()){
  const target={index:i,selector:'[data-resource-index="'+i+'"] input'}
  if(!r.alias?.trim())return {...target,message:'请填写第 '+(i+1)+' 项资源别名'}
  if(seen.has(r.alias.trim()))return {...target,message:'资源别名重复，请使用不同的别名'}
  seen.add(r.alias.trim());const environments=new Set()
  for(const [j,b]of props.resources.bindings.entries())if(b.alias===r.alias){
   const location={index:i,selector:'[data-binding-index="'+j+'"] [data-binding-field=environment] button[aria-label]'}
   if(!b.environment||environments.has(b.environment))return {...location,message:'请选择环境，同一资源的环境不可重复'}
   if(!b.connection||!props.connections.some(c=>c.name===b.connection&&c.kind===r.kind))return {...location,selector:'[data-binding-index="'+j+'"] [data-binding-field=connection] button[aria-label]',message:'请选择存在且类型匹配的资源连接'}
   if(r.kind==='mysql'&&!/^[A-Za-z_][A-Za-z0-9_]{0,63}$/.test(b.config?.database||''))return {...location,selector:'[data-binding-index="'+j+'"] .mysql-database-select button[aria-label]',message:'请为 '+r.alias+' 的 '+b.environment+' 环境选择数据库（schema）'}
   if(r.kind==='nacos'&&(!b.config?.data_id||!b.config?.group))return {...location,selector:'[data-binding-index="'+j+'"] input',message:'请填写该环境的 Data ID 和 Group'}
   environments.add(b.environment)
  }
 }
 return null
}
async function focusInvalid(problem){environment.value='';kind.value='';query.value='';onlyProblems.value=false;if(problem.index!=null)panels.value=[...new Set([...panels.value,String(problem.index)])];await nextTick();const el=root.value?.querySelector(problem.selector);el?.scrollIntoView({block:'center'});focusControl(el)}
function focusBinding(alias,env){const index=props.resources.requirements.findIndex(r=>r.alias===alias);if(index<0)return;environment.value=env;query.value='';kind.value='';onlyProblems.value=false;panels.value=[String(index)];nextTick(()=>root.value?.querySelector('[data-resource-index="'+index+'"]')?.scrollIntoView({block:'center'}))}
defineExpose({validate,focusInvalid,focusBinding})
</script>
<template>
 <section ref="root" class="min-w-0 space-y-3" aria-label="直接资源与环境绑定">
  <div class="flex flex-wrap items-center justify-between gap-2"><h3 class="text-sm font-medium">直接资源 <span class="text-muted-foreground">{{resources.requirements.length}} 项</span></h3><Button class="add-resource" size="sm" variant="outline" :disabled="disabled" @click="addResource">＋ 添加资源</Button></div>
  <div class="grid gap-2 sm:grid-cols-[minmax(0,1fr)_140px_140px_auto]"><Input v-model="query" placeholder="搜索资源、连接、库名或 Data ID" aria-label="搜索资源绑定"/><OptionPicker v-model="environment" :options="(environments||[]).map(e=>({value:e.name,label:e.display_name||e.name}))" all-label="所有环境" aria-label="筛选绑定环境"/><OptionPicker v-model="kind" :options="[{value:'mysql',label:'MySQL'},{value:'nacos',label:'Nacos'},{value:'redis',label:'Redis'}]" all-label="所有类型" aria-label="筛选资源类型"/><Button size="sm" :variant="onlyProblems?'secondary':'ghost'" :aria-pressed="onlyProblems" @click="onlyProblems=!onlyProblems">只看异常</Button></div>
  <div class="max-h-[52dvh] overflow-auto rounded-lg border px-3">
   <Accordion v-model="panels" type="multiple"><AccordionItem v-for="{r,index} in filtered" :key="index" :value="String(index)" :data-resource-index="index"><div class="flex items-center gap-2"><AccordionTrigger class="min-w-0 flex-1"><div class="flex min-w-0 flex-wrap items-center gap-2 text-left"><span class="break-words">{{r.alias||'新资源'}}</span><Badge variant="secondary">{{r.kind}}</Badge><span class="text-xs text-muted-foreground">{{rowsFor(r).length}} 个环境绑定</span><Badge v-if="rowsFor(r).some(({b})=>['failed','stale'].includes(bindingCheck(b)?.status))" variant="destructive">需核对</Badge></div></AccordionTrigger><Button variant="ghost" size="sm" :disabled="disabled" :aria-label="'删除资源 '+r.alias" @click="removeResource(r)">删除</Button></div><AccordionContent class="space-y-4">
    <div class="grid gap-3 sm:grid-cols-2"><Field><FieldLabel>类型</FieldLabel><OptionPicker :model-value="r.kind" :options="[{value:'mysql',label:'MySQL'},{value:'nacos',label:'Nacos'},{value:'redis',label:'Redis'}]" :disabled="disabled" @update:model-value="value=>changeKind(r,value)"/></Field><Field><FieldLabel>逻辑资源别名</FieldLabel><Input :model-value="r.alias" :disabled="disabled" placeholder="例如 estate_bus" @update:model-value="value=>rename(r,value)"/></Field></div>
    <Field><FieldLabel>用途（可选）</FieldLabel><Input v-model="r.description" :disabled="disabled"/></Field>
    <div v-if="Object.keys(r.sources||{}).length" class="space-y-2 rounded-md bg-muted/30 p-3 text-xs"><p class="font-medium">识别来源与环境使用</p><p v-for="(source,env) in r.sources" :key="env" class="break-all">{{environments.find(e=>e.name===env)?.display_name||env}}：{{resourceSourceLabels[source.state]||source.state}} · {{source.file||'来源未登记'}}<span v-if="source.runtime_name"> · 运行名 {{source.runtime_name}}</span><span v-if="source.profile"> · profile {{source.profile}}</span></p></div>
    <div v-for="{b,index:bindingIndex} in rowsFor(r)" :key="bindingIndex" :data-binding-index="bindingIndex" class="space-y-3 rounded-lg border bg-muted/20 p-3">
     <div class="grid items-end gap-3 sm:grid-cols-[140px_minmax(0,1fr)_auto]"><Field data-binding-field="environment"><FieldLabel>环境</FieldLabel><OptionPicker v-model="b.environment" :options="(environments||[]).map(e=>({value:e.name,label:e.display_name||e.name}))" :disabled="disabled" placeholder="选择环境" :aria-label="r.alias+' 环境'"/></Field><Field data-binding-field="connection"><FieldLabel>连接</FieldLabel><OptionPicker :model-value="b.connection" :options="(connections||[]).filter(c=>c.kind===r.kind).map(c=>({value:c.name,label:c.name+' · '+c.endpoint}))" :disabled="disabled" placeholder="选择连接" :aria-label="r.alias+' '+b.environment+' 连接'" @update:model-value="value=>changeConnection(b,value)"/></Field><Button variant="ghost" size="sm" :disabled="disabled" @click="resources.bindings.splice(bindingIndex,1)">移除绑定</Button></div>
     <Field v-if="r.kind==='mysql'"><FieldLabel>数据库（schema）</FieldLabel><MySQLDatabaseSelect :model-value="b.config?.database||''" :connection="b.connection" :disabled="disabled" required @update:model-value="value=>b.config={...(b.config||{}),database:value}"/></Field>
     <div v-if="r.kind==='nacos'" class="grid gap-3 sm:grid-cols-2"><Field><FieldLabel>Data ID</FieldLabel><Input :model-value="b.config?.data_id||''" :disabled="disabled" :aria-label="r.alias+' '+b.environment+' Data ID'" @update:model-value="value=>b.config={...(b.config||{}),data_id:value}"/></Field><Field><FieldLabel>Group</FieldLabel><Input :model-value="b.config?.group||''" :disabled="disabled" @update:model-value="value=>b.config={...(b.config||{}),group:value}"/></Field></div>
     <p v-if="bindingCheck(b)" class="text-xs" :class="bindingCheck(b).status==='failed'?'text-destructive':'text-muted-foreground'">{{resourceCheckLabels[bindingCheck(b).status]}} · {{bindingCheck(b).message}}<span v-if="bindingCheck(b).suggestion">；{{bindingCheck(b).suggestion}}</span></p><p v-else class="text-xs text-muted-foreground">已保存或待保存的绑定，目标尚未核验</p>
    </div>
    <p v-if="!rowsFor(r).length" class="text-sm text-muted-foreground">当前环境尚未绑定此资源</p><Button variant="outline" size="sm" :disabled="disabled" @click="addBinding(r)">＋ 环境绑定</Button>
   </AccordionContent></AccordionItem></Accordion>
   <Empty v-if="!filtered.length"><EmptyHeader><EmptyDescription>没有匹配的资源，调整筛选或添加资源</EmptyDescription></EmptyHeader></Empty>
  </div>
 </section>
</template>
