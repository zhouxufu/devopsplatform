<script setup>
import {ref as paginationRef} from 'vue'
const listPageSize=paginationRef(20)
import ListPagination from '../../shared/components/ListPagination.vue'
import {computed, nextTick, onMounted, onBeforeUnmount, ref, watch} from 'vue'



import {toast} from '../../shared/composables/toast.js'
import {useTableLayout} from '../../shared/composables/tableLayout.js'
import {filterRunners, runnerKindLabel, runnerLabels, runnerPayload} from '../resourceRunners.js'

const props = defineProps({request:{type:Function,required:true},active:{type:Boolean,default:true}})
const emit = defineEmits(['state'])
const {pinTableActions} = useTableLayout()
const records=ref([]), loading=ref(true), loadError=ref(''), query=ref(''), status=ref(''), page=ref(1)
const visible=ref(false), editing=ref(false), saving=ref(false), form=ref({}), initial=ref(''), formRef=ref(null), nameInput=ref(null), parallelInput=ref(null)
let readSequence=0
onBeforeUnmount(()=>{readSequence++})
const dirty=computed(()=>visible.value && JSON.stringify(runnerPayload(form.value))!==initial.value)
watch(()=>props.active,active=>{if(!active&&!saving.value)visible.value=false})
watch(()=>({dirty:dirty.value,busy:saving.value}), value=>emit('state',value),{immediate:true})
const filtered=computed(()=>filterRunners(records.value,query.value,status.value))
const rows=computed(()=>filtered.value.slice((page.value-1)*listPageSize.value,page.value*listPageSize.value))
watch([query,status],()=>page.value=1)
watch(()=>filtered.value.length,total=>page.value=Math.min(page.value,Math.max(1,Math.ceil(total/listPageSize.value))))
const rules={
  name:[{required:true,message:'请填写 Runner 标识'},{match:/^[a-z][a-z0-9_-]{0,31}$/,message:'小写字母开头，支持小写字母、数字、短横线和下划线，长度 1–32'},{validator:(value,callback)=>callback(!editing.value&&records.value.some(row=>row.name===String(value||'').trim())?'标识已存在，请编辑已有 Runner':undefined)}],
  max_parallel:[{required:true,type:'number',message:'请填写最大并发数'},{validator:(value,callback)=>callback(Number.isInteger(value)&&value>=1&&value<=32?undefined:'并发数须为 1–32 的整数')}]
}
async function load(){
  const sequence=++readSequence
  loading.value=true;loadError.value=''
  try{const data=await props.request('api/runners');if(!Array.isArray(data))throw new Error('Runner 数据格式异常，请重试');if(sequence===readSequence)records.value=data}
  catch(e){if(sequence===readSequence)loadError.value=e.message||'请检查网络后重试'}
  finally{if(sequence===readSequence)loading.value=false}
}
onMounted(load)
function edit(row){
  editing.value=!!row
  form.value=row?runnerPayload(row):{name:'',kind:'local',labels:'',enabled:true,max_parallel:1}
  initial.value=JSON.stringify(runnerPayload(form.value));visible.value=true
}
async function close(){
  if(saving.value)return
  if(dirty.value&&!await confirmAction('关闭将放弃本次未保存的修改，已保存的 Runner 不受影响。','放弃 Runner 修改？',{confirmButtonText:'放弃修改',cancelButtonText:'继续编辑'}))return
  visible.value=false
}
function drawerCancel(){close();return false}
async function save(){
  if(saving.value)return
  // Set before async validation too, so repeated clicks cannot submit twice.
  saving.value=true
  try{
    const errors=await validateFields(formRef.value,form.value,rules,_validationErrors)
    if(errors){saving.value=false;const field=Object.keys(errors)[0];scrollToValidationField(formRef.value,field);await nextTick();focusControl(field==='name'?nameInput.value:parallelInput.value);toast('请完善标记的配置项','warning');return}
    const payload=runnerPayload(form.value)
    await props.request('api/runners',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(payload)})
    visible.value=false;toast('Runner 配置已保存','success');await load()
  }catch(e){toast(e.message||'保存失败，修改已保留，请重试','error')}
  finally{saving.value=false}
}

