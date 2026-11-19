<script setup>
import {computed,nextTick,ref,watch} from 'vue'


import {MULTILINE_MODES,selectCollectApplication,validateCollections} from '../agentCollection.js'

const props=defineProps({items:{type:Array,default:()=>[]},applications:{type:Array,default:()=>[]},disabled:Boolean})
const emit=defineEmits(['add','remove','create-application'])
const root=ref(null),expanded=ref([]),problem=ref(null),ids=new WeakMap()
let sequence=0
function key(item){if(!ids.has(item))ids.set(item,String(++sequence));return ids.get(item)}
watch(()=>props.items.map(key),(keys,previous=[])=>{
  const added=keys.filter(id=>!previous.includes(id))
  const retained=expanded.value.filter(id=>keys.includes(id))
  expanded.value=added.length?[previous.length?added[added.length-1]:keys[0]]:retained.length?retained:keys.slice(0,1)
  problem.value=null
},{immediate:true})
const appOptions=computed(()=>props.applications.map(app=>({value:app.application_id,label:app.name})))
function name(item){return props.applications.find(app=>app.application_id===item.application_id)?.name||item.service||'未选择应用'}
function pathSummary(item){const paths=(item.paths_text||'').split('\n').map(path=>path.trim()).filter(Boolean);return paths.length?`${paths[0]}${paths.length>1?' 等 '+paths.length+' 个路径':''}`:'尚未配置日志路径'}
function fieldError(index,field){return problem.value?.index===index&&problem.value?.field===field?problem.value.message:''}
function clearError(index,field){if(fieldError(index,field))problem.value=null}
function validate(){problem.value=validateCollections(props.items);return problem.value}
async function focusInvalid(value){
  problem.value=value
  const item=props.items[value.index];if(!item)return
  if(!expanded.value.includes(key(item)))expanded.value=[...expanded.value,key(item)]
  await nextTick()
  const target=root.value?.querySelector(`[data-collect-index="${value.index}"] [data-collect-field="${value.field}"]`)
  target?.scrollIntoView({block:'center',behavior:'smooth'})
  focusControl(target?.querySelector('input:not([disabled]),textarea:not([disabled])'),{preventScroll:true})
}
defineExpose({validate,focusInvalid})

import OptionPicker from '../../shared/components/OptionPicker.vue'

import {focusControl} from '../../shared/focusControl.js'


