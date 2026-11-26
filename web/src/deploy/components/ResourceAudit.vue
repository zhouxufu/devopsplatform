<script setup>
import {ref as paginationRef} from 'vue'
const listPageSize=paginationRef(20)
import ListPagination from '../../shared/components/ListPagination.vue'
import {computed,onBeforeUnmount,onDeactivated,ref,watch} from 'vue'
import {RefreshCwIcon} from '@lucide/vue'
import DataTable from '../../shared/components/DataTable.vue'
import DateRangeFilter from '../../shared/components/DateRangeFilter.vue'
import OptionPicker from '../../shared/components/OptionPicker.vue'
import {auditActionLabel,auditResourceLabel,auditActionLabels} from '../resourceCatalog.js'
import {Button,Empty,EmptyHeader,EmptyTitle,EmptyDescription,EmptyContent,Input,Spinner} from '../../shared/shadcn-ui.js'
const props=defineProps({request:Function,scope:Object})
const query=ref(''),type=ref(props.scope?.type||''),resource=ref(props.scope?.id||''),start=ref(''),end=ref(''),page=ref(1),items=ref([]),total=ref(0),types=ref([]),loading=ref(false),error=ref('')
let sequence=0,controller,timer
const options=computed(()=>[{value:'',label:'全部资源类型'},...types.value.map(value=>({value,label:auditResourceLabel(value)}))])
const columns=[{id:'created_at',label:'时间',size:180,required:true},{id:'actor',label:'操作者',size:150},{id:'action',label:'操作',size:150},{id:'resource_type',label:'资源类型',size:160},{id:'resource_id',label:'资源标识',size:240,flex:true,required:true}]
async function load(){
 clearTimeout(timer);controller?.abort();const current=++sequence;controller=new AbortController();loading.value=true;error.value=''
 try{
  const params=new URLSearchParams({page:String(page.value),page_size:String(listPageSize.value)})
  const term=query.value.trim(),action=Object.entries(auditActionLabels).find(([,label])=>label===term)?.[0]
  if(term)params.set('q',action||term)
  if(type.value)params.set('resource_type',type.value)
  if(resource.value)params.set('resource_id',resource.value)
  for(const [key,value] of [['start',start.value],['end',end.value]])if(value){const date=new Date(value);if(!Number.isFinite(date.getTime()))throw Error('请选择有效时间');params.set(key,date.toISOString())}
  const result=await props.request('api/audit/page?'+params,{signal:controller.signal})
  if(current!==sequence)return
  if(!Array.isArray(result.items)||!Number.isFinite(result.total))throw Error('操作记录数据格式异常')
  items.value=result.items;total.value=result.total;types.value=result.resource_types||[]
  const last=Math.max(1,Math.ceil(total.value/listPageSize.value));if(page.value>last)page.value=last
 }catch(e){if(current===sequence&&e.name!=='AbortError')error.value=e.message||'操作记录读取失败'}finally{if(current===sequence)loading.value=false}
}
function queue(){clearTimeout(timer);controller?.abort();sequence++;loading.value=true;timer=setTimeout(load,250)}
watch([query,type,resource,start,end],()=>{page.value=1;queue()})
watch(page,queue)
watch(()=>props.scope,scope=>{type.value=scope?.type||'';resource.value=scope?.id||'';page.value=1;queue()})
function clear(){query.value='';type.value='';resource.value='';start.value='';end.value='';page.value=1;queue()}
function dispose(){clearTimeout(timer);sequence++;controller?.abort()}
onBeforeUnmount(dispose);onDeactivated(dispose)
load()
</script>
<template>
 <section aria-label="操作记录" class="min-w-0 space-y-4">
  <div class="flex flex-wrap items-center gap-3">
   <Input v-model="query" class="w-full sm:max-w-xs" aria-label="搜索操作记录" placeholder="搜索操作者、操作或资源标识"/>
   <OptionPicker v-model="type" :options="options" width="190px" aria-label="操作记录资源类型"/>
   <DateRangeFilter v-model:start="start" v-model:end="end" width="330px" label="时间"/>
   <Button v-if="query||type||resource||start||end" variant="ghost" @click="clear">清空筛选</Button>
   <Button class="ml-auto" variant="outline" size="icon" aria-label="刷新操作记录" :disabled="loading" @click="load"><Spinner v-if="loading"/><RefreshCwIcon v-else/></Button>
  </div>
  <div v-if="resource" class="flex items-center gap-3 text-sm"><span>当前资源：{{resource}}</span><Button variant="ghost" size="sm" @click="resource=''">查看全部资源</Button></div>
  <Empty v-if="error"><EmptyHeader><EmptyTitle>操作记录读取失败</EmptyTitle><EmptyDescription>{{error}}</EmptyDescription></EmptyHeader><EmptyContent><Button variant="outline" @click="load">重新读取</Button></EmptyContent></Empty>
  <template v-else>
   <DataTable :rows="loading?[]:items" :columns="columns" table-key="resource-audit-page-v1" :loading="loading">
    <template #cell-created_at="{record}">{{record.created_at>0?new Date(record.created_at*1000).toLocaleString('zh-CN',{hour12:false}):'—'}}</template>
    <template #cell-action="{record}">{{auditActionLabel(record.action)}}</template>
    <template #cell-resource_type="{record}">{{auditResourceLabel(record.resource_type)}}</template>
    <template #empty><Empty><EmptyHeader><EmptyTitle>{{loading?'正在查询操作记录':'没有匹配的操作记录'}}</EmptyTitle><EmptyDescription v-if="!loading">可调整筛选条件，或在资源配置和交付操作后查看。</EmptyDescription></EmptyHeader></Empty></template>
   </DataTable>
   <ListPagination :page="page" :total="total" :page-size="listPageSize" :disabled="loading" @update:page="!loading&&(page=$event)" @update:page-size="listPageSize=$event;page=1;queue()"/>
  </template>
 </section>
</template>
