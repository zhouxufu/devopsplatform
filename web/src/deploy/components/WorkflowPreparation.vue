<script setup>
import {computed, inject, onBeforeUnmount, ref, watch} from 'vue'
import {randomUUID} from '../../shared/uuid.js'

import {toast} from '../../shared/composables/toast.js'
import CodePreparationPanel from './CodePreparationPanel.vue'
import {preparationIdentity} from '../workflowPreparation.js'

const props=defineProps({request:Object,me:Object,recordOnly:Boolean})
const emit=defineEmits(['checkpoint','ready','state','updated','restart'])
const api=inject('api'),run=ref(null),preview=ref(null),busy=ref(false),comment=ref(''),error=ref('')
let sequence=0,timer,creationKey='',emitted=''
const post=(url,body)=>api(url,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)})
const body=computed(()=>({strategy_kind:'custom',template_id:props.request.template_id,source_type:props.request.source_type,version_id:props.request.version_id||0,work_item_ids:props.request.work_item_ids||[],source_change_ids:props.request.source_change_ids||null,selected_change_ids:props.request.selected_change_ids,environment:props.request.environment,scene:props.request.scene||'',artifact_sources:props.request.artifact_sources||{},reason:props.request.reason||''}))
const origin=computed(()=>preparationIdentity(body.value))
const active=computed(()=>run.value?.steps.find(s=>!['success','skipped'].includes(s.status)))
const node=computed(()=>run.value?.plan.nodes.find(n=>n.id===active.value?.node_id))
const canReview=computed(()=>node.value&&['manual','approval','verify'].includes(node.value.kind)&&node.value.approvers?.includes(props.me.username)&&!active.value.reviews?.some(r=>r.actor===props.me.username&&r.approved)&&(node.value.kind!=='approval'||run.value.owner!==props.me.username))
const canManage=computed(()=>run.value&&(run.value.owner===props.me.username||props.me.role==='admin'))
const canAdvance=computed(()=>run.value&&['approved','running'].includes(run.value.status)&&canManage.value&&!(active.value?.detail&&['code-prepare','code-verify'].includes(active.value.kind)))
const stateNames={waiting_confirmation:'待范围确认',pending_approval:'待审核',approved:'待执行下一节点',running:'准备节点执行中',blocked:'代码准备待处理',success:'准备完成',failed:'准备失败',rejected:'已拒绝',canceled:'已终止'}
const stepNames={pending:'未开始',reviewing:'待确认',executing:'执行中 / 中断待核对',running:'进行中',blocked:'代码准备待处理',success:'已完成',failed:'失败',rejected:'已拒绝',canceled:'已终止'}
async function act(fn){if(busy.value)return;busy.value=true;error.value='';try{await fn()}catch(e){error.value=e.message;toast(e.message,'error')}finally{busy.value=false}}
function requestMatches(value){const stored=value.plan.preparation_request;return !!stored&&preparationIdentity(stored)===origin.value}
async function accept(value){
 const generation=sequence
 if(!props.recordOnly&&!requestMatches(value))throw Error('准备记录与当前范围、环境或制品来源不一致，请重新建立本次准备')
 run.value=value;emit('updated',value)
 emit('checkpoint',{id:value.id,client_id:creationKey})
 emit('state',{active:value.status==='running',pending:!['success','failed','rejected','canceled'].includes(value.status)})
 clearTimeout(timer)
 if(value.status==='success'&&emitted!==value.id){
  emitted=value.id
  try{
  const code=value.steps.find(s=>['code-prepare','code-verify'].includes(s.kind)&&s.detail&&s.detail!=='本次无需构建代码，已跳过')
  const prepared=code?await api('api/delivery/code-preparations/'+code.detail):null
  if(generation!==sequence)return
  if(prepared&&prepared.state!=='ready')throw Error('代码制品尚未准备完成')
  emitted=value.id;emit('ready',{run_id:value.id,id:prepared?.id||'',artifacts:Object.fromEntries((prepared?.builds||[]).map(b=>[b.service,b.artifact_id])),preparation:prepared})
  }catch(e){if(generation===sequence&&emitted===value.id)emitted='';throw e}
 }
 if(['approved','running','blocked','waiting_confirmation','pending_approval'].includes(value.status))timer=setTimeout(poll,5000)
}
let lastCodeState=''
function codeStateChanged(value){emit('state',{...value,pending:value.pending||!!run.value&&!['success','failed','rejected','canceled'].includes(run.value.status)});if(value.codeState&&value.codeState!==lastCodeState){lastCodeState=value.codeState;poll()}}
async function poll(){const id=run.value?.id,generation=sequence;if(!id)return;try{const value=await api('api/delivery/preparations/runs/'+id);if(generation===sequence){error.value='';await accept(value)}}catch(e){if(generation===sequence){error.value=e.message;timer=setTimeout(poll,10000)}}}
function restart(){creationKey='';preview.value=null;error.value='';emit('checkpoint',{id:0,client_id:''})}
async function inspect(){await act(async()=>{const generation=sequence;const value=await post('api/delivery/preparations/preview',body.value);if(generation===sequence)preview.value=value})}
async function create(){await act(async()=>{if(!preview.value)return;creationKey=creationKey||props.request.preparation_client_id||randomUUID().replaceAll('-','');emit('checkpoint',{id:0,client_id:creationKey});const generation=sequence,value=await post('api/delivery/preparations/runs',{...body.value,client_request_id:creationKey,expected_digest:preview.value.digest});if(generation===sequence){await accept(value);toast('已建立本次准备任务，尚未合并、构建或部署','success')}})}
async function review(approve){await act(async()=>{const generation=sequence,value=await post('api/research/tasks/'+run.value.id+'/'+node.value.id,{digest:run.value.digest,comment:comment.value,approve,work_item_ids:node.value.assigned_work_items?.[props.me.username]});if(generation===sequence){await accept(value);comment.value=''}})}
async function advance(){await act(async()=>{const generation=sequence,value=await post('api/delivery/preparations/runs/'+run.value.id+'/advance',{confirm:true,digest:run.value.digest});if(generation===sequence){error.value='';await accept(value)}})}
watch(()=>props.request.preparation_run_id,id=>{if(!id&&run.value){++sequence;clearTimeout(timer);run.value=null;preview.value=null;emitted='';emit('state',{active:false,pending:false})}})
watch(origin,async()=>{const generation=++sequence;clearTimeout(timer);run.value=null;preview.value=null;error.value='';emitted='';creationKey=props.request.preparation_client_id||'';emit('state',{active:false,pending:false});const id=props.request.preparation_run_id;if(id){try{const value=await api('api/delivery/preparations/runs/'+id);if(generation===sequence){if(!props.recordOnly&&!requestMatches(value)){creationKey='';emit('checkpoint',{id:0,client_id:''});error.value='范围或制品来源已调整，请重新预览；原准备记录保留，不会继续执行';return}await accept(value)}}catch(e){if(generation===sequence){error.value=e.message;toast(e.message,'error')}}}},{immediate:true})
onBeforeUnmount(()=>{++sequence;clearTimeout(timer);emit('state',{active:false,pending:false})})



