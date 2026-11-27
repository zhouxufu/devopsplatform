<script setup>



import {computed, onBeforeUnmount, ref, watch} from 'vue'
import {onBeforeRouteLeave, useRoute} from 'vue-router'
import {api, post, operationId} from '../api.js'
import {toast} from '../../shared/composables/toast.js'


import {deliveryDrafts} from '../../deploy/deliveryDrafts.js'
import WorkDeliveryEditor from '../../deploy/components/WorkDeliveryEditor.vue'
import EditorDialogTitle from '../../shared/components/EditorDialogTitle.vue'
import ReleaseIntentFields from './ReleaseIntentFields.vue'
import CommentComposer from './CommentComposer.vue'
import {refreshActionConflict, conflictRefreshedMessage} from '../actionConflict.js'
const props=defineProps({show:Boolean,item:Object,readIdentity:String,operation:Object})
const emit=defineEmits(['close','saved','state'])
const route=useRoute(),loading=ref(false),saving=ref(false),uploading=ref(false),record=ref(null),options=ref(null),form=ref({}),baseline=ref(''),error=ref(''),blocked=ref(false),operation=ref(null)
const tab=ref('resolve'),editorReady=ref(false),editor=ref(null),changeState=ref({dirty:false,busy:false}),services=ref([]),me=ref({}),editorLoading=ref(false),draftsSaved=ref(false)
const attachmentFiles=ref([])
const fullscreen=ref(false)
watch(()=>props.show,show=>{if(show)fullscreen.value=false})
function updateAttachments(files){attachmentFiles.value=files;form.value.attachments=files.map(file=>file.id)}
const canManage=value=>me.value.role==='admin'||value?.owner===me.value.username
let generation=0
const busy=computed(()=>loading.value||saving.value||uploading.value||editorLoading.value||changeState.value.busy)
const dirty=computed(()=>props.show&&!operation.value&&(changeState.value.dirty||!!options.value&&JSON.stringify(form.value)!==baseline.value))
const title=computed(()=>'解决 Bug · '+(record.value?.external_id||props.item?.external_id||props.item?.id||''))
const resolutions=computed(()=>Object.entries(options.value?.resolutions||{fixed:'已修复',duplicate:'重复 Bug',notrepro:'无法重现',bydesign:'设计如此',external:'外部原因',postponed:'延期处理',willnotfix:'不予解决'}).map(([value,label])=>({value,label})))
const builds=computed(()=>Object.entries(options.value?.builds||{}).map(([value,label])=>({value,label})))
const users=computed(()=>Object.entries(options.value?.users||{}).filter(([value])=>value&&value!=='closed').map(([value,label])=>({value,label})))
const verification=computed(()=>({path:'/settings',query:{connection:props.item?.source?.slice(7),return_to:route.fullPath}}))
watch([dirty,busy],()=>emit('state',{dirty:dirty.value,busy:busy.value}))
async function close(){if(saving.value||uploading.value||changeState.value.busy)return;if(dirty.value&&!await confirmAction('关闭将放弃未提交的解决信息；交付内容草稿会保留。是否关闭？'))return;++generation;loading.value=false;emit('close')}
async function openChanges(){
 tab.value='changes';if(editorReady.value||editorLoading.value)return
 const current=generation;editorLoading.value=true
 try{const result=await Promise.all([api('services'),api('/api/me')]);if(current!==generation||!props.show)return;[services.value,me.value]=result;editorReady.value=true}
 catch(e){if(current===generation){error.value=e.message;toast(e.message,'error')}}finally{if(current===generation)editorLoading.value=false}
}
async function load(){
 const current=++generation;loading.value=true;error.value='';blocked.value=false;options.value=null;operation.value=props.operation||null;record.value=null;attachmentFiles.value=[]
 try {
  if(props.readIdentity==='public'){blocked.value=true;error.value='当前使用公共身份读取，请先验证本人来源账号后解决 Bug。';return}
  const selected=props.item
  if(!selected?.source?.startsWith('zentao:'))throw new Error('此操作仅适用于来源平台 Bug')
  const reference=selected.id>0?selected:await post('research/work-items/attach',{connection:selected.source.slice(7),external_id:selected.external_id})
  if(current!==generation)return
  const detail=await api('research/work-items/'+reference.id)
  if(current!==generation)return
  record.value=detail.item
  if(detail.source_identity==='public'||!detail.remote){blocked.value=true;error.value=detail.source_error||'请先验证本人来源账号后解决 Bug。';return}
  const pending=props.operation||(detail.pending_operations||[]).find(op=>op.request?.action==='resolve')
  if(pending){operation.value=pending;return}
  if(detail.remote.bug?.status!=='active'){blocked.value=true;error.value='该 Bug 已不处于激活状态，请关闭弹窗后核对最新状态。';return}
  const result=await api('research/work-items/'+reference.id+'/action-options?action=resolve')
  if(current!==generation)return
  options.value=result
  const assigned=result.assignedTo||result.bug?.assignedTo||''
  form.value={operation_id:operationId(),action:'resolve',expected_digest:result.digest,title:result.bug?.title||record.value.title,description:result.bug?.steps??record.value.description,resolution:result.resolutions?.fixed?'fixed':Object.keys(result.resolutions||{fixed:'已修复'})[0],resolved_build:result.bug?.resolvedBuild||(result.builds?.trunk?'trunk':''),assigned_to:assigned==='closed'?'':assigned,duplicate_bug:'',comment:'',attachments:[],keep_following:true,metadata:{release_required:null,no_release_reason:'',revision:record.value.metadata_revision||0}}
  baseline.value=JSON.stringify(form.value)
  if(deliveryDrafts.has(record.value.id))await openChanges()
 }catch(e){if(current===generation){error.value=e.message;toast(e.message,'error')}}finally{if(current===generation)loading.value=false}
}
function validation(){
 if(!form.value.resolution)return '请选择解决方式'
 if(form.value.resolution==='fixed'&&!form.value.resolved_build)return '已修复时请选择来源解决版本'
 if(form.value.resolution==='duplicate'&&!/^[1-9]\d*$/.test(form.value.duplicate_bug||''))return '请填写有效的重复 Bug 编号'
 return ''
}
function completed(){toast('已解决并同步来源平台','success');emit('saved',record.value)}
async function refreshConflict(){
 try{const refreshed=await refreshActionConflict(record.value.id,form.value,api,detail=>{record.value={...detail.item,external_status:detail.remote?.bug?.status||detail.item.external_status}});options.value=refreshed.options;form.value=refreshed.form;error.value=record.value.external_status==='active'?conflictRefreshedMessage:'已刷新：该 Bug 已不处于激活状态；填写内容已保留，请核对最新详情。';toast(error.value,'warning',{duration:6000})}
 catch(e){error.value='刷新未完成，填写内容已保留。'+e.message;toast(error.value,'error')}
}
async function submit(){
 if(busy.value||operation.value||!options.value)return
 if(record.value.external_status!=='active'){toast('该 Bug 已不处于激活状态，请核对最新详情','warning');return}
 if(!form.value.expected_digest){saving.value=true;try{await refreshConflict()}finally{saving.value=false}return}
 const message=validation();if(message){tab.value='resolve';error.value=message;toast(message,'warning');return}
 saving.value=true;error.value=''
 // Local draft save and a remote Bug action are separate transactions. Never
 // resolve the Bug if draft validation/save failed, and never replay a source
 // action merely because its result is uncertain.
 if(editor.value?.hasDraft()){
  try{if(!await editor.value.saveDrafts()){tab.value='changes';error.value='变更未全部保存，尚未提交 Bug 解决操作。';saving.value=false;return}}
  catch(e){tab.value='changes';error.value=e.message;saving.value=false;return}
 }
 try{const result=await post('research/work-items/'+record.value.id+'/actions',form.value);if(result.state==='success'){completed();return}operation.value=result;toast(result.message||'来源结果待核对，未重复提交','warning')}
 catch(e){if(e.code==='research_source_conflict'){await refreshConflict();return}error.value=e.message;toast(e.message,'error');if(!e.status||e.status>=500){try{operation.value=await api('research/operations/'+form.value.operation_id);if(operation.value.state==='success'){completed();return}}catch{operation.value={id:form.value.operation_id,state:'uncertain',message:'提交结果尚未确认，请核对结果，不要重复提交。'}}}}
 finally{saving.value=false}
}
async function reconcile(){if(busy.value||!operation.value)return;saving.value=true;try{operation.value=await post('research/operations/'+operation.value.id+'/reconcile',{});if(operation.value.state==='success')completed();else toast(operation.value.message||'仍需核对来源结果','warning')}catch(e){error.value=e.message;toast(e.message,'warning')}finally{saving.value=false}}
function warn(e){if(props.show&&(dirty.value||busy.value)){e.preventDefault();e.returnValue=''}}
window.addEventListener('beforeunload',warn)
onBeforeRouteLeave(async ()=>{if(!props.show)return;if(saving.value||uploading.value){toast('操作进行中，请等待结果','warning');return false}if(dirty.value&&!await confirmAction('离开会丢弃未保存的解决内容，是否继续？'))return false})
watch(()=>props.show,show=>{if(show){tab.value='resolve';load()}else{++generation;options.value=null;form.value={};baseline.value='';record.value=null;operation.value=null;error.value='';blocked.value=false;loading.value=false;editorReady.value=false;editorLoading.value=false;changeState.value={dirty:false,busy:false};draftsSaved.value=false;emit('state',{dirty:false,busy:false})}},{immediate:true})
onBeforeUnmount(()=>{++generation;window.removeEventListener('beforeunload',warn);emit('state',{dirty:false,busy:false})})

