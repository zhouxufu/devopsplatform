<script setup>
import {ref,computed} from 'vue'
import SchemaFields from './SchemaFields.vue'
import {schemaExample,jsonText} from './schemaExample.js'
import {Accordion,AccordionItem,AccordionTrigger,AccordionContent,Badge,Tabs,TabsList,TabsTrigger} from '../shared/shadcn-ui.js'
const props=defineProps({schema:Object,document:Object,rows:{type:Array,default:()=>[]},changes:{type:Array,default:()=>[]},location:String,example:{default:undefined},definition:{default:undefined},label:{type:String,default:'字段'}})
const mode=ref('table')
const sample=computed(()=>schemaExample(props.schema||{},props.document||{},{example:props.example}))
</script>
<template>
 <section class="my-3 min-w-0 space-y-3" :aria-label="label+'视图'" :data-document-copy-heading="label+' · '+({table:'缩进表格',json:'JSON',stack:'上下对照'})[mode]">
  <div class="flex flex-wrap items-center gap-3"><Tabs v-model="mode"><TabsList><TabsTrigger value="table">缩进表格</TabsTrigger><TabsTrigger value="json">JSON</TabsTrigger><TabsTrigger value="stack">上下对照</TabsTrigger></TabsList></Tabs></div>
  <SchemaFields v-if="mode!=='json'" :rows="rows" :changes="changes" :location="location"/>
  <div v-if="mode!=='table'" class="min-w-0 space-y-2"><div class="flex flex-wrap items-center gap-2 text-xs text-muted-foreground"><Badge variant="outline">{{sample.inferred?'按定义生成的结构示例':'文档声明的示例'}}</Badge><span>文档展示，不代表实际请求或响应</span><span v-if="sample.truncated">递归、外部引用或过大结构已省略，完整定义见下方。</span></div><pre class="max-h-[32rem] max-w-full select-text overflow-auto rounded-lg border bg-muted/30 p-4 font-mono text-xs">{{jsonText(sample.value)}}</pre><Accordion type="single" collapsible><AccordionItem value="schema-json"><AccordionTrigger>查看 JSON 定义</AccordionTrigger><AccordionContent><pre class="mt-3 max-h-[32rem] max-w-full select-text overflow-auto rounded-lg border p-4 font-mono text-xs">{{jsonText(definition??schema)}}</pre></AccordionContent></AccordionItem></Accordion></div>
 </section>
</template>
