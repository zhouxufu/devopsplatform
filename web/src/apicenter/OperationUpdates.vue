<script setup>
import {computed,ref,watch} from 'vue'
import {Badge,Button,Checkbox,Field,FieldLabel,Accordion,AccordionItem,AccordionTrigger,AccordionContent,Spinner} from '../shared/shadcn-ui.js'
import {reportTime,reportRelation,documentDestination,openDocumentWorkspace} from './reportModel.js'
const props=defineProps({info:Object,service:String,environment:String,protocol:String,snapshot:Number,operation:String})
const update=computed(()=>props.info?.operations?.[props.operation]),open=ref(''),history=ref([]),loading=ref(false),error=ref(''),before=ref(0),more=ref(false),include=ref(false)
let epoch=0
function reports(id){openDocumentWorkspace('#/reports?'+new URLSearchParams({service:props.service,environment:props.environment||'',protocol:props.protocol||'http',...(id?{report:id}:{})}))}
async function load(reset=false){const seq=++epoch;if(reset){history.value=[];before.value=0}loading.value=true;error.value='';try{const q=new URLSearchParams({service:props.service,environment:props.environment||'',protocol:props.protocol||'http',operation:props.operation,include_unchanged:include.value?'1':'0',...(before.value?{before:String(before.value)}:{})});const r=await fetch('api/api-documents/operation-history?'+q),v=await r.json();if(seq!==epoch)return;if(!r.ok)throw Error(v.error||'读取历史失败');history.value.push(...v.items);before.value=v.next_before;more.value=v.has_more}catch(e){if(seq===epoch)error.value=e.message}finally{if(seq===epoch)loading.value=false}}
function view(id){openDocumentWorkspace(documentDestination({service:props.service,environment:props.environment,protocol:props.protocol,snapshot_id:id},{operation:props.operation}))}
watch(()=>[props.service,props.environment,props.protocol,props.operation,props.snapshot],()=>{epoch++;open.value='';history.value=[];error.value='';loading.value=false})
watch(open,value=>{if(value)load(true)})
watch(include,()=>{if(open.value)load(true)})
</script>
<template>
 <section class="my-4 space-y-2 text-sm" aria-label="接口文档更新信息">
  <div class="flex flex-wrap items-center gap-2"><Badge variant="outline">{{info?.is_current?'当前版本':'历史版本'}} · #{{snapshot}}</Badge><Button v-if="info?.current_id&&info.current_id!==snapshot" variant="link" size="sm" @click="view(info.current_id)">查看当前版本 #{{info.current_id}}</Button></div>
  <div>本接口最近变更：<template v-if="update?.changed_at">{{reportTime(update.changed_at)}} · {{update.kind==='added'?'新增':'修改'}}于 #{{update.snapshot_id}}<span v-if="update.snapshot_id!==snapshot" class="text-muted-foreground"> · 本文档未变</span></template><span v-else class="text-muted-foreground">历史变更时间未记录</span></div>
  <div v-if="info?.last_successful_report" class="text-muted-foreground">本应用／环境最近成功上报：{{reportTime(info.last_successful_report.created_at)}} · {{info.last_successful_report.state==='unchanged'?'内容一致':'已保存'}} · {{reportRelation(info.last_successful_report)}}</div>
  <div class="flex flex-wrap gap-2"><Button v-if="info?.report_id" variant="link" size="sm" @click="reports(info.report_id)">查看所属上报</Button><span v-else class="text-muted-foreground">所属上报未记录</span><Button v-if="update?.report_id&&update.report_id!==info?.report_id" variant="link" size="sm" @click="reports(update.report_id)">查看最近修改的上报</Button></div>
  <Accordion v-model="open" type="single" collapsible><AccordionItem value="history"><AccordionTrigger>接口变更历史</AccordionTrigger><AccordionContent class="space-y-3"><Field orientation="horizontal" class="w-auto"><Checkbox id="operation-unchanged" v-model="include"/><FieldLabel for="operation-unchanged">包含未变化的收录记录</FieldLabel></Field><p v-if="error" role="alert">{{error}} <Button variant="outline" @click="load(true)">重试</Button></p><div v-for="item in history" :key="item.snapshot.id" class="flex flex-wrap items-center gap-2"><Button variant="link" @click="view(item.snapshot.id)">#{{item.snapshot.id}}</Button><span>{{reportTime(item.snapshot.created_at)}}</span><span>{{item.unknown?'历史变更未知':item.unchanged?'收录，未变化':item.kind?({added:'新增',modified:'修改'})[item.kind]:[...new Set(item.events.map(e=>({added:'新增',modified:'修改',removed:'删除'})[e.change]))].join('、')}}</span></div><p v-if="loading" role="status"><Spinner/>正在读取历史</p><p v-else-if="!history.length">{{more?'本段历史没有接口变化，可继续向前查看':'暂无已记录的接口变化'}}</p><Button v-if="more" variant="outline" :disabled="loading" @click="load()">查看更早记录</Button></AccordionContent></AccordionItem></Accordion>
 </section>
</template>
