<script setup>



import {computed, inject, onBeforeUnmount, ref, watch} from 'vue'


import HelpTip from '../../shared/components/HelpTip.vue'
import StatePanel from '../../shared/components/StatePanel.vue'
import {toast} from '../../shared/composables/toast.js'
import {browserView} from '../../shared/viewMemory.js'
const props=defineProps({versionId:{type:Number,required:true},revision:[String,Number],actor:String,environments:{type:Array,default:()=>[]}})
const emit=defineEmits(['resolve'])
const memory=browserView('readiness:'+props.versionId,props.actor),defaults={environment:''}
const remembered=memory.read(defaults)
const api=inject('api'),environment=ref(props.environments.some(e=>e.name===remembered.environment)?remembered.environment:''),report=ref(null),loading=ref(false),failed=ref(false),showPassed=ref(false)
let sequence=0,controller
const statusColors={pass:'green',blocked:'red',pending:'gray',manual:'orange'}
const labels={pass:'已核对',blocked:'待补齐',pending:'未验证',manual:'需人工复核'}
const actions={scope:'核对范围',change:'查看变更',artifact:'核对制品',environment:'配置目标',preparation:'查看准备流程',preflight:'进入交付'}
const counts=computed(()=>report.value?.counts||{})
const checks=computed(()=>(report.value?.checks||[]).filter(c=>showPassed.value||c.status!=='pass'))
async function load(){
 const request=++sequence;controller?.abort();controller=new AbortController();loading.value=true;failed.value=false;report.value=null
 try{
  const data=await api(`api/release-versions/${props.versionId}/readiness?${new URLSearchParams({environment:environment.value})}`,{signal:controller.signal})
  if(request!==sequence)return
  if(!data?.advisory||!Array.isArray(data.checks)||!Array.isArray(data.code)||!data.counts)throw Error('就绪检查响应无效，请重试')
  report.value=data
 }catch(e){if(request===sequence&&e.name!=='AbortError'){failed.value=true;toast(e.message||'就绪检查读取失败','error')}}
 finally{if(request===sequence)loading.value=false}
}
watch(()=>[props.versionId,props.revision,environment.value],load,{immediate:true})
watch(environment,()=>memory.write({environment:environment.value},defaults))
onBeforeUnmount(()=>{sequence++;controller?.abort()})

import OptionPicker from '../../shared/components/OptionPicker.vue'

import {ref as _viewRef} from 'vue'
const _view1=_viewRef([])


