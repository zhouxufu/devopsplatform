<script setup>
import {computed,ref,watch} from 'vue'
import {PlusIcon,RefreshCwIcon} from '@lucide/vue'
import DataTable from '../../shared/components/DataTable.vue'
import OptionPicker from '../../shared/components/OptionPicker.vue'
import {filterResourceInventory,resourceInventory} from '../resourceWorkspace.js'
import {resourceConnectionStatus} from '../resourceExperience.js'
import {resourceKindLabels} from '../resourceConnections.js'
import {Badge,Button,DropdownMenu,DropdownMenuContent,DropdownMenuItem,DropdownMenuTrigger,Empty,EmptyHeader,EmptyTitle,EmptyDescription,EmptyContent,Input,Pagination,PaginationContent,PaginationPrevious,PaginationNext,Spinner} from '../../shared/shadcn-ui.js'
const props=defineProps({connections:Array,hosts:Array,environments:Array,connectionKinds:Array,allowHosts:Boolean,hostOnly:Boolean,loading:Boolean,error:String,showAgents:Boolean,hostPending:Object,connectionPending:Object,hostTests:Object,connectionTests:Object,agentState:Function})
const emit=defineEmits(['create','edit','fork','remove','test','refresh','agent-config','agent-check','agent-install','agent-deployment','audit'])
const query=ref(''),kind=ref(''),page=ref(1)
const kinds=computed(()=>[...(props.allowHosts?[{value:'host',label:'主机（SSH）'}]:[]),...(props.connectionKinds||[])])
const all=computed(()=>resourceInventory(props.connections,props.allowHosts?props.hosts:[],props.connectionKinds).map(r=>({...r,typeLabel:r.kind==='host'?'主机（SSH）':resourceKindLabels[r.kind]||r.kind,environmentLabel:r.category==='hosts'?(props.environments||[]).find(e=>e.name===r.environment)?.display_name||r.environment:'跨环境复用'})))
const filtered=computed(()=>filterResourceInventory(all.value,query.value,kind.value))
const rows=computed(()=>filtered.value.slice((page.value-1)*20,page.value*20))
watch([query,kind],()=>page.value=1)
watch(()=>filtered.value.length,n=>page.value=Math.min(page.value,Math.max(1,Math.ceil(n/20))))
const pending=row=>row.category==='hosts'?props.hostPending||{}:props.connectionPending||{}
const testing=row=>(row.category==='hosts'?props.hostTests:props.connectionTests)?.[row.name]
const busy=row=>props.loading||!!pending(row)[row.name]||testing(row)==='testing'
const columns=computed(()=>props.hostOnly?[{id:'name',label:'主机',size:200,required:true,flex:true},{id:'endpoint',label:'地址',size:220,flex:true},{id:'environmentLabel',label:'环境',size:120},{id:'agent',label:'Agent 状态',size:160,enabled:props.showAgents},{id:'actions',label:'操作',size:420,required:true,pin:'right'}]:[{id:'name',label:'名称',size:200,required:true},{id:'typeLabel',label:'类型',size:180},{id:'endpoint',label:'地址 / 实例',size:280,flex:true},{id:'environmentLabel',label:'适用范围',size:140},{id:'status',label:'连接状态',size:120},{id:'actions',label:'操作',size:220,required:true,pin:'right'}])
function clear(){query.value='';kind.value=''}
</script>
<template>
 <section :aria-label="hostOnly?'主机管理':'服务连接管理'" class="min-w-0 space-y-4">
  <div class="flex flex-wrap items-center gap-3">
   <Input v-model="query" class="w-full sm:max-w-sm" :placeholder="hostOnly?'搜索主机名称、环境或地址':'搜索名称、类型或地址'" :aria-label="hostOnly?'搜索主机':'搜索服务连接'"/>
   <OptionPicker v-if="!hostOnly" v-model="kind" :options="[{value:'',label:'全部类型'},...kinds]" width="190px" aria-label="资源类型"/>
   <div class="ml-auto flex items-center gap-2">
    <Button variant="outline" size="icon" :aria-label="hostOnly?'刷新主机':'刷新服务连接'" :disabled="loading" @click="emit('refresh')"><Spinner v-if="loading"/><RefreshCwIcon v-else/></Button>
    <Button v-if="hostOnly" :disabled="loading||!!error" @click="emit('create','host')"><PlusIcon/>新增主机</Button><DropdownMenu v-else><DropdownMenuTrigger as-child><Button :disabled="loading||!!error"><PlusIcon/>新增服务连接</Button></DropdownMenuTrigger><DropdownMenuContent align="end" class="max-h-80 overflow-auto"><DropdownMenuItem v-for="k in kinds" :key="k.value" @select="emit('create',k.value)">{{k.label}}</DropdownMenuItem></DropdownMenuContent></DropdownMenu>
   </div>
  </div>
  <Empty v-if="error"><EmptyHeader><EmptyTitle>{{hostOnly?'主机读取失败':'服务连接读取失败'}}</EmptyTitle><EmptyDescription>{{error}}</EmptyDescription></EmptyHeader><EmptyContent><Button variant="outline" @click="emit('refresh')">重新读取</Button></EmptyContent></Empty>
  <template v-else>
   <DataTable :rows="rows" :columns="columns" :table-key="hostOnly?'resource-hosts-v2':'resource-connections-v2'" :loading="loading">
    <template #cell-name="{record:r}"><strong>{{r.name}}</strong><div v-if="hostOnly" class="mt-1 text-sm text-muted-foreground">SSH · {{r.user||'默认用户'}}</div></template>
    <template #cell-agent="{record:r}"><span>{{agentState?.(r)?.text||'未检测'}}</span></template>
    <template #cell-endpoint="{record:r}"><span class="break-all">{{r.kind==='aliyun-sae'?[r.config?.region,r.config?.oss_bucket].filter(Boolean).join(' / '):r.endpoint||'—'}}</span></template>
    <template #cell-environmentLabel="{record:r}"><span>{{r.environmentLabel}}</span><div v-if="r.kind==='mysql'" class="text-sm text-muted-foreground">绑定应用时选库</div></template>
    <template #cell-status="{record:r}"><div class="space-y-1"><p class="text-xs text-muted-foreground">{{r.config?.nacos_auth==='none'||r.config?.redis_auth==='none'?'无需凭据':r.secret_configured?'凭据已配置':'凭据未配置'}}</p><Badge :variant="r.last_check?.status==='failed'?'destructive':'secondary'">{{resourceConnectionStatus(r,testing(r))}}</Badge><p v-if="r.last_check?.checked_at" class="text-xs text-muted-foreground">{{new Date(r.last_check.checked_at*1000).toLocaleString('zh-CN')}}</p><p v-if="r.last_check?.affected_applications?.length" class="text-xs whitespace-normal text-muted-foreground">影响应用：{{r.last_check.affected_applications.join('、')}}</p><p v-if="r.last_check?.message" class="text-xs whitespace-normal text-muted-foreground">{{r.last_check.message}}</p></div></template>
    <template #cell-actions="{record:r}"><div class="flex flex-wrap gap-1">
     <Button variant="ghost" size="sm" :disabled="busy(r)" @click="emit('edit',r)">{{hostOnly?'连接设置':'编辑'}}</Button>
     <Button variant="ghost" size="sm" :disabled="busy(r)" @click="emit('test',r)"><Spinner v-if="testing(r)==='testing'"/>{{hostOnly?'SSH 测试':'测试连接'}}</Button>
     <template v-if="hostOnly&&showAgents"><Button variant="ghost" size="sm" :disabled="busy(r)" @click="emit('agent-check',r)"><Spinner v-if="pending(r)[r.name]==='agent-check'"/>检测 Agent</Button><Button variant="ghost" size="sm" :disabled="busy(r)" @click="emit('agent-config',r)"><Spinner v-if="pending(r)[r.name]==='config-read'"/>采集配置</Button></template>
     <DropdownMenu><DropdownMenuTrigger as-child><Button variant="ghost" size="sm" :disabled="busy(r)">更多</Button></DropdownMenuTrigger><DropdownMenuContent align="end">
      <template v-if="r.category==='hosts'&&showAgents"><DropdownMenuItem @select="emit('agent-deployment',r)">部署 Agent 接入</DropdownMenuItem><DropdownMenuItem @select="emit('agent-install',r)">安装 / 升级 Agent</DropdownMenuItem></template>
      <DropdownMenuItem v-if="r.category!=='hosts'" @select="emit('fork',r)">新建专用连接</DropdownMenuItem><DropdownMenuItem @select="emit('audit',r)">操作记录</DropdownMenuItem><DropdownMenuItem class="text-destructive" @select="emit('remove',r)">删除资源</DropdownMenuItem>
     </DropdownMenuContent></DropdownMenu>
    </div></template>
    <template #empty><Empty><EmptyHeader><EmptyTitle>{{loading?'正在读取资源':all.length?'没有匹配的资源':hostOnly?'尚未登记主机':'尚未添加服务连接'}}</EmptyTitle><EmptyDescription v-if="!all.length&&!loading">{{hostOnly?'登记主机后即可在应用环境直接选择；需要采集日志时再配置 Agent。':'添加所需服务连接，再在应用中按环境绑定。'}}</EmptyDescription></EmptyHeader><EmptyContent v-if="all.length&&!loading"><Button variant="outline" @click="clear">清空筛选</Button></EmptyContent></Empty></template>
   </DataTable>
   <div class="flex flex-wrap items-center justify-between gap-3"><span class="text-sm text-muted-foreground">共 {{filtered.length}} 项</span><Pagination class="mx-0 w-auto" :page="page" :total="filtered.length" :items-per-page="20" @update:page="page=$event"><PaginationContent><PaginationPrevious/><span class="px-2 text-sm">{{page}} / {{Math.max(1,Math.ceil(filtered.length/20))}}</span><PaginationNext/></PaginationContent></Pagination></div>
  </template>
 </section>
</template>
