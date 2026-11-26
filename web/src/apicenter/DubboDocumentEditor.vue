<script setup>
import {ref,computed,watch} from 'vue'
import {Accordion,AccordionItem,AccordionTrigger,AccordionContent,Button,Field,FieldLabel,Textarea,Spinner} from '../shared/shadcn-ui.js'
import {confirmAction} from '../shared/shadcnFeedback.js'
import {toast} from '../shared/composables/toast.js'
import {parseDocumentText,documentTextFromResponse} from './documentEditModel.js'
const props=defineProps({text:String,source:Object,workItem:[String,Number]}),emit=defineEmits(['state','saved','cancel'])
const draft=ref(props.text),busy=ref(false),uncertain=ref(false),issue=ref(''),latest=ref(null)
const dirty=computed(()=>draft.value!==props.text)
watch([dirty,busy],()=>emit('state',{dirty:dirty.value,busy:busy.value}),{immediate:true})
const query=extra=>new URLSearchParams({service:props.source.service,environment:props.source.environment,protocol:'dubbo',...extra})
function submission(){if(!props.workItem)return draft.value;const id=Number(props.workItem);if(!/^[1-9]\d*$/.test(String(props.workItem))||!Number.isSafeInteger(id))throw Error('请选择有效的工作项');return '{"document":'+draft.value+',"change_context":'+JSON.stringify({work_item:{id}})+'}'}
async function request(action,extra={}){const r=await fetch('api/api-documents/'+props.source.id+'/'+action+'?'+query(extra),{method:'POST',headers:{'Content-Type':'application/json'},body:submission()});const data=await r.json();if(!r.ok){const e=Error(data.error||'文档保存失败');e.status=r.status;throw e}return data}
async function canClose(){return !busy.value&&(!dirty.value||await confirmAction('文档尚未保存，确认放弃改动？','退出文档编辑'))}
async function save(){if(busy.value||uncertain.value)return;busy.value=true;issue.value='';let writing=false;try{const d=parseDocumentText(draft.value);if(d.dubbo!=='1.0')throw Error('请保留 Dubbo 文档协议');const p=await request('edit-preview');writing=true;const result=await request('edit',{report_id:p.report_id,expected_baseline_id:String(p.source_id),expected_digest:p.digest,expected_maintainer_digest:p.maintainer_digest});emit('saved',result)}catch(e){uncertain.value=writing&&(!e.status||e.status>=500);issue.value=e.message+(uncertain.value?'；保存结果待核对，未自动重试。':'；草稿已保留。')}finally{busy.value=false}}
async function inspect(){if(busy.value)return;busy.value=true;try{const r=await fetch('api/api-documents?'+query()),list=await r.json();if(!r.ok)throw Error(list.error);if(!list[0])throw Error('没有最新修订');const v=await fetch('api/api-documents/'+list[0].id+'?'+query());const raw=await v.text();if(!v.ok)throw Error('最新修订读取失败');latest.value={id:list[0].id,text:documentTextFromResponse(raw)};if(JSON.stringify(parseDocumentText(draft.value))===JSON.stringify(parseDocumentText(latest.value.text))){emit('saved',{id:latest.value.id});return}issue.value='最新修订 #'+latest.value.id+' 已读回；请保留草稿，核对差异后重新打开最新修订编辑。'}catch(e){issue.value=e.message}finally{busy.value=false}}
defineExpose({canClose})
</script>
<template><section class="min-w-0 space-y-4"><Field><FieldLabel>Dubbo 文档 JSON</FieldLabel><Textarea v-model="draft" aria-label="Dubbo 文档 JSON 编辑" class="min-h-[55vh] font-mono text-sm" :disabled="busy" spellcheck="false"/></Field><p v-if="issue" role="alert" class="text-sm text-destructive">{{issue}}</p><Accordion v-if="latest" type="single" collapsible><AccordionItem value="latest"><AccordionTrigger>最新修订 #{{latest.id}}</AccordionTrigger><AccordionContent><pre class="whitespace-pre-wrap break-all text-xs">{{latest.text}}</pre></AccordionContent></AccordionItem></Accordion><div class="flex justify-end gap-2"><Button variant="outline" :disabled="busy" @click="inspect">核对最新修订</Button><Button variant="ghost" :disabled="busy" @click="async()=>{if(await canClose())emit('cancel')}">取消</Button><Button :disabled="busy||uncertain" @click="save"><Spinner v-if="busy"/>保存</Button></div></section></template>
