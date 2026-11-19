<script setup>
import {computed,inject,ref,watch,useId} from 'vue'
import {PlusIcon,Trash2Icon} from '@lucide/vue'
import OptionPicker from '../shared/components/OptionPicker.vue'
import ContextHelp from '../shared/components/ContextHelp.vue'
import QueryEditor from '../shared/components/QueryEditor.vue'
const props=defineProps({model:Object,disabled:Boolean,forceOpen:Boolean,draftScripts:{type:Object,default:()=>({})}})
const api=inject('api'),environments=ref([]),busy=ref(false),error=ref(''),loaded=ref(false),expanded=ref(''),uid=useId()
const overrides=computed(()=>props.model.environment_overrides||[])
async function load(){if(loaded.value||busy.value)return;busy.value=true;error.value='';try{const data=await api('api/environments');if(!Array.isArray(data))throw Error('环境列表响应无效');environments.value=data;loaded.value=true}catch(e){error.value=e.message}finally{busy.value=false}}
function add(){props.model.environment_overrides=[...overrides.value,{environment:'',content:props.model.content||'',reason:''}]}
function remove(index){const entries=overrides.value.filter((_,i)=>i!==index),cache={...props.draftScripts};Object.keys(props.draftScripts).forEach(key=>delete props.draftScripts[key]);for(const [key,value] of Object.entries(cache))if(Number(key)!==index)props.draftScripts[Number(key)>index?Number(key)-1:key]=value;if(entries.length)props.model.environment_overrides=entries;else delete props.model.environment_overrides}
function mode(entry,index,value){if(value==='skip'){props.draftScripts[index]=entry.content||'';entry.skip=true;delete entry.content}else{entry.skip=false;entry.content=props.draftScripts[index]??props.model.content??''}}
function options(entry){const values=environments.value.filter(e=>e.name===entry.environment||!overrides.value.some(x=>x.environment===e.name)).map(e=>({value:e.name,label:e.display_name||e.name}));if(entry.environment&&!values.some(e=>e.value===entry.environment))values.push({value:entry.environment,label:entry.environment+'（待核对）'});return values}
watch(()=>props.forceOpen,v=>{if(v){expanded.value='environments';load()}},{immediate:true})

import {Accordion,AccordionContent,AccordionItem,AccordionTrigger,Button,Field,FieldLabel,Input,Spinner} from '../shared/shadcn-ui.js'
</script>
<template>
 <Field class="md:col-span-2" data-field="sql"><FieldLabel :for="uid+'sql'">SQL 脚本<ContextHelp text="保存草稿会保留原始脚本，包括说明注释；SQL 类型不变。校验并就绪时检查平台支持的语法，发布前还需目标环境预检与审批。保存不会执行 SQL。"/></FieldLabel><QueryEditor :input-id="uid+'sql'" v-model="model.content" :disabled="disabled" script label="默认 SQL 脚本"/></Field>
 <Field data-field="max_affected_rows"><FieldLabel :for="uid+'rows'">允许影响行数</FieldLabel><Input :id="uid+'rows'" v-model.number="model.max_affected_rows" type="number" min="1" :disabled="disabled"/></Field>
 <Accordion v-if="!model.data_sync" v-model="expanded" type="single" collapsible class="md:col-span-2" data-field="sql_environment_overrides" @update:model-value="v=>{if(v)load()}"><AccordionItem value="environments"><AccordionTrigger>环境例外{{overrides.length?' · '+overrides.length:''}}</AccordionTrigger><AccordionContent class="space-y-4">
  <div class="flex items-center justify-between"><span class="text-sm text-muted-foreground">未配置的环境使用默认 SQL</span><ContextHelp text="环境专用脚本单独预检、审核；其他环境执行成功不代表此脚本已验证。无需执行必须填写原因。"/></div>
  <div v-if="error" role="alert" class="flex items-center gap-2 text-destructive">{{error}}<Button variant="outline" size="sm" @click="load">重试</Button></div><Spinner v-if="busy" aria-label="正在读取环境"/>
  <section v-for="(entry,index) in overrides" :key="index" class="space-y-4 rounded-lg border p-4"><div class="flex items-center justify-between"><strong class="text-sm">环境例外 {{index+1}}</strong><Button v-if="!disabled" variant="ghost" size="icon-sm" :aria-label="'移除环境例外 '+(index+1)" @click="remove(index)"><Trash2Icon class="size-4"/></Button></div>
   <div class="grid items-start gap-4 md:grid-cols-2"><Field><FieldLabel :for="uid+'env'+index">目标环境</FieldLabel><OptionPicker :id="uid+'env'+index" v-model="entry.environment" :options="options(entry)" :disabled="disabled||busy"/></Field><Field><FieldLabel :for="uid+'mode'+index">执行方式</FieldLabel><OptionPicker :id="uid+'mode'+index" :model-value="entry.skip?'skip':'script'" :options="[{value:'script',label:'使用专用 SQL'},{value:'skip',label:'该环境无需执行'}]" :disabled="disabled" @update:model-value="v=>mode(entry,index,v)"/></Field></div>
   <Field v-if="!entry.skip"><FieldLabel :for="uid+'script'+index">环境专用 SQL</FieldLabel><QueryEditor :input-id="uid+'script'+index" v-model="entry.content" :disabled="disabled" script :label="'环境专用 SQL '+(index+1)"/></Field><Field><FieldLabel :for="uid+'reason'+index">{{entry.skip?'无需执行的原因':'使用不同 SQL 的原因'}}</FieldLabel><Input :id="uid+'reason'+index" v-model="entry.reason" :disabled="disabled"/></Field>
  </section><Button v-if="!disabled" variant="outline" :disabled="busy||!loaded||overrides.length>=20" @click="add"><PlusIcon class="size-4"/>添加环境例外</Button>
 </AccordionContent></AccordionItem></Accordion>
</template>
