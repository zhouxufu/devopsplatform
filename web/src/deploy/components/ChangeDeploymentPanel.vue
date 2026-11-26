<script setup>
import {computed, inject, onBeforeUnmount, ref, watch} from 'vue'
import {useRoute, useRouter} from 'vue-router'
import OptionPicker from '../../shared/components/OptionPicker.vue'
import {Accordion,AccordionItem,AccordionTrigger,AccordionContent,Button, Spinner} from '../../shared/shadcn-ui.js'
import {randomUUID} from '../../shared/uuid.js'

const props=defineProps({changes:Array, request:Object, preparationId:Number, taskId:String})
const api=inject('api'), route=useRoute(), router=useRouter()
const grant=ref(null), task=ref(null), busy=ref(false), error=ref('')
let timer, sequence=0, requestId=''
const modes=ref({})
const codeApps=computed(()=>[...new Set((props.changes||[]).filter(c=>c.kind==='code'&&props.request?.artifact_sources?.[c.service]!=='existing').map(c=>c.service))])
const choices=[{value:'direct',label:'直接构建并发布'},{value:'merge',label:'合并后构建并发布'}]
const spec=computed(()=>{
 const rows=props.changes||[],r=props.request||{},first=rows[0],base={mode:'change',environment:r.environment,preparation_id:props.preparationId||undefined,reason:r.reason||first?.title}
 if(rows.length===1&&!first.work_items?.length&&first.status==='draft')return {...base,service:first.service,change_id:first.id}
 const request={environment:r.environment,reason:base.reason,source_type:r.source_type,template_id:r.template_id||undefined,version_id:r.version_id||undefined,work_item_ids:r.work_item_ids,selected_change_ids:rows.map(c=>c.id),artifact_sources:r.artifact_sources,artifacts:r.artifacts,scene:r.scene,nodes:r.nodes?.length?r.nodes:undefined}
 if(!request.artifact_sources||!Object.keys(request.artifact_sources).length)delete request.artifact_sources
 if(request.source_type==='changes'){request.source_change_ids=request.selected_change_ids;const linked=[...new Set(rows.flatMap(c=>(c.work_items||[]).map(w=>w.id)))];if(linked.length&&rows.every(c=>c.work_items?.length)){request.source_type='work_items';request.work_item_ids=linked;delete request.source_change_ids}}
 const selectedModes={...r.code_modes,...modes.value};if(Object.keys(selectedModes).length)request.code_modes=selectedModes
 return {...base,delivery:request}
})
watch(()=>JSON.stringify([props.request?.environment,props.changes?.map(c=>c.id)]),()=>{modes.value={}})
const post=(path,body)=>api('api/service-deployments'+path,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)})
const stages={authorized:'发布已授权',create_preparation:'创建准备单',confirm_scope:'确认本次范围',prepare:'准备合并候选',merge_build:'合并与构建',merging:'合并代码',building:'构建制品',create_delivery:'创建发布执行单',delivery:'发布与结果核对',deliver:'发布与结果核对',waiting_review:'等待指定人员',waiting_conflict:'等待审核冲突解决结果',complete:'发布完成'}
async function preview(){
  if(busy.value)return
  const seq=sequence;busy.value=true;error.value=''
  try{
    const {service,environment,change_id}=spec.value
    const existing=change_id&&await api('api/service-deployments/by-change?'+new URLSearchParams({service,environment,change_id}))
    if(seq!==sequence)return
    if(existing?.state==='running'){await router.push({path:route.path,query:{...route.query,change_deployment:existing.id}});return}
    const value=await post('/preview',spec.value);if(seq===sequence){if(value.original_task_id){await router.push({path:route.path,query:{...route.query,change_deployment:value.original_task_id}});return}grant.value=value;requestId=randomUUID().replaceAll('-','')}}
  catch(e){if(seq===sequence)error.value=e.message}finally{if(seq===sequence)busy.value=false}
}
async function confirm(){
  if(busy.value||!grant.value)return
  const seq=sequence;busy.value=true;error.value=''
  try{const result=await post('',{id:requestId,spec:grant.value.spec,expected_digest:grant.value.digest});if(seq!==sequence)return;await router.push({path:route.path,query:{...route.query,change_deployment:result.id}})}
  catch(e){if(seq===sequence){error.value=e.message;try{const original=await api('api/service-deployments/'+requestId);if(seq===sequence&&original?.grant?.digest===grant.value?.digest)await router.push({path:route.path,query:{...route.query,change_deployment:original.id}})}catch{/* Only the original receipt can resolve an interrupted submission. */}}}finally{if(seq===sequence)busy.value=false}
}
async function read(){
  if(!props.taskId||busy.value)return
  const seq=sequence;clearTimeout(timer);busy.value=true
  try{const result=await api('api/service-deployments/'+encodeURIComponent(props.taskId));if(seq===sequence){task.value=result;error.value=''}}catch(e){if(seq===sequence)error.value=e.message}
  finally{if(seq===sequence){busy.value=false;if(task.value?.state==='running')timer=setTimeout(read,3000)}}
}
watch(()=>[props.taskId,JSON.stringify(spec.value)],()=>{sequence++;clearTimeout(timer);grant.value=null;task.value=null;busy.value=false;error.value='';if(props.taskId)read()},{immediate:true})
onBeforeUnmount(()=>{sequence++;clearTimeout(timer)})
</script>
<template>
  <section class="my-3 space-y-3 rounded-lg border p-4" aria-label="整次变更发布">
    <template v-if="taskId">
      <header class="flex items-center justify-between gap-3"><h3 class="font-semibold">发布进度</h3><Button variant="outline" :disabled="busy" @click="read">刷新状态</Button></header>
      <p v-if="task" role="status">{{task.state==='blocked'?'发布待核对':task.state==='failed'?'发布失败':stages[task.stage]||task.stage}} · {{task.message}}</p>
      <p v-if="task" class="text-sm text-muted-foreground">{{task.grant.spec.environment}} <span v-if="task.grant.spec.change_id">· 变更 #{{task.grant.spec.change_id}}</span> <span v-if="task.preparation_id"> · 准备单 #{{task.preparation_id}}</span><span v-if="!task.artifacts && task.run_id"> · 构建记录 ID {{task.run_id}}</span><span v-if="!task.artifacts && task.artifact_id"> · 制品 #{{task.artifact_id}}</span><span v-if="!task.artifacts && task.release_id"> · 发布记录 ID {{task.release_id}}</span></p>
      <p v-for="(id,app) in task?.artifacts" :key="app">{{app}} · 制品 #{{id}} <span v-if="task.releases?.[app]">· 发布 #{{task.releases[app]}}</span></p>
      <div v-if="task?.preparation_id||task?.delivery_id" class="flex gap-2"><Button v-if="task.preparation_id" variant="outline" @click="router.push({path:route.path,query:{preparation:String(task.preparation_id)}})">查看准备单</Button><Button v-if="task.delivery_id" variant="outline" @click="router.push({path:route.path,query:{run:String(task.delivery_id)}})">查看执行单</Button></div>
      <p v-if="task?.built_commit && !task.artifacts" class="text-sm break-all">本次制品提交：{{task.built_commit}}</p>
      <p v-if="task?.grant.change?.source_branch" class="text-sm break-all">{{task.grant.change.source_branch}} · {{task.grant.change.source_commit}} → {{task.grant.change.target_branch}}</p>
    </template>
    <template v-else>
      <div v-if="!grant && spec.delivery && !preparationId" v-for="app in codeApps" :key="app" class="flex items-center gap-3"><span>{{app}}</span><OptionPicker :aria-label="app+'发布方式'" :model-value="modes[app]||request?.code_modes?.[app]||'merge'" :options="choices" :disabled="busy" @update:model-value="v=>modes={...modes,[app]:v}"/></div>
      <Button v-if="!grant" :disabled="busy" @click="preview"><Spinner v-if="busy"/>预览整次发布</Button>
      <template v-else>
        <h3 class="font-semibold">{{grant.spec.service || '本次交付'}} 发布到 {{grant.spec.environment}}</h3>
        <template v-if="grant.change.publication_scope"><ul><li v-for="app in grant.change.publication_scope.applications" :key="app.service">{{app.service}} · {{({direct:'直接构建并发布',merge:'合并后构建并发布',verify:'核对已合入代码并构建',artifact:'使用已有制品',resource:'资源变更'})[app.mode]}} · {{app.targets?.join('、')}} <span v-if="app.artifact_id">· 制品 #{{app.artifact_id}} · {{app.artifact_digest}}</span></li></ul><p v-for="repo in grant.change.publication_scope.repositories" :key="repo.service+repo.target_branch" class="text-sm break-all">{{repo.sources.map(s=>s.branch+' @ '+s.commit).join('、')}} → {{repo.target_branch}} @ {{repo.target_commit}}</p><p v-for="c in grant.change.publication_scope.changes" :key="c.id">#{{c.id}} · {{c.title}}</p><Accordion v-if="grant.change.publication_scope.resource_preview?.length" type="single" collapsible><AccordionItem value="resources"><AccordionTrigger>查看环境变更</AccordionTrigger><AccordionContent><ul><li v-for="(d,index) in grant.change.publication_scope.resource_preview" :key="index"><span>{{d.target}} · #{{d.change_id}}</span><pre class="whitespace-pre-wrap break-words">{{d.content}}</pre></li></ul></AccordionContent></AccordionItem></Accordion></template><template v-else><p>#{{grant.spec.change_id}} · {{grant.change.title}}</p>
        <p class="text-sm break-all">{{grant.change.source_branch}} · {{grant.change.source_commit}} → {{grant.change.target_branch}} · {{grant.change.target_commit}}</p>
        <p class="text-sm">发布目标：{{grant.targets.join('、')||grant.target_type}}。仅发布本次构建的制品。</p></template>
        <p v-if="grant.change.publication_scope?.requires_review" class="text-sm">执行前需要独立审批<span v-if="grant.change.publication_scope.environment_reviewers?.length"> · {{grant.change.publication_scope.environment_reviewers.join('、')}}</span></p>
        <ol class="list-decimal pl-6 text-sm"><li v-if="grant.change.mark_ready_draft_digest">校验并将变更 #{{grant.spec.change_id}} 标为就绪</li><li v-for="n in [...grant.change.preparation_steps,...grant.change.release_steps]" :key="n.kind+':'+n.id">{{n.name}}<span v-if="n.approvers?.length"> · {{n.approvers.join('、')}}</span><div v-for="test in n.extension?.tests" :key="test.service">{{test.service}} · 回归测试集 #{{test.suite_id}}（修订 {{test.suite_revision}}） · {{test.target_key}}</div></li></ol>
        <div class="flex gap-2"><Button :disabled="busy" @click="confirm"><Spinner v-if="busy"/>确认并发布到 {{grant.spec.environment}}</Button><Button variant="ghost" :disabled="busy" @click="grant=null">返回</Button></div>
      </template>
    </template>
    <p v-if="error" role="alert" class="text-sm text-destructive">{{error}}</p>
  </section>
</template>
