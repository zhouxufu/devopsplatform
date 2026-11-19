<script setup>
import {PlusIcon} from '@lucide/vue'
import DataTable from "../../shared/components/DataTable.vue"

import {useTableLayout} from '../../shared/composables/tableLayout.js'
const {pinTableActions}=useTableLayout()



import PageHeader from "../../shared/components/PageHeader.vue"
import {ref, inject, watch, computed} from 'vue'


import StatePanel from '../../shared/components/StatePanel.vue'
import {useAsyncData,requestJSON,requestList} from '../../shared/composables/asyncData.js'

import {toast} from '../../shared/composables/toast.js'

const curEnvironment=inject('curEnvironment',ref('')), environments=inject('environments',ref([]))
const show=ref(false),editing=ref(null),form=ref({}),formRef=ref(null),saving=ref(false),deleting=ref('')
const {data,loading,error,load:read}=useAsyncData()
const rules=computed(()=>data.value?.rules||[]),services=computed(()=>data.value?.services||[])
const environmentOptions=computed(()=>[...new Set([curEnvironment.value,...environments.value].filter(e=>e&&e!=='*'))])
function load(){const environment=curEnvironment.value||'';return read(async signal=>{
 const [rules,a,b]=await Promise.all([requestList('api/avail-rules?'+new URLSearchParams({environment}),{signal}),requestList('api/services',{signal}),requestList('api/otlp/services',{signal})])
 return {rules,services:[...new Set([...a,...b])]}
})}
watch(curEnvironment,load,{immediate:true})
function open(r){editing.value=r||null;form.value=r?{...r}:{service:'',environment:curEnvironment.value||environments.value[0]||'*',name:'',ok_codes:[],ignore_codes:[]};show.value=true}
async function save(){
 if(saving.value)return
 if(await validateFields(formRef.value,form.value,{},_validationErrors)||saving.value)return
 saving.value=true
 try{await requestJSON('api/avail-rule',{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify(form.value)});show.value=false;toast('已保存','success');await load()}
 catch(e){toast(e.message,'error')}finally{saving.value=false}
}
async function del(r){
 if(deleting.value)return
 deleting.value=r.service+':'+r.environment+':'+r.name
 try{
  await confirmDialog.confirm(`删除规则 ${r.service}[${r.environment==='*'?'全部环境':r.environment}]${r.name?' · '+r.name:''}？`,'删除规则',{confirmButtonText:'删除'})
 }catch{deleting.value='';return}
 try{await requestJSON('api/avail-rule?'+new URLSearchParams({service:r.service,environment:r.environment,name:r.name}),{method:'DELETE'});toast('已删除','success');await load()}
 catch(e){toast(e.message,'error')}finally{deleting.value=''}
}

import OptionPicker from '../../shared/components/OptionPicker.vue'

import {confirmDialog} from '../../shared/shadcnFeedback.js'

import {reactive as _validationReactive} from 'vue'
import {validateFields,setFieldErrors,scrollToValidationField} from '../../shared/formValidation.js'
const _validationErrors=_validationReactive({})


