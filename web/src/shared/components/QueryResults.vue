<script setup>
import DataTable from './DataTable.vue'
import {tableSchemaKey} from '../tablePreferences.js'

import {useTableLayout} from '../composables/tableLayout.js'
const {pinTableActions}=useTableLayout()

import {computed, ref} from 'vue'
import {prettyValue} from '../prettyValue.js'

const props=defineProps({result:Object,previous:Object,sortable:Boolean,sortField:String,sortDirection:String,expandable:Boolean,focused:Boolean}),emit=defineEmits(['pin','inspect-table','inspect-key','sort','focus']),expanded=ref(-1),compareKey=ref('id'),compare=ref(false)
const text=v=>v===null?'NULL':typeof v==='object'?JSON.stringify(v):String(v??'')
// Bound sampling cost and column widths so large values cannot squeeze other columns.
const columnWidths=computed(()=>(props.result?.columns||[]).map(col=>{
 const width=v=>Array.from(text(v).slice(0,80)).reduce((n,c)=>n+(c.charCodeAt(0)>255?13:7.5),56)
 return Math.ceil(Math.min(360,Math.max(144,width(col)+24,...(props.result?.rows||[]).slice(0,20).map(row=>width(row[col])))))
}))
const resultColumns=computed(()=>[...(props.result?.columns||[]).map((label,i)=>({id:'c'+i,label,size:columnWidths.value[i],minSize:80,pin:i===0?'left':false,ariaSort:props.sortable&&props.sortField===label?(props.sortDirection==='DESC'?'descending':'ascending'):'none'})),...(props.result?.metadata==='tables'?[{id:'actions',label:'操作',size:110,required:true,pin:'right'}]:[])])
const resultLayoutKey=computed(()=>'query-result:'+tableSchemaKey([JSON.stringify(props.result?.target||{}),...(props.result?.columns||[])]))
function toggleSort(col){if(props.sortable)emit('sort',col,props.sortField===col&&props.sortDirection==='ASC'?'DESC':'ASC')}
const tableWidth=computed(()=>columnWidths.value.reduce((a,b)=>a+b,96))
const pretty=prettyValue
const difference=computed(()=>{if(!compare.value||!props.previous||!props.result)return null;const key=compareKey.value,a=props.previous.rows||[],b=props.result.rows||[];if(!props.result.columns.includes(key)||!props.previous.columns.includes(key))return {error:'请选择两次结果共有的唯一键列'};const index=rows=>{const m=new Map();for(const r of rows){const k=r[key];if(k===null||k===undefined||m.has(String(k)))throw Error('该列不是非空唯一键，无法可靠对比');m.set(String(k),r)}return m};try{const x=index(a),y=index(b);let added=0,changed=0,removed=0;for(const [k,v] of y){if(!x.has(k))added++;else if(JSON.stringify(v)!==JSON.stringify(x.get(k)))changed++}for(const k of x.keys())if(!y.has(k))removed++;return {added,changed,removed}}catch(e){return {error:e.message}}})

import OptionPicker from './OptionPicker.vue'

import {copyText} from '../clipboard.js'



import {Button,Empty,EmptyContent,EmptyDescription,EmptyHeader,Field,FieldLabel,Table,TableBody,TableCell,TableHead,TableHeader,TableRow} from '../shadcn-ui.js'

