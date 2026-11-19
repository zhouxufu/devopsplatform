<script setup>

import {computed, onBeforeUnmount, ref, watch} from 'vue'


import {toast} from '../../shared/composables/toast.js'
import GitBranchSelect from './GitBranchSelect.vue'
import {normalizeBuildVariant} from '../buildVariant.js'

const props=defineProps({service:{type:String,required:true},environment:{type:String,required:true},disabled:Boolean})
const value=ref(null),revision=ref(0),inheritedSource=ref(''),loading=ref(false),loaded=ref(false),baseline=ref('null')
const fields=ref(null)
const buildTool=ref('')
let invalidField=''
let sequence=0,controller
const normalized=()=>value.value?{artifact_source:value.value.artifact_source||'',build_variant:normalizeBuildVariant(value.value.build_variant),code_policy:{mode:value.value.code_policy?.mode||'',base_branch:value.value.code_policy?.mode==='isolated'?value.value.code_policy.base_branch||'':'',target_branch:value.value.code_policy?.mode?value.value.code_policy.target_branch||'':''}}:null
const dirty=computed(()=>JSON.stringify(normalized())!==baseline.value)
async function load(){
 const generation=++sequence;controller?.abort();controller=new AbortController()
 value.value=null;baseline.value='null';loaded.value=false;loading.value=true
 try{
  const r=await fetch(`api/services/${encodeURIComponent(props.service)}/environments/${encodeURIComponent(props.environment)}/delivery-default`,{signal:controller.signal})
  const data=await r.json();if(!r.ok)throw Error(data.error||'读取应用交付策略失败')
  if(generation!==sequence)return
  buildTool.value=data.build_tool||''
  value.value=data.value?{artifact_source:data.value.artifact_source||'',build_variant:{...data.value.build_variant},code_policy:{mode:'',base_branch:'',target_branch:'',...data.value.code_policy}}:null
  revision.value=data.revision;inheritedSource.value=data.artifact_source||'existing';baseline.value=JSON.stringify(normalized());loaded.value=true
 }catch(e){if(generation===sequence&&e.name!=='AbortError')toast(e.message,'error')}
 finally{if(generation===sequence)loading.value=false}
}
function configure(){value.value={artifact_source:'',build_variant:{},code_policy:{mode:'',base_branch:'',target_branch:''}}}
async function reload(){if(dirty.value&&!await confirmAction('重新载入会丢弃当前代码与制品策略的修改，部署目标等其他草稿不变。继续？'))return;load()}
function patch(){
 invalidField=''
 if(!loaded.value||loading.value)throw Error('请先成功载入代码与制品策略，再保存环境配置')
 if(!dirty.value)return undefined
 const current=normalized()
 if(current&&Object.keys(current.build_variant).length&&!/^[a-z][a-z0-9_-]{0,31}$/.test(current.build_variant.name)){invalidField='variant';throw Error('请填写有效构建方案标识，如 dev、test、prod 或 shared')}
 const missing=current?.code_policy.mode==='isolated'&&!current.code_policy.base_branch.trim()?'base':current?.code_policy.mode&&!current.code_policy.target_branch.trim()?'target':''
 if(missing){invalidField=missing;throw Error(missing==='base'?'独立发布分支需要填写基线分支':'请填写目标分支')}
 return {revision:revision.value,value:current}
}
watch(()=>[props.service,props.environment],load,{immediate:true})
onBeforeUnmount(()=>{sequence++;controller?.abort()})
function focusInvalid(){if(!invalidField)return;const input=fields.value?.querySelector(`[data-policy-field="${invalidField}"] input`);input?.scrollIntoView({block:'nearest'});focusControl(input);invalidField=''}
defineExpose({patch,dirty,load,focusInvalid})

import OptionPicker from '../../shared/components/OptionPicker.vue'

import {focusControl} from '../../shared/focusControl.js'


import {Button,Input,Textarea} from '../../shared/shadcn-ui.js'

import {confirmAction} from "../../shared/shadcnFeedback.js"
</script>

