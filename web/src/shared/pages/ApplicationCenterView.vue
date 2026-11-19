<script setup>

import PageHeader from "../components/PageHeader.vue"
import {computed, onMounted, ref, watch} from 'vue'
import HelpTip from '../components/HelpTip.vue'
import ApplicationNameEditor from '../components/ApplicationNameEditor.vue'
import {applicationName,applicationIdentifier,validApplicationIdentifier,applicationNameError,announceApplicationName} from '../applicationName.js'
import UiIcon from '../components/UiIcon.vue'

import {toast} from '../composables/toast.js'

const applications=ref([]),loading=ref(true),keyword=ref(''),showCreate=ref(false),name=ref(''),creating=ref(false)
const emit=defineEmits(['state'])
const identifier=ref(''),renaming=ref(null),me=ref({}),deleting=ref(''),loadError=ref('')
fetch('/api/me').then(r=>r.json()).then(v=>me.value=v).catch(()=>{})
function renamed(app){const existing=applications.value.find(s=>s.application_id===app.application_id);if(existing)Object.assign(existing,app)}
watch([showCreate,name,identifier,creating,deleting],()=>emit('state',{dirty:showCreate.value&&!!(name.value.trim()||identifier.value.trim()),busy:creating.value||!!deleting.value}),{immediate:true})
const sourceLabel={deploy:'部署',logs:'日志',otel:'OTel',manual:'手动'}
const filtered=computed(()=>{const q=keyword.value.trim().toLocaleLowerCase();return q?applications.value.filter(a=>[applicationName(a),applicationIdentifier(a),a.name].some(v=>v.toLocaleLowerCase().includes(q))):applications.value})
async function load(){loading.value=true;loadError.value='';try{const r=await fetch('/api/registry/applications');if(!r.ok)throw Error('应用列表读取失败，请重试');applications.value=await r.json()||[]}catch(e){loadError.value=e.message;toast(loadError.value,'error')}finally{loading.value=false}}
async function openDeployment(app){
 try{
  const response=await fetch('/api/plugins/deploy/api/application-references')
  const body=await response.json()
  if(!response.ok||body.complete!==true||!Array.isArray(body.applications))throw Error('无法核实部署关联，请稍后重试')
  const actual=body.applications.find(item=>item.application_id===app.application_id)||body.applications.find(item=>item.name===app.name)
  if(!actual)return toast('原部署配置已不存在，可通过“删除应用”清理失效关联；历史记录保留','warning')
  const hash='#/svc/'+encodeURIComponent(actual.name)+'/settings?settings_section=source'
  if(window.parent!==window)window.parent.postMessage({type:'workspace.open',plugin:'deploy',hash,context:{service:actual.name}},location.origin);else window.open('/api/plugins/deploy/ui'+hash,'_blank','noopener')
 }catch{toast('无法核实部署关联，请确认部署服务正常后重试','error')}
}
async function deleteApplication(app){
 if(deleting.value||me.value.role!=='admin')return
 deleting.value=app.application_id
 try{
  try{await confirmDialog.confirm(`将「${applicationName(app)}」从应用目录移除？会核实部署关联并清理已失效的关联；仍有部署配置时会阻止删除。应用身份与历史记录保留，不停止服务。后续日志或 OTel 上报不会自动恢复目录展示。`,'删除目录应用',{confirmButtonText:'确认删除',cancelButtonText:'取消'})}catch{return}
  const r=await fetch('/api/registry/applications/'+encodeURIComponent(app.application_id),{method:'DELETE'})
  const body=await r.json().catch(()=>({}));if(!r.ok){
   if(r.status===409){try{await confirmDialog.confirm(body.error||'应用仍有部署配置，请先处理部署关联。','先处理部署关联',{confirmButtonText:'前往部署设置',cancelButtonText:'取消'});await openDeployment(app)}catch{};return}
   return toast(body.error||'删除失败，请刷新后重试','error')
  }
  applications.value=applications.value.filter(a=>a.application_id!==app.application_id);announceApplicationName(app);toast('应用已移出目录，历史记录保留','success')
 }catch{toast('删除结果未确认，请刷新列表核对，不要重复提交','error')}finally{deleting.value=''}
}
async function createApplication(){
  if(creating.value)return
  const value=name.value.trim();if(applicationNameError(value))return toast(applicationNameError(value),'warning');if(!validApplicationIdentifier(identifier.value))return toast('应用标识以小写英文开头，只能包含小写英文、数字和短横线，最长 63 字符','warning')
  creating.value=true
  try{const r=await fetch('/api/registry/applications/observe',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({name:identifier.value,display_name:value,source:'manual',create_only:true})})
  const body=await r.json().catch(()=>({}))
  if(!r.ok)return toast(body.error||'创建失败','error')
  showCreate.value=false;name.value='';identifier.value='';announceApplicationName(body);toast('应用已创建','success');load()}catch(e){toast(e.message||'创建失败','error')}finally{creating.value=false}
}
onMounted(load)

