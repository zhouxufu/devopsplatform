<script setup>
import ListPagination from '../shared/components/ListPagination.vue'
import {computed,ref,watch,onMounted,onBeforeUnmount} from 'vue'
import {Badge,Button,Field,FieldGroup,FieldLabel,InputGroup,InputGroupInput,InputGroupAddon,InputGroupButton,Popover,PopoverTrigger,PopoverContent,Spinner} from '../shared/shadcn-ui.js'
import {DropdownMenu,DropdownMenuTrigger,DropdownMenuContent,DropdownMenuItem} from '@/components/ui/dropdown-menu'
import {FilterIcon,MoreHorizontalIcon,XIcon} from '@lucide/vue'
import DataTable from '../shared/components/DataTable.vue'
import OptionPicker from '../shared/components/OptionPicker.vue'
import HelpTip from '../shared/components/HelpTip.vue'
import {reportTime} from './reportModel.js'
import {applicationName} from '../shared/applicationName.js'
const props=defineProps({protocol:{type:String,default:'http'},service:{type:String,default:''},search:{type:String,default:''},group:{type:String,default:''},maintainer:{type:String,default:''},environment:{type:String,default:'__all'},services:{type:Array,default:()=>[]},environments:{type:Array,default:()=>[]},opening:Boolean})
const emit=defineEmits(['update:search','update:group','update:maintainer','open','total'])
const sort=ref(''),rows=ref([]),groups=ref([]),page=ref(1),pageSize=ref(20),total=ref(0),snapshots=ref(0),loading=ref(false),error=ref('')
const filterCount=computed(()=>Number(!!props.group)+Number(!!props.maintainer)+Number(props.protocol!=='http'))
let controller,timer,epoch=0
const columns=[{id:'operation',label:'功能 / 接口路径',size:340,minSize:220,flex:true,required:true},{id:'application',label:'所属应用 / 环境',size:145,minSize:110,required:true},{id:'update',label:'文档更新',size:185,minSize:155,required:true},{id:'tags',label:'功能分组',size:130,minSize:100},{id:'maintainer',label:'维护人',size:100,minSize:90},{id:'actions',label:'操作',size:100,minSize:100,pin:'right',required:true}]
function appName(row){return applicationName(props.services.find(s=>s.name===row.service))||row.display_name||row.service}
function envName(row){return props.environments.find(e=>e.name===row.environment)?.display_name||row.environment||'未区分环境'}
async function refresh(){
 clearTimeout(timer);controller?.abort();controller=new AbortController();const token=++epoch;loading.value=true;error.value='';rows.value=[]
 const q=new URLSearchParams({protocol:props.protocol,q:props.search,group:props.group,maintainer:props.maintainer,page:String(page.value),page_size:String(pageSize.value),sort:sort.value})
 if(props.service)q.set('service',props.service)
 if(props.environment!=='__all')q.set('environment',props.environment==='__unassigned'?'':props.environment)
 try{const r=await fetch('api/api-documents/search?'+q,{signal:controller.signal}),body=await r.json();if(token!==epoch)return;if(!r.ok)throw Error(body.error||'接口检索失败');rows.value=body.items||[];groups.value=body.groups||[];total.value=body.total||0;snapshots.value=body.snapshots||0;emit('total',total.value)}
 catch(e){if(token===epoch&&e.name!=='AbortError'){error.value=e.message;total.value=0;emit('total',0)}}
 finally{if(token===epoch)loading.value=false}
}
watch(()=>[props.protocol,props.search,props.group,props.maintainer,props.environment,props.service],()=>{page.value=1;controller?.abort();epoch++;rows.value=[];loading.value=true;clearTimeout(timer);timer=setTimeout(refresh,250)})
function turn(value){page.value=value;refresh()}
onMounted(refresh)
onBeforeUnmount(()=>{epoch++;clearTimeout(timer);controller?.abort()})
</script>
<template>
 <section :aria-label="service?'应用接口检索':'跨应用接口检索'" class="document-search flex min-h-0 min-w-0 flex-1 flex-col gap-2">
  <div class="flex shrink-0 flex-wrap items-center gap-2">
   <InputGroup class="min-w-0 flex-1 basis-64"><InputGroupInput :model-value="search" @update:model-value="emit('update:search',$event)" placeholder="搜索功能、路径、分组或维护人" aria-label="按功能搜索接口" @keydown.enter="refresh"/><InputGroupAddon align="inline-end"><InputGroupButton v-if="search" aria-label="清空功能搜索" size="icon-xs" @click="emit('update:search','')"><XIcon/></InputGroupButton></InputGroupAddon></InputGroup>
   <slot name="scope"/>
   <Popover><PopoverTrigger as-child><Button variant="outline" size="sm"><FilterIcon/>更多筛选<Badge v-if="filterCount" variant="secondary">{{filterCount}}</Badge></Button></PopoverTrigger><PopoverContent align="end" class="w-80 max-w-[calc(100vw-2rem)]"><FieldGroup class="gap-4">
    <slot name="advanced"/>
    <Field><FieldLabel>维护人</FieldLabel><InputGroup><InputGroupInput :model-value="maintainer" @update:model-value="emit('update:maintainer',$event)" placeholder="维护人姓名或账号" aria-label="搜索维护人" @keydown.enter="refresh"/><InputGroupAddon align="inline-end"><InputGroupButton v-if="maintainer" aria-label="清空维护人搜索" size="icon-xs" @click="emit('update:maintainer','')"><XIcon/></InputGroupButton></InputGroupAddon></InputGroup></Field>
    <Field><FieldLabel>功能分组</FieldLabel><OptionPicker :model-value="group" @update:model-value="emit('update:group',$event)" aria-label="功能分组" :options="[{value:'',label:'全部分组'},...groups.map(value=>({value,label:value}))]"/></Field>
   </FieldGroup></PopoverContent></Popover>
   <Button variant="outline" size="sm" :disabled="loading" @click="refresh"><Spinner v-if="loading"/>搜索</Button>
  </div>
  <div v-if="filterCount" class="flex shrink-0 flex-wrap items-center gap-2 text-xs" aria-label="已选筛选条件">
   <Badge v-if="protocol!=='http'" variant="outline">Dubbo</Badge>
   <Button v-if="group" variant="outline" size="sm" class="h-7 max-w-full text-xs" @click="emit('update:group','')" :aria-label="'清除分组：'+group"><span class="truncate">分组：{{group}}</span><XIcon class="size-3"/></Button>
   <Button v-if="maintainer" variant="outline" size="sm" class="h-7 max-w-full text-xs" @click="emit('update:maintainer','')" :aria-label="'清除维护人：'+maintainer"><span class="truncate">维护人：{{maintainer}}</span><XIcon class="size-3"/></Button>
  </div>
  <div v-if="error" role="alert" class="flex flex-wrap items-center justify-between gap-3 rounded-lg border p-4"><span>{{error}}</span><Button variant="outline" @click="refresh">重试</Button></div>
  <DataTable v-if="!error" fill compact :loading="loading||opening" :rows="rows" :columns="columns" :row-key="row=>`${row.snapshot_id}:${row.id}`" table-key="api-document-global-search">
   <template #toolbar><div class="mr-auto flex min-w-0 flex-wrap items-center gap-2 text-xs text-muted-foreground"><Spinner v-if="loading"/><span role="status">{{loading?'正在检索接口':'各应用／环境当前文档'}}</span><HelpTip text="当前指平台最近保存的文档，不能据此证明与代码或运行版本一致。可选择应用查看历史版本。"/></div><OptionPicker v-model="sort" aria-label="接口排序" class="w-36" :options="[{value:'',label:'默认排序'},{value:'updated',label:'最近变更优先'}]" @update:model-value="page=1;refresh()"/></template>
   <template #cell-operation="{record}"><div class="flex min-w-0 items-center gap-2"><Badge variant="secondary" class="shrink-0 px-1.5 py-0 text-[11px]">{{record.method}}</Badge><Badge v-if="record.sse" variant="outline" class="shrink-0 px-1 py-0 text-[11px]">SSE</Badge><Button class="h-auto min-w-0 justify-start truncate p-0 text-left font-medium" variant="link" :disabled="opening||loading" :title="record.summary||record.path" @click="emit('open',record,'view')">{{record.summary||record.path}}</Button></div><div class="mt-0.5 truncate font-mono text-xs text-muted-foreground" :title="record.path">{{record.path}}</div></template>
   <template #cell-application="{record}"><div class="truncate font-medium" :title="appName(record)">{{appName(record)}}</div><div class="truncate text-xs text-muted-foreground" :title="envName(record)">{{envName(record)}}</div></template>
   <template #cell-update="{record}"><div class="truncate text-xs" :title="record.update?.changed_at?reportTime(record.update.changed_at):'变更时间未记录'">{{record.update?.changed_at?reportTime(record.update.changed_at):'变更时间未记录'}}</div><div class="truncate text-xs text-muted-foreground">文档 #{{record.snapshot_id}}<span v-if="record.update?.changed_at"> · {{record.update.kind==='added'?'新增':'修改'}}</span><span v-if="record.update?.snapshot_id&&record.update.snapshot_id!==record.snapshot_id"> · 本次未变</span></div></template>
   <template #cell-tags="{record}"><span class="block truncate" :title="record.tags?.join(' / ')||'—'">{{record.tags?.join(' / ')||'—'}}</span></template>
   <template #cell-maintainer="{record}"><span class="block truncate" :title="record.maintainer_name||record.maintainer||'未填写'">{{record.maintainer_name||record.maintainer||'未填写'}}</span></template>
   <template #cell-actions="{record}"><div class="flex items-center gap-0.5"><Button size="sm" variant="ghost" class="px-2" :disabled="opening||loading" @click="emit('open',record,'test-execution')">调试</Button><DropdownMenu><DropdownMenuTrigger as-child><Button size="icon-sm" variant="ghost" :disabled="opening||loading" :aria-label="(record.summary||record.path)+'更多操作'"><MoreHorizontalIcon/></Button></DropdownMenuTrigger><DropdownMenuContent align="end"><DropdownMenuItem @select="emit('open',record,'view')">查看详情</DropdownMenuItem><DropdownMenuItem @select="emit('open',record,'test-cases')">生成用例</DropdownMenuItem></DropdownMenuContent></DropdownMenu></div></template>
   <template #empty><p class="p-6 text-center text-sm text-muted-foreground">{{loading?'正在检索接口':snapshots?'没有匹配的接口，可调整关键词或筛选条件':'暂无接口文档；导入或生成文档后即可按功能查询'}}</p></template>
  </DataTable>
  <ListPagination class="shrink-0" :page="page" :total="total" :page-size="pageSize" :disabled="loading" @update:page="turn($event)" @update:page-size="pageSize=$event;page=1;refresh()"/>
 </section>
</template>
