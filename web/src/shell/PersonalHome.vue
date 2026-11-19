<script setup>
import {formatUnixTime} from '../shared/datePresentation.js'
import {availableDailyWork,taskActionLabel} from './dailyWork.js'


import OptionPicker from '../shared/components/OptionPicker.vue'
import PageHeader from '../shared/components/PageHeader.vue'
import {ArrowRightIcon,PlusIcon,RefreshCwIcon} from '@lucide/vue'
import {computed, onMounted, onBeforeUnmount, reactive, ref, watch} from 'vue'
import ConsoleIcon from './ConsoleIcon.vue'
const props=defineProps({me:Object,plugins:Array,workspaces:Array,active:Boolean})
const emit=defineEmits(['open','resume','platform'])
const taskGroups=computed(()=>availableDailyWork(props.plugins))
const tasks=reactive({items:[],total:0,busy:false,error:'',at:0}),deliveries=reactive({items:[],total:0,busy:false,error:'',at:0})
const category=ref(''),search=ref(''),openingWork=ref('')
const assigned=reactive({items:[],total:0,busy:false,error:'',at:0,hasMore:false})
const research=computed(()=>props.plugins.find(p=>p.name==='research'))
const enabled=name=>props.plugins.some(p=>p.name===name)
const personal=computed(()=>props.me?.auth_enabled!==false&&!!props.me?.username)
const labels={approval:'待审批',manual:'待确认','code-conflict':'代码待处理',exception:'异常待处理'}
const states={blocked:'代码准备待处理',pending_approval:'待审批',waiting_confirmation:'待确认',waiting_next_batch:'待批次确认',approved:'待执行',running:'执行中',success:'执行完成',failed:'执行失败',rejected:'已拒绝',canceled:'已取消',rolled_back:'已回退',uncertain:'结果待核对',executing:'结果待核对',reviewing:'待会签'}
const state=t=>t.status==='success'&&t.stage?.includes('前置')?'前置完成 · 未发布':states[t.status]||t.status||'待处理'
const meta=t=>[t.environment,...(t.services||[t.service]).filter(Boolean),t.version].filter(Boolean).join(' · ')||'工作项协作'
const stamp=at=>formatUnixTime(at,{month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit'})
let generation=0,controllers=new Set(),timer
async function load(which){
 if(!research.value||!personal.value||which.busy)return
 const seq=generation,controller=new AbortController();controllers.add(controller)
 which.busy=true;which.error=''
 const timeout=setTimeout(()=>controller.abort(),10000)
 try{
  const query=new URLSearchParams(which===assigned?{page:'1',page_size:'20',connection:'',mine:'1',person_role:'assigned_to',status:'active'}:{page:'1',...(which===tasks?{view:'todo',kind:category.value,q:search.value}:{})})
  const response=await fetch('/api/plugins/research/api/research/'+(which===assigned?'work-items/page?':which===tasks?'tasks?':'deliveries?')+query,{signal:controller.signal,cache:'no-store'})
  const body=await response.json()
  if(!response.ok)throw Error(body.error||'服务暂时不可用')
  if(!Array.isArray(body.items)||!Number.isFinite(body.total))throw Error('服务返回格式无效')
  if(seq!==generation)return
  which.items=body.items;which.total=which===assigned&&body.total_exact===false&&body.has_more?body.items.length:body.total;which.hasMore=body.has_more===true;which.at=Date.now()
 }catch(e){if(seq===generation)which.error=controller.signal.aborted?'读取超时，可重试':e.message}
 finally{clearTimeout(timeout);controllers.delete(controller);if(seq===generation)which.busy=false}
}
function openWork(quick='assigned'){emit('open',{plugin:'research',hash:'#/work?quick='+quick})}
function openTask(task,view='todo'){if(task.work_loop_id){emit('open',{plugin:'research',hash:'#/work/'+task.work_item_id+'?'+new URLSearchParams({loop:task.work_loop_id,return:'home'})});return}emit('open',{plugin:'research',hash:'#/?'+new URLSearchParams({view,task:task.id,return:'home'})})}
async function openAssigned(item){
 if(openingWork.value)return
 const key=item.source+':'+(item.external_id||item.id);openingWork.value=key
 try{
  let reference=item
  if(!reference.id&&item.source?.startsWith('zentao:')){const response=await fetch('/api/plugins/research/api/research/work-items/attach',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({connection:item.source.slice(7),external_id:item.external_id})});reference=await response.json();if(!response.ok)throw Error(reference.error||'工作项读取失败')}
  if(!reference.id)throw Error('工作项暂不可打开，请刷新后重试')
  emit('open',{plugin:'research',hash:'#/work/'+reference.id+'?'+new URLSearchParams({return_to:'/work?quick=assigned'})})
 }catch(e){sonner.error(e.message)}finally{openingWork.value=''}
}

function refresh(){load(tasks);load(assigned);load(deliveries)}
function clearTaskFilters(){category.value='';search.value='';load(tasks)}
watch(()=>props.active,value=>{if(value)refresh()})
watch(()=>props.me?.username,()=>{generation++;controllers.forEach(c=>c.abort());for(const section of [tasks,assigned,deliveries])Object.assign(section,{items:[],total:0,error:'',busy:false,at:0});if(props.active)refresh()})
onMounted(()=>{refresh();timer=setInterval(()=>{if(props.active&&!document.hidden)refresh()},30000)})
onBeforeUnmount(()=>{generation++;clearInterval(timer);controllers.forEach(c=>c.abort())})

import {toast as sonner} from 'vue-sonner'
import {Badge,Button,Card,CardAction,CardContent,CardHeader,CardTitle,Empty,EmptyDescription,EmptyHeader,EmptyTitle,Input,Spinner,DropdownMenu,DropdownMenuTrigger,DropdownMenuContent,DropdownMenuItem} from '../shared/shadcn-ui.js'
</script>
<template>
<main data-slot="personal-home" class="h-full overflow-auto bg-background p-4 md:p-6" aria-label="个人工作台">
 <div class="console-home-body mx-auto w-full min-w-0 space-y-5">
  <PageHeader title="个人工作台" heading-tag="h1">
   <template #context>
    <div class="flex min-w-0 flex-wrap items-center gap-1" aria-label="个人待办数量">
     <Button variant="ghost" size="sm" :disabled="!personal||!research" @click="openWork('assigned')">指派给我<Badge variant="secondary" class="tabular-nums">{{assigned.error?'—':assigned.busy&&!assigned.at?'…':assigned.total}}{{assigned.hasMore?'+':''}}</Badge></Button>
     <Button variant="ghost" size="sm" :disabled="!personal||!research" @click="emit('open',{plugin:'research',hash:'#/?view=todo'})">待我处理<Badge v-if="!category&&!search" variant="secondary" class="tabular-nums">{{tasks.error?'—':tasks.busy&&!tasks.at?'…':tasks.total}}</Badge></Button>
    </div>
   </template>
   <template #actions>
    <Button v-if="enabled('devtools')" variant="ghost" @click="emit('platform','mcp')"><ConsoleIcon name="devtools"/>AI 协作接入</Button>
    <DropdownMenu v-if="research"><DropdownMenuTrigger as-child><Button variant="outline">我的工作</Button></DropdownMenuTrigger><DropdownMenuContent align="end"><DropdownMenuItem v-for="entry in [{id:'assigned',label:'指派给我'},{id:'participating',label:'我参与的'},{id:'following',label:'我关注的'},{id:'release',label:'与我相关的待上线'}]" :key="entry.id" @select="openWork(entry.id)">{{entry.label}}</DropdownMenuItem></DropdownMenuContent></DropdownMenu>
    <Button variant="ghost" size="icon" aria-label="刷新个人工作台" :disabled="tasks.busy||assigned.busy||deliveries.busy" @click="refresh"><RefreshCwIcon class="size-4"/></Button>
    <Button v-if="research" @click="emit('open',{plugin:'research',hash:'#/work/new'})"><PlusIcon class="size-4"/>新建工作项</Button>
   </template>
  </PageHeader>
  <nav v-if="taskGroups.length" aria-label="常用任务" data-home-shortcuts class="daily-work grid min-w-0 gap-5 border-y py-4" :style="{gridTemplateColumns:'repeat('+taskGroups.length+',minmax(0,1fr))'}">
   <section v-for="group in taskGroups" :key="group.id" class="min-w-0 space-y-2" :aria-label="group.label">
    <h2 class="text-xs font-medium text-muted-foreground">{{group.label}}</h2>
    <div class="flex min-w-0 flex-wrap gap-1"><Button v-for="entry in group.items" :key="entry.plugin+entry.hash" variant="ghost" size="sm" class="min-w-0 justify-start px-2" @click="emit('open',entry)"><ConsoleIcon :name="entry.plugin" :size="14"/><span>{{entry.label}}</span></Button></div>
   </section>
  </nav>
  <section v-if="workspaces.length" class="flex min-w-0 flex-wrap items-center gap-2" aria-label="继续工作">
   <h2 class="mr-1 shrink-0 text-xs font-medium text-muted-foreground">继续工作</h2>
   <Button v-for="workspace in [...workspaces].reverse().slice(0,5)" :key="workspace.id" variant="outline" size="sm" class="min-w-0 max-w-80" :title="workspace.displayTitle" @click="emit('resume',workspace.id)"><ConsoleIcon :name="workspace.plugin?.name" class="shrink-0"/><span class="truncate">{{workspace.displayTitle}}</span><Badge v-if="workspace.busy" variant="secondary">处理中</Badge><Badge v-else-if="workspace.dirty" variant="secondary">未保存</Badge></Button>
  </section>
  <div class="home-task-layout min-w-0">
   <section class="console-home-lists grid min-w-0 grid-cols-2 items-stretch gap-5">
    <Card data-home-list="tasks" class="home-queue-card min-w-0">
     <CardHeader><CardTitle>待我处理</CardTitle><CardAction><Button v-if="research&&personal" variant="ghost" size="sm" @click="emit('open',{plugin:'research',hash:'#/?view=todo'})">{{category||search?'打开待办列表':'全部待办 '+tasks.total}}<ArrowRightIcon class="size-3.5"/></Button></CardAction></CardHeader>
     <CardContent class="home-queue-content">
      <Empty v-if="!personal||!research"><EmptyHeader><EmptyDescription>{{!personal?'登录后查看个人待办':'研发工作台未启用'}}</EmptyDescription></EmptyHeader></Empty>
      <template v-else>
       <form class="grid min-h-9 shrink-0 items-center grid-cols-[130px_minmax(0,1fr)_auto] gap-2" @submit.prevent="load(tasks)"><OptionPicker v-model="category" :options="[{value:'',label:'全部待办'},...Object.entries(labels).map(([value,label])=>({value,label}))]" aria-label="待办类型" :disabled="tasks.busy" @change="load(tasks)"/><Input v-model="search" aria-label="搜索待办" placeholder="搜索任务、应用或版本"/><Button type="submit" variant="outline" :disabled="tasks.busy"><Spinner v-if="tasks.busy"/>搜索</Button></form>
       <div v-if="tasks.error" role="alert" class="flex flex-wrap items-center gap-2 text-sm text-destructive">{{tasks.error}}<Button variant="ghost" size="sm" @click="load(tasks)"><RefreshCwIcon class="size-3.5"/>重试</Button></div>
       <div v-else :aria-busy="tasks.busy" class="home-queue-list divide-y" tabindex="0" role="region" aria-label="待我处理列表">
        <article v-for="task in tasks.items.slice(0,6)" :key="task.id" class="flex min-w-0 items-start justify-between gap-3 py-3 first:pt-0">
         <div class="min-w-0 flex-1 space-y-2"><div class="flex min-w-0 flex-wrap items-center gap-2"><Badge :variant="['exception','code-conflict'].includes(task.kind)?'destructive':'secondary'">{{labels[task.kind]||task.stage||'待处理'}}</Badge><Button variant="link" class="h-auto min-w-0 whitespace-normal p-0 text-left font-medium" @click="openTask(task)">{{task.title}}</Button></div><p class="break-words text-xs text-muted-foreground">{{meta(task)}}</p><p class="line-clamp-2 text-sm text-muted-foreground">{{task.blocked_reason||task.next_action||task.instructions||'查看内容后处理'}}</p></div>
         <Button variant="outline" size="sm" class="shrink-0" @click="openTask(task)">{{taskActionLabel(task)}}</Button>
        </article>
        <Empty v-if="!tasks.items.length"><EmptyHeader><EmptyTitle class="text-sm">{{tasks.busy?'正在读取待办':category||search?'没有匹配的待办':'暂无待办'}}</EmptyTitle><EmptyDescription v-if="!tasks.busy&&(category||search)">调整类型或关键词后继续查找。</EmptyDescription></EmptyHeader><Button v-if="!tasks.busy&&(category||search)" variant="outline" size="sm" @click="clearTaskFilters">清空筛选</Button></Empty>
       </div>
      </template>
     </CardContent>
    </Card>
    <Card data-home-list="work" v-if="personal&&research" class="home-queue-card min-w-0">
     <CardHeader><CardTitle>待办工作项</CardTitle><CardAction><Button variant="ghost" size="sm" @click="openWork('assigned')">全部工作项 {{assigned.total}}{{assigned.hasMore?'+':''}}<ArrowRightIcon class="size-3.5"/></Button></CardAction></CardHeader>
     <CardContent class="home-queue-content">
      <div class="flex min-h-9 shrink-0 items-center justify-between gap-2"><span class="text-sm text-muted-foreground">指派给我的进行中工作项</span><Button variant="outline" size="sm" :disabled="assigned.busy" @click="load(assigned)"><RefreshCwIcon class="size-3.5"/>刷新</Button></div>
      <div v-if="assigned.error" role="alert" class="flex flex-wrap items-center gap-2 text-sm text-destructive">{{assigned.error}}<Button variant="ghost" size="sm" @click="load(assigned)">重试读取工作项</Button></div>
      <div v-else class="home-queue-list divide-y" :aria-busy="assigned.busy" tabindex="0" role="region" aria-label="待办工作项列表">
       <article v-for="item in assigned.items.slice(0,5)" :key="item.source+':'+(item.external_id||item.id)" class="flex min-w-0 items-start justify-between gap-3 py-3 first:pt-0">
        <div class="min-w-0 flex-1 space-y-2"><div class="flex min-w-0 flex-wrap items-center gap-2"><Badge variant="secondary">{{item.kind==='requirement'?'需求':'Bug'}} #{{item.external_id||item.id}}</Badge><Button variant="link" class="h-auto min-w-0 whitespace-normal p-0 text-left font-medium" :disabled="!!openingWork" @click="openAssigned(item)">{{item.title}}</Button></div><p class="text-xs text-muted-foreground">{{item.source==='manual'?'平台自建':item.source?.replace(/^zentao:/,'')||'本平台'}} · {{item.external_status==='active'?'处理中':item.external_status||'待处理'}}</p></div>
        <Button variant="outline" size="sm" class="shrink-0" :disabled="!!openingWork" @click="openAssigned(item)"><Spinner v-if="openingWork===item.source+':'+(item.external_id||item.id)"/>去处理</Button>
       </article>
       <Empty v-if="!assigned.items.length"><EmptyHeader><EmptyDescription>{{assigned.busy?'正在实时查询工作项…':'暂无指派给我的待处理工作项'}}</EmptyDescription></EmptyHeader></Empty>
      </div>
     </CardContent>
    </Card>
    <Card data-home-list="delivery" class="col-span-full min-w-0">
     <CardHeader><CardTitle>交付追踪</CardTitle><CardAction class="flex items-center gap-1"><Button v-if="research&&personal" variant="ghost" size="sm" @click="emit('open',{plugin:'research',hash:'#/?view=owned&attention=archived'})">已归档</Button><Button v-if="research&&personal" variant="ghost" size="sm" @click="emit('open',{plugin:'research',hash:'#/?view=owned'})">我发起的<ArrowRightIcon class="size-3.5"/></Button></CardAction></CardHeader>
     <CardContent><div v-if="deliveries.error" role="alert" class="flex flex-wrap items-center gap-2 text-sm text-destructive">{{deliveries.error}}<Button variant="ghost" size="sm" @click="load(deliveries)">重试</Button></div><div v-else class="divide-y" :aria-busy="deliveries.busy"><article v-for="task in deliveries.items.slice(0,5)" :key="task.id" class="flex min-w-0 items-start justify-between gap-3 py-3 first:pt-0"><div class="min-w-0 space-y-2"><Button variant="link" class="h-auto min-w-0 whitespace-normal p-0 text-left" @click="openTask(task,'owned')">{{task.title}}</Button><p class="break-words text-xs text-muted-foreground">{{meta(task)}}</p><p class="text-xs text-muted-foreground">{{task.stage||state(task)}} · {{task.progress||state(task)}} · {{stamp(task.updated_at||task.at)}}</p></div><Button variant="ghost" size="sm" class="shrink-0" @click="openTask(task,'owned')">查看进展<ArrowRightIcon class="size-3.5"/></Button></article><Empty v-if="!deliveries.items.length"><EmptyHeader><EmptyDescription>{{deliveries.busy?'正在读取交付':'暂无本人发起的交付'}}</EmptyDescription></EmptyHeader></Empty></div></CardContent>
    </Card>
   </section>
  </div>
 </div>
</main>
</template>

<style scoped>
.home-queue-card{height:clamp(400px,52dvh,560px);min-height:0}
.home-queue-content{display:flex;flex:1;min-height:0;flex-direction:column;gap:16px;overflow:hidden}
.home-queue-list{flex:1;min-height:0;overflow:auto;overscroll-behavior:contain;scrollbar-gutter:stable}
@media(min-width:1100px){.home-task-layout>section.console-home-lists{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));align-items:stretch}}
</style>
