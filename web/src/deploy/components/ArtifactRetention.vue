<script setup>

import {computed,onBeforeUnmount,ref,watch} from 'vue'
import {toast} from '../../shared/composables/toast.js'
const props=defineProps({service:{type:String,required:true},canManage:Boolean})
const show=ref(false),loading=ref(false),saving=ref(false),error=ref(''),value=ref(0),policy=ref(null)
const valid=computed(()=>Number.isInteger(value.value)&&value.value>=0&&value.value<=10000)
let sequence=0,controller
async function load(){
 const seq=++sequence;controller?.abort();controller=new AbortController();loading.value=true;error.value=''
 try{const r=await fetch(`api/services/${encodeURIComponent(props.service)}/artifact-retention`,{signal:controller.signal});const data=await r.json();if(!r.ok)throw Error(data.error||'制品保留策略读取失败');if(seq===sequence){policy.value=data;value.value=data.keep_count}}
 catch(e){if(seq===sequence&&e.name!=='AbortError')error.value=e.message}
 finally{if(seq===sequence)loading.value=false}
}
function open(){show.value=true;policy.value=null;load()}
async function save(){
 if(saving.value||loading.value||!policy.value||!valid.value)return
 saving.value=true;error.value='';const name=props.service,count=value.value,seq=sequence
 try{const r=await fetch(`api/services/${encodeURIComponent(name)}/artifact-retention`,{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({keep_count:count})});const data=await r.json();if(!r.ok)throw Error(data.error||'策略保存失败');if(seq===sequence){show.value=false;toast('制品保留策略已保存，将由自动清理任务执行','success')}}
 catch(e){if(seq===sequence)error.value=e.message}
 finally{if(seq===sequence)saving.value=false}
}
watch(()=>props.service,()=>{sequence++;controller?.abort();show.value=false;policy.value=null;loading.value=false;saving.value=false;error.value=''})
onBeforeUnmount(()=>{sequence++;controller?.abort()})



import {Button,NumberField,NumberFieldContent,NumberFieldDecrement,NumberFieldIncrement,NumberFieldInput,Sheet,SheetContent,SheetDescription,SheetFooter,SheetHeader,SheetTitle,Spinner} from '../../shared/shadcn-ui.js'
</script>
<template><div class="retention-entry"><Button @click="open" variant="outline" size="sm" type="button">制品保留策略</Button></div><Sheet :open="show" @update:open="async v=>{if(!v){show=false}}"><SheetContent class="data-[side=right]:w-full gap-0 overflow-hidden p-0 data-[side=right]:sm:max-w-[var(--editor-width)]" :class="false?'inset-0 h-dvh max-h-dvh w-screen max-w-none translate-x-0 translate-y-0 rounded-none sm:max-w-none':''" :style="{'--editor-width':typeof (&quot;min(480px, 100vw)&quot;)==='number'?(&quot;min(480px, 100vw)&quot;)+'px':(&quot;min(480px, 100vw)&quot;)}" :show-close-button="!saving" @interact-outside="e=>{if(!(!saving))e.preventDefault()}" @escape-key-down="e=>{if(!(!saving))e.preventDefault()}"><SheetHeader class="shrink-0 border-b p-5 pr-12"><SheetTitle>制品保留策略</SheetTitle><SheetDescription class="sr-only">详情与操作</SheetDescription></SheetHeader><div class="min-h-0 flex-1 overflow-auto p-5"><div class="retention-form"><p>{{service}}</p><p v-if="loading" role="status">正在读取保留策略…</p><div v-if="error" role="alert" class="retention-error">{{error}} <Button v-if="!policy" @click="load" variant="ghost" size="default" type="button">重试</Button></div><template v-if="policy&&!loading"><label for="artifact-keep-count">保留最近多少份制品</label><NumberField id="artifact-keep-count" v-model="value" :min="0" :max="10000" :disabled="saving||!canManage" aria-label="保留最近多少份制品"><NumberFieldContent><NumberFieldDecrement/><NumberFieldInput/><NumberFieldIncrement/></NumberFieldContent></NumberField><small>0 表示保留全部。按应用保留最新 N 份，不区分构建环境。</small><p>正在使用、发布中和历史发布回滚依赖的制品，以及版本、交付单和代码准备引用的制品，会额外保留，实际数量可能超过设置值。</p><p>保存后每分钟自动检查，仅删除超出保留范围且无引用的本地制品文件。构建、发布和审计记录继续保留；外部镜像仓库和平台目录外的文件不自动删除。</p><div class="retention-summary">当前制品 {{policy.total}} 份 · 引用保护 {{policy.protected}} 份 · 累计清理 {{policy.cleaned}} 份<span v-if="policy.pending"> · 待完成清理 {{policy.pending}} 份</span></div><small v-if="!valid" class="retention-error">请填写 0–10000 的整数。</small><small v-if="!canManage">仅管理员可以修改保留策略。</small></template></div></div><SheetFooter class="shrink-0 flex-wrap border-t p-4 sm:flex-row sm:justify-end"><Button @click="show=false" variant="outline" size="default" type="button" :disabled="(saving)">取消</Button><Button v-if="canManage" @click="save" variant="default" size="default" type="button" :disabled="(loading||!policy||!valid)||(saving)"><Spinner v-if="saving"/>保存策略</Button></SheetFooter></SheetContent></Sheet></template>
<style scoped>.retention-entry{display:flex;justify-content:flex-end;margin-bottom:12px}.retention-form{display:grid;gap:14px;line-height:1.6}.retention-form p{margin:0}.retention-form small,.retention-summary{color:var(--muted-foreground);font-size:.75rem}.retention-error{color:var(--error)}</style>