import {Accordion,AccordionContent,AccordionItem,AccordionTrigger,Badge,Button,Card,CardAction,CardContent,CardFooter,CardHeader,CardTitle,Checkbox,Empty,EmptyContent,EmptyDescription,EmptyHeader,FieldLabel,Spinner,Table,TableBody,TableCell,TableHead,TableHeader,TableRow} from '../../shared/shadcn-ui.js'
</script>
<template><Card aria-label="发布就绪检查" :aria-busy="loading" class="readiness"><CardHeader><CardTitle><div class="flex flex-wrap items-center gap-2">发布就绪<HelpTip text="只读核对当前平台记录，不连接远端、不构建、不发布，也不替代正式预检与审批。代码证明核对包含链路，不证明没有夹带其他需求。"/></div></CardTitle><CardAction><div class="readiness-tools"><span>核对环境</span><OptionPicker v-model="environment" :options="environments.map(e=>({value:e.name,label:e.display_name||e.name}))" all-label="暂不指定" :style="{width:'150px',maxWidth:'100%'}" aria-label="就绪检查目标环境"/><Button @click="load" variant="outline" size="default" type="button" :disabled="(loading)"><Spinner v-if="loading"/>重新检查</Button></div></CardAction></CardHeader><CardContent class="min-w-0"><StatePanel v-if="loading" title="正在核对发布记录…" loading/><StatePanel v-else-if="failed" title="暂未读取到就绪结果" message="没有沿用旧结果。可重试，不影响已保存的版本内容。"><Button @click="load" variant="outline" size="default" type="button">重试</Button></StatePanel><template v-else-if="report"><div role="status" class="readiness-summary"><div :size="8" class="flex flex-wrap items-center gap-2"><Badge v-for="(label,key) in labels" :key="key" :variant="(statusColors[key])==='red'?'destructive':'secondary'">{{label}} {{counts[key]||0}}</Badge></div><span class="readiness-time">仅供核对 · {{new Date(report.checked_at*1000).toLocaleTimeString('zh-CN')}}</span><FieldLabel class="flex items-center gap-2 font-normal"><Checkbox v-model="showPassed"/>显示已核对</FieldLabel></div><ul aria-label="就绪问题列表，可滚动" tabindex="0" class="readiness-checks divide-y"><template v-for="{item:c} in (checks||[]).map(item=>({item}))"><li class="py-3"><div class="ml-auto flex gap-2"><Button v-if="actions[c.action]" @click="emit('resolve',c)" variant="ghost" size="sm" type="button">{{actions[c.action]}}</Button></div><div class="readiness-check"><Badge :variant="(statusColors[c.status]||'gray')==='red'?'destructive':'secondary'">{{labels[c.status]||'未验证'}}</Badge><div><strong>{{c.title}}</strong><p>{{c.detail}}</p></div></div></li></template><li v-if="!(checks||[]).length"><Empty><EmptyHeader><EmptyDescription>{{"当前筛选下没有检查项"}}</EmptyDescription></EmptyHeader></Empty></li></ul><Accordion v-if="report.code.length" class="proof platform-collapse" type="multiple" :model-value="(_view1||[]).map(String)" @update:model-value="_view1=$event"><AccordionItem :value="String(&quot;panel&quot;)"><AccordionTrigger>代码覆盖与制品证据 · {{report.code.length}} 应用</AccordionTrigger><AccordionContent force-mount v-show="!(!(_view1||[]).map(String).includes(String(&quot;panel&quot;)))"><div class="evidence-scroll"><div class="console-table min-w-0 max-w-full"><div class="max-w-full overflow-auto rounded-lg border" :style="{maxHeight:({x:780}).y?({x:780}).y+'px':undefined}"><Table class="table-fixed min-w-[var(--table-min-width)]" :style="{'--table-min-width':(({x:780}).x||({x:780}).minWidth||780)+'px'}"><TableHeader><TableRow><TableHead :style="{width:(240)+'px'}">应用 / 变更</TableHead><TableHead :style="{width:(180)+'px'}">代码证明</TableHead><TableHead :style="{width:(180)+'px'}">构建 / 制品</TableHead><TableHead :style="{width:(180)+'px'}">Commit / 摘要</TableHead></TableRow></TableHeader><TableBody><template v-for="(record,rowIndex) in (((report.code)||[]).map((e,rowIndex)=>({...e,__rowKey:e.service}))||[])" :key="typeof (&quot;__rowKey&quot;)==='function'?(&quot;__rowKey&quot;)(record):record[&quot;__rowKey&quot;]??rowIndex"><TableRow><TableCell class="align-top"><div class="min-w-0 break-words" :class="{truncate:!!(true)}"><template v-for="{record:e,rowIndex:rowIndex} in [{record,rowIndex}]"><div><b>{{e.service}}</b><small>{{e.change_ids.map(id=>'#'+id).join('、')}}</small></div></template></div></TableCell><TableCell class="align-top"><div class="min-w-0 break-words" :class="{truncate:!!(true)}"><template v-for="{record:e,rowIndex:rowIndex} in [{record,rowIndex}]"><div><Badge :variant="(statusColors[e.status]||'gray')==='red'?'destructive':'secondary'">{{labels[e.status]||'未验证'}}</Badge><p>{{e.detail}}</p><small v-for="(source,i) in e.sources" :key="i">{{source.branch}} → {{source.commit}} · {{source.change_ids.map(id=>'#'+id).join('、')}}</small><small v-if="e.preparation_id">准备任务 {{e.preparation_id}}</small></div></template></div></TableCell><TableCell class="align-top"><div class="min-w-0 break-words" :class="{truncate:!!(true)}"><template v-for="{record:e,rowIndex:rowIndex} in [{record,rowIndex}]"><div>{{e.build_id?'构建 #'+e.build_id:'无来源构建'}}<small>{{e.artifact_id?'制品 #'+e.artifact_id:'未固定制品'}}</small><small v-if="e.branch">{{e.branch}}</small></div></template></div></TableCell><TableCell class="align-top"><div class="min-w-0 break-words" :class="{truncate:!!(true)}"><template v-for="{record:e,rowIndex:rowIndex} in [{record,rowIndex}]"><div><code>{{e.commit||'完整 Commit 未知'}}</code><small><code>{{e.artifact_digest||'摘要未知'}}</code></small></div></template></div></TableCell></TableRow></template><TableRow v-if="!(((report.code)||[]).map((e,rowIndex)=>({...e,__rowKey:e.service}))||[]).length"><TableCell :colspan="4"><Empty><EmptyHeader><EmptyDescription>暂无数据</EmptyDescription></EmptyHeader></Empty></TableCell></TableRow></TableBody></Table></div></div></div><p class="muted">仅展示可追溯记录，不以分支名、人工勾选或构建成功推断测试通过。发布前仍须复核实际代码差异。</p></AccordionContent></AccordionItem></Accordion></template></CardContent></Card></template>
<style scoped>.readiness{min-width:0;margin-bottom:20px}
.readiness-tools{display:flex;align-items:center;gap:8px;flex-wrap:wrap;color:var(--muted-foreground);font-size:.875rem}
.readiness-summary{display:flex;align-items:center;gap:12px;flex-wrap:wrap;margin-bottom:16px}
.readiness-time{color:var(--muted-foreground);font-size:.75rem;margin-right:auto}
.readiness-checks{max-height:360px;overflow:auto;overscroll-behavior:contain;scrollbar-gutter:stable}
.readiness-check{display:flex;align-items:flex-start;gap:12px;min-width:0}
.readiness-check>div{min-width:0}.readiness-check strong{font-size:1rem;font-weight:500}
.readiness-check p{margin:4px 0 0;font-size:.875rem;line-height:22px;color:var(--muted-foreground);overflow-wrap:anywhere}
.proof{margin-top:16px}.evidence-scroll{overflow:auto}.proof p{line-height:22px;margin:8px 0;color:var(--muted-foreground);font-size:.875rem}.proof small{display:block;margin-top:4px;color:var(--muted-foreground)}code{font:.75rem/1.6 var(--mono)}
@media(max-width:900px){}</style>
