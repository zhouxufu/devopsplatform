<script setup>
import {ref as paginationRef} from 'vue'
const listPageSize=paginationRef(20)
import ListPagination from '../../shared/components/ListPagination.vue'
import {computed,nextTick,onMounted,onDeactivated,ref,watch} from 'vue'


import ResourceBindingsFields from './ResourceBindingsFields.vue'

import {toast} from '../../shared/composables/toast.js'
import {useTableLayout} from '../../shared/composables/tableLayout.js'
const props=defineProps({request:Function,connections:Array,environments:Array})
const emit=defineEmits(['state'])
const {pinTableActions}=useTableLayout()
const groups=ref([]),form=ref(null),busy=ref(false),reading=ref(true),error=ref(''),query=ref(''),page=ref(1),formRef=ref(null),nameInput=ref(null),bindingFields=ref(null),baseline=ref('')
const dirty=computed(()=>!!form.value&&JSON.stringify(form.value)!==baseline.value)
watch([dirty,busy],()=>emit('state',{dirty:dirty.value,busy:busy.value}),{immediate:true})
const filtered=computed(()=>groups.value.filter(g=>[g.name,g.description,...(g.requirements||[]).map(r=>r.alias),...(g.bindings||[]).map(b=>b.environment)].some(v=>String(v||'').toLocaleLowerCase().includes(query.value.trim().toLocaleLowerCase()))))
const rows=computed(()=>filtered.value.slice((page.value-1)*listPageSize.value,page.value*listPageSize.value))
watch(query,()=>page.value=1)
watch(()=>filtered.value.length,n=>page.value=Math.min(page.value,Math.max(1,Math.ceil(n/listPageSize.value))))
const rules={name:[{required:true,message:'请填写资源组标识'},{match:/^[a-z][a-z0-9_-]{0,31}$/,message:'小写字母开头，支持小写字母、数字、短横线和下划线，最多 32 位'}]}
async function load(){reading.value=true;error.value='';try{const data=await props.request('api/resource-groups');if(!Array.isArray(data))throw Error('资源组数据格式异常');groups.value=data}catch(e){error.value=e.message}finally{reading.value=false}}
function edit(g){form.value=g?JSON.parse(JSON.stringify(g)):{name:'',description:'',revision:0,requirements:[{alias:'',kind:'mysql',required:false,description:''}],bindings:[]};for(const b of form.value.bindings)b.config=b.config||{};baseline.value=JSON.stringify(form.value)}
async function close(){if(busy.value)return;if(dirty.value&&!await confirmAction('关闭将放弃本次未保存的资源组修改。','放弃未保存的修改？',{confirmButtonText:'放弃修改',cancelButtonText:'继续编辑'}))return;form.value=null}
function cancel(){close();return false}
async function save(){
 if(busy.value)return
 busy.value=true
 try{
  if(await validateFields(formRef.value,form.value,rules,_validationErrors)){busy.value=false;await nextTick();focusControl(nameInput.value);toast('请完善资源组标识','warning');return}
  const problem=bindingFields.value?.validate?.()
  if(problem){busy.value=false;await nextTick();bindingFields.value?.focusInvalid?.(problem);toast(problem.message,'warning');return}
  await props.request('api/resource-groups',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(form.value)})
  form.value=null;toast('资源组已保存；已有交付预检需重新核对','success');await load()
 }catch(e){toast(e.message,'error')}finally{busy.value=false}
}
async function remove(g){if(busy.value||!await confirmAction('删除资源组「'+g.name+'」？被引用的资源组可能无法删除。','删除资源组',{confirmButtonText:'删除'}))return;busy.value=true;try{await props.request('api/resource-groups/'+encodeURIComponent(g.name),{method:'DELETE'});await load();toast('资源组已删除','success')}catch(e){toast(e.message,'error')}finally{busy.value=false}}
onMounted(load)
onDeactivated(()=>{if(!busy.value)form.value=null})
const environmentNames=g=>[...new Set((g.bindings||[]).map(b=>b.environment))].map(name=>props.environments.find(e=>e.name===name)?.display_name||name).join('、')||'尚未绑定'

import {PlusIcon as IconPlus,SearchIcon as IconSearch,RefreshCwIcon as IconRefresh} from '@lucide/vue'

import {confirmAction} from '../../shared/shadcnFeedback.js'

import {reactive as _validationReactive} from 'vue'
import {validateFields,setFieldErrors,scrollToValidationField} from '../../shared/formValidation.js'
const _validationErrors=_validationReactive({})

