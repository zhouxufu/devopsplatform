<script setup>
// shadcn Table rendering with TanStack's official column visibility, sizing and pinning APIs.
import {computed,nextTick,onBeforeUnmount,onMounted,ref,useSlots,watch} from 'vue'
import {getCoreRowModel,useVueTable} from '@tanstack/vue-table'
import {ChevronDownIcon,ChevronRightIcon,Columns3Icon,MoreHorizontalIcon,RotateCcwIcon} from '@lucide/vue'
import {DropdownMenu,DropdownMenuTrigger,DropdownMenuContent,DropdownMenuItem,DropdownMenuSeparator,DropdownMenuCheckboxItem,DropdownMenuLabel} from '@/components/ui/dropdown-menu'
import {Button,Empty,EmptyHeader,EmptyDescription,Table,TableHeader,TableBody,TableRow,TableHead,TableCell} from '../shadcn-ui.js'
import {browserView} from '../viewMemory.js'
import {cleanTablePreferences,defaultTablePreferences} from '../tablePreferences.js'
import {tableColumnLayout} from '../tableSizing.js'
const slots=useSlots()
const props=defineProps({rows:{type:Array,default:()=>[]},columns:{type:Array,required:true},tableKey:{type:String,required:true},actor:String,rowKey:{type:[String,Function],default:'id'},settingsHost:Object,resultMode:Boolean,fill:Boolean,compact:Boolean,maxHeight:{type:[Number,String],default:'auto'},emptyText:{type:String,default:'暂无数据'},loading:Boolean})
const root=ref(null),width=ref(0),visibility=ref({}),sizing=ref({}),pinning=ref({left:[],right:[]}),expanded=ref({}),identity=ref(''),ready=ref(false)
let memory,observer,restoreGeneration=0,disposed=false,saveTimer
const availableHeight=ref(720)
const scrollHeight=computed(()=>props.maxHeight==='auto'?`min(65dvh,720px,${availableHeight.value}px)`:props.maxHeight?(typeof props.maxHeight==='number'?props.maxHeight+'px':props.maxHeight):undefined)
function measureHeight(){
 if(!root.value)return
 const container=root.value.querySelector('[data-slot=table-container]');if(!container)return
 let bottom=window.innerHeight
 for(let node=root.value.parentElement;node;node=node.parentElement){
  const css=getComputedStyle(node)
  // Ignore auto-height wrappers: their bottom follows this table and would shrink it repeatedly.
  if(/auto|scroll|hidden/.test(css.overflowY)&&(css.maxHeight!=='none'||node.matches('main,[role=dialog]')||node.scrollHeight>node.clientHeight+1))bottom=Math.min(bottom,node.getBoundingClientRect().bottom)
 }
 availableHeight.value=Math.max(160,Math.floor(bottom-container.getBoundingClientRect().top-88))
}
const defs=computed(()=>props.columns.filter(c=>c.enabled!==false).map(c=>({...c,accessorFn:row=>row[c.id],size:c.size||128,minSize:c.minSize||64,maxSize:props.resultMode?720:4096,enableHiding:!c.required,enableResizing:!c.selection})))
const defaults=computed(()=>defaultTablePreferences(defs.value))
const apply=value=>{const next=cleanTablePreferences(value,defs.value);visibility.value=Object.fromEntries(next.hidden.map(id=>[id,false]));sizing.value=Object.fromEntries(next.widths.map(v=>{const at=v.lastIndexOf('=');return [v.slice(0,at),Number(v.slice(at+1))]}));pinning.value={left:next.left,right:next.right}}
function persist(){if(!ready.value||!memory)return;memory.write(cleanTablePreferences({hidden:Object.keys(visibility.value).filter(id=>visibility.value[id]===false),widths:Object.entries(sizing.value).map(([id,n])=>id+'='+n),...pinning.value},defs.value),defaults.value)}
const update=(state,value)=>{state.value=typeof value==='function'?value(state.value):value;clearTimeout(saveTimer);saveTimer=setTimeout(persist,150)}
const table=useVueTable({get data(){return props.rows},get columns(){return defs.value},getCoreRowModel:getCoreRowModel(),getRowId:(row,index)=>String(typeof props.rowKey==='function'?props.rowKey(row,index):row[props.rowKey]??index),columnResizeMode:'onChange',state:{get columnVisibility(){return visibility.value},get columnSizing(){return sizing.value},get columnPinning(){return pinning.value}},onColumnVisibilityChange:v=>update(visibility,v),onColumnSizingChange:v=>update(sizing,v),onColumnPinningChange:v=>update(pinning,v)})
const visible=computed(()=>[...table.getLeftVisibleLeafColumns(),...table.getCenterVisibleLeafColumns(),...table.getRightVisibleLeafColumns()])
const hidden=computed(()=>table.getAllLeafColumns().filter(c=>!c.getIsVisible()))
const anyExpanded=computed(()=>Object.values(expanded.value).some(Boolean))
function toggleDetails() {
 expanded.value = anyExpanded.value ? {} : Object.fromEntries(table.getRowModel().rows.map(row => [row.id, true]))
}
const canExpand=computed(()=>hidden.value.length>0||!!slots.expanded)
const detailWidth=computed(()=>canExpand.value?36:0)
const layout=computed(()=>tableColumnLayout(visible.value.map(c=>({id:c.id,size:c.getSize(),flex:c.columnDef.flex,selection:c.columnDef.selection,pinned:c.getIsPinned(),resized:Object.hasOwn(sizing.value,c.id)})),{viewportWidth:width.value,detailWidth:detailWidth.value,resultMode:props.resultMode}))
const effectiveSize=column=>layout.value.sizes[column.id]
const totalWidth=computed(()=>layout.value.totalWidth)
const pinActive=computed(()=>width.value>=600&&visible.value.filter(c=>c.getIsPinned()).reduce((sum,c)=>sum+effectiveSize(c),0)<width.value-160)
function cellStyle(column,header=false){const pinned=pinActive.value&&column.getIsPinned(),side=pinned==='left'?'left':pinned==='right'?'right':'';return {width:effectiveSize(column)+'px',...(side?{position:'sticky',[side]:visible.value.filter(c=>c.getIsPinned()===side&&(side==='left'?visible.value.indexOf(c)<visible.value.indexOf(column):visible.value.indexOf(c)>visible.value.indexOf(column))).reduce((sum,c)=>sum+effectiveSize(c),side==='right'?detailWidth.value:0)+'px',zIndex:2,background:header?'var(--muted)':'var(--background)',boxShadow:side==='left'?'1px 0 0 var(--border)':'-1px 0 0 var(--border)'}:{})}}
function reset(){apply(defaults.value);persist()}
function resizeStart(event,column){
 if(event.type==='touchstart'&&event.touches?.length>1)return
 if(event.type==='mousedown'&&event.button!==0)return
 // TanStack starts from its stored size; seed it with the width actually rendered.
 const size=effectiveSize(column)
 table.setColumnSizing(old=>({...old,[column.id]:size}))
 table.getHeaderGroups()[0].headers.find(h=>h.column.id===column.id)?.getResizeHandler()(event)
}
function resizeKey(event,column){if(!['ArrowLeft','ArrowRight','Home'].includes(event.key))return;event.preventDefault();const size=event.key==='Home'?(column.columnDef.size||128):effectiveSize(column)+(event.key==='ArrowRight'?16:-16);table.setColumnSizing(old=>({...old,[column.id]:Math.max(column.columnDef.minSize,Math.min(column.columnDef.maxSize,size))}))}
async function restore(){const generation=++restoreGeneration;ready.value=false;apply(defaults.value);try{const actor=props.actor||(await fetch('/api/me').then(r=>r.ok?r.json():{})).username;if(disposed||generation!==restoreGeneration)return;identity.value=actor||'';if(actor){memory=browserView('table:'+props.tableKey,actor);apply(memory.read(defaults.value))}else memory=null}catch{memory=null}finally{if(generation===restoreGeneration&&!disposed)ready.value=true}}
watch([()=>props.tableKey,()=>props.actor,()=>defs.value.map(c=>c.id).join('|')],restore,{immediate:true})
watch(()=>props.rows,()=>{expanded.value={}},{flush:'post'})
onMounted(()=>{window.addEventListener('beforeunload',persist);window.addEventListener('resize',measureHeight);observer=new ResizeObserver(()=>{width.value=root.value?.querySelector('[data-slot=table-container]')?.clientWidth||root.value?.clientWidth||0;measureHeight()});if(root.value){observer.observe(root.value);const container=root.value.querySelector('[data-slot=table-container]');if(container)observer.observe(container);observer.observe(root.value.closest('[data-slot=page-surface]')||root.value.parentElement)}measureHeight()})
onBeforeUnmount(()=>{persist();clearTimeout(saveTimer);window.removeEventListener('beforeunload',persist);window.removeEventListener('resize',measureHeight);disposed=true;restoreGeneration++;observer?.disconnect()})
</script>
<template>
 <section ref="root" class="data-table min-w-0 max-w-full" :class="fill?'flex min-h-0 flex-1 flex-col gap-2':'space-y-2'" :aria-label="resultMode?'查询结果表格':'数据列表'" :aria-busy="loading">
  <Teleport :to="settingsHost || 'body'" :disabled="!settingsHost">
  <div class="flex min-h-8 min-w-0 shrink-0 flex-wrap items-center justify-end gap-2"><slot name="toolbar"/><DropdownMenu><DropdownMenuTrigger as-child><Button variant="outline" size="sm"><Columns3Icon class="size-3.5"/>{{settingsHost?'视图设置':'显示列'}}<span v-if="!settingsHost" class="text-muted-foreground">{{visible.length}} / {{defs.length}}</span></Button></DropdownMenuTrigger><DropdownMenuContent align="end" class="max-h-96 w-60 overflow-auto"><DropdownMenuLabel>显示的列</DropdownMenuLabel><DropdownMenuCheckboxItem v-for="column in table.getAllLeafColumns()" :key="column.id" :model-value="column.getIsVisible()" :disabled="!column.getCanHide()" @select.prevent @update:model-value="value=>column.toggleVisibility(!!value)">{{column.columnDef.label}}</DropdownMenuCheckboxItem><DropdownMenuSeparator/><DropdownMenuItem v-if="settingsHost && hidden.length" @select="toggleDetails">{{anyExpanded?'收起全部详情':'展开全部详情'}}</DropdownMenuItem><DropdownMenuItem @select="reset"><RotateCcwIcon class="size-4"/>恢复默认布局</DropdownMenuItem></DropdownMenuContent></DropdownMenu><Button v-if="!settingsHost && hidden.length" variant="ghost" size="sm" @click="toggleDetails()">{{anyExpanded?'收起详情':'展开详情'}}</Button></div>
  </Teleport>
  <Table class="console-density-table table-fixed w-[var(--data-table-width)]" :style="{'--data-table-width':totalWidth+'px'}" :container-class="[fill?'min-h-0 flex-1':'','overflow-auto focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-ring']" :container-style="fill?undefined:{maxHeight:scrollHeight}" :container-tabindex="0" :container-label="resultMode?'查询结果，可滚动':'数据列表，可滚动'">
   <TableHeader class="sticky top-0 z-20 bg-muted"><TableRow><TableHead :aria-sort="column.columnDef.ariaSort" v-for="column in visible" :key="column.id" class="group/column relative h-10 px-2" :style="cellStyle(column,true)"><div class="flex min-w-0 items-center gap-1"><div class="min-w-0 flex-1 truncate" :class="{'group-hover/column:pr-6 group-focus-within/column:pr-6':!column.columnDef.selection}" :title="column.columnDef.label"><slot :name="'header-'+column.id" :column="column">{{column.columnDef.label}}</slot></div><DropdownMenu v-if="!column.columnDef.selection"><DropdownMenuTrigger as-child><Button variant="ghost" size="icon-xs" class="absolute right-1 top-1/2 -translate-y-1/2 shrink-0 bg-muted opacity-0 group-hover/column:opacity-100 focus-visible:opacity-100 data-[state=open]:opacity-100" :aria-label="column.columnDef.label+'列设置'"><MoreHorizontalIcon class="size-3.5"/></Button></DropdownMenuTrigger><DropdownMenuContent align="end"><DropdownMenuItem @select="column.pin('left')">固定到左侧</DropdownMenuItem><DropdownMenuItem @select="column.pin('right')">固定到右侧</DropdownMenuItem><DropdownMenuItem :disabled="!column.getIsPinned()" @select="column.pin(false)">取消固定</DropdownMenuItem><DropdownMenuSeparator/><DropdownMenuItem :disabled="!column.getCanHide()" @select="column.toggleVisibility(false)">隐藏此列</DropdownMenuItem></DropdownMenuContent></DropdownMenu></div><div v-if="column.getCanResize()" role="separator" tabindex="0" aria-orientation="vertical" :aria-label="'调整'+column.columnDef.label+'列宽'" :aria-valuemin="column.columnDef.minSize" :aria-valuemax="column.columnDef.maxSize" :aria-valuenow="effectiveSize(column)" class="absolute inset-y-0 right-0 z-10 w-1.5 cursor-col-resize touch-none select-none hover:bg-primary/30 focus-visible:bg-primary/30 focus-visible:outline-none" @mousedown="resizeStart($event,column)" @touchstart="resizeStart($event,column)" @keydown="resizeKey($event,column)"/></TableHead><TableHead v-if="canExpand" class="sticky right-0 z-10 w-9 bg-muted"><span class="sr-only">详情</span></TableHead></TableRow></TableHeader>
   <TableBody><template v-for="row in table.getRowModel().rows" :key="row.id"><TableRow><TableCell v-for="column in visible" :key="column.id" class="whitespace-normal px-2" :class="compact?'align-middle py-1!':'align-top'" :style="cellStyle(column)"><div class="min-w-0 [overflow-wrap:anywhere]"><slot :name="'cell-'+column.id" :record="row.original" :row-index="row.index" :column="column">{{row.original[column.id]}}</slot></div></TableCell><TableCell v-if="canExpand" class="sticky right-0 z-10 w-9 bg-background px-1" :class="compact?'align-middle py-1!':'align-top'"><Button variant="ghost" size="icon-sm" :aria-label="(expanded[row.id]?'收起':'展开')+'第 '+(row.index+1)+' 行详情'" :aria-expanded="!!expanded[row.id]" @click="expanded={...expanded,[row.id]:!expanded[row.id]}"><ChevronDownIcon v-if="expanded[row.id]" class="size-4"/><ChevronRightIcon v-else class="size-4"/></Button></TableCell></TableRow><TableRow v-if="expanded[row.id]"><TableCell :colspan="visible.length+detailWidth/36" class="bg-muted/30 px-4 py-4"><div class="sticky left-4 min-w-0 max-w-full" :style="{width:Math.max(0,width-32)+'px'}"><dl v-if="!$slots.expanded" class="grid min-w-0 gap-4 sm:grid-cols-2 lg:grid-cols-3"><div v-for="column in hidden" :key="column.id" class="min-w-0 space-y-1"><dt class="text-xs text-muted-foreground">{{column.columnDef.label}}</dt><dd class="min-w-0 whitespace-normal break-words text-sm"><slot :name="'cell-'+column.id" :record="row.original" :row-index="row.index" :column="column">{{row.original[column.id]}}</slot></dd></div></dl><slot name="expanded" :record="row.original" :row-index="row.index"/></div></TableCell></TableRow></template><TableRow v-if="!rows.length"><TableCell :colspan="visible.length+(canExpand?1:0)" class="py-8"><slot name="empty"><Empty><EmptyHeader><EmptyDescription>{{loading?'正在读取…':emptyText}}</EmptyDescription></EmptyHeader></Empty></slot></TableCell></TableRow></TableBody>
  </Table>
 </section>
</template>
<style scoped>.data-table :deep([data-slot=table-container]){overscroll-behavior:contain}.data-table :deep([data-slot=table-cell] button){max-width:100%}.data-table :deep(.truncate){max-width:100%}.data-table :deep(.cell-actions){gap:4px;flex-wrap:wrap}</style>
