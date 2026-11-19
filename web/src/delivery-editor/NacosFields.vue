<script setup>
import {ref,nextTick,useId,watch} from 'vue'
import {PlusIcon,Trash2Icon} from '@lucide/vue'
import OptionPicker from '../shared/components/OptionPicker.vue'
import ContextHelp from '../shared/components/ContextHelp.vue'
import {nacosValueType} from './nacosValue.js'
const props=defineProps({model:Object,disabled:Boolean,forceOpen:Boolean}),entries=ref(null),uid=useId(),advanced=ref('')
watch(()=>props.forceOpen,value=>{if(value)advanced.value='fallback'},{immediate:true})
async function add(){props.model.entries.push({key:'',value:'',action:'set'});await nextTick();entries.value?.lastElementChild?.querySelector('input')?.focus()}

import {Accordion,AccordionContent,AccordionItem,AccordionTrigger,Button,Field,FieldLabel,Input,Textarea} from '../shared/shadcn-ui.js'
</script>
<template><section class="space-y-5 md:col-span-2">
 <Field data-field="format" class="max-w-xs"><FieldLabel :for="uid+'format'">配置格式</FieldLabel><OptionPicker :id="uid+'format'" v-model="model.format" :options="['yaml','properties','json']" :disabled="disabled||!!model.restore"/></Field>
 <Accordion v-model="advanced" type="single" collapsible><AccordionItem value="fallback"><AccordionTrigger>备用目标设置（按需填写）</AccordionTrigger><AccordionContent class="grid items-start gap-4 md:grid-cols-2"><Field data-field="data_id"><FieldLabel :for="uid+'data'">备用 Data ID<ContextHelp text="优先使用资源的环境绑定。仅当目标环境未绑定 Data ID 时使用这里的值。"/></FieldLabel><Input :id="uid+'data'" v-model="model.data_id" :disabled="disabled||!!model.restore" placeholder="沿用环境绑定"/></Field><Field data-field="group"><FieldLabel :for="uid+'group'">备用 Group<ContextHelp text="优先使用环境绑定的 Group；未绑定时使用这里的值。"/></FieldLabel><Input :id="uid+'group'" v-model="model.group" :disabled="disabled||!!model.restore"/></Field></AccordionContent></AccordionItem></Accordion>
 <div class="flex items-center justify-between gap-3"><div class="flex items-center gap-1"><strong class="text-sm">配置项</strong><ContextHelp text="环境绑定的 Data ID / Group 优先。只修改已有文档中的配置项。YAML/JSON 点表示层级，值会按类型解析；properties 保留原文。保存不发布，请勿填写生产密钥。"/></div><Button v-if="!disabled&&!model.restore" variant="outline" size="sm" @click="add"><PlusIcon class="size-4"/>添加配置项</Button></div>
 <div ref="entries" class="space-y-4" data-field="entries"><section v-for="(entry,i) in model.entries" :key="i" :data-entry-index="i" class="grid items-start gap-3 rounded-lg border p-3 md:grid-cols-[minmax(0,1fr)_120px_minmax(0,1.2fr)_auto]" :aria-label="'配置项 '+(i+1)">
  <Field data-field="key"><FieldLabel :for="uid+'key'+i">配置项</FieldLabel><Input :id="uid+'key'+i" v-model="entry.key" :disabled="disabled||!!model.restore" placeholder="feature.enabled"/></Field><Field data-field="action"><FieldLabel :for="uid+'act'+i">操作</FieldLabel><OptionPicker :id="uid+'act'+i" v-model="entry.action" :disabled="disabled||!!model.restore" :options="[{value:'set',label:'设置值'},{value:'delete',label:'删除项'}]"/></Field><Field data-field="value"><FieldLabel :for="uid+'value'+i">期望值</FieldLabel><Textarea :id="uid+'value'+i" v-model="entry.value" rows="1" class="min-h-9" :disabled="disabled||!!model.restore||entry.action==='delete'"/><span class="text-xs text-muted-foreground" aria-live="polite">{{nacosValueType(entry.value,model.format,entry.action)}}</span></Field><Button v-if="!disabled&&!model.restore" variant="ghost" size="icon" class="md:mt-6" :aria-label="'删除第 '+(i+1)+' 个配置项'" @click="model.entries.splice(i,1)"><Trash2Icon class="size-4"/></Button>
 </section></div><p v-if="model.restore" class="text-sm text-muted-foreground">历史快照只读；执行前会重新预览审核。</p>
</section></template>
