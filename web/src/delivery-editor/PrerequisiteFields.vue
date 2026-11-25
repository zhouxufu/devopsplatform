<script setup>
import {Accordion, AccordionContent, AccordionItem, AccordionTrigger, Button, Field, FieldLabel, Input} from '../shared/shadcn-ui.js';
defineProps({initiallyOpen:Boolean,id:{type:String,required:true},readonly:Boolean,disabled:Boolean,choices:{type:Array,default:()=>[]}});
const entries=defineModel({type:Array});
</script>
<template>
<Accordion :default-value="initiallyOpen?'prerequisites':undefined" type="single" collapsible class="md:col-span-2"><AccordionItem value="prerequisites">
        <AccordionTrigger>发布前置变更{{entries?.length?' · '+entries.length:''}}</AccordionTrigger>
        <AccordionContent class="space-y-3">
         <datalist :id="id+'-dependencies'"><option v-for="change in choices" :key="change.id" :value="change.id">{{change.title}} · {{change.service}}</option></datalist>
         <div v-for="(dep,i) in entries" :key="i" class="flex flex-wrap items-end gap-2">
          <Field class="w-32"><FieldLabel :for="id+'-dep-'+i">先完成变更 #</FieldLabel><Input :id="id+'-dep-'+i" :model-value="String(dep.change_id||'')" type="number" min="1" :list="id+'-dependencies'" :disabled="readonly||disabled" @update:model-value="dep.change_id=Number($event)"/></Field>
          <Field class="min-w-40 flex-1"><FieldLabel :for="id+'-why-'+i">依赖原因</FieldLabel><Input :id="id+'-why-'+i" v-model="dep.reason" placeholder="例如：新服务启动时需要读取新增字段" :disabled="readonly||disabled"/></Field>
          <Button v-if="!readonly" type="button" variant="ghost" :disabled="disabled" @click="entries.splice(i,1)">移除</Button>
         </div>
         <Button v-if="!readonly" type="button" variant="outline" size="sm" :disabled="disabled||(entries?.length||0)>=32" @click="entries=[...(entries||[]),{change_id:0,reason:''}]">添加前置变更</Button>
         <span v-else-if="!entries?.length">未登记前置变更</span>
        </AccordionContent></AccordionItem></Accordion>
</template>