import OptionPicker from '../../shared/components/OptionPicker.vue'

import {PlusIcon as IconPlus,SearchIcon as IconSearch,RefreshCwIcon as IconRefresh} from '@lucide/vue'

import {confirmAction} from '../../shared/shadcnFeedback.js'

import {reactive as _validationReactive} from 'vue'
import {validateFields,setFieldErrors,scrollToValidationField} from '../../shared/formValidation.js'
const _validationErrors=_validationReactive({})

import {focusControl} from '../../shared/focusControl.js'


import {Badge,Button,Empty,EmptyContent,EmptyDescription,EmptyHeader,EmptyTitle,Field,FieldDescription,FieldError,FieldLabel,Input,NumberField,NumberFieldContent,NumberFieldDecrement,NumberFieldIncrement,NumberFieldInput,Sheet,SheetContent,SheetDescription,SheetFooter,SheetHeader,SheetTitle,Spinner,Switch,Table,TableBody,TableCell,TableHead,TableHeader,TableRow} from '../../shared/shadcn-ui.js'

import {InputGroup,InputGroupInput,InputGroupAddon,InputGroupButton} from '../../shared/shadcn-ui.js'

import {XIcon} from '@lucide/vue'
</script>

<template><section aria-label="构建 Runner 管理"><div class="runner-heading"><h3>构建 Runner</h3><Button @click="edit()" variant="default" size="default" type="button" :disabled="(loading||!!loadError)"><IconPlus/>新建 Runner</Button></div><div class="runner-toolbar"><InputGroup  class="min-w-0 "><InputGroupInput v-model="query" aria-label="搜索 Runner" placeholder="搜索 Runner 名称或标签"/><InputGroupAddon align="inline-end"><InputGroupButton v-if="query" :disabled="false" size="icon-xs" aria-label="清空输入" @click="query='' "><XIcon/></InputGroupButton></InputGroupAddon></InputGroup><OptionPicker v-model="status" aria-label="Runner 启用状态" :style="{width:'160px'}" :options="[{value:&quot;&quot;,label:&quot;全部状态&quot;},{value:&quot;enabled&quot;,label:&quot;已启用&quot;},{value:&quot;disabled&quot;,label:&quot;已停用&quot;}]"/><Button aria-label="重新加载 Runner" @click="load" variant="outline" size="default" type="button" :disabled="(loading)"><Spinner v-if="loading"/><IconRefresh/></Button></div><Empty v-if="loadError"><EmptyHeader><EmptyTitle>{{"Runner 列表加载失败"}}</EmptyTitle><EmptyDescription>{{loadError}}</EmptyDescription></EmptyHeader><EmptyContent><Button @click="load" variant="default" size="default" type="button">重新加载</Button></EmptyContent></Empty><template v-else><div class="console-table runner-table min-w-0 max-w-full"><div v-if="loading" class="flex items-center gap-2 py-2" role="status"><Spinner/>正在读取</div><div class="max-w-full overflow-auto rounded-lg border" :style="{maxHeight:({x:900}).y?({x:900}).y+'px':undefined}"><Table class="table-fixed min-w-[var(--table-min-width)]" :style="{'--table-min-width':(({x:900}).x||({x:900}).minWidth||940)+'px'}"><TableHeader><TableRow><TableHead :style="{width:(220)+'px'}">Runner 名称</TableHead><TableHead :style="{width:(140)+'px'}">执行位置</TableHead><TableHead :style="{width:(240)+'px'}">标签</TableHead><TableHead :style="{width:(110)+'px'}">最大并发</TableHead><TableHead :style="{width:(120)+'px'}">配置状态</TableHead><TableHead :class="(pinTableActions?'right':undefined)==='right'?'sticky right-0 z-10 bg-background':(pinTableActions?'right':undefined)==='left'?'sticky left-0 z-10 bg-background':''" :style="{width:(110)+'px'}">操作</TableHead></TableRow></TableHeader><TableBody><template v-for="(record,rowIndex) in (rows||[])" :key="typeof (&quot;name&quot;)==='function'?(&quot;name&quot;)(record):record[&quot;name&quot;]??rowIndex"><TableRow><TableCell class="align-top"><div class="min-w-0 break-words" :class="{truncate:!!(&quot;&quot;)}"><template v-for="{record} in [{record,rowIndex}]"><b>{{record.name}}</b></template></div></TableCell><TableCell class="align-top"><div class="min-w-0 break-words" :class="{truncate:!!(false)}"><template v-for="{record} in [{record,rowIndex}]">{{runnerKindLabel(record.kind)}}</template></div></TableCell><TableCell class="align-top"><div class="min-w-0 break-words" :class="{truncate:!!(false)}"><template v-for="{record} in [{record,rowIndex}]"><div class="runner-tags"><Badge v-for="label in runnerLabels(record.labels)" :key="label" variant="secondary">{{label}}</Badge><span v-if="!runnerLabels(record.labels).length" class="runner-muted">未设置</span></div></template></div></TableCell><TableCell class="align-top"><div class="min-w-0 break-words" :class="{truncate:!!(false)}"><template v-for="{record} in [{record,rowIndex}]">{{record.max_parallel}} 个任务</template></div></TableCell><TableCell class="align-top"><div class="min-w-0 break-words" :class="{truncate:!!(false)}"><template v-for="{record} in [{record,rowIndex}]"><Badge :variant="(record.enabled?'green':'gray')==='red'?'destructive':'secondary'">{{record.enabled?'已启用':'已停用'}}</Badge></template></div></TableCell><TableCell class="align-top" :class="(pinTableActions?'right':undefined)==='right'?'sticky right-0 z-10 bg-background':(pinTableActions?'right':undefined)==='left'?'sticky left-0 z-10 bg-background':''"><div class="min-w-0 break-words" :class="{truncate:!!(false)}"><template v-for="{record} in [{record,rowIndex}]"><Button @click="edit(record)" variant="ghost" size="sm" type="button">编辑</Button></template></div></TableCell></TableRow></template><TableRow v-if="!(rows||[]).length"><TableCell :colspan="6"><Empty v-if="!loading"><EmptyHeader><EmptyDescription>{{records.length?'没有匹配的 Runner':'还没有构建 Runner'}}</EmptyDescription></EmptyHeader></Empty></TableCell></TableRow></TableBody></Table></div></div><div class="runner-footer"><span class="runner-muted">启用状态不代表在线或工具检测结果。</span><ListPagination :page="page" :total="filtered.length" :page-size="listPageSize" @update:page="page=$event;" @update:page-size="listPageSize=$event;page=1" :disabled="loading"/></div></template><Sheet :open="visible" @update:open="async v=>{if(!v){if(await (drawerCancel)()===false)return;}}"><SheetContent class="data-[side=right]:w-full gap-0 overflow-hidden p-0 data-[side=right]:sm:max-w-[var(--editor-width)]" :class="false?'inset-0 h-dvh max-h-dvh w-screen max-w-none translate-x-0 translate-y-0 rounded-none sm:max-w-none':''" :style="{'--editor-width':typeof ('min(480px, 100vw)')==='number'?('min(480px, 100vw)')+'px':('min(480px, 100vw)')}" :show-close-button="!saving" @interact-outside="e=>{if(!(false))e.preventDefault()}" @escape-key-down="e=>{if(!(!saving))e.preventDefault()}" @open-auto-focus="!editing&amp;&amp;nameInput?.focus()"><SheetHeader class="shrink-0 border-b p-5 pr-12"><SheetTitle>{{editing?'编辑 Runner':'新建 Runner'}}</SheetTitle><SheetDescription class="sr-only">详情与操作</SheetDescription></SheetHeader><div class="min-h-0 flex-1 overflow-auto p-5"><form ref="formRef" @submit.prevent=""><fieldset   class="min-w-0 space-y-5" :disabled="saving"><Field data-validation-field="name" :data-invalid="!!_validationErrors['name']" @vue:mounted="delete _validationErrors['name']"><FieldLabel>Runner 标识</FieldLabel><Input ref="nameInput" v-model="form.name" :disabled="editing" placeholder="例如 java-build" :maxlength="32"/><FieldDescription>应用通过标识选择执行位置，创建后不可修改。</FieldDescription><FieldError v-if="_validationErrors['name']">{{_validationErrors['name'].message}}</FieldError></Field><Field><FieldLabel>执行位置</FieldLabel><Input model-value="平台本机" disabled/><FieldDescription>当前仅支持平台所在机器；容器与工具版本在应用构建配置中选择。</FieldDescription></Field><Field data-validation-field="labels" :data-invalid="!!_validationErrors['labels']" @vue:mounted="delete _validationErrors['labels']"><FieldLabel>标签</FieldLabel><Input v-model="form.labels" placeholder="例如 java21,docker"/><FieldDescription>用逗号分隔，供查找与识别；填写标签不会安装构建工具。</FieldDescription><FieldError v-if="_validationErrors['labels']">{{_validationErrors['labels'].message}}</FieldError></Field><Field data-validation-field="max_parallel" :data-invalid="!!_validationErrors['max_parallel']" @vue:mounted="delete _validationErrors['max_parallel']"><FieldLabel>最大并发任务数</FieldLabel><NumberField ref="parallelInput" v-model="form.max_parallel" :min="1" :max="32" mode="button"><NumberFieldContent><NumberFieldDecrement/><NumberFieldInput/><NumberFieldIncrement/></NumberFieldContent></NumberField><FieldDescription>同一 Runner 同时运行的构建任务上限。</FieldDescription><FieldError v-if="_validationErrors['max_parallel']">{{_validationErrors['max_parallel'].message}}</FieldError></Field><Field data-validation-field="enabled" :data-invalid="!!_validationErrors['enabled']" @vue:mounted="delete _validationErrors['enabled']"><FieldLabel>配置状态</FieldLabel><div class="runner-switch"><Switch v-model="form.enabled" aria-label="启用 Runner"/><span>{{form.enabled?'已启用':'已停用'}}</span></div><FieldError v-if="_validationErrors['enabled']">{{_validationErrors['enabled'].message}}</FieldError></Field></fieldset></form></div><SheetFooter class="shrink-0 flex-wrap border-t p-4 sm:flex-row sm:justify-end"><Button @click="close" variant="outline" size="default" type="button" :disabled="(saving)">取消</Button><Button @click="save" variant="default" size="default" type="button" :disabled="(saving)"><Spinner v-if="saving"/>保存配置</Button></SheetFooter></SheetContent></Sheet></section></template>

<style scoped>.runner-heading{display:flex;justify-content:space-between;align-items:center;gap:16px;margin-bottom:16px}.runner-heading h3{font-size:1.125rem;margin:0}
.runner-toolbar{display:flex;gap:12px;align-items:center;flex-wrap:wrap;margin-bottom:16px}
.runner-tags{display:flex;gap:4px;flex-wrap:wrap;overflow-wrap:anywhere}.runner-muted{color:var(--muted-foreground);font-size:.875rem}.runner-footer{display:flex;gap:16px;align-items:center;justify-content:space-between;flex-wrap:wrap;margin-top:16px}.runner-switch{display:flex;gap:10px;align-items:center}
@media(max-width:600px){.runner-heading{align-items:flex-start}}</style>