import {Button,Card,CardAction,CardContent,CardFooter,CardHeader,CardTitle,Dialog,DialogContent,DialogDescription,DialogFooter,DialogHeader,DialogTitle,Empty,EmptyDescription,EmptyHeader,Field,FieldDescription,FieldError,FieldLabel,Input,Spinner,Table,TableBody,TableCell,TableHead,TableHeader,TableRow} from '../../shared/shadcn-ui.js'
</script>
<template><section class="console-content list-page-surface "><PageHeader title="可用率规则" help="规则默认按当前环境隔离；只有显式选择“全部环境”才作为跨环境兜底。优先级：环境指标 → 环境默认 → 全环境指标 → 全环境默认 → 内置。"><template #actions><Button @click="open()" variant="default" size="sm" type="button"><PlusIcon class="size-4"/>新建规则</Button></template></PageHeader><div class="table-scroll"><div v-if="!error" class="console-table min-w-0 max-w-full"><div v-if="loading" class="flex items-center gap-2 py-2" role="status"><Spinner/>正在读取</div><div class="min-w-0 max-w-full" :style="{maxHeight:({x:1136}).y?({x:1136}).y+'px':undefined}"><DataTable class="[&_[data-slot=table-container]]:rounded-lg [&_[data-slot=table-container]]:border" :rows="(((rules)||[]).map((r,rowIndex)=>({...r,__rowKey:r.service + ':' + r.environment + ':' + r.name}))||[])" :columns="[{id:'c0',label:&quot;服务&quot;,size:240,minSize:64,required:true,defaultHidden:false,flex:true,selection:false,pin:'left'},{id:'c1',label:&quot;环境&quot;,size:180,minSize:64,required:false,defaultHidden:false,flex:false,selection:false,pin:false},{id:'c2',label:&quot;指标&quot;,size:180,minSize:64,required:false,defaultHidden:false,flex:false,selection:false,pin:false},{id:'c3',label:&quot;成功码&quot;,size:180,minSize:64,required:false,defaultHidden:false,flex:false,selection:false,pin:false},{id:'c4',label:&quot;忽略码&quot;,size:180,minSize:64,required:false,defaultHidden:false,flex:false,selection:false,pin:false},{id:'c5',label:&quot;操作&quot;,size:128,minSize:64,required:true,defaultHidden:false,flex:false,selection:false,pin:'right'}]" table-key="monitor-pages-RulesView-0" :row-key="(record,rowIndex)=>typeof (&quot;__rowKey&quot;)==='function'?(&quot;__rowKey&quot;)(record):record[&quot;__rowKey&quot;]??rowIndex"><template #header-c0>服务</template><template #cell-c0="{record,rowIndex}"><div class="min-w-0 break-words" :class="{truncate:!!(true)}"><template v-for="{record:r,rowIndex:rowIndex} in [{record,rowIndex}]"><div>{{ r.service }}</div></template></div></template><template #header-c1>环境</template><template #cell-c1="{record,rowIndex}"><div class="min-w-0 break-words" :class="{truncate:!!(true)}"><template v-for="{record:r,rowIndex:rowIndex} in [{record,rowIndex}]"><div><code style="color:var(--brand)">{{ r.environment === '*' ? '全部环境' : r.environment }}</code></div></template></div></template><template #header-c2>指标</template><template #cell-c2="{record,rowIndex}"><div class="min-w-0 break-words" :class="{truncate:!!(true)}"><template v-for="{record:r,rowIndex:rowIndex} in [{record,rowIndex}]"><div :style="{ color: r.name ? 'var(--text)' : 'var(--dim)' }">{{ r.name || '（服务默认）' }}</div></template></div></template><template #header-c3>成功码</template><template #cell-c3="{record,rowIndex}"><div class="min-w-0 break-words" :class="{truncate:!!(true)}"><template v-for="{record:r,rowIndex:rowIndex} in [{record,rowIndex}]"><div style="color:var(--ok)">{{ (r.ok_codes || []).join(', ') || '内置' }}</div></template></div></template><template #header-c4>忽略码</template><template #cell-c4="{record,rowIndex}"><div class="min-w-0 break-words" :class="{truncate:!!(true)}"><template v-for="{record:r,rowIndex:rowIndex} in [{record,rowIndex}]"><div style="color:var(--warn)">{{ (r.ignore_codes || []).join(', ') || '—' }}</div></template></div></template><template #header-c5>操作</template><template #cell-c5="{record,rowIndex}"><div class="min-w-0 break-words" :class="{truncate:!!(false)}"><template v-for="{record:r,rowIndex:rowIndex} in [{record,rowIndex}]"><div><Button @click="open(r)" variant="ghost" size="sm" type="button" :disabled="(!!deleting||saving)">编辑</Button><Button @click="del(r)" class="text-destructive hover:text-destructive" variant="ghost" size="sm" type="button" :disabled="(!!deleting||saving)">删除</Button></div></template></div></template><template #empty><Empty><EmptyHeader><EmptyDescription>暂无数据</EmptyDescription></EmptyHeader></Empty></template></DataTable></div></div></div><StatePanel v-if="error" error title="规则读取失败" :message="error"><Button @click="load" variant="outline" size="default" type="button">重试</Button></StatePanel><Dialog :open="show" @update:open="async v=>{if(!v){!saving&amp;&amp;(show=false)}}"><DialogContent class="flex max-h-[92dvh] flex-col gap-0 overflow-hidden p-0 sm:max-w-[var(--editor-width)]" :class="false?'inset-0 h-dvh max-h-dvh w-screen max-w-none translate-x-0 translate-y-0 rounded-none sm:max-w-none':''" :style="{'--editor-width':typeof (&quot;min(560px,92vw)&quot;)==='number'?(&quot;min(560px,92vw)&quot;)+'px':(&quot;min(560px,92vw)&quot;)}" :show-close-button="!saving" @interact-outside="e=>{if(!(false))e.preventDefault()}" @escape-key-down="e=>{if(!(!saving))e.preventDefault()}"><DialogHeader class="shrink-0 border-b p-5 pr-12"><DialogTitle>{{editing?'编辑可用率规则':'新建可用率规则'}}</DialogTitle><DialogDescription class="sr-only">详情与操作</DialogDescription></DialogHeader><div class="min-h-0 flex-1 overflow-auto p-5"><form ref="formRef" @submit.prevent=""><fieldset   class="rule-fields" :disabled="saving"><Field data-validation-field="service" :data-validation-rules="JSON.stringify([{required:true,message:'请选择应用'}])" :data-invalid="!!_validationErrors['service']" @vue:mounted="delete _validationErrors['service']"><FieldLabel>应用</FieldLabel><OptionPicker v-model="form.service" :options="services"  width="100%"/><FieldError v-if="_validationErrors['service']">{{_validationErrors['service'].message}}</FieldError></Field><Field data-validation-field="environment" :data-validation-rules="JSON.stringify([{required:true,message:'请选择环境'}])" :data-invalid="!!_validationErrors['environment']" @vue:mounted="delete _validationErrors['environment']"><FieldLabel>环境</FieldLabel><OptionPicker v-model="form.environment" :options="[{value:'*',label:'全部环境（显式兜底）'},...environmentOptions.map(e=>({value:e,label:e}))]" width="100%"/><FieldError v-if="_validationErrors['environment']">{{_validationErrors['environment'].message}}</FieldError></Field><Field class="col-span-2"><FieldLabel>指标（可选）</FieldLabel><Input v-model="form.name" placeholder="例如：rpc.server.duration"/><FieldDescription>留空作为该应用的默认规则。</FieldDescription></Field><Field><FieldLabel>成功码</FieldLabel><Input :model-value="(form.ok_codes||[]).join(', ')" @update:model-value="v=>form.ok_codes=v.split(',').map(x=>x.trim()).filter(Boolean)" placeholder="逗号分隔；留空使用内置判定"/></Field><Field><FieldLabel>忽略码</FieldLabel><Input :model-value="(form.ignore_codes||[]).join(', ')" @update:model-value="v=>form.ignore_codes=v.split(',').map(x=>x.trim()).filter(Boolean)" placeholder="逗号分隔；这些码不计入可用率"/></Field></fieldset></form></div><DialogFooter class="shrink-0 flex-wrap border-t p-4"><Button @click="show=false" variant="outline" size="default" type="button" :disabled="(saving)">取消</Button><Button @click="save" variant="default" size="default" type="button" :disabled="(saving)"><Spinner v-if="saving"/>保存</Button></DialogFooter></DialogContent></Dialog></section></template>

<style scoped>.list-page-surface{min-width:0}</style>

<style scoped>.rule-fields{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:20px 16px}</style>