import OptionPicker from '../../shared/components/OptionPicker.vue'



import {Button,Checkbox,Dialog,DialogContent,DialogDescription,DialogFooter,DialogHeader,DialogTitle,Field,FieldLabel,Input,Spinner,Tabs,TabsContent,TabsList,TabsTrigger} from '../../shared/shadcn-ui.js'

import {confirmAction} from "../../shared/shadcnFeedback.js"
</script>
<template><Dialog :open="show" @update:open="async v=>{if(!v){close()}}"><DialogContent class="flex max-h-[92dvh] flex-col gap-0 overflow-hidden p-0 sm:max-w-[var(--editor-width)]" :class="fullscreen?'inset-0 h-dvh max-h-dvh w-screen max-w-none translate-x-0 translate-y-0 rounded-none sm:max-w-none':''" :style="{'--editor-width':typeof (tab==='changes'?'min(1000px, 94vw)':'min(660px, 92vw)')==='number'?(tab==='changes'?'min(1000px, 94vw)':'min(660px, 92vw)')+'px':(tab==='changes'?'min(1000px, 94vw)':'min(660px, 92vw)')}" :show-close-button="!(saving||uploading)" @interact-outside="e=>{if(!(!(saving||uploading)))e.preventDefault()}" @escape-key-down="e=>{if(!(!(saving||uploading)))e.preventDefault()}"><DialogHeader class="shrink-0 border-b p-5 pr-12"><DialogTitle><EditorDialogTitle :title="title" :fullscreen="fullscreen" @toggle="fullscreen=!fullscreen"/></DialogTitle><DialogDescription class="sr-only">详情与操作</DialogDescription></DialogHeader><div class="min-h-0 flex-1 overflow-auto p-5"><p :title="item?.title" class="resolve-subject">{{item?.title}}</p><p v-if="loading" role="status" class="r-muted">正在读取最新状态和解决选项…</p><template v-else-if="operation"><p role="status">{{operation.message||'上次操作结果待核对'}}</p><p class="r-muted">只读取和核对上次操作，不重放解决请求；也可稍后在个人待办中处理。</p></template><template v-else-if="options&&!blocked"><Tabs class="section-tabs" :model-value="String(tab)" @update:model-value="(value=>{if(value==='changes')openChanges();else tab='resolve'})($event)"><div class="flex min-w-0 items-center gap-2"><TabsList class="h-auto! max-w-full flex-wrap gap-y-1 [&>[data-slot=tabs-trigger]]:h-7"><TabsTrigger :disabled="saving" :value="&quot;resolve&quot;">解决信息</TabsTrigger><TabsTrigger :disabled="saving" :value="&quot;changes&quot;">维护交付内容</TabsTrigger></TabsList></div><TabsContent :disabled="saving" :value="&quot;resolve&quot;" force-mount v-show="!(String(tab)!==&quot;resolve&quot;)" class="min-w-0"></TabsContent><TabsContent :disabled="saving" :value="&quot;changes&quot;" force-mount v-show="!(String(tab)!==&quot;changes&quot;)" class="min-w-0"></TabsContent></Tabs><div v-show="tab==='resolve'" class="r-grid resolve-fields"><Field ><FieldLabel as="div">解决方式</FieldLabel><OptionPicker v-model="form.resolution" :options="resolutions" :style="{width:'100%',maxWidth:'100%'}" :disabled="busy"/></Field><Field ><FieldLabel as="div">解决版本</FieldLabel><OptionPicker v-model="form.resolved_build" :options="builds"  :style="{width:'100%',maxWidth:'100%'}" :disabled="busy"/></Field><Field v-if="form.resolution==='duplicate'"><FieldLabel as="div">重复 Bug 编号</FieldLabel><Input v-model="form.duplicate_bug" :disabled="busy"/></Field><Field ><FieldLabel as="div">指派给</FieldLabel><OptionPicker v-model="form.assigned_to" :options="users"  :style="{width:'100%',maxWidth:'100%'}" :disabled="busy"/></Field><ReleaseIntentFields :item="record" :model="form.metadata" :disabled="busy"/><label class="resolve-full"><span><Checkbox v-model="form.keep_following" :disabled="busy"/>解决后继续关注交付进展</span></label><CommentComposer compact label="处理说明" input-label="处理说明" placeholder="填写处理说明，可直接粘贴截图，也可拖入多张图片" v-model="form.comment" :attachments="attachmentFiles" :show-submit="false" :disabled="loading||saving||editorLoading||changeState.busy" @update:attachments="updateAttachments" @uploading="uploading=$event" class="resolve-full"/></div><div v-show="tab==='changes'" :inert="saving||operation?true:undefined"><p v-if="editorLoading" role="status">正在读取应用和交付内容…</p><WorkDeliveryEditor v-if="editorReady" ref="editor" inline embedded-submit show :item="record" :services="services" :work-items="[record]" :can-manage="canManage" @state="changeState=$event" @saved="draftsSaved=true"/></div><p v-if="draftsSaved" role="status" class="r-muted">交付内容草稿已保存；解决结果需另行确认，未执行任何变更。</p></template><p v-if="error" role="alert" class="r-error">{{error}}</p><RouterLink v-if="blocked&&!operation" :to="verification">验证本人来源账号并返回</RouterLink></div><DialogFooter class="shrink-0 flex-wrap border-t p-4"><Button @click="close" variant="outline" size="default" type="button" :disabled="(saving||uploading)">{{operation||blocked?'关闭':'取消'}}</Button><Button v-if="operation" @click="reconcile" variant="default" size="default" type="button" :disabled="(saving)"><Spinner v-if="saving"/>核对结果（不重放写操作）</Button><template v-else-if="options&&!blocked&&tab==='changes'"><Button @click="tab='resolve'" variant="outline" size="default" type="button" :disabled="(busy)">返回解决信息</Button><Button @click="async()=>{if(await editor?.saveDrafts()){draftsSaved=true}}" variant="default" size="default" type="button" :disabled="(busy)"><Spinner v-if="busy"/>保存交付草稿</Button></template><Button v-else-if="options&&!blocked" @click="submit" variant="default" size="default" type="button" :disabled="(loading||editorLoading||changeState.busy)||(saving||uploading)"><Spinner v-if="saving||uploading"/>确认并同步来源平台</Button><Button v-else-if="error&&!blocked" @click="load" variant="outline" size="default" type="button" :disabled="(loading)"><Spinner v-if="loading"/>重试读取</Button></DialogFooter></DialogContent></Dialog></template>
<style scoped>.resolve-subject{margin:0 0 16px;font-size:.875rem;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;color:var(--muted-foreground)}.resolve-full{grid-column:1/-1}.resolve-fields{gap:14px}.r-error{margin:12px 0 0}@media(max-width:600px){.resolve-fields{grid-template-columns:1fr}}</style>
