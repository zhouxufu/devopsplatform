<script setup>
import PreparationScopeNotice from './PreparationScopeNotice.vue'
import {buildRunLabel} from '../buildNumber.js'


import {computed, inject, nextTick, onBeforeUnmount, onMounted, ref, watch} from 'vue'


import {toast} from '../../shared/composables/toast.js'
import {openWorkspace} from '../../research/api.js'
import {codePreparationRecovery} from '../codePreparationRecovery.js'
import {codePreparationIssue} from '../codePreparationIssue.js'
const props=defineProps({request:Object,apps:Array,protected:Boolean,managed:Boolean,inPlace:Boolean,initialId:String,canManage:{type:Boolean,default:true},defaults:{type:Object,default:()=>({})}})
const emit=defineEmits(['ready','invalidate','state','restart'])
const api=inject('api'),policies=ref({}),job=ref(null),busy=ref(false),scopeConfirmed=ref(false),error=ref('')
const buildConcurrency=ref(2)
const retryBuilds=computed(()=>job.value?.state==='failed'&&job.value.repositories?.length&&job.value.repositories.every(r=>r.status==='merged')&&job.value.builds?.some(b=>b.status==='failed'&&b.run_id>0))
const buildStates={pending:'待构建',queued:'排队中',starting:'正在启动',running:'构建中',success:'成功',failed:'失败'}
const scopeState=ref({review:null,loading:false,error:''})
const readinessIssue=computed(()=>codePreparationIssue(error.value))
function openReadinessChange(){if(readinessIssue.value)openWorkspace('deploy',readinessIssue.value.hash)}
let sequence=0,timer,clock,emitted=''
const now=ref(Date.now())
onMounted(()=>{clock=setInterval(()=>now.value=Date.now(),1000)})
const storageKey=()=>`lp-code-preparation:${window.name||'standalone'}:${origin.value}`
const pending=computed(()=>job.value&&!['ready','canceled','preview'].includes(job.value.state))
async function restore(){const generation=sequence;let id=props.initialId;try{id=id||sessionStorage.getItem(storageKey())}catch{}if(!id)return;try{const value=await api('api/delivery/code-preparations/'+id);if(generation!==sequence)return;for(const repo of value.repositories||[])for(const service of repo.services||[])policies.value[service]={...repo.policy};await nextTick();if(generation===sequence)accept(value)}catch(e){if(generation===sequence){if(props.initialId)error.value=e.message;else try{sessionStorage.removeItem(storageKey())}catch{}}}}
const origin=computed(()=>JSON.stringify([props.request?.strategy_kind,props.request?.selected_change_ids,props.request?.artifact_sources,props.request?.template_id,props.request?.version_id,props.request?.work_item_ids,props.request?.environment,props.request?.source_type]))
const states={preview:'待确认合并',merging:'正在合并 / 核对',conflict:'合并冲突',uncertain:'推送结果待核对',building:'正在构建',ready:'制品已就绪',failed:'准备失败',canceled:'已取消'}
const active=computed(()=>['merging','building'].includes(job.value?.state)&&Number(job.value?.lease_until||0)*1000>now.value)
watch([active,pending,()=>job.value?.state],()=>emit('state',{active:active.value,pending:!!pending.value,codeState:job.value?.state||''}),{immediate:true})
const recovery=computed(()=>codePreparationRecovery(job.value,{now:now.value,busy:busy.value,protectedScope:props.protected,scopeConfirmed:scopeConfirmed.value,canManage:props.canManage,scopeReview:scopeState.value.review,scopeLoading:scopeState.value.loading,scopeError:scopeState.value.error}))
watch(origin,()=>{++sequence;clearTimeout(timer);job.value=null;error.value='';scopeConfirmed.value=false;emitted='';emit('invalidate');restore()},{immediate:true})
watch(()=>props.protected,value=>{if(value)for(const service of props.apps||[])if(policies.value[service])policies.value[service].mode='isolated'})
watch(policies,()=>{if(!props.managed&&job.value?.state==='preview'){job.value=null;emitted='';emit('invalidate');try{sessionStorage.removeItem(storageKey())}catch{}}},{deep:true})
watch(()=>[props.apps,props.request?.environment],()=>{const next={};for(const service of props.apps||[])next[service]={mode:props.protected?'isolated':'integration',base_branch:'',target_branch:'',...props.defaults[service]?.code_policy,...policies.value[service]};if(JSON.stringify(policies.value)!==JSON.stringify(next))policies.value=next},{immediate:true,deep:true})
const post=(url,body)=>api(url,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)})
async function action(fn){if(busy.value)return;const generation=sequence;busy.value=true;error.value='';try{await fn()}catch(e){if(generation===sequence){error.value=e.message;toast(readinessIssue.value?.message||e.message,'error')}}finally{busy.value=false}}
function accept(value){job.value=value;try{sessionStorage.setItem(storageKey(),value.id)}catch{}if(value.state==='ready'&&emitted!==value.id){emitted=value.id;emit('ready',{id:value.id,artifacts:Object.fromEntries(value.builds.map(b=>[b.service,b.artifact_id]))})}clearTimeout(timer);if(['merging','building'].includes(value.state))timer=setTimeout(poll,3000)}
async function poll(){const id=job.value?.id,generation=sequence;if(!id)return;try{const next=await api('api/delivery/code-preparations/'+id);if(generation===sequence)accept(next)}catch(e){if(generation===sequence){error.value=e.message;timer=setTimeout(poll,5000)}}}
async function inspect(){if(busy.value)return;const generation=++sequence;await action(async()=>{emit('invalidate');emitted='';const value=await post('api/delivery/code-preparations/preview',{delivery:{...props.request,artifacts:{},preflight_token:'',code_preparation_id:''},policies:policies.value,build_concurrency:buildConcurrency.value});if(generation===sequence)accept(value)})}
async function operate(kind){await action(async()=>{
 if(kind==='resume'&&!recovery.value.enabled)throw Error(recovery.value.reason||'请先核对原任务')
 if(kind!=='cancel'&&props.protected&&!scopeConfirmed.value)throw Error('请先核对并确认生产代码范围')
 const id=job.value.id,revision=job.value.revision,generation=sequence
 const title=kind==='start'?'合并并构建':kind==='cancel'&&job.value.preparation_run_id?'终止原交付准备':kind==='cancel'?'取消代码准备':recovery.value.actionLabel
 const content=kind==='start'?'确认合并并推送到所选目标分支，然后构建？不会发布应用、SQL 或 Nacos。':kind==='cancel'?'确认终止原准备任务并释放未执行范围？原快照和审核记录保留，已推送的代码与已有制品不会撤回；不启动合并、构建或部署。':'验证指定目标分支包含全部冻结源提交，通过后恢复同一个任务并继续构建。不会自动发布应用。'
 if(!await confirmAction(kind==='retry-builds'?'只重试原提交和原配置的失败构建。成功制品保留，不重新合并或部署。':content,kind==='retry-builds'?'从原提交重试构建':title,{confirmButtonText:kind==='retry-builds'?'重试构建':kind==='start'?'确认合并并构建':kind==='cancel'?'确认取消准备':recovery.value.actionLabel}))return
 if(generation!==sequence||job.value?.id!==id)return
 const next=await post('api/delivery/code-preparations/'+id+'/'+kind,{confirm:true,confirm_scope:scopeConfirmed.value,expected_revision:revision})
 if(generation===sequence){accept(next);if(kind==='cancel'&&next.preparation_run_id){const parent=await api('api/delivery/preparations/runs/'+next.preparation_run_id);if(parent.status!=='canceled')throw Error('代码准备已取消，原交付终止状态待核对；请核对原单，不要重复提交')}}
})}
onBeforeUnmount(()=>{++sequence;clearTimeout(timer);clearInterval(clock);emit('state',{active:false,pending:false})})