import {Accordion,AccordionContent,AccordionItem,AccordionTrigger,Button,Empty,EmptyContent,EmptyDescription,EmptyHeader,EmptyTitle,Field,FieldDescription,FieldError,FieldLabel,Input,Textarea} from '../../shared/shadcn-ui.js'
</script>
<template><section ref="root" class="collection-editor"><div class="collection-toolbar"><div><b>日志采集项 <span>{{items.length}}</span></b><p>选择应用与日志文件，多个应用可分别配置。</p></div><div class="collection-actions"><Button @click="emit('create-application')" variant="ghost" size="default" type="button" :disabled="(disabled)">新建应用</Button><Button @click="emit('add')" variant="outline" size="default" type="button" :disabled="(disabled||!applications.length)">＋ 添加采集项</Button></div></div><Empty v-if="!applications.length"><EmptyHeader><EmptyDescription>{{"还没有平台应用，先新建应用，再配置采集路径。"}}</EmptyDescription></EmptyHeader></Empty><Empty v-else-if="!items.length"><EmptyHeader><EmptyTitle>暂无日志采集项</EmptyTitle><EmptyDescription>添加采集项后再保存配置。</EmptyDescription></EmptyHeader></Empty><Accordion v-else class="collection-list" type="multiple" :model-value="(expanded||[]).map(String)" @update:model-value="expanded=$event;"><AccordionItem v-for="(item,index) in items" :key="key(item)" :value="String(key(item))"><div class="flex items-center gap-2 [&>h3]:min-w-0 [&>h3]:flex-1"><AccordionTrigger :disabled="disabled"><div class="collection-summary"><b>{{index+1}}. {{name(item)}}</b><span>{{pathSummary(item)}}</span></div></AccordionTrigger><Button :aria-label="'删除采集项 '+(index+1)+'：'+name(item)" @click="emit('remove',index)" class="shrink-0 text-destructive hover:text-destructive" variant="ghost" size="sm" type="button" :disabled="disabled">删除</Button></div><AccordionContent force-mount v-show="!(!(expanded||[]).map(String).includes(String(key(item))))"><div :data-collect-index="index" class="collection-fields"><Field data-collect-field="application_id"><FieldLabel>平台应用 <span class="text-destructive">*</span></FieldLabel><OptionPicker :model-value="item.application_id" :options="appOptions" placeholder="搜索并选择应用"  :style="{width:'100%',maxWidth:'100%'}" :disabled="disabled" @update:model-value="id=>{selectCollectApplication(item,applications.find(app=>app.application_id===id));clearError(index,'application_id')}"/><FieldError v-if="fieldError(index,'application_id')">{{fieldError(index,'application_id')}}</FieldError></Field><Field><FieldLabel>日志合并方式</FieldLabel><OptionPicker v-model="item.multiline_mode" :options="MULTILINE_MODES" :style="{width:'100%',maxWidth:'100%'}" :disabled="disabled"/><FieldDescription>{{item.multiline_mode==='java'?'按时间戳识别新日志，后续异常堆栈并入上一条。':item.multiline_mode==='custom'?'按首行正则识别新日志，其余行并入上一条。':'每行独立上报，适用于单行文本或 JSON 日志。'}}</FieldDescription></Field><Field data-collect-field="paths_text" class="full-width"><FieldLabel>日志绝对路径 <span class="text-destructive">*</span></FieldLabel><Textarea v-model="item.paths_text" :disabled="disabled" placeholder="/opt/apps/order/logs/*.log" @update:model-value="clearError(index,'paths_text')" :rows="({minRows:3,maxRows:8}).minRows||4"/><FieldError v-if="fieldError(index,'paths_text')">{{fieldError(index,'paths_text')}}</FieldError><FieldDescription>每行一个路径，支持 *.log 等 glob 通配符；运行用户须有读取权限。</FieldDescription></Field><Field v-if="item.multiline_mode==='custom'" data-collect-field="multiline" class="full-width"><FieldLabel>首行正则 <span class="text-destructive">*</span></FieldLabel><Input v-model="item.multiline" :disabled="disabled" placeholder="例如 ^\d{4}-\d{2}-\d{2}" @update:model-value="clearError(index,'multiline')"/><FieldError v-if="fieldError(index,'multiline')">{{fieldError(index,'multiline')}}</FieldError><FieldDescription>匹配一条新日志的首行，无需 /…/ 包裹。规则由 Agent 校验。</FieldDescription></Field></div></AccordionContent></AccordionItem></Accordion><p class="collection-footnote">添加或删除采集项需保存配置；仅保存不会下发，保存并下发后影响新采集的日志。</p></section></template>
<style scoped>.collection-editor{min-width:0}.collection-toolbar{display:flex;justify-content:space-between;align-items:center;gap:16px;margin:8px 0 16px}.collection-toolbar b{font-size:1rem}.collection-toolbar b span{margin-left:4px;color:var(--muted-foreground);font-weight:400}.collection-toolbar p,.collection-footnote{color:var(--muted-foreground);font-size:.75rem;line-height:1.6;margin:4px 0 0}.collection-actions{display:flex;align-items:center;gap:8px;flex-shrink:0}.collection-summary{min-width:0;display:flex;align-items:center;gap:16px}.collection-summary b{min-width:0;overflow-wrap:anywhere}.collection-summary span{color:var(--muted-foreground);overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-size:.75rem}.collection-fields{display:grid;grid-template-columns:minmax(0,1fr) minmax(0,1fr);gap:0 24px;padding:8px 4px 0}.full-width{grid-column:1/-1}.collection-footnote{margin-top:16px}@media(max-width:650px){.collection-toolbar{align-items:flex-start;flex-direction:column;gap:8px}.collection-fields{grid-template-columns:minmax(0,1fr)}.collection-summary{align-items:flex-start;flex-direction:column;gap:2px}.collection-summary span{max-width:200px}}</style>