import {CopyIcon,ArrowUpIcon,ArrowDownIcon,ArrowUpDownIcon} from '@lucide/vue'
</script>
<template><div v-if="result" class="query-results"><div class="result-head"><b>查询结果</b><small>{{(result.rows||[]).length}} 行<span v-if="result.elapsed_ms!==undefined"> · {{result.elapsed_ms}} ms</span><span v-if="result.target"> · {{result.target.connection}} / {{result.target.database||'DB '+result.target.db}}</span></small><Button v-if="expandable" :aria-pressed="focused" @click="emit('focus')" variant="outline" size="default" type="button">{{focused?'还原布局':'放大结果'}}</Button><Button @click="emit('pin')" variant="outline" size="default" type="button">保留本次结果作对照</Button><Button v-if="previous" @click="compare=!compare" variant="outline" size="default" type="button">{{compare?'收起对比':'对比保留结果'}}</Button></div><p v-if="result.truncated||result.limit_reached" class="warning">{{result.truncated?'内容已截断，结果不完整。':'已达到行数上限，可能还有更多记录。'}}请缩小范围或使用主键分页。</p><div v-if="difference" class="comparison"><Field ><FieldLabel as="div">唯一键列 </FieldLabel><OptionPicker v-model="compareKey" class="min-w-36" :options="[...(result.columns).map((col)=>({value:col,label:String(col)}))]"/></Field><span v-if="difference.error">{{difference.error}}</span><span v-else>新增 {{difference.added}} · 修改 {{difference.changed}} · 消失 {{difference.removed}}（仅对比已返回范围，不生成同步）</span></div><div class="result-scroll"><DataTable :rows="result.rows||[]" :columns="resultColumns" :table-key="resultLayoutKey" :row-key="(record,index)=>index" :max-height="focused?'calc(100dvh - 300px)':320" result-mode empty-text="没有匹配的数据">
<template v-for="(col,i) in result.columns" :key="col" #[`header-c${i}`]><Button v-if="sortable" variant="ghost" size="sm" class="h-7 max-w-full px-0" :aria-label="'按 '+col+' 排序'" @click="toggleSort(col)">{{col}}<ArrowUpIcon v-if="sortField===col&&sortDirection==='ASC'" class="size-3"/><ArrowDownIcon v-else-if="sortField===col&&sortDirection==='DESC'" class="size-3"/><ArrowUpDownIcon v-else class="size-3 opacity-50"/></Button><span v-else :title="col">{{col}}</span></template>
<template v-for="(col,i) in result.columns" :key="col" #[`cell-c${i}`]="{record}"><div class="group/value flex min-w-0 items-start gap-1"><span class="min-w-0 flex-1 truncate font-mono text-xs" :class="{'text-muted-foreground':record[col]===null}" :title="text(record[col])">{{text(record[col])}}</span><Button variant="ghost" size="icon-xs" class="shrink-0" :aria-label="'复制 '+col" @click="copyText(text(record[col]))"><CopyIcon class="size-3"/></Button></div></template>
<template #cell-actions="{record}"><Button variant="ghost" size="sm" @click="emit('inspect-table',record.table_name)">表结构</Button></template>
<template #expanded="{record}"><dl class="expanded-values"><template v-for="col in result.columns" :key="col"><dt>{{col}}</dt><dd><pre>{{pretty(record[col])}}</pre></dd></template></dl></template>
</DataTable></div></div></template>
<style scoped>.query-results{border-top:1px solid var(--border);margin-top:8px;padding-top:6px;min-width:0}.result-head{display:flex;align-items:center;gap:8px;flex-wrap:wrap;flex-shrink:0}.result-head b{white-space:nowrap}.result-head small{flex:1;min-width:100px;color:var(--muted-foreground);overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.warning{color:var(--warn);padding:0;margin:4px 0;font-size:.75rem;flex-shrink:0}.result-scroll{margin-top:6px;max-height:420px;min-height:120px;overflow:auto;resize:vertical;overscroll-behavior:contain}.data-cell{max-width:100%;margin:0;font-family:var(--mono);font-size:.75rem}.data-cell.is-null{color:var(--muted-foreground)}.expanded-values{display:grid;grid-template-columns:120px minmax(0,1fr);gap:8px;padding:8px}dt{font-weight:600}pre{white-space:pre-wrap;overflow-wrap:anywhere;max-height:280px;overflow:auto;font:.75rem/1.6 var(--mono)}.comparison{display:flex;gap:12px;align-items:center;flex-wrap:wrap;padding:8px 0;font-size:.75rem}</style>
