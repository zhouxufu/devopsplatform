<script setup>
import {computed, inject, onBeforeUnmount, ref, watch} from 'vue'
import {useRoute, useRouter} from 'vue-router'
import {Button, Spinner} from '../../shared/shadcn-ui.js'
import {randomUUID} from '../../shared/uuid.js'

const props=defineProps({change:Object, request:Object, preparationId:Number, taskId:String})
const api=inject('api'), route=useRoute(), router=useRouter()
const grant=ref(null), task=ref(null), busy=ref(false), error=ref('')
let timer, sequence=0, requestId=''
const spec=computed(()=>({mode:'change',service:props.change?.service,change_id:props.change?.id,environment:props.request?.environment,
  preparation_id:props.preparationId||undefined,reason:props.request?.reason||props.change?.title}))
const post=(path,body)=>api('api/service-deployments'+path,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)})
const stages={authorized:'发布已授权',create_preparation:'创建准备单',confirm_scope:'确认本次范围',prepare:'准备合并候选',merge_build:'合并与构建',merging:'合并代码',building:'构建制品',create_delivery:'创建发布执行单',delivery:'发布与结果核对',deliver:'发布与结果核对',waiting_review:'等待指定人员',complete:'发布完成'}
async function preview(){
  if(busy.value)return
  const seq=sequence;busy.value=true;error.value=''
  try{
    const {service,environment,change_id}=spec.value
    const existing=await api('api/service-deployments/by-change?'+new URLSearchParams({service,environment,change_id}))
    if(seq!==sequence)return
    if(existing?.state==='running'){await router.push({path:route.path,query:{...route.query,change_deployment:existing.id}});return}
    const value=await post('/preview',spec.value);if(seq===sequence){grant.value=value;requestId=randomUUID().replaceAll('-','')}}
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
      <header class="flex items-center justify-between gap-3"><h3 class="font-semibold">{{task?.grant?.spec?.service}} 发布进度</h3><Button variant="outline" :disabled="busy" @click="read">刷新状态</Button></header>
      <p v-if="task" role="status">{{task.state==='blocked'?'发布待核对':task.state==='failed'?'发布失败':stages[task.stage]||task.stage}} · {{task.message}}</p>
      <p v-if="task" class="text-sm text-muted-foreground">{{task.grant.spec.environment}} · 变更 #{{task.grant.spec.change_id}} <span v-if="task.preparation_id"> · 准备单 #{{task.preparation_id}}</span><span v-if="task.run_id"> · 构建记录 ID {{task.run_id}}</span><span v-if="task.artifact_id"> · 制品 #{{task.artifact_id}}</span><span v-if="task.release_id"> · 发布记录 ID {{task.release_id}}</span></p>
      <div v-if="task?.preparation_id||task?.delivery_id" class="flex gap-2"><Button v-if="task.preparation_id" variant="outline" @click="router.push({path:route.path,query:{preparation:String(task.preparation_id)}})">查看准备单</Button><Button v-if="task.delivery_id" variant="outline" @click="router.push({path:route.path,query:{run:String(task.delivery_id)}})">查看执行单</Button></div>
      <p v-if="task?.built_commit" class="text-sm break-all">本次制品提交：{{task.built_commit}}</p>
      <p v-if="task?.grant.change" class="text-sm break-all">{{task.grant.change.source_branch}} · {{task.grant.change.source_commit}} → {{task.grant.change.target_branch}}</p>
    </template>
    <template v-else>
      <Button v-if="!grant" :disabled="busy" @click="preview"><Spinner v-if="busy"/>预览整次发布</Button>
      <template v-else>
        <h3 class="font-semibold">将 {{grant.spec.service}} 发布到 {{grant.spec.environment}}</h3>
        <p>#{{grant.spec.change_id}} · {{grant.change.title}}</p>
        <p class="text-sm break-all">{{grant.change.source_branch}} · {{grant.change.source_commit}} → {{grant.change.target_branch}} · {{grant.change.target_commit}}</p>
        <p class="text-sm">发布目标：{{grant.targets.join('、')||grant.target_type}}。仅发布本次构建的制品。</p>
        <ol class="list-decimal pl-6 text-sm"><li v-if="grant.change.mark_ready_draft_digest">校验并将变更 #{{grant.spec.change_id}} 标为就绪</li><li v-for="n in [...grant.change.preparation_steps,...grant.change.release_steps]" :key="n.kind+':'+n.id">{{n.name}}<span v-if="n.approvers?.length"> · {{n.approvers.join('、')}}</span></li></ol>
        <div class="flex gap-2"><Button :disabled="busy" @click="confirm"><Spinner v-if="busy"/>确认并发布</Button><Button variant="ghost" :disabled="busy" @click="grant=null">返回</Button></div>
      </template>
    </template>
    <p v-if="error" role="alert" class="text-sm text-destructive">{{error}}</p>
  </section>
</template>