import {confirmDialog} from '../shadcnFeedback.js'


import {Button,Dialog,DialogContent,DialogDescription,DialogFooter,DialogHeader,DialogTitle,Input,Spinner} from '../shadcn-ui.js'

import {InputGroup,InputGroupInput,InputGroupAddon,InputGroupButton} from '../shadcn-ui.js'

import {XIcon} from '@lucide/vue'
</script>

<template><div class="application-center"><PageHeader title="应用中心" help="应用是日志、监控、构建和部署共享的稳定身份。数据可以先从任意入口接入，后续仍关联到同一个 application_id。"><template #actions><Button @click="showCreate=true" variant="default" size="default" type="button" :disabled="(me.role!=='admin'||!!deleting)">＋ 新建应用</Button></template></PageHeader><div class="toolbar"><InputGroup  class="min-w-0 "><InputGroupInput v-model="keyword" placeholder="搜索应用名称或标识"/><InputGroupAddon align="inline-end"><InputGroupButton v-if="keyword" :disabled="false" size="icon-xs" aria-label="清空输入" @click="keyword='' "><XIcon/></InputGroupButton></InputGroupAddon></InputGroup><Button @click="load" variant="outline" size="default" type="button" :disabled="(!!deleting)||(loading)"><Spinner v-if="loading"/>刷新</Button></div><div v-if="loading" class="empty">正在加载…</div><div v-else-if="loadError" class="empty"><b>{{loadError}}</b><Button @click="load" variant="outline" size="default" type="button">重新加载</Button></div><div v-else-if="filtered.length" class="app-grid"><article v-for="app in filtered" :key="app.application_id"><div class="app-title"><span><UiIcon name="apps" :size="22"/></span><div><b>{{applicationName(app)}}</b><code>{{applicationIdentifier(app)}}</code></div></div><div class="sources"><span v-for="source in app.sources||[]" :key="source">{{sourceLabel[source]||source}}</span><em v-if="!(app.sources||[]).length">待接入</em></div><small>{{app.status==='managed'?'已纳入平台管理':'由数据上报自动发现'}}</small><div class="application-actions"><Button v-if="app.sources?.includes('deploy')" @click="openDeployment(app)" variant="ghost" size="sm" type="button">部署设置</Button><template v-if="me.role==='admin'"><Button @click="renaming=app" variant="ghost" size="sm" type="button" :disabled="(!!deleting)">修改名称</Button><Button @click="deleteApplication(app)" class="text-destructive" :variant="'destructive'" size="sm" type="button" :disabled="(!!deleting)||(deleting===app.application_id)"><Spinner v-if="deleting===app.application_id"/>删除应用</Button></template></div></article></div><div v-else class="empty"><UiIcon name="apps" :size="32"/><b>{{keyword?'没有匹配的应用':'还没有应用'}}</b><p v-if="!keyword">可以先创建应用，也可以等待日志或 OTel 首次上报后自动发现。</p></div><ApplicationNameEditor :show="!!renaming" :service="renaming" catalog @close="renaming=null" @saved="renamed"/><Dialog :open="showCreate" @update:open="async v=>{if(!v){!creating&amp;&amp;(showCreate=false)}}"><DialogContent class="flex max-h-[92dvh] flex-col gap-0 overflow-hidden p-0 sm:max-w-[var(--editor-width)]" :class="false?'inset-0 h-dvh max-h-dvh w-screen max-w-none translate-x-0 translate-y-0 rounded-none sm:max-w-none':''" :style="{'--editor-width':typeof (&quot;min(520px, 92vw)&quot;)==='number'?(&quot;min(520px, 92vw)&quot;)+'px':(&quot;min(520px, 92vw)&quot;)}" :show-close-button="!creating" @interact-outside="e=>{if(!(!creating))e.preventDefault()}" @escape-key-down="e=>{if(!(!creating))e.preventDefault()}"><DialogHeader class="shrink-0 border-b p-5 pr-12"><DialogTitle>新建平台应用</DialogTitle><DialogDescription class="sr-only">详情与操作</DialogDescription></DialogHeader><div class="min-h-0 flex-1 overflow-auto p-5"><div class="fld"><label>应用名称</label><Input v-model="name" placeholder="支持中文，例如智能搜索服务" @keydown.enter="createApplication"/><small>显示名称支持中文，后续可修改。</small></div><div class="fld"><label>应用标识</label><Input v-model="identifier" placeholder="例如 estate-ai-seek，创建后不可修改"/><small>小写英文开头，可含数字和短横线。日志、OTel 和部署继续使用固定标识关联。</small></div></div><DialogFooter><Button @click="showCreate=false" variant="outline" size="default" type="button" :disabled="(creating)">取消</Button><Button @click="createApplication" variant="default" size="default" type="button" :disabled="(creating)"><Spinner v-if="creating"/>创建</Button></DialogFooter></DialogContent></Dialog></div></template>

