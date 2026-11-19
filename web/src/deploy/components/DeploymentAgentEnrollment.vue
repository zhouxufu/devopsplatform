<script setup>
import {computed,onBeforeUnmount,ref,watch} from 'vue'
import OptionPicker from '../../shared/components/OptionPicker.vue'
import HelpTip from '../../shared/components/HelpTip.vue'
import {toast} from '../../shared/composables/toast.js'
import {confirmAction} from '../../shared/shadcnFeedback.js'
import {Accordion,AccordionContent,AccordionItem,AccordionTrigger,Badge,Button,Field,FieldError,FieldLabel,Input,Sheet,SheetContent,SheetDescription,SheetFooter,SheetHeader,SheetTitle,Spinner} from '../../shared/shadcn-ui.js'
const props=defineProps({host:Object,request:Function})
const emit=defineEmits(['close','state'])
const loading=ref(false),busy=ref(false),error=ref(''),options=ref([]),enrollment=ref(null),preview=ref(null)
const services=ref([]),url=ref(''),runUser=ref('logagent'),runGroup=ref('logagent')
const labels={preview:'待确认',applying:'正在接入',awaiting_connection:'等待 Agent 回连',needs_review:'接入结果待核对',ready:'可用于部署',disabled:'已停用',failed:'接入失败'}
const phase=computed(()=>enrollment.value?.phase||'unconfigured')
const pending=computed(()=>['applying','awaiting_connection','needs_review'].includes(phase.value))
const selectedRoots=computed(()=>options.value.filter(o=>services.value.includes(o.service)).map(o=>o.root))
const base=computed(()=>'api/hosts/'+encodeURIComponent(props.host?.name||'')+'/deployment')
let seq=0,pollTimer=null,polls=0
watch(busy,value=>emit('state',{dirty:false,busy:value}),{immediate:true})
async function read(initial=false){
 const current=++seq;loading.value=true;error.value=''
 try{const data=await props.request(base.value);if(current!==seq)return;const previous=phase.value;options.value=data.options||[];enrollment.value=data.enrollment||null;if(previous!=='ready'&&phase.value==='ready')toast('部署 Agent 已回连，可用于部署','success')
  if(initial){const plan=enrollment.value?.plan;services.value=plan?.services||[];url.value=plan?.url||location.origin+'/api/plugins/deploy/api/deployment-agent';runUser.value=plan?.run_user||'logagent';runGroup.value=plan?.run_group||runUser.value}
 }catch(e){if(current===seq)error.value=e.message}finally{if(current===seq){loading.value=false;clearTimeout(pollTimer);if(props.host&&pending.value&&polls++<30)pollTimer=setTimeout(()=>read(),2000)}}
}
watch(()=>props.host?.name,()=>{seq++;clearTimeout(pollTimer);polls=0;preview.value=null;if(props.host)read(true)},{immediate:true})
watch([services,url,runUser,runGroup],()=>{preview.value=null},{deep:true})
onBeforeUnmount(()=>{seq++;clearTimeout(pollTimer);emit('state',{dirty:false,busy:false})})
async function post(suffix,body){return props.request(base.value+suffix,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)})}
async function prepare(){
 if(busy.value)return
 if(!services.value.length){error.value='请选择允许部署的应用';return}
 busy.value=true;error.value=''
 try{preview.value=await post('/preview',{services:services.value,url:url.value,run_user:runUser.value,run_group:runGroup.value})}catch(e){error.value=e.message}finally{busy.value=false}
}
async function confirm(){
 if(!preview.value||busy.value)return
 const id=preview.value.operation_id;busy.value=true;error.value=''
 try{enrollment.value=await post('/confirm',{operation_id:id,confirm:true});preview.value=null;polls=0;if(pending.value)pollTimer=setTimeout(()=>read(),1500);toast(enrollment.value.phase==='ready'?'部署 Agent 接入已验证':'接入已提交，请刷新查看 Agent 回连结果','success')}
 catch(e){preview.value=null;error.value='提交结果尚未确认：'+e.message+'。请刷新状态核对，不要重复提交。';await read()}
 finally{busy.value=false}
}
async function disable(){
 if(busy.value||!await confirmAction('停止接收新的部署任务，保留采集配置、任务记录和已运行的应用。','停用此主机的部署授权？',{confirmButtonText:'停用部署'}))return
 busy.value=true;try{enrollment.value=await post('/disable',{});preview.value=null;toast('部署授权已停用','success')}catch(e){error.value=e.message}finally{busy.value=false}
}
function close(){if(!busy.value)emit('close')}
</script>
<template>
 <Sheet :open="!!host" @update:open="value=>{if(!value)close()}">
  <SheetContent class="gap-0 overflow-hidden p-0 data-[side=right]:w-full data-[side=right]:sm:max-w-2xl" :show-close-button="!busy" @interact-outside="event=>event.preventDefault()" @escape-key-down="event=>{if(busy)event.preventDefault()}">
   <SheetHeader class="shrink-0 border-b p-5 pr-12"><SheetTitle>部署 Agent 接入</SheetTitle><SheetDescription>{{host?.name}} · {{host?.addr}} · {{host?.environment}}</SheetDescription></SheetHeader>
   <div class="min-h-0 flex-1 space-y-5 overflow-auto p-5">
    <div class="flex items-center justify-between gap-2"><Badge variant="secondary">{{labels[phase]||'尚未接入'}}</Badge><Button variant="outline" size="sm" :disabled="busy||loading" @click="read()"><Spinner v-if="loading"/>刷新状态</Button></div>
    <FieldError v-if="error" role="alert">{{error}}</FieldError>
    <template v-if="preview">
     <dl class="grid gap-4 text-sm"><div><dt class="text-muted-foreground">执行用户</dt><dd>{{preview.plan.run_user}} / {{preview.plan.run_group}}</dd></div><div><dt class="text-muted-foreground">允许发布的应用</dt><dd>{{preview.plan.services.join('、')}}</dd></div><div><dt class="text-muted-foreground">允许的部署目录</dt><dd v-for="root in preview.plan.scope.roots" :key="root" class="break-all font-mono">{{root}}</dd></div><div><dt class="text-muted-foreground">平台回连地址</dt><dd class="break-all">{{preview.plan.url}}</dd></div></dl>
     <p class="text-sm text-muted-foreground">确认后安装或升级 Agent，自动下发独立部署令牌及固定 worker 授权，并验证隔离和连接。采集设置与安装身份沿用原值；不会执行应用发布。预览 10 分钟有效。</p>
     <FieldError v-if="preview.plan.run_user==='root'">此主机将以 root 身份执行部署，请确认上述应用与目录范围。</FieldError>
    </template>
    <template v-else-if="!pending">
     <Field><FieldLabel>允许部署的应用<HelpTip text="仅列出已绑定此主机的托管 Java 应用；目录沿用各应用的环境配置。接入完成后，在应用环境选择 Agent 通道才会用于后续发布。"/></FieldLabel><OptionPicker v-model="services" multiple :options="options.map(o=>({value:o.service,label:o.service}))" :disabled="busy||loading" placeholder="选择应用" aria-label="允许部署的应用"/></Field>
     <div v-if="selectedRoots.length" class="space-y-1 text-sm text-muted-foreground"><div v-for="root in selectedRoots" :key="root" class="break-all font-mono">{{root}}</div></div>
     <p v-if="!loading&&!options.length" class="text-sm text-muted-foreground">此主机尚未绑定托管 Java 应用，请先配置应用的环境和部署目录。</p>
     <Accordion type="single" collapsible><AccordionItem value="connection"><AccordionTrigger>接入设置</AccordionTrigger><AccordionContent class="space-y-4"><Field><FieldLabel>平台回连地址</FieldLabel><Input v-model="url" :disabled="busy"/></Field><div class="grid gap-4 sm:grid-cols-2"><Field><FieldLabel>新装执行用户<HelpTip text="已有 Agent 保留原执行用户。不会递归更改已有目录属主；目录须允许该用户读写。"/></FieldLabel><Input v-model="runUser" :disabled="busy"/></Field><Field><FieldLabel>新装用户组</FieldLabel><Input v-model="runGroup" :disabled="busy"/></Field></div></AccordionContent></AccordionItem></Accordion>
    </template>
    <p v-else class="text-sm text-muted-foreground">{{phase==='needs_review'?'安装或回连结果尚未确认。刷新只查询原操作，不会重新安装或更换令牌。':'正在等待 Agent 以独立令牌回连并提交 worker 验证结果。'}}原操作 {{enrollment?.operation_id}}</p>
   </div>
   <SheetFooter class="shrink-0 flex-row justify-end gap-2 border-t p-5"><Button v-if="enrollment&&!preview" variant="ghost" :disabled="busy||loading" @click="disable">停用部署</Button><Button variant="outline" :disabled="busy" @click="preview?preview=null:close()">{{preview?'上一步':'关闭'}}</Button><Button v-if="preview" :disabled="busy" @click="confirm"><Spinner v-if="busy"/>确认并自动配置</Button><Button v-else-if="!pending" :disabled="busy||loading||!options.length" @click="prepare"><Spinner v-if="busy"/>{{phase==='ready'?'核对并轮换令牌':'预览启用部署'}}</Button></SheetFooter>
  </SheetContent>
 </Sheet>
</template>
