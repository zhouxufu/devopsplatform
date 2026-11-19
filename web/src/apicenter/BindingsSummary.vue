<script setup>
import {computed} from 'vue'

const props=defineProps({bindings:{type:Array,default:()=>[]},cases:{type:Array,default:()=>[]}})
const label=id=>{const c=props.cases.find(c=>c.id===id);return 'TC-'+id+(c?' · '+c.name:'')}
const rows=computed(()=>props.bindings.map(b=>({...b,source:label(b.source_case_id),target:label(b.target_case_id),reference:'{{case.'+b.name+'}}'})))

import {ref as _viewRef} from 'vue'
const _view1=_viewRef([])


import {Accordion,AccordionContent,AccordionItem,AccordionTrigger,Button,Empty,EmptyDescription,EmptyHeader,Table,TableBody,TableCell,TableHead,TableHeader,TableRow} from '../shared/shadcn-ui.js'
</script>
<template><Accordion v-if="rows.length" class="bindings-summary" type="multiple" :model-value="(_view1||[]).map(String)" @update:model-value="_view1=$event"><AccordionItem item-key="bindings" :value="String('panel')"><AccordionTrigger>详细设置</AccordionTrigger><AccordionContent force-mount v-show="!(!(_view1||[]).map(String).includes(String('panel')))"><div class="min-w-0 max-w-full"><div class="max-w-full overflow-auto rounded-lg border" :style="{maxHeight:({x:650}).y?({x:650}).y+'px':undefined}"><Table class="table-fixed min-w-[var(--table-min-width)]" :style="{'--table-min-width':(({x:650}).x||({x:650}).minWidth||650)+'px'}"><TableHeader><TableRow><TableHead>来源用例 / 数据组</TableHead><TableHead :style="{width:(150)+'px'}">提取变量</TableHead><TableHead>目标用例</TableHead><TableHead :style="{width:(200)+'px'}">引用</TableHead></TableRow></TableHeader><TableBody><template v-for="(record,rowIndex) in (rows||[])" :key="typeof ('id')==='function'?('id')(record):record['id']??rowIndex"><TableRow><TableCell class="align-top"><div class="min-w-0 break-words" :class="{truncate:!!(false)}"><template v-for="{record} in [{record,rowIndex}]">{{record.source}}<div v-if="record.source_row">{{record.source_row}}</div></template></div></TableCell><TableCell class="align-top"><div class="min-w-0 break-words" :class="{truncate:!!(false)}">{{record["source_name"]}}</div></TableCell><TableCell class="align-top"><div class="min-w-0 break-words" :class="{truncate:!!(false)}"><template v-for="{record} in [{record,rowIndex}]">{{record.target}}</template></div></TableCell><TableCell class="align-top"><div class="min-w-0 break-words" :class="{truncate:!!(false)}"><template v-for="{record} in [{record,rowIndex}]"><span class="min-w-0 break-words"><code>{{record.reference}}</code></span></template></div></TableCell></TableRow></template><TableRow v-if="!(rows||[]).length"><TableCell :colspan="4"><Empty><EmptyHeader><EmptyDescription>暂无数据</EmptyDescription></EmptyHeader></Empty></TableCell></TableRow></TableBody></Table></div></div></AccordionContent></AccordionItem></Accordion></template>
<style scoped>.bindings-summary{margin:12px 0}</style>
