<script setup>
import {ref,watch,onMounted,onBeforeUnmount} from 'vue'
import {Badge,Button,InputGroup,InputGroupInput,InputGroupAddon,InputGroupButton,Spinner} from '../shared/shadcn-ui.js'
import {XIcon} from '@lucide/vue'
import DataTable from '../shared/components/DataTable.vue'
import OptionPicker from '../shared/components/OptionPicker.vue'
import HelpTip from '../shared/components/HelpTip.vue'
import {applicationName} from '../shared/applicationName.js'
const props=defineProps({protocol:{type:String,default:'http'},service:{type:String,default:''},search:{type:String,default:''},group:{type:String,default:''},maintainer:{type:String,default:''},environment:{type:String,default:'__all'},services:{type:Array,default:()=>[]},environments:{type:Array,default:()=>[]},opening:Boolean})
const emit=defineEmits(['update:search','update:group','update:maintainer','open'])
const rows=ref([]),groups=ref([]),page=ref(1),total=ref(0),snapshots=ref(0),loading=ref(false),error=ref('')
let controller,timer,epoch=0
const columns=[{id:'operation',label:'功能 / 接口路径',size:340,minSize:230,flex:true,required:true},{id:'application',label:'所属应用 / 环境',size:220,minSize:170,required:true},{id:'method',label:'方法',size:90,minSize:80},{id:'tags',label:'功能分组',size:150,minSize:110},{id:'maintainer',label:'维护人',size:140,minSize:110},{id:'actions',label:'操作',size:210,minSize:190,pin:'right',required:true}]
function appName(row){return applicationName(props.services.find(s=>s.name===row.service))||row.display_name||row.service}
function envName(row){return props.environments.find(e=>e.name===row.environment)?.display_name||row.environment||'未区分环境'}
async function refresh(){
 clearTimeout(timer);controller?.abort();controller=new AbortController();const token=++epoch;loading.value=true;error.value='';rows.value=[]
 const q=new URLSearchParams({protocol:props.protocol,q:props.search,group:props.group,maintainer:props.maintainer,page:String(page.value),page_size:'15'})
 if(props.service)q.set('service',props.service)
 if(props.environment!=='__all')q.set('environment',props.environment==='__unassigned'?'':props.environment)
 try{const r=await fetch('api/api-documents/search?'+q,{signal:controller.signal}),body=await r.json();if(token!==epoch)return;if(!r.ok)throw Error(body.error||'接口检索失败');rows.value=body.items||[];groups.value=body.groups||[];total.value=body.total||0;snapshots.value=body.snapshots||0}
 catch(e){if(token===epoch&&e.name!=='AbortError'){error.value=e.message;total.value=0}}
 finally{if(token===epoch)loading.value=false}
}
watch(()=>[props.protocol,props.search,props.group,props.maintainer,props.environment,props.service],()=>{page.value=1;controller?.abort();epoch++;rows.value=[];loading.value=true;clearTimeout(timer);timer=setTimeout(refresh,250)})
function turn(value){page.value=value;refresh()}
onMounted(refresh)
onBeforeUnmount(()=>{epoch++;clearTimeout(timer);controller?.abort()})
</script>
<template>
 <section :aria-label="service?'应用接口检索':'跨应用接口检索'" class="min-w-0 space-y-4">
  <div class="flex flex-wrap items-center gap-3">
   <InputGroup class="min-w-0 flex-1 basis-72 sm:max-w-lg"><InputGroupInput :model-value="search" @update:model-value="emit('update:search',$event)" placeholder="搜索功能、路径、分组或维护人" aria-label="按功能搜索接口" @keydown.enter="refresh"/><InputGroupAddon align="inline-end"><InputGroupButton v-if="search" aria-label="清空功能搜索" size="icon-xs" @click="emit('update:search','')"><XIcon/></InputGroupButton></InputGroupAddon></InputGroup>
   <InputGroup class="w-48 max-w-full"><InputGroupInput :model-value="maintainer" @update:model-value="emit('update:maintainer',$event)" placeholder="维护人姓名或账号" aria-label="搜索维护人" @keydown.enter="refresh"/><InputGroupAddon align="inline-end"><InputGroupButton v-if="maintainer" aria-label="清空维护人搜索" size="icon-xs" @click="emit('update:maintainer','')"><XIcon/></InputGroupButton></InputGroupAddon></InputGroup><OptionPicker :model-value="group" @update:model-value="emit('update:group',$event)" aria-label="功能分组" class="w-44 max-w-full" :options="[{value:'',label:'全部分组'},...groups.map(value=>({value,label:value}))]"/>
   <Button variant="outline" :disabled="loading" @click="refresh"><Spinner v-if="loading"/>搜索</Button>
  </div>
  <div class="flex flex-wrap items-center gap-2 text-sm text-muted-foreground"><span>共 {{total}} 个接口</span><Badge variant="secondary">各环境最新文档</Badge><HelpTip text="无需选择应用即可检索功能名称、功能描述、接口路径、方法和分组。每个应用、每个环境使用最新文档快照；可选择应用查看历史版本。"/></div>
  <div v-if="error" role="alert" class="flex flex-wrap items-center justify-between gap-3 rounded-lg border p-4"><span>{{error}}</span><Button variant="outline" @click="refresh">重试</Button></div>
  <div v-if="loading" role="status" class="flex items-center gap-2 text-sm text-muted-foreground"><Spinner/>正在检索接口</div>
  <DataTable v-if="!error" :rows="rows" :columns="columns" :row-key="row=>`${row.snapshot_id}:${row.id}`" table-key="api-document-global-search">
   <template #cell-operation="{record}"><Button class="h-auto max-w-full justify-start whitespace-normal px-0 text-left" variant="link" :disabled="opening||loading" @click="emit('open',record,'view')">{{record.summary||record.path}}</Button><div class="break-all font-mono text-xs text-muted-foreground">{{record.path}}</div></template>
   <template #cell-application="{record}"><div class="break-words font-medium">{{appName(record)}}</div><div class="text-xs text-muted-foreground">{{envName(record)}} · 快照 #{{record.snapshot_id}}</div></template>
   <template #cell-method="{record}"><Badge variant="secondary">{{record.method}}</Badge><Badge v-if="record.sse" variant="outline">SSE</Badge></template>
   <template #cell-tags="{record}"><span class="break-words">{{record.tags?.join(' / ')||'—'}}</span></template>
   <template #cell-maintainer="{record}"><span class="break-words">{{record.maintainer_name||record.maintainer||'未填写'}}</span></template>
   <template #cell-actions="{record}"><div class="flex items-center gap-1"><Button size="sm" variant="ghost" :disabled="opening||loading" @click="emit('open',record,'view')">查看</Button><Button size="sm" variant="ghost" :disabled="opening||loading" @click="emit('open',record,'test-execution')">调试</Button><Button size="sm" variant="ghost" :disabled="opening||loading" @click="emit('open',record,'test-cases')">生成用例</Button></div></template>
   <template #empty><p class="p-6 text-center text-sm text-muted-foreground">{{loading?'正在检索接口':snapshots?'没有匹配的接口，可调整关键词或筛选条件':'暂无接口文档；导入或生成文档后即可按功能查询'}}</p></template>
  </DataTable>
  <div class="flex items-center justify-end gap-3"><Button variant="outline" size="sm" :disabled="loading||page<=1" @click="turn(page-1)">上一页</Button><span class="text-sm">{{page}} / {{Math.max(1,Math.ceil(total/15))}}</span><Button variant="outline" size="sm" :disabled="loading||page*15>=total" @click="turn(page+1)">下一页</Button></div>
 </section>
</template>