<template><section ref="fields" aria-label="代码与制品策略" :aria-busy="loading" class="application-delivery-defaults"><div v-if="loading" role="status">正在读取此应用在当前环境的策略…</div><div v-else-if="!loaded" class="strategy-empty"><span>策略尚未载入，当前草稿未保存。</span><Button @click="load" variant="outline" size="default" type="button" :disabled="(disabled)">重新载入策略</Button></div><template v-else><div class="strategy-heading"><p>{{service}} 在此环境的交付策略与构建约束；保存不会合并、构建或发布。</p><div class="strategy-tools"><Button @click="reload" variant="ghost" size="default" type="button" :disabled="(disabled)">重新载入策略</Button><Button v-if="value" @click="value=null" variant="ghost" size="default" type="button" :disabled="(disabled)">恢复继承</Button></div></div><div v-if="!value" class="strategy-empty"><span>制品来源：{{inheritedSource==='build'?'按本次范围合并并构建':'使用已有制品'}}；分支沿用编排通用策略。</span><Button @click="configure" variant="outline" size="default" type="button" :disabled="(disabled)">配置应用策略</Button></div><template v-else><div class="fld"><label>默认制品来源</label><OptionPicker v-model="value.artifact_source" :disabled="disabled" :options="[{value:'',label:'沿用环境默认（'+(inheritedSource==='build'?'合并并构建':'已有制品')+'）'},{value:'build',label:'按本次范围合并并构建'},{value:'existing',label:'使用已有制品'}]" :style="{width:'100%',maxWidth:'100%'}"/></div><fieldset v-if="['maven','gradle'].includes(buildTool)" :disabled="disabled" class="variant-fields"><legend>环境构建方案</legend><div data-policy-field="variant" class="fld"><label>方案标识</label><Input v-model="value.build_variant.name" :disabled="disabled" placeholder="如 dev、test、prod；跨环境通用可填写 shared"/></div><template v-if="buildTool==='maven'"><div class="fld"><label>Maven Profiles（完整列表，逗号分隔）</label><Input v-model="value.build_variant.maven_profiles" :disabled="disabled" placeholder="如 dev；不填写表示不激活显式 Profile"/></div><div class="fld"><label>覆盖公共 -D 属性（每行 key=value）</label><Textarea v-model="value.build_variant.maven_properties" :disabled="disabled" :rows="({minRows:(2),maxRows:Math.max((2),16)}).minRows||4"/></div></template><template v-else><div class="fld"><label>Gradle Tasks（空则沿用公共任务）</label><Input v-model="value.build_variant.gradle_tasks" :disabled="disabled" placeholder="如 clean bootJar"/></div><div class="fld"><label>覆盖公共 -P 项目属性</label><Textarea v-model="value.build_variant.gradle_project_properties" :disabled="disabled" placeholder="每行 key=value，如 env=dev；具体名称以项目为准" :rows="({minRows:(2),maxRows:Math.max((2),16)}).minRows||4"/></div><div class="fld"><label>覆盖公共 -D 系统属性</label><Textarea v-model="value.build_variant.gradle_system_properties" :disabled="disabled" :rows="({minRows:(2),maxRows:Math.max((2),16)}).minRows||4"/></div></template><small>保存不触发构建。普通发布、Bug、版本与 Hotfix 均校验原始构建方案及配置；不同方案不能混发。同名方案也必须参数一致。密钥仅使用变量引用。</small><Button @click="value.build_variant={}" variant="ghost" size="default" type="button" :disabled="(disabled)">清空环境方案</Button></fieldset><div class="fld"><label>分支方式</label><OptionPicker v-model="value.code_policy.mode" :disabled="disabled" :options="[{value:'',label:'沿用编排通用策略'},{value:'integration',label:'常驻分支'},{value:'isolated',label:'独立发布分支'}]" :style="{width:'100%',maxWidth:'100%'}"/></div><div v-if="value.code_policy.mode==='isolated'" data-policy-field="base" class="fld"><label>基线分支</label><GitBranchSelect v-model="value.code_policy.base_branch" :disabled="disabled" :repositories="[{service}]" placeholder="选择或输入生产基线，如 main"/></div><div v-if="value.code_policy.mode" data-policy-field="target" class="fld"><label>{{value.code_policy.mode==='isolated'?'目标分支 / 命名规则':'目标分支'}}</label><GitBranchSelect v-if="value.code_policy.mode==='integration'" v-model="value.code_policy.target_branch" :disabled="disabled" :repositories="[{service}]" placeholder="选择或输入公共分支，如 test"/><Input v-else v-model="value.code_policy.target_branch" :disabled="disabled" placeholder="如 release/{version} 或 hotfix/{work_item}"/><small v-if="value.code_policy.mode==='isolated'">发起交付时展开命名规则并核对新分支；不会覆盖已有分支。</small></div><small>交付时仍需核对范围和制品覆盖证明；审批与流程绑定沿用公共环境设置。</small></template></template></section></template>

<style scoped>.application-delivery-defaults{max-width:760px;min-width:0}.strategy-heading{display:flex;align-items:center;flex-wrap:wrap;gap:12px;margin-bottom:14px}.strategy-heading p{margin:0;flex:1;min-width:180px;color:var(--muted-foreground);font-size:.75rem}.strategy-tools{display:flex;flex-wrap:wrap;gap:6px}.strategy-empty{display:flex;align-items:center;flex-wrap:wrap;gap:12px;color:var(--muted-foreground);font-size:.875rem}.application-delivery-defaults small{color:var(--muted-foreground);font-size:.75rem}.fld{margin-bottom:16px}.fld>label{display:block;margin-bottom:6px;font-size:.875rem}</style>
<style scoped>.variant-fields{border:1px solid var(--border);border-radius:var(--radius);padding:14px;margin:0 0 16px;min-width:0}.variant-fields legend{font-size:.875rem;padding:0 6px}.variant-fields small{display:block}</style>