import {Button,Input,Spinner} from '../../shared/shadcn-ui.js'
</script>
<template><section aria-label="本次交付准备" class="workflow-preparation"><header><h3>范围确认与代码准备</h3><span>{{run?stateNames[run.status]||run.status:'按环境绑定的流程执行'}}</span><Button v-if="run" @click="act(poll)" variant="outline" size="default" type="button" :disabled="(busy)"><Spinner v-if="busy"/>核对状态</Button><Button v-else-if="!recordOnly" @click="inspect" variant="outline" size="default" type="button" :disabled="(busy)"><Spinner v-if="busy"/>预览范围与确认人</Button></header><p v-if="error" role="alert" class="error">{{error}}</p><template v-if="!run&&preview"><ol><li v-for="n in preview.plan.nodes" :key="n.id"><b>{{n.name}}</b><small v-if="n.approvers?.length">{{n.approvers.join('、')}} · {{n.approval_mode==='all'?'全部确认':'任一通过'}}</small><small v-if="['code-prepare','code-verify'].includes(n.kind)&&!Object.keys(n.policies||{}).length">本次无需构建，自动跳过</small><small v-for="(policy,app) in n.policies||{}" :key="app">{{app}} → {{policy.target_branch}}{{policy.mode==='isolated'?' · 基线 '+policy.base_branch:''}}</small></li></ol><p class="hint">此处只建立本次任务；代码节点仍需负责人明确执行。保存流程和建立任务均不自动部署。</p><Button @click="create" variant="default" size="default" type="button" :disabled="(busy)"><Spinner v-if="busy"/>确认建立本次准备</Button></template><template v-if="run"><ol><li v-for="step in run.steps" :key="step.node_id"><b>{{step.name}}</b> · {{stepNames[step.status]||step.status}}<small v-if="step.evidence">{{step.evidence.kind==='manual-verification'?'人工验证（非机器证明）':'HTTP 验证'}} · {{Object.entries(step.evidence.artifacts||{}).map(([app,digest])=>app+' / '+digest).join('、')}}</small><small v-if="step.detail==='本次无需构建代码，已跳过'">{{step.detail}}</small><small v-for="r in step.reviews||[]" :key="r.actor">{{r.actor}} · {{r.approved?'同意':'拒绝'}} · {{r.comment}}</small></li></ol><p v-if="node?.approvers?.length" class="hint">当前处理人：{{node.approvers.join('、')}}</p><div v-if="canReview" class="review"><Input v-model="comment" placeholder="确认 / 审核意见" :disabled="busy"/><Button @click="review(false)" variant="outline" size="default" type="button" :disabled="(busy)"><Spinner v-if="busy"/>拒绝</Button><Button @click="review(true)" variant="default" size="default" type="button" :disabled="(busy)"><Spinner v-if="busy"/>{{node.kind==='approval'?'同意审核':node.kind==='verify'?'记录验证通过':'确认本次范围'}}</Button></div><CodePreparationPanel v-if="active?.detail&&['code-prepare','code-verify'].includes(active.kind)" :key="active.detail" :initial-id="active.detail" managed in-place @restart="emit('restart')" :can-manage="!!canManage" :request="run.plan.preparation_request" :apps="Object.keys(node?.policies||{})" :protected="run.plan.environment.protected||run.plan.environment.purpose==='production'" @ready="poll" @state="codeStateChanged"/><Button v-if="canAdvance" @click="advance" variant="default" size="default" type="button" :disabled="(busy)"><Spinner v-if="busy"/>{{node?.kind==='verify'?'执行 HTTP 验证':active?.detail?'核对代码结果并继续':'冻结并检查代码来源'}}</Button><Button v-if="!recordOnly&&['failed','rejected','canceled'].includes(run.status)" @click="restart" variant="outline" size="default" type="button" :disabled="(busy)">重新建立准备（保留原记录）</Button><p v-if="run.status==='success'" role="status">代码与制品准备已完成；在本页继续发布预检与审批，尚未发布。</p></template></section></template>
<style scoped>.workflow-preparation{border:1px solid var(--border);border-radius:var(--radius);padding:14px;margin:12px 0;min-width:0}header,.review{display:flex;gap:10px;align-items:center;flex-wrap:wrap}header h3{margin:0}header span{flex:1;color:var(--muted-foreground);font-size:.75rem}ol{padding-left:22px}li{padding:7px 0}small{display:block;color:var(--muted-foreground);overflow-wrap:anywhere}.hint{color:var(--muted-foreground);font-size:.75rem}.error{color:var(--error)}</style>
