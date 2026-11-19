<script setup>
import {computed,nextTick,onMounted,onUnmounted,ref,watch} from 'vue'
import {onBeforeRouteLeave} from 'vue-router'
import {formatUnixTime} from '../../shared/datePresentation.js'
import {formatMetric,formatMetricBytes,metricNumber,sumMetrics} from '../../shared/observedMetrics.js'
import StatePanel from '../../shared/components/StatePanel.vue'
import PageHeader from '../../shared/components/PageHeader.vue'
import ResourcesView from '../../deploy/pages/ResourcesView.vue'
import {useWorkspaceActivity} from '../../shared/composables/workspaceActivity.js'
import {toast} from '../../shared/shadcnFeedback.js'
import {Badge,Button,Card,CardContent,CardHeader,CardTitle,Table,TableBody,TableCell,TableHead,TableHeader,TableRow} from '../../shared/shadcn-ui.js'

const stats=ref(null),loading=ref(false),loadError=ref(''),loadedAt=ref(0)
const active=useWorkspaceActivity(),managing=ref(false),agentManager=ref(null),me=ref({})
const canManage=computed(()=>me.value.role==='admin')
let timer,controller,disposed=false
async function load(){
 if(loading.value)return
 loading.value=true;controller=new AbortController();const current=controller,timeout=setTimeout(()=>current.abort(),10000)
 try{const r=await fetch('api/stats',{signal:current.signal});if(!r.ok)throw Error('采集状态读取失败');const data=await r.json();if(!Array.isArray(data.agents))throw Error('采集状态响应无效');if(disposed)return;stats.value=data;loadedAt.value=Date.now()/1000;loadError.value=''}catch(e){if(!disposed)loadError.value=current.signal.aborted?'采集状态读取超时，请重试':e.message}finally{clearTimeout(timeout);if(!disposed)loading.value=false}
}
async function manage(mode='config',agentID=''){
 managing.value=true;await nextTick()
 try{await agentManager.value?.openAgentFlow(mode,agentID)}catch(e){toast(e.message,'error')}
}
async function returnToStatus(){if(await agentManager.value?.closeAgentFlow()===false)return;managing.value=false;load()}
function managerChanged(state){if(window.parent!==window)window.parent.postMessage({type:'workspace.dirty',...state},location.origin)}
onBeforeRouteLeave(()=>!managing.value||agentManager.value?.closeAgentFlow())
watch(active,value=>{if(value)load()})
onMounted(async()=>{load();timer=setInterval(()=>{if(active.value&&!document.hidden&&!managing.value)load()},10000);try{const r=await fetch('/api/me');if(r.ok)me.value=await r.json()}catch{}})
onUnmounted(()=>{disposed=true;controller?.abort();clearInterval(timer);if(window.parent!==window)window.parent.postMessage({type:'workspace.dirty',dirty:false,busy:false},location.origin)})
const bytes=formatMetricBytes
const totalEvents=computed(()=>{const total=stats.value?.services==null?null:sumMetrics(Object.values(stats.value.services));return total===null?'—':total.toLocaleString()})
const queueDepth=computed(()=>sumMetrics(stats.value?.agents.map(a=>a.queue_depth)))
const spoolBytes=computed(()=>sumMetrics(stats.value?.agents.map(a=>a.spool_bytes)))
const onlineCount=computed(()=>sumMetrics(stats.value?.agents.map(a=>typeof a.online==='boolean'?Number(a.online):null)))
const stamp=value=>formatUnixTime(value,{month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit',second:'2-digit'})
const count=value=>metricNumber(value)?.toLocaleString()??'—'
const agentStatus=agent=>agent.online===true?(loadError.value?'上次在线':'在线'):agent.online===false?(loadError.value?'上次离线':'离线'):'未知'
</script>
<template>
 <section class="min-w-0 space-y-6">
  <PageHeader :title="managing?'采集接入与配置':'日志采集'" :show-back="managing" @back="returnToStatus">
   <template #actions><template v-if="!managing"><Button v-if="canManage" @click="manage('install')">安装 Agent</Button><Button v-if="canManage" variant="outline" @click="manage('config')">采集配置</Button><Button v-if="canManage" variant="ghost" @click="manage('records')">操作记录</Button><Button variant="ghost" :disabled="loading" @click="load">刷新</Button></template></template>
  <template #subtitle><span role="status">{{loading?'正在读取采集状态…':loadedAt?'读取于 '+stamp(loadedAt):'尚未读取采集状态'}}</span><Badge v-if="loadError&&stats" variant="secondary">上次数据 · 当前状态待核对</Badge></template></PageHeader>
  <ResourcesView v-if="managing" ref="agentManager" plugin="logs" agent-only @state="managerChanged"/>
  <template v-else>
   <StatePanel v-if="loadError&&!loading" error title="采集状态读取失败" :message="loadError"><Button variant="outline" @click="load">重试读取</Button></StatePanel>
   <StatePanel v-if="loading&&!stats" loading title="正在读取采集状态…"/>
   <template v-if="stats">
   <div class="grid gap-4 sm:grid-cols-2 xl:grid-cols-4" :aria-busy="loading">
    <Card><CardHeader><CardTitle class="text-sm font-medium text-muted-foreground">Agent 在线</CardTitle></CardHeader><CardContent><div class="text-2xl font-semibold tabular-nums">{{formatMetric(onlineCount)}} / {{stats.agents.length}}</div><p class="mt-2 truncate text-xs text-muted-foreground">{{stats.agents.map(a=>a.name||a.id).join(' · ')||'暂无'}}</p></CardContent></Card>
    <Card><CardHeader><CardTitle class="text-sm font-medium text-muted-foreground">今日事件</CardTitle></CardHeader><CardContent><div class="text-2xl font-semibold tabular-nums">{{totalEvents}}</div><p class="mt-2 text-xs text-muted-foreground">{{stats.services==null?'服务统计暂不可用':Object.keys(stats.services).length+' 个服务'}}</p></CardContent></Card>
    <Card><CardHeader><CardTitle class="text-sm font-medium text-muted-foreground">磁盘占用</CardTitle></CardHeader><CardContent><div class="text-2xl font-semibold tabular-nums">{{bytes(stats.disk_used)}}</div><p class="mt-2 text-xs text-muted-foreground">VictoriaLogs · {{stats.retention_days?'保留 '+stats.retention_days+' 天':'按服务端保留配置'}}</p></CardContent></Card>
    <Card><CardHeader><CardTitle class="text-sm font-medium text-muted-foreground">Agent 待发送</CardTitle></CardHeader><CardContent><div class="text-2xl font-semibold tabular-nums">{{bytes(spoolBytes)}}</div><p class="mt-2 text-xs text-muted-foreground">内存队列 {{formatMetric(queueDepth)}} 条</p></CardContent></Card>
   </div>
   <section v-if="stats.storage" class="space-y-3" aria-label="日志存储状态">
    <div class="flex flex-wrap items-center justify-between gap-2"><h3 class="text-base font-semibold">存储与投递</h3><Badge variant="secondary">VictoriaLogs</Badge></div>
    <dl class="grid grid-cols-2 gap-4 rounded-lg border p-4 text-sm sm:grid-cols-4">
     <div><dt class="text-muted-foreground">待投递 / 确认</dt><dd class="mt-1 font-medium tabular-nums">{{count(stats.storage.pending_batches)}} 批</dd></div>
     <div><dt class="text-muted-foreground">短期回放</dt><dd class="mt-1 font-medium tabular-nums">{{count(stats.storage.replay_batches)}} 批</dd></div>
     <div><dt class="text-muted-foreground">队列磁盘</dt><dd class="mt-1 font-medium tabular-nums">{{bytes(stats.storage.queue_bytes)}} / {{bytes(stats.storage.queue_limit_bytes)}}</dd></div>
     <div><dt class="text-muted-foreground">隔离记录</dt><dd class="mt-1 font-medium tabular-nums" :class="stats.storage.quarantined?'text-destructive':''">{{count(stats.storage.quarantined)}} 条</dd></div>
    </dl>
    <StatePanel v-if="stats.storage.last_error" :error="true" title="日志投递或查询尚未恢复" :message="stats.storage.last_error"><Button variant="outline" :disabled="loading" @click="load">重试读取</Button></StatePanel>
   </section>
   <StatePanel v-if="!loading&&!loadError&&!stats.agents.length" title="暂未发现采集 Agent" message="请核对主机采集配置与接收地址。"><Button v-if="canManage" @click="manage('install')">安装 Agent</Button></StatePanel>
   <section class="space-y-3"><h3 class="text-base font-semibold">Agent 列表</h3><div class="max-w-full overflow-auto rounded-lg border"><Table class="min-w-[76rem] table-fixed" :container-tabindex="0" container-label="Agent 列表，可横向滚动"><TableHeader><TableRow><TableHead class="w-52">Agent</TableHead><TableHead class="w-24">状态</TableHead><TableHead class="w-44">最近上报</TableHead><TableHead class="w-28">内存队列</TableHead><TableHead class="w-32">磁盘待发送</TableHead><TableHead class="w-28">累计事件</TableHead><TableHead class="w-28">累计丢弃</TableHead><TableHead v-if="canManage" class="w-52">操作</TableHead></TableRow></TableHeader><TableBody><TableRow v-for="agent in stats.agents" :key="agent.id"><TableCell :title="agent.id">{{agent.name||agent.id}}</TableCell><TableCell><Badge variant="secondary">{{agentStatus(agent)}}</Badge></TableCell><TableCell class="text-muted-foreground">{{stamp(agent.last_seen)}}</TableCell><TableCell :class="agent.queue_depth?'text-destructive':'text-muted-foreground'">{{count(agent.queue_depth)}}</TableCell><TableCell :class="agent.spool_bytes?'text-destructive':'text-muted-foreground'">{{bytes(agent.spool_bytes)}}</TableCell><TableCell>{{count(agent.events)}}</TableCell><TableCell :class="agent.dropped?'text-destructive':'text-muted-foreground'">{{count(agent.dropped)}}</TableCell><TableCell v-if="canManage"><div class="flex gap-1"><Button variant="ghost" size="sm" @click="manage('config',agent.id)">配置与下发</Button><Button variant="ghost" size="sm" @click="manage('records',agent.id)">记录</Button></div></TableCell></TableRow><TableRow v-if="!stats.agents.length"><TableCell :colspan="canManage?8:7" class="py-8 text-center text-muted-foreground">暂无采集主机</TableCell></TableRow></TableBody></Table></div></section>
   <section class="space-y-3"><h3 class="text-base font-semibold">服务（今日）</h3><div class="max-w-full overflow-auto rounded-lg border"><Table class="min-w-[340px] table-fixed"><TableHeader><TableRow><TableHead>服务</TableHead><TableHead class="w-40">事件数</TableHead></TableRow></TableHeader><TableBody><TableRow v-for="(count,service) in stats.services" :key="service"><TableCell>{{service}}</TableCell><TableCell>{{metricNumber(count)?.toLocaleString()??'—'}}</TableCell></TableRow><TableRow v-if="!Object.keys(stats.services||{}).length"><TableCell colspan="2" class="py-8 text-center text-muted-foreground">{{stats.services==null?'服务统计暂不可用':'今日暂无事件'}}</TableCell></TableRow></TableBody></Table></div></section>
   </template>
  </template>
 </section>
</template>
