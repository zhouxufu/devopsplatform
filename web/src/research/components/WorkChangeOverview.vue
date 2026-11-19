<script setup>
import {computed,ref,watch} from 'vue'
import SQLCode from '../../shared/components/SQLCode.vue'
import MarkdownContent from '../../shared/components/MarkdownContent.vue'
import {Badge,Button,Input,Spinner,Empty,EmptyHeader,EmptyTitle,EmptyDescription} from '../../shared/shadcn-ui.js'
import OptionPicker from '../../shared/components/OptionPicker.vue'
import {applicationOption} from '../../shared/applicationName.js'
import {sourcePerson} from '../issueFields.js'
import {changeKinds,overviewGroups} from '../workChangeOverview.js'
const props=defineProps({workItemId:[Number,String],changes:{type:Array,default:()=>[]},services:{type:Array,default:()=>[]},people:{type:Object,default:()=>({})},loading:Boolean,error:String})
const emit=defineEmits(['retry','edit'])
const application=ref(''),kind=ref(''),query=ref('')
watch(()=>props.workItemId,()=>{application.value='';kind.value='';query.value=''})
const groups=computed(()=>overviewGroups(props.changes,{application:application.value,kind:kind.value,query:query.value}))
const visibleCount=computed(()=>groups.value.reduce((n,g)=>n+g.changes.length,0))
const applications=computed(()=>[...new Set(props.changes.map(c=>c.service).filter(Boolean))].map(name=>applicationOption(props.services.find(s=>s.name===name)||{name})))
const kinds=computed(()=>[...new Set(props.changes.map(c=>c.kind))].map(value=>({value,label:changeKinds[value]||value})))
const appLabel=name=>applications.value.find(a=>a.value===name)?.label||'未指定应用'
const person=account=>sourcePerson(account,props.people)
const risk={low:'低风险',medium:'中风险',high:'高风险'}
function clear(){application.value='';kind.value='';query.value=''}
</script>

