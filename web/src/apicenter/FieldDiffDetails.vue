<script setup>
import {computed} from 'vue'
import {Badge,Table,TableHeader,TableBody,TableRow,TableHead,TableCell} from '../shared/shadcn-ui.js'
import {describeFieldChanges} from './fieldDiff.js'
const props=defineProps({record:{type:Object,required:true}})
const changes=computed(()=>describeFieldChanges(props.record.fields,props.record.truncated))
</script>
<template>
 <section class="min-w-0 space-y-3" aria-label="字段变化明细">
  <h3 class="break-words text-sm font-medium">{{record.method}} {{record.path}} · 字段变化</h3>
  <p v-if="changes.truncated" role="status" class="text-sm text-muted-foreground">差异包含截断或无法完整解析的内容，请查看对应文档核对完整定义。</p>
  <Table class="table-fixed w-full" container-class="max-h-96 overflow-auto" :container-tabindex="0" container-label="字段变化，可滚动">
   <TableHeader class="sticky top-0 bg-muted"><TableRow><TableHead class="w-1/3">字段 / 位置</TableHead><TableHead class="w-16">变化</TableHead><TableHead>修改前</TableHead><TableHead>修改后</TableHead></TableRow></TableHeader>
   <TableBody><TableRow v-for="(row,i) in changes.rows" :key="i"><TableCell class="align-top"><code class="whitespace-normal break-all text-xs">{{row.path}}</code><div v-if="row.incomplete" class="text-xs text-muted-foreground">原值不完整，保留原文</div></TableCell><TableCell class="align-top"><Badge variant="outline">{{row.kind}}</Badge></TableCell><TableCell class="align-top"><div class="whitespace-pre-wrap break-all text-sm">{{row.before}}</div></TableCell><TableCell class="align-top"><div class="whitespace-pre-wrap break-all text-sm">{{row.after}}</div></TableCell></TableRow><TableRow v-if="!changes.rows.length"><TableCell :colspan="4">查看接口定义了解详情</TableCell></TableRow></TableBody>
  </Table>
 </section>
</template>
