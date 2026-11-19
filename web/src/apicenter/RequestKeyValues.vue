<script setup>
import {ref,computed} from 'vue'

import {randomUUID} from '../shared/uuid.js'
const props=defineProps({rows:{type:Array,required:true},label:{type:String,required:true},disabled:Boolean,empty:String})
const blank=()=>({key:randomUUID(),name:'',value:'',enabled:true}),emptyRow=ref(blank())
const displayRows=computed(()=>props.rows.length?props.rows:[emptyRow.value])
function update(row,field,value){let target=props.rows.find(r=>r.key===row.key);if(!target){target={...row};props.rows.push(target);emptyRow.value=blank()}target[field]=value}
const first=ref(null)
defineExpose({focus:()=>focusControl(first.value)})

import {focusControl} from '../shared/focusControl.js'


import {Button,Checkbox,Empty,EmptyDescription,EmptyHeader,Input,Table,TableBody,TableCell,TableHead,TableHeader,TableRow} from '../shared/shadcn-ui.js'
</script>
<template><div class="min-w-0 max-w-full"><div class="max-w-full overflow-auto rounded-lg border" :style="{maxHeight:({x:500}).y?({x:500}).y+'px':undefined}"><Table class="table-fixed min-w-[var(--table-min-width)]" :style="{'--table-min-width':(({x:500}).x||({x:500}).minWidth||445)+'px'}"><TableHeader><TableRow><TableHead :style="{width:(65)+'px'}">使用</TableHead><TableHead>{{label}}</TableHead><TableHead>值</TableHead><TableHead :style="{width:(80)+'px'}">操作</TableHead></TableRow></TableHeader><TableBody><template v-for="(record,rowIndex) in (displayRows||[])" :key="typeof (&quot;key&quot;)==='function'?(&quot;key&quot;)(record):record[&quot;key&quot;]??rowIndex"><TableRow><TableCell class="align-top"><div class="min-w-0 break-words" :class="{truncate:!!(false)}"><template v-for="{record} in [{record,rowIndex}]"><Checkbox :model-value="record.enabled" @update:model-value="v=>update(record,'enabled',v)" :disabled="disabled" :aria-label="'使用'+label"/></template></div></TableCell><TableCell class="align-top"><div class="min-w-0 break-words" :class="{truncate:!!(false)}"><template v-for="{record,rowIndex} in [{record,rowIndex}]"><Input :ref="el=>{if(rowIndex===0)first=el}" :model-value="record.name" placeholder="名称" @update:model-value="v=>update(record,'name',v)" :disabled="disabled" :aria-label="label+'名称'"/></template></div></TableCell><TableCell class="align-top"><div class="min-w-0 break-words" :class="{truncate:!!(false)}"><template v-for="{record} in [{record,rowIndex}]"><Input :model-value="record.value" placeholder="值" @update:model-value="v=>update(record,'value',v)" :disabled="disabled" :aria-label="label+'值'"/></template></div></TableCell><TableCell class="align-top"><div class="min-w-0 break-words" :class="{truncate:!!(false)}"><template v-for="{rowIndex} in [{record,rowIndex}]"><Button @click="rows.splice(rowIndex,1)" variant="ghost" size="default" type="button" :disabled="(disabled||!rows.length)">移除</Button></template></div></TableCell></TableRow></template><TableRow v-if="!(displayRows||[]).length"><TableCell :colspan="4">{{empty||'暂无'+label}}</TableCell></TableRow></TableBody></Table></div></div><Button @click="props.rows.push({key:randomUUID(),name:'',value:'',enabled:true})" class="add-row" variant="outline" size="default" type="button" :disabled="(disabled||rows.length>=64)">添加{{label}}</Button></template>
<style scoped>.add-row{margin-top:12px}</style>
