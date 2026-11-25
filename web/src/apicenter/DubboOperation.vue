<script setup>
import {dubboMethod} from './dubboModel.js'
import {computed} from 'vue'
import {Badge} from '../shared/shadcn-ui.js'
import SchemaView from './SchemaView.vue'
import MarkdownContent from '../shared/components/MarkdownContent.vue'
import {jsonText} from './schemaExample.js'
import {schemaRows} from './model.js'
const props=defineProps({operation:Object,document:Object})
const method=computed(()=>dubboMethod(props.operation,props.document))
</script>
<template>
 <section class="min-w-0 space-y-6" aria-label="Dubbo 方法文档">
  <div class="flex flex-wrap gap-2"><Badge variant="outline">Group · {{method.group||'默认'}}</Badge><Badge variant="outline">Version · {{method.version||'未指定'}}</Badge></div>
  <section class="space-y-4"><h3 class="font-medium">调用参数（按顺序）</h3><p v-if="!method.parameters.length" class="text-sm text-muted-foreground">无参数方法</p>
   <div v-for="(parameter,index) in method.parameters" :key="index" class="min-w-0 space-y-2"><div class="flex flex-wrap items-baseline gap-2"><span class="font-medium">{{index+1}}. {{parameter.name}}</span><code class="break-all text-xs text-muted-foreground">{{parameter.java_type}}</code></div><MarkdownContent v-if="parameter.description" :content="parameter.description" :label="parameter.name+' 参数说明'"/><pre v-if="parameter.example!==undefined&&!parameter.schema" class="overflow-auto rounded-md bg-muted p-3 text-sm">{{jsonText(parameter.example)}}</pre><SchemaView v-if="parameter.schema" :rows="schemaRows(parameter.schema,document)" :schema="parameter.schema" :example="parameter.example" :document="document" :label="parameter.name+' 参数'"/></div>
  </section>
  <section class="space-y-3"><h3 class="font-medium">返回值 <code class="text-xs text-muted-foreground">{{method.returns.java_type}}</code></h3><MarkdownContent v-if="method.returns.description" :content="method.returns.description" label="返回值说明"/><SchemaView v-if="method.returns.schema" :rows="schemaRows(method.returns.schema,document)" :schema="method.returns.schema" :example="method.returns.example" :document="document" label="Dubbo 返回值"/></section>
  <section v-if="method.exceptions?.length" class="space-y-2"><h3 class="font-medium">声明异常</h3><div v-for="value in method.exceptions" :key="value" class="break-all font-mono text-sm">{{value}}</div></section>
 </section>
</template>
