<script setup>
import ContextHelp from '../shared/components/ContextHelp.vue'
import {computed, inject, ref, watch} from 'vue'

import {toast} from '../shared/shadcnFeedback.js'
const props=defineProps({workItemId:Number,services:{type:Array,default:()=>[]},disabled:Boolean})
const emit=defineEmits(['created','busy'])
const api=inject('api'),open=ref(false),busy=ref(false),base=ref(''),commit=ref(''),branch=ref(''),plan=ref(null)
const input=computed(()=>JSON.stringify([props.workItemId,props.services,base.value,commit.value,branch.value]))
let generation=0
watch(input,()=>{++generation;if(plan.value?.state==='preview')plan.value=null})
const post=(url,body)=>api(url,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)})
async function refreshPlan(){if(busy.value)return;busy.value=true;emit('busy',true);try{plan.value=await api('api/research/development/'+plan.value.id)}catch(e){toast(e.message,'error')}finally{busy.value=false;emit('busy',false)}}
async function act(action){if(busy.value)return;busy.value=true;emit('busy',true);const seq=generation;try{
 if(action==='preview'){const value=await post('api/research/development/preview',{work_item_id:props.workItemId,services:props.services,base_branch:base.value,base_commit:commit.value,branch:branch.value});if(seq===generation)plan.value=value}
 else {const value=await post('api/research/development/'+plan.value.id+'/'+action,{confirm:true,revision:plan.value.revision});plan.value=value;if(value.state==='success'){emit('created',{branch:value.branch,services:value.repositories.flatMap(r=>r.services)});toast('分支已创建并填入当前交付内容，请保存草稿完成关联','success')}else toast(value.message,'warning',{duration:8000})}
 }catch(e){if(action!=='preview'&&plan.value?.id){try{plan.value=await api('api/research/development/'+plan.value.id)}catch{}}toast(e.message,'error',{duration:7000})}finally{busy.value=false;emit('busy',false)}}

import {Accordion,AccordionContent,AccordionItem,AccordionTrigger,Badge,Button,Field,FieldLabel,Input,Spinner} from '../shared/shadcn-ui.js'
</script>
<template><Accordion type="single" collapsible class="md:col-span-2"><AccordionItem value="development"><AccordionTrigger :disabled="disabled||!workItemId||!services.length">自动创建开发分支</AccordionTrigger><AccordionContent class="space-y-4">
 <div class="flex items-center justify-between"><span class="text-sm text-muted-foreground">从已核对的基线创建分支</span><ContextHelp text="同一仓库只创建一次；不合并、构建或发布。先预览，再确认创建。已有分支可直接在上方选择。"/></div>
 <div class="grid gap-4 md:grid-cols-2"><Field><FieldLabel>基线分支</FieldLabel><Input v-model="base" :disabled="busy||!!plan&&plan.state!=='preview'" placeholder="远程默认分支"/></Field><Field><FieldLabel>新分支名称</FieldLabel><Input v-model="branch" :disabled="busy||!!plan&&plan.state!=='preview'" placeholder="自动生成 feature/work-编号-唯一后缀"/></Field></div>
 <Field v-if="!plan||plan.state==='preview'"><FieldLabel>基线 Commit（选填）</FieldLabel><Input v-model="commit" :disabled="busy" placeholder="完整 Git SHA；热修复需核对生产基线"/></Field>
 <section v-if="plan" class="space-y-2 rounded-lg border p-3 text-sm"><strong>{{plan.branch}}</strong><Badge variant="outline">{{plan.state==='success'?'已创建':plan.state==='preview'?'待确认创建':'结果待核对'}}</Badge><ul class="space-y-2"><li v-for="r in plan.repositories" :key="r.url" class="break-all text-muted-foreground">{{r.services.join('、')}} · {{r.base_branch||'指定提交'}} → {{r.base_commit}} · {{({pending:'待创建',created:'已确认',unconfirmed:'待核对',uncertain:'结果待核对'})[r.status]||r.status}}</li></ul><p v-if="plan.message" role="status">{{plan.message}}</p></section>
 <div class="flex flex-wrap gap-2"><Button v-if="!plan||plan.state==='preview'" variant="outline" :disabled="busy||disabled" @click="act('preview')"><Spinner v-if="busy"/>预览仓库与基线</Button><Button v-if="plan?.state==='preview'" :disabled="busy||disabled" @click="act('create')">确认创建远程分支</Button><Button v-if="plan" variant="outline" :disabled="busy" @click="refreshPlan">核对计划状态</Button><Button v-if="['running','uncertain'].includes(plan?.state)" variant="outline" :disabled="busy" @click="act('reconcile')">只读核对结果</Button><Button v-if="plan&&!busy&&plan.state!=='preview'" variant="ghost" @click="plan=null;generation++">新建另一份计划</Button></div>
 </AccordionContent></AccordionItem></Accordion></template>