import {focusControl} from '../../shared/focusControl.js'


import {Badge,Button,Empty,EmptyContent,EmptyDescription,EmptyHeader,EmptyTitle,Field,FieldDescription,FieldError,FieldLabel,Input,Sheet,SheetContent,SheetDescription,SheetFooter,SheetHeader,SheetTitle,Spinner,Table,TableBody,TableCell,TableHead,TableHeader,TableRow} from '../../shared/shadcn-ui.js'

import {InputGroup,InputGroupInput,InputGroupAddon,InputGroupButton} from '../../shared/shadcn-ui.js'

import {XIcon} from '@lucide/vue'
</script>
<template><section><div class="group-head"><h3>资源组</h3><Button @click="edit()" variant="default" size="default" type="button" :disabled="(reading||busy||!!error)"><IconPlus/>新建资源组</Button></div><div class="group-toolbar"><InputGroup  class="min-w-0 "><InputGroupInput v-model="query" placeholder="搜索名称、说明、逻辑资源或环境" v-bind="{'aria-label':'搜索资源组'}"/><InputGroupAddon align="inline-end"><InputGroupButton v-if="query" :disabled="false" size="icon-xs" aria-label="清空输入" @click="query='' "><XIcon/></InputGroupButton></InputGroupAddon></InputGroup><Button aria-label="重新加载资源组" @click="load" variant="outline" size="default" type="button" :disabled="(reading)"><Spinner v-if="reading"/><IconRefresh/></Button></div><Empty v-if="error"><EmptyHeader><EmptyTitle>{{"资源组加载失败"}}</EmptyTitle><EmptyDescription>{{error}}</EmptyDescription></EmptyHeader><EmptyContent><Button @click="load" variant="default" size="default" type="button">重新加载</Button></EmptyContent></Empty><template v-else><div class="console-table min-w-0 max-w-full"><div v-if="reading" class="flex items-center gap-2 py-2" role="status"><Spinner/>正在读取</div><div class="max-w-full overflow-auto rounded-lg border" :style="{maxHeight:({x:1000}).y?({x:1000}).y+'px':undefined}"><Table class="table-fixed min-w-[var(--table-min-width)]" :style="{'--table-min-width':(({x:1000}).x||({x:1000}).minWidth||1000)+'px'}"><TableHeader><TableRow><TableHead :style="{width:(220)+'px'}">资源组</TableHead><TableHead :style="{width:(280)+'px'}">逻辑资源</TableHead><TableHead :style="{width:(230)+'px'}">已绑定环境</TableHead><TableHead :style="{width:(100)+'px'}">修订</TableHead><TableHead :class="(pinTableActions?'right':undefined)==='right'?'sticky right-0 z-10 bg-background':(pinTableActions?'right':undefined)==='left'?'sticky left-0 z-10 bg-background':''" :style="{width:(170)+'px'}">操作</TableHead></TableRow></TableHeader><TableBody><template v-for="(record,rowIndex) in (rows||[])" :key="typeof (&quot;name&quot;)==='function'?(&quot;name&quot;)(record):record[&quot;name&quot;]??rowIndex"><TableRow><TableCell class="align-top"><div class="min-w-0 break-words" :class="{truncate:!!(&quot;&quot;)}"><template v-for="{record:g} in [{record,rowIndex}]"><b>{{g.name}}</b><small>{{g.description||'未填写说明'}}</small></template></div></TableCell><TableCell class="align-top"><div class="min-w-0 break-words" :class="{truncate:!!(false)}"><template v-for="{record:g} in [{record,rowIndex}]"><div class="group-tags"><Badge v-for="r in (g.requirements||[]).slice(0,4)" :key="r.alias" variant="secondary">{{r.alias}}</Badge><Badge v-if="g.requirements?.length>4" variant="secondary">+{{g.requirements.length-4}}</Badge><span v-if="!g.requirements?.length">尚未声明</span></div></template></div></TableCell><TableCell class="align-top"><div class="min-w-0 break-words" :class="{truncate:!!(&quot;&quot;)}"><template v-for="{record:g} in [{record,rowIndex}]">{{environmentNames(g)}}</template></div></TableCell><TableCell class="align-top"><div class="min-w-0 break-words" :class="{truncate:!!(false)}"><template v-for="{record:g} in [{record,rowIndex}]">{{g.revision}}</template></div></TableCell><TableCell class="align-top" :class="(pinTableActions?'right':undefined)==='right'?'sticky right-0 z-10 bg-background':(pinTableActions?'right':undefined)==='left'?'sticky left-0 z-10 bg-background':''"><div class="min-w-0 break-words" :class="{truncate:!!(false)}"><template v-for="{record:g} in [{record,rowIndex}]"><Button @click="edit(g)" variant="ghost" size="sm" type="button" :disabled="(busy)">编辑</Button><Button @click="remove(g)" class="text-destructive" :variant="'destructive'" size="sm" type="button" :disabled="(busy)">删除</Button></template></div></TableCell></TableRow></template><TableRow v-if="!(rows||[]).length"><TableCell :colspan="5"><Empty v-if="!reading"><EmptyHeader><EmptyDescription><p>{{groups.length?'没有匹配的资源组':'暂无资源组'}}</p><Button v-if="groups.length" @click="query=''" variant="ghost" size="default" type="button">清空筛选</Button><span v-else>应用仍可使用独立资源配置，无需强制创建资源组。</span></EmptyDescription></EmptyHeader></Empty></TableCell></TableRow></TableBody></Table></div></div><div class="group-footer"><small>在应用资源设置中选择复用，按目标环境解析实际连接。</small><ListPagination :page="page" :total="filtered.length" :page-size="listPageSize" @update:page="page=$event;" @update:page-size="listPageSize=$event;page=1" :disabled="reading"/></div></template><Sheet :open="!!form" @update:open="async v=>{if(!v){if(await (cancel)()===false)return;}}"><SheetContent class="data-[side=right]:w-full gap-0 overflow-hidden p-0 data-[side=right]:sm:max-w-[var(--editor-width)]" :class="false?'inset-0 h-dvh max-h-dvh w-screen max-w-none translate-x-0 translate-y-0 rounded-none sm:max-w-none':''" :style="{'--editor-width':typeof ('min(1040px, 100vw)')==='number'?('min(1040px, 100vw)')+'px':('min(1040px, 100vw)')}" :show-close-button="!busy" @interact-outside="e=>{if(!(false))e.preventDefault()}" @escape-key-down="e=>{if(!(!busy))e.preventDefault()}"><SheetHeader class="shrink-0 border-b p-5 pr-12"><SheetTitle>{{form?.revision?'编辑资源组':'新建资源组'}}</SheetTitle><SheetDescription class="sr-only">详情与操作</SheetDescription></SheetHeader><div class="min-h-0 flex-1 overflow-auto p-5"><form ref="formRef" @submit.prevent=""><fieldset v-if="form"   class="min-w-0 space-y-5" :disabled="busy"><div class="group-form"><Field data-validation-field="name" :data-invalid="!!_validationErrors['name']" @vue:mounted="delete _validationErrors['name']"><FieldLabel>资源组标识</FieldLabel><Input ref="nameInput" v-model="form.name" :disabled="!!form.revision" placeholder="如 business-resources"/><FieldError v-if="_validationErrors['name']">{{_validationErrors['name'].message}}</FieldError></Field><Field data-validation-field="description" :data-invalid="!!_validationErrors['description']" @vue:mounted="delete _validationErrors['description']"><FieldLabel>说明</FieldLabel><Input v-model="form.description" placeholder="说明这组资源的用途"/><FieldError v-if="_validationErrors['description']">{{_validationErrors['description'].message}}</FieldError></Field></div><ResourceBindingsFields ref="bindingFields" catalog-nacos :min-resources="1" :resources="form" :connections="connections" :environments="environments" :disabled="busy"/></fieldset></form></div><SheetFooter class="shrink-0 flex-wrap border-t p-4 sm:flex-row sm:justify-end"><Button @click="close" variant="outline" size="default" type="button" :disabled="(busy)">取消</Button><Button @click="save" variant="default" size="default" type="button" :disabled="(busy)"><Spinner v-if="busy"/>保存资源组</Button></SheetFooter></SheetContent></Sheet></section></template>
<style scoped>.group-head,.group-toolbar,.group-footer{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-bottom:16px;flex-wrap:wrap}.group-head h3{font-size:1.125rem;margin:0}.group-form{display:grid;grid-template-columns:1fr 2fr;gap:16px}.group-tags{display:flex;gap:4px;flex-wrap:wrap}small{display:block;color:var(--muted-foreground);font-size:.875rem}.group-footer{margin-top:16px;margin-bottom:0}@media(max-width:700px){.group-form{grid-template-columns:1fr}}</style>

<style scoped>.group-toolbar :deep([data-slot="input-group"]){flex:1;min-width:220px;max-width:420px;width:auto}</style>