<template>
 <section class="min-w-0 space-y-6" aria-label="工作项变更总览" :aria-busy="loading">
  <div class="flex flex-wrap items-center justify-between gap-3">
   <div><h3 class="text-lg font-semibold">变更总览</h3><p class="text-sm text-muted-foreground">共 {{changes.length}} 项 · {{changes.filter(c=>c.status==='ready').length}} 项就绪</p></div>
   <div class="flex gap-2"><Button variant="outline" :disabled="loading" @click="emit('retry')"><Spinner v-if="loading"/>刷新</Button><Button variant="outline" @click="emit('edit')">维护交付内容</Button></div>
  </div>
  <div class="grid gap-3 sm:grid-cols-[minmax(0,1fr)_minmax(150px,220px)_minmax(130px,170px)]">
   <Input v-model="query" aria-label="搜索变更内容" placeholder="搜索标题、分支、SQL 或配置项"/>
   <OptionPicker v-model="application" :options="applications" all-label="全部应用" aria-label="筛选变更应用"/>
   <OptionPicker v-model="kind" :options="kinds" all-label="全部类型" aria-label="筛选变更类型"/>
  </div>
  <div v-if="loading" role="status" class="flex items-center gap-2 py-8 text-muted-foreground"><Spinner/>正在读取全部变更</div>
  <Empty v-else-if="error"><EmptyHeader><EmptyTitle>变更内容读取失败</EmptyTitle><EmptyDescription>{{error}}</EmptyDescription></EmptyHeader><Button variant="outline" @click="emit('retry')">重试读取</Button></Empty>
  <Empty v-else-if="!visibleCount"><EmptyHeader><EmptyTitle>{{changes.length?'没有匹配的变更':'尚无交付内容'}}</EmptyTitle><EmptyDescription>{{changes.length?'调整应用、类型或搜索条件。':'录入代码、SQL、配置或人工操作后，可在这里连续查看。'}}</EmptyDescription></EmptyHeader><Button v-if="changes.length" variant="outline" @click="clear">清空筛选</Button><Button v-else variant="outline" @click="emit('edit')">录入交付内容</Button></Empty>
  <template v-else>
   <p class="text-sm text-muted-foreground" aria-live="polite">当前显示 {{visibleCount}} / {{changes.length}} 项 · 录入内容预览，实际环境目标及差异以发布预检为准</p>
   <section v-for="group in groups" :key="group.service" class="min-w-0 space-y-4" :aria-label="appLabel(group.service)">
    <h4 class="border-b pb-3 text-base font-semibold break-words">{{appLabel(group.service)}} <span class="font-normal text-muted-foreground">· {{group.changes.length}} 项</span></h4>
    <article v-for="change in group.changes" :key="change.id" class="min-w-0 space-y-4 border-b pb-6 last:border-b-0" :aria-label="'变更 #'+change.id+' '+change.title">
     <header class="space-y-2"><div class="flex flex-wrap items-center gap-2"><Badge variant="secondary">{{changeKinds[change.kind]||change.kind}}</Badge><h5 class="min-w-0 break-words font-medium">#{{change.id}} · {{change.title}}</h5><Badge variant="secondary">{{change.status==='ready'?'已就绪':change.status==='draft'?'草稿':change.status}}</Badge></div><p class="text-sm text-muted-foreground break-words">提交人 {{person(change.owner)}}<span v-if="change.risk_level"> · {{risk[change.risk_level]||change.risk_level}}</span><span v-if="change.delivery_ownership?.mode==='follow'"> · 随工作项 #{{change.delivery_ownership.work_item_id||change.delivery_ownership.delivery_work_item_id}} 交付</span></p></header>
     <MarkdownContent v-if="change.summary" :content="change.summary"/>
     <template v-if="change.kind==='mysql'&&change.sql"><p class="text-sm break-words">资源 {{change.sql.resource_alias}} · 允许影响 {{change.sql.max_affected_rows}} 行</p><SQLCode :content="change.sql.content"/><p v-if="change.sql.data_sync" class="text-sm text-muted-foreground">冻结的数据同步计划，任务 #{{change.sql.data_sync.task_id}}</p></template>
     <template v-else-if="change.kind==='nacos'&&change.nacos"><dl class="grid gap-2 text-sm sm:grid-cols-2"><div class="break-words">资源 {{change.nacos.resource_alias}}</div><div class="break-words">Data ID：{{change.nacos.data_id||'使用环境绑定'}}</div><div class="break-words">Group：{{change.nacos.group}}</div><div>格式：{{change.nacos.format}}</div></dl><div class="divide-y rounded-md border"><div v-for="(entry,index) in change.nacos.entries" :key="index" class="grid min-w-0 gap-2 p-3 sm:grid-cols-[minmax(0,1fr)_80px_minmax(0,2fr)]"><code class="break-all text-sm">{{entry.key}}</code><span class="text-sm">{{entry.action==='delete'?'删除项':'设置值'}}</span><pre v-if="entry.action!=='delete'" class="min-w-0 whitespace-pre-wrap break-all text-sm">{{entry.value}}</pre><span v-else class="text-sm text-muted-foreground">—</span></div></div></template>
     <template v-else-if="change.kind==='redis'&&change.redis"><p class="text-sm">资源 {{change.redis.resource_alias}}</p><pre class="change-content" aria-label="Redis 命令">{{change.redis.commands}}</pre></template>
     <div v-else-if="change.reference" class="space-y-1"><p class="text-sm text-muted-foreground">{{change.kind==='code'?'代码分支 / 引用':change.kind==='artifact'?'制品引用':'操作说明'}}</p><MarkdownContent v-if="change.kind==='manual'" :content="change.reference" label="操作说明"/><pre v-else class="change-content">{{change.reference}}</pre></div>
     <div v-if="change.rollback_plan" class="space-y-1"><p class="text-sm font-medium">回退说明</p><MarkdownContent :content="change.rollback_plan" label="回退说明"/></div>
    </article>
   </section>
  </template>
 </section>
</template>
<style scoped>
.change-content{max-width:100%;overflow-x:auto;white-space:pre-wrap;overflow-wrap:anywhere;border-radius:var(--radius);background:var(--muted);color:var(--foreground);padding:1rem;font-size:.8125rem;line-height:1.7;tab-size:2}
</style>
