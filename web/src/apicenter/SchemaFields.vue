<script setup>
import {ref,computed,watch} from 'vue'
import {copyText} from '../shared/clipboard.js'
import {getCoreRowModel,getExpandedRowModel,useVueTable} from '@tanstack/vue-table'
import {ChevronDownIcon,ChevronRightIcon} from '@lucide/vue'
import {Badge,Button,Table,TableBody,TableCell,TableHead,TableHeader,TableRow} from '../shared/shadcn-ui.js'
const props=defineProps({rows:{type:Array,default:()=>[]},location:{type:String,default:''},changes:{type:Array,default:()=>[]}})
const expanded=ref({})
const decorated=computed(()=>{const decorate=rows=>rows.map(row=>{const addition=props.changes.find(f=>(f.field_path&&row.field_path?f.field_path===row.field_path:f.name===(row.in?row.field:row.key))&&(!row.in||row.in===f.in));const children=row.children?decorate(row.children):undefined;return {...row,addition,...(children?{children}:{}),containsAdded:!!addition&&addition.is_added!==false||children?.some(c=>c.containsAdded)}});return decorate(props.rows)})
watch(()=>JSON.stringify(props.changes),()=>{const open={};const walk=rows=>{for(const row of rows){if(row.children?.some(c=>c.containsAdded))open[String(row.field_path||row.key)]=true;if(row.children)walk(row.children)}};walk(decorated.value);expanded.value=open},{immediate:true})
function openRequirement(ref){const hash='#/work/'+ref.id;if(window.parent!==window)window.parent.postMessage({type:'workspace.open',plugin:'research',hash},location.origin);else window.open('/api/plugins/research/ui'+hash,'_blank','noopener')}

const table=useVueTable({get data(){return decorated.value},columns:[{accessorKey:'field'}],getRowId:row=>row.in?row.in+':'+row.field_path:String(row.field_path||row.key),getSubRows:row=>row.children,getCoreRowModel:getCoreRowModel(),getExpandedRowModel:getExpandedRowModel(),autoResetExpanded:false,state:{get expanded(){return expanded.value}},onExpandedChange:value=>{expanded.value=typeof value==='function'?value(expanded.value):value}})
const locations={query:'查询参数',path:'路径参数',header:'请求头',cookie:'Cookie',body:'请求体',response:'响应体'}
</script>
<template>
 <div class="my-3 min-w-0 max-w-full overflow-hidden rounded-lg border" aria-label="接口字段定义">
  <Table class="table-fixed" :class="changes.length?'min-w-[64rem]':'min-w-[48rem]'" :container-tabindex="0" container-label="接口字段，可横向滚动">
   <TableHeader><TableRow>
    <TableHead class="w-48">字段</TableHead><TableHead class="w-28">类型</TableHead><TableHead class="w-16">必填</TableHead><TableHead>说明</TableHead><TableHead class="w-24">位置</TableHead><TableHead class="w-48">取值与约束</TableHead><TableHead v-if="changes.length" class="w-56">来源需求 / 本次变化</TableHead>
   </TableRow></TableHeader>
   <TableBody>
    <TableRow v-for="row in table.getRowModel().rows" :key="row.id" :data-document-field-depth="row.depth" :class="row.original.addition&&row.original.addition.is_added!==false?'bg-emerald-50/80 dark:bg-emerald-950/30':''">
     <TableCell class="align-top whitespace-normal font-mono [overflow-wrap:anywhere]"><div class="flex min-w-0 items-start gap-1" :style="{paddingInlineStart:row.depth*16+'px'}"><Button v-if="row.getCanExpand()" variant="ghost" size="icon-sm" class="size-6 shrink-0" :aria-expanded="row.getIsExpanded()" :aria-label="(row.getIsExpanded()?'收起':'展开')+' '+row.id" @click="row.toggleExpanded()"><ChevronDownIcon v-if="row.getIsExpanded()" class="size-4"/><ChevronRightIcon v-else class="size-4"/></Button><span v-else class="w-6 shrink-0" aria-hidden="true"/><Button variant="ghost" size="sm" class="h-auto min-h-0 min-w-0 max-w-full flex-1 shrink! justify-start whitespace-normal break-all px-0 py-0 text-left font-mono font-normal select-text" data-document-copy-include :aria-label="'复制字段名 '+row.original.field" :title="'点击复制字段名：'+row.original.field" @click.stop="copyText(row.original.field,{successMessage:'字段名已复制'})">{{row.original.field}}</Button></div></TableCell>
     <TableCell class="align-top whitespace-normal [overflow-wrap:anywhere]">{{row.original.type}}</TableCell>
     <TableCell class="align-top whitespace-normal">{{row.original.required}}</TableCell>
     <TableCell class="align-top whitespace-pre-wrap [overflow-wrap:anywhere]">{{row.original.description||'—'}}</TableCell>
     <TableCell class="align-top whitespace-normal [overflow-wrap:anywhere]">{{locations[row.original.in||location]||row.original.in||location||'—'}}</TableCell>
     <TableCell class="align-top whitespace-pre-wrap [overflow-wrap:anywhere]">{{row.original.details||'—'}}</TableCell><TableCell v-if="changes.length" class="align-top whitespace-normal"><template v-if="row.original.addition"><Badge v-if="row.original.addition.is_added!==false" variant="outline" class="border-emerald-600 text-emerald-700 dark:text-emerald-300">本次新增</Badge><Button v-if="row.original.addition.work_item" data-document-copy-include variant="link" class="h-auto whitespace-normal px-0 text-left" @click="openRequirement(row.original.addition.work_item)">#{{row.original.addition.work_item.external_id||row.original.addition.work_item.id}} · {{row.original.addition.work_item.title}}</Button><span v-else class="ml-2 text-xs text-muted-foreground">来源未关联</span><span v-if="row.original.addition.first_snapshot" class="block text-xs text-muted-foreground">首次修订 #{{row.original.addition.first_snapshot}}</span></template><span v-else>—</span></TableCell>
    </TableRow>
    <TableRow v-if="!table.getRowModel().rows.length"><TableCell :colspan="changes.length?7:6" class="py-6 text-center text-muted-foreground">未声明字段</TableCell></TableRow>
   </TableBody>
  </Table>
 </div>
</template>
