<script setup>
import {ref,watch} from 'vue'
import {Badge,Button,Field,FieldLabel,Input,Textarea} from '../shared/shadcn-ui.js'
import OptionPicker from '../shared/components/OptionPicker.vue'
import HelpTip from '../shared/components/HelpTip.vue'
const props=defineProps({editor:Object,disabled:Boolean})
const region=ref(null),types=ref('')
watch(()=>props.editor.request.rpc,v=>{types.value=(v?.parameter_types||[]).join(', ')},{immediate:true})
function updateTypes(value){types.value=value;props.editor.request.rpc.parameter_types=value.trim()?value.split(',').map(v=>v.trim()):[]}
const checks=[{value:'rpc_success',label:'RPC 成功'},{value:'exception',label:'异常类型等于'},{value:'json',label:'返回字段等于'},{value:'contains',label:'返回正文包含'},{value:'json_range',label:'数值范围'},{value:'schema',label:'JSON Schema'},{value:'duration',label:'耗时上限（ms）'}]
defineExpose({focus(field,index=0){const label=field==='headers'?'Dubbo 附加参数':field==='body'?'Dubbo 参数值':field==='checks'?'Dubbo 断言期望':field==='extractions'?'Dubbo 提取变量名':'Dubbo 接口名';region.value?.querySelectorAll('[aria-label="'+label+'"]')[index]?.focus()}})
</script>
<template>
 <fieldset ref="region" :disabled="disabled" class="min-w-0 space-y-5" aria-label="Dubbo 调用参数">
  <div class="grid min-w-0 grid-cols-2 gap-4"><Field><FieldLabel>接口全限定名</FieldLabel><Input v-model="editor.request.rpc.interface" aria-label="Dubbo 接口名" placeholder="com.example.HouseService"/></Field><Field><FieldLabel>方法</FieldLabel><Input v-model="editor.request.rpc.method" aria-label="Dubbo 方法名" placeholder="getHouse"/></Field><Field><FieldLabel>Group</FieldLabel><Input v-model="editor.request.rpc.group" aria-label="Dubbo Group" placeholder="默认分组"/></Field><Field><FieldLabel>Version</FieldLabel><Input v-model="editor.request.rpc.version" aria-label="Dubbo Version" placeholder="未指定版本"/></Field></div>
  <Field><FieldLabel>有序 Java 参数类型 <HelpTip text="按方法声明顺序填写，以逗号分隔；重载方法通过完整参数类型区分。无参数方法留空。"/></FieldLabel><Input :model-value="types" @update:model-value="updateTypes" aria-label="Dubbo 参数类型" placeholder="java.lang.Long, com.example.Query"/></Field>
  <Field><FieldLabel>参数值（JSON 数组）<HelpTip text="顺序与上方 Java 类型一致；支持嵌套对象、数组和变量引用。Long 大整数可直接填写，平台保留原始精度。"/></FieldLabel><Textarea v-model="editor.request.body" aria-label="Dubbo 参数值" :rows="8" spellcheck="false" class="font-mono text-sm" placeholder='[123, {"status":"active"}]'/></Field>
  <div class="grid grid-cols-2 gap-4"><Field><FieldLabel>附加参数（JSON 对象）</FieldLabel><Textarea v-model="editor.headersJSON" aria-label="Dubbo 附加参数" :rows="3" placeholder='{"token":"{{var.API_TOKEN}}"}' @update:model-value="editor.headerMode='json'"/></Field><div class="space-y-4"><Field><FieldLabel>调用超时（秒）</FieldLabel><Input v-model.number="editor.request.timeout_seconds" type="number" min="1" max="120" aria-label="Dubbo 超时"/></Field><Field v-if="editor.id"><FieldLabel>入口标识（可选）<HelpTip text="同应用有多个 Dubbo 入口时填写 endpoint:入口标识；留空使用所选入口或唯一匹配入口。"/></FieldLabel><Input v-model="editor.target_key" aria-label="Dubbo 节点入口" placeholder="endpoint:entry-name"/></Field></div></div>
  <section class="space-y-3"><div class="flex items-center justify-between"><h3 class="font-medium">自动断言</h3><Button variant="outline" size="sm" :disabled="disabled||editor.request.assertions.length>=30" @click="editor.request.assertions.push({type:'json',path:'',expected:''})">添加断言</Button></div>
   <div v-for="(check,index) in editor.request.assertions" :key="index" class="grid min-w-0 grid-cols-[190px_minmax(0,1fr)_minmax(0,1fr)_auto] gap-2"><OptionPicker v-model="check.type" :options="checks" :disabled="disabled" aria-label="Dubbo 断言类型"/><Input v-model="check.path" :disabled="disabled||!['json','json_range','schema'].includes(check.type)" aria-label="Dubbo 断言字段" placeholder="/data/id"/><Input v-model="check.expected" aria-label="Dubbo 断言期望" :placeholder="check.type==='rpc_success'?'true / false':'期望值'"/><Button variant="ghost" :disabled="disabled" @click="editor.request.assertions.splice(index,1)">移除</Button></div>
  </section>
  <section v-if="editor.extractions" class="space-y-3"><div class="flex items-center justify-between"><h3 class="font-medium">传给后续节点的变量</h3><Button variant="outline" size="sm" :disabled="disabled||editor.extractions.length>=10" @click="editor.extractions.push({source:'json',name:'',path:''})">添加提取变量</Button></div><div v-for="(value,index) in editor.extractions" :key="index" class="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] gap-2"><Input v-model="value.name" aria-label="Dubbo 提取变量名" placeholder="house_id"/><Input v-model="value.path" aria-label="Dubbo 提取字段" placeholder="/data/id"/><Button variant="ghost" :disabled="disabled" @click="editor.extractions.splice(index,1)">移除</Button></div></section>
 </fieldset>
</template>