import OptionPicker from '../../shared/components/OptionPicker.vue'

import {confirmAction} from '../../shared/shadcnFeedback.js'

import {ref as _viewRef} from 'vue'
const _view1=_viewRef([])



import {Accordion,AccordionContent,AccordionItem,AccordionTrigger,Button,Checkbox,Field,FieldLabel,Input,Spinner} from '../../shared/shadcn-ui.js'
</script>
<template><section class="code-preparation"><div class="prep-head"><h3>代码准备</h3><span>合并与构建在发布审批之前完成；不会自动部署。</span><Button v-if="!managed" @click="inspect" variant="outline" size="default" type="button" :disabled="(busy||active||pending)||(busy)"><Spinner v-if="busy"/>{{job||error?'重新检查来源':'检查代码来源'}}</Button></div><Field v-if="!managed&&!job"><FieldLabel as="div">同时构建的应用数</FieldLabel><OptionPicker v-model="buildConcurrency" :disabled="busy" :options="[1,2,3,4,5,6,7,8].map(value=>({value,label:value===1?'1 · 串行':String(value)}))"/></Field><Accordion v-if="!managed&&!active&&!pending" class="platform-collapse" type="multiple" :model-value="(_view1||[]).map(String)" @update:model-value="_view1=$event"><AccordionItem :value="String(&quot;panel&quot;)"><AccordionTrigger>目标分支策略 · {{apps.length}} 个应用</AccordionTrigger><AccordionContent force-mount v-show="!(!(_view1||[]).map(String).includes(String(&quot;panel&quot;)))"><div v-for="service in apps" :key="service" class="policy-row"><b>{{service}}</b><Field ><FieldLabel as="div">方式</FieldLabel><OptionPicker v-model="policies[service].mode" :options="protected?[{value:'isolated',label:'独立发布分支'}]:[{value:'integration',label:'常驻分支'},{value:'isolated',label:'独立发布分支'}]" :style="{width:'100%',maxWidth:'100%'}"/></Field><Field v-if="policies[service]?.mode==='isolated'"><FieldLabel as="div">基线分支</FieldLabel><Input v-model="policies[service].base_branch" placeholder="明确填写生产基线，如 main"/></Field><Field ><FieldLabel as="div">目标分支</FieldLabel><Input v-model="policies[service].target_branch" :placeholder="policies[service].mode==='isolated'?'新的 release/… 或 hotfix/…':'明确填写常驻分支，如 dev'"/></Field></div></AccordionContent></AccordionItem></Accordion><PreparationScopeNotice v-if="job?.preparation_run_id" :preparation="job" :in-place="inPlace" @restart="emit('restart')" :load="path=>api('api/'+path)" :can-manage="canManage" :busy="busy" @state="scopeState=$event" @cancel="operate('cancel')"/><div v-if="retryBuilds" class="recovery-summary"><p>代码已合并，构建失败，尚未发布。</p><Button :disabled="busy||!canManage" @click="operate('retry-builds')">从原提交重试构建</Button></div><div v-else-if="recovery.needsRecovery" role="status" class="recovery-summary"><div class="recovery-action-row"><h4>{{recovery.title}}</h4><Button v-if="recovery.needsRecovery" @click="operate('resume')" variant="default" size="default" type="button" :disabled="!recovery.enabled" :aria-describedby="recovery.reason?'code-recovery-reason-'+job.id:undefined"><Spinner v-if="busy"/>{{recovery.actionLabel}}</Button></div><p>{{recovery.instruction}}</p><p v-if="recovery.reason" :id="'code-recovery-reason-'+job.id">{{recovery.reason}}</p></div><div v-if="error" role="alert" class="preparation-issue"><p class="error">{{readinessIssue?.message||error}}</p><template v-if="readinessIssue"><Button :title="'保留当前交付草稿，在新标签页打开变更 #'+readinessIssue.id" @click="openReadinessChange" variant="ghost" size="default" type="button" :disabled="(busy)">打开变更 #{{readinessIssue.id}} →</Button><small>在新标签页处理；完成后回到此页点击“重新检查来源”。</small></template></div><template v-if="job"><p role="status"><b v-if="!recovery.needsRecovery">{{states[job.state]||job.state}} · </b>{{job.message}} <small>任务 {{job.id}}</small></p><article v-for="repo in job.repositories" :key="repo.url+' '+repo.target_branch"><b>{{repo.services.join('、')}} → {{repo.target_branch}}</b><p>基线：{{repo.base_branch}} · <code>{{repo.base_commit}}</code></p><p v-for="source in repo.sources" :key="source.branch">{{source.branch}} · <code>{{source.commit}}</code></p><p v-if="repo.result_commit">合并提交：<code>{{repo.result_commit}}</code></p><div v-if="repo.conflicts?.length"><b>冲突文件</b><ul><li v-for="file in repo.conflicts" :key="file">{{file}}</li></ul><p>请在本地从上述基线将冻结源提交合入 {{repo.target_branch}} 并推送。验证会核对提交覆盖关系，请保留真实合并历史。</p></div></article><p v-if="protected" class="risk">源分支可能包含其他需求的提交。系统不能自动证明提交属于哪个 Bug；请核对源分支范围，必要时先整理为独立补丁分支。</p><label v-if="protected" class="check"><Checkbox v-model="scopeConfirmed"/> 我已核对生产基线与源提交范围，不包含其他未上线需求</label><p v-if="job.builds?.length" class="text-sm text-muted-foreground">同时构建上限 {{job.build_concurrency||1}} · 已完成 {{job.builds.filter(b=>b.status==='success').length}} / {{job.builds.length}}</p><p v-for="build in job.builds" :key="build.service">{{build.service}} · {{buildStates[build.status]||build.status}} <a v-if="build.run_id" :href="'#/run/'+build.run_id" target="_blank" rel="noopener">构建 {{buildRunLabel({id:build.run_id,number:build.run_number})}}</a><span v-if="build.message"> · {{build.message}}</span><span v-if="build.artifact_id"> · 制品 #{{build.artifact_id}}</span></p><div class="prep-actions"><Button v-if="job.state==='preview'" @click="operate('start')" variant="default" size="default" type="button" :disabled="!canManage||protected&&!scopeConfirmed||busy"><Spinner v-if="busy"/>合并并构建，不部署</Button><Button v-if="active" @click="action(poll)" variant="outline" size="default" type="button" :disabled="(busy)"><Spinner v-if="busy"/>核对状态</Button><Button v-if="canManage&&!['merging','building','ready','canceled'].includes(job.state)" @click="operate('cancel')" variant="outline" size="default" type="button" :disabled="(busy)"><Spinner v-if="busy"/>取消准备</Button></div></template></section></template>
<style scoped>
.recovery-action-row{display:flex;align-items:center;justify-content:space-between;flex-wrap:wrap;gap:.75rem}.recovery-summary{padding:.25rem 0;margin:.75rem 0;overflow-wrap:anywhere}.recovery-summary h4{font-size:.875rem;font-weight:600;margin:0}.recovery-summary p{font-size:.8125rem;color:var(--muted-foreground);margin:.375rem 0 0}
.code-preparation{border:1px solid var(--border);border-radius:var(--radius);padding:14px;display:grid;gap:10px;min-width:0}.prep-head,.prep-actions{display:flex;align-items:center;gap:10px;flex-wrap:wrap}.prep-head span{color:var(--muted-foreground);flex:1}.code-preparation article{padding:10px;border-top:1px solid var(--border);overflow-wrap:anywhere}.code-preparation code{font-size:.75rem}.code-preparation p{overflow-wrap:anywhere}.policy-row{display:grid;grid-template-columns:1fr 1fr 1fr 1fr;gap:10px;align-items:end;margin-top:12px}.policy-row label{display:grid;gap:4px;min-width:0}.check{display:flex;gap:8px}.risk{color:var(--warn)}small{color:var(--muted-foreground)}@media(max-width:760px){.policy-row{grid-template-columns:1fr}.prep-head{align-items:flex-start}}</style>
<style scoped>.preparation-issue{min-width:0}.preparation-issue p{margin:0}.preparation-issue small{display:block;margin-top:4px;overflow-wrap:anywhere}</style>
