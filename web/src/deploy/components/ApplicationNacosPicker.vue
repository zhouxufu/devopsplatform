<script setup>
import {ref as paginationRef} from 'vue'
const listPageSize=paginationRef(20)
import ListPagination from '../../shared/components/ListPagination.vue'
import {computed,nextTick,ref,watch} from 'vue'
import OptionPicker from '../../shared/components/OptionPicker.vue'
import {toast} from '../../shared/composables/toast.js'
import {nacosReferenceKey,scopedNacosReference} from '../resourceExperience.js'
import {Accordion,AccordionContent,AccordionItem,AccordionTrigger,Badge,Button,Checkbox,Empty,EmptyHeader,EmptyDescription,Field,FieldLabel,Input,Spinner,Table,TableBody,TableCell,TableHead,TableHeader,TableRow} from '../../shared/shadcn-ui.js'
const props=defineProps({modelValue:Array,environments:Array,connections:{type:Array,default:()=>[]},request:Function,disabled:Boolean})
const emit=defineEmits(['update:modelValue'])
const root=ref(null)
const environment=ref(''),connection=ref(''),query=ref(''),group=ref(''),items=ref([]),page=ref(1),total=ref(0),busy=ref(false),searched=ref(false),namespace=ref(''),panels=ref([])
const nacosConnections=computed(()=>props.connections.filter(c=>c.kind==='nacos'))
let sequence=0
watch([environment,connection],()=>{sequence++;items.value=[];total.value=0;searched.value=false;busy.value=false})
const key=nacosReferenceKey
const selected=item=>(props.modelValue||[]).some(v=>key(v)===key(item))
function toggle(item){emit('update:modelValue',selected(item)?props.modelValue.filter(v=>key(v)!==key(item)):[...(props.modelValue||[]),{...item}])}
function updateReference(index,field,value){emit('update:modelValue',props.modelValue.map((item,i)=>i===index?{...item,[field]:value}:item))}
async function search(next=1){
 if(!environment.value||busy.value||props.disabled)return
 const token=++sequence;busy.value=true
 try{const data=await props.request(`api/environments/${encodeURIComponent(environment.value)}/nacos/configs?`+new URLSearchParams({connection:connection.value,q:query.value,group:group.value,page:next,page_size:listPageSize.value}));if(token!==sequence)return;items.value=(data.items||[]).map(item=>scopedNacosReference(item,environment.value,connection.value||data.connection));total.value=data.total;page.value=next;namespace.value=data.namespace;searched.value=true}
 catch(e){if(token===sequence)toast(e.message,'error')}finally{if(token===sequence)busy.value=false}
}
function validate(){const seen=new Set();for(const [index,item]of (props.modelValue||[]).entries()){const id=JSON.stringify([item.environment,item.alias]);if(!item.environment||!item.alias?.trim()||!item.data_id||!item.group||seen.has(id))return {index,message:'请补齐 Nacos 的环境、资源别名、Data ID 和 Group，同一环境别名不能重复'};seen.add(id)}return null}
async function focusReference(alias,env){const index=(props.modelValue||[]).findIndex(item=>item.alias===alias&&(!env||item.environment===env));if(index<0)return;await nextTick();const row=root.value?.querySelector('[data-nacos-reference="'+index+'"]');row?.scrollIntoView({block:'center'});row?.querySelector('input')?.focus()}
defineExpose({validate,focusReference})
</script>
<template>
 <section ref="root" class="min-w-0 space-y-3 border-t pt-4" aria-label="按环境选择 Nacos 配置">
  <div class="flex flex-wrap items-center justify-between gap-2"><h3 class="text-sm font-medium">Nacos 目录选择</h3><Badge variant="secondary">{{modelValue?.length||0}} 条环境映射</Badge></div>
  <div v-if="modelValue?.length" class="max-h-72 overflow-auto rounded-lg border"><Table class="table-fixed min-w-[720px]"><TableHeader><TableRow><TableHead class="w-28">环境</TableHead><TableHead class="w-44">逻辑资源</TableHead><TableHead>连接 / 配置</TableHead><TableHead class="w-16">操作</TableHead></TableRow></TableHeader><TableBody><TableRow v-for="(item,index) in modelValue" :key="index" :data-nacos-reference="index"><TableCell class="align-top"><OptionPicker :model-value="item.environment||''" :options="(environments||[]).map(e=>({value:e.name,label:e.display_name||e.name}))" :disabled="disabled" placeholder="选择环境" :aria-label="'配置环境 '+item.data_id" @update:model-value="value=>updateReference(index,'environment',value)"/></TableCell><TableCell class="align-top"><Input :model-value="item.alias" :disabled="disabled" :aria-label="'逻辑资源 '+item.data_id" @update:model-value="value=>updateReference(index,'alias',value)"/></TableCell><TableCell class="whitespace-normal break-all text-sm"><OptionPicker :model-value="item.connection||''" :options="nacosConnections.map(c=>({value:c.name,label:c.name+' · '+(c.config?.namespace||'public')}))" all-label="该环境默认连接" :disabled="disabled" :aria-label="'配置连接 '+item.data_id" @update:model-value="value=>updateReference(index,'connection',value)"/><p class="text-muted-foreground">{{item.group}} / {{item.data_id}}</p></TableCell><TableCell><Button variant="ghost" size="sm" :disabled="disabled" :aria-label="'移除 '+item.environment+' '+item.data_id" @click="toggle(item)">移除</Button></TableCell></TableRow></TableBody></Table></div>
  <Accordion v-model="panels" type="multiple"><AccordionItem value="catalog"><AccordionTrigger>搜索并批量选择配置</AccordionTrigger><AccordionContent class="space-y-3">
   <div class="grid gap-3 sm:grid-cols-2"><Field><FieldLabel>目标环境</FieldLabel><OptionPicker v-model="environment" :options="(environments||[]).map(e=>({value:e.name,label:e.display_name||e.name}))" all-label="选择环境" :disabled="disabled||busy" aria-label="Nacos 查询环境"/></Field><Field><FieldLabel>资源连接</FieldLabel><OptionPicker v-model="connection" :options="nacosConnections.map(c=>({value:c.name,label:c.name+' · '+(c.config?.namespace||'public')}))" all-label="使用该环境默认连接" :disabled="disabled||busy" aria-label="Nacos 查询连接"/></Field></div>
   <div class="flex flex-wrap gap-2"><Input v-model="query" class="min-w-40 flex-1" placeholder="搜索 Data ID" :disabled="disabled||busy" @keyup.enter="search()"/><Input v-model="group" class="min-w-32 flex-1" placeholder="Group（不限）" :disabled="disabled||busy" @keyup.enter="search()"/><Button variant="outline" :disabled="disabled||!environment||busy" @click="search()"><Spinner v-if="busy"/>搜索</Button></div>
   <p v-if="searched" class="break-all text-xs text-muted-foreground">命名空间：{{namespace||'public'}} · 只绑定所选环境；配置内容不会发布</p>
   <div v-if="searched" class="max-h-72 overflow-auto rounded-lg border"><Table class="table-fixed min-w-[480px]"><TableHeader><TableRow><TableHead class="w-14">选择</TableHead><TableHead>Data ID</TableHead><TableHead>Group</TableHead></TableRow></TableHeader><TableBody><TableRow v-for="item in items" :key="key(item)"><TableCell><Checkbox :model-value="selected(item)" :disabled="disabled||busy" :aria-label="'选择 '+item.data_id" @update:model-value="toggle(item)"/></TableCell><TableCell class="whitespace-normal break-all">{{item.data_id}}</TableCell><TableCell class="whitespace-normal break-all">{{item.group}}</TableCell></TableRow><TableRow v-if="!items.length"><TableCell colspan="3"><Empty><EmptyHeader><EmptyDescription>没有匹配的配置，请调整搜索条件</EmptyDescription></EmptyHeader></Empty></TableCell></TableRow></TableBody></Table></div>
   <ListPagination v-if="searched" :page="page" :total="total" :page-size="listPageSize" :disabled="disabled||busy" @update:page="search($event)" @update:page-size="listPageSize=$event;search(1)"/>
  </AccordionContent></AccordionItem></Accordion>
 </section>
</template>
