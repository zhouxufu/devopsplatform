<script setup>
import {computed,onBeforeUnmount,ref,watch} from 'vue'
import {agentTransport,changeAgentTransport,eligibleDeploymentAgents} from '../deploymentAgent.js'
import OptionPicker from '../../shared/components/OptionPicker.vue'
import HelpTip from '../../shared/components/HelpTip.vue'
import {Button,Field,FieldError,FieldLabel} from '../../shared/shadcn-ui.js'
const props=defineProps({service:String,environment:String,hosts:{type:Array,default:()=>[]},modelValue:{type:Object,default:()=>({})}})
const emit=defineEmits(['update:modelValue'])
const agents=ref([]),loading=ref(false),error=ref('')
const active=computed(()=>agentTransport(props.modelValue)),eligible=computed(()=>eligibleDeploymentAgents(agents.value,props.hosts))
let sequence=0
async function load(){
 const seq=++sequence;agents.value=[];error.value=''
 if(!props.service||!props.environment)return
 loading.value=true
 try{const r=await fetch('api/deployment-agents?'+new URLSearchParams({service:props.service,environment:props.environment}));const body=await r.json();if(!r.ok||!Array.isArray(body))throw Error(body.error||'部署 Agent 授权读取失败');if(seq===sequence)agents.value=body}catch(e){if(seq===sequence)error.value=e.message}finally{if(seq===sequence)loading.value=false}
}
watch(()=>[props.service,props.environment],load,{immediate:true})
onBeforeUnmount(()=>sequence++)
function select(value){emit('update:modelValue',changeAgentTransport(props.modelValue,value))}
</script>
<template>
 <Field v-if="active||eligible" class="fld">
  <FieldLabel>部署执行通道<HelpTip text="Agent 按已登记主机接收持久任务，支持整包、分批及明确启动失败后的回滚。启动慢或断连仅待核对；保存配置不会执行发布。"/></FieldLabel>
  <OptionPicker :model-value="active?'agent':'ssh'" :disabled="loading" :options="[{value:'ssh',label:'SSH（现有方式）'},{value:'agent',label:'Agent（按目标主机匹配）',disabled:!eligible}]" aria-label="部署执行通道" @update:model-value="select"/>
  <FieldError v-if="active&&!loading&&(error||!eligible)">{{error||'所选主机需要各自唯一、已授权的部署 Agent'}}<Button v-if="error" variant="ghost" size="sm" type="button" @click="load">重试</Button></FieldError>
 </Field>
</template>