<style scoped>.application-center{min-width:0}.page-head{display:flex;align-items:center;gap:12px;margin-bottom:14px}.page-head>div{flex:1}.title-row{display:flex;align-items:center;gap:7px}.page-head h2{margin:0;font-size:1.25rem}.toolbar{max-width:420px;margin-bottom:14px}.app-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(300px,1fr));gap:10px}.app-grid article{padding:14px;border:1px solid var(--border);border-radius:var(--radius);background:var(--panel)}.app-title{display:flex;gap:9px;align-items:center}.app-title>span{display:grid;place-items:center;width:34px;height:34px;border-radius:var(--radius);background:var(--panel2)}.app-title div{display:flex;flex-direction:column;min-width:0}.app-title code{margin-top:2px;color:var(--dim);font-size:.75rem;overflow:hidden;text-overflow:ellipsis}.sources{display:flex;gap:5px;margin:12px 0 7px}.sources span,.sources em{padding:2px 7px;border-radius:var(--radius);background:color-mix(in srgb,var(--brand) 12%,var(--panel2));color:var(--brand);font-size:.75rem;font-style:normal}.sources em{color:var(--dim);background:var(--panel2)}article>small{color:var(--muted-foreground)}.empty{min-height:260px;display:flex;flex-direction:column;align-items:center;justify-content:center;color:var(--muted-foreground);gap:7px}.empty>span{font-size:2rem}.empty p{margin:0}.actions{display:flex;justify-content:flex-end;gap:8px;margin-top:16px}.fld small{display:block;margin-top:5px;color:var(--muted-foreground)}</style>

<style scoped>.toolbar{display:flex;gap:8px}.application-actions{display:flex;gap:8px;flex-wrap:wrap;justify-content:flex-end;margin-top:12px;border-top:1px solid var(--border);padding-top:8px}
.page-head{margin-bottom:20px}.page-head h2{font-size:1.25rem}.app-grid{grid-template-columns:repeat(auto-fill,minmax(min(100%,300px),1fr));gap:16px}.app-grid article{padding:20px;border-radius:var(--radius);box-shadow:var(--shadow-sm)}.app-title>span{width:44px;height:44px;flex-shrink:0;border-radius:var(--radius);background:var(--brand-soft);color:var(--brand-ink)}.app-title b{font-size:1rem;overflow-wrap:anywhere}.app-title code{font-size:.75rem}.sources span,.sources em{font-size:.75rem}.empty{background:var(--panel);border:1px solid var(--border);border-radius:var(--radius)}.empty>.ui-icon{color:var(--brand-ink);margin-bottom:10px}.empty>b{font-size:1.125rem}</style>
