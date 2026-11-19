<script setup>
import {computed,onBeforeUnmount,onMounted,ref,watch} from 'vue'
import {Badge,Button,Field,FieldLabel,Input,Spinner} from '../shared/shadcn-ui.js'
import OptionPicker from '../shared/components/OptionPicker.vue'
import {applicationOption} from '../shared/applicationName.js'
const props=defineProps({service:{type:String,default:''},initialService:{type:String,default:''},services:{type:Array,default:()=>[]},opening:Boolean})
const emit=defineEmits(['choose','close'])
const search=ref(''),application=ref(props.service||props.initialService),environment=ref('__all'),environments=ref([]),rows=ref([]),total=ref(0),page=ref(1),loading=ref(false),error=ref('')
const applications=computed(()=>props.services.map(applicationOption))
let controller,timer,epoch=0,destroyed=false
async function load(){
 clearTimeout(timer);controller?.abort();controller=new AbortController();const token=++epoch;loading.value=true;error.value='';rows.value=[]
 const q=new URLSearchParams({q:search.value.trim(),page:String(page.value),page_size:'10'})
 if(props.service||application.value)q.set('service',props.service||application.value)
 if(environment.value!=='__all')q.set('environment',environment.value==='__unassigned'?'':environment.value)
 try{const r=await fetch('api/api-documents/search?'+q,{signal:controller.signal}),v=await r.json();if(token!==epoch)return;if(!r.ok||v.error)throw Error(v.error||'接口检索失败');rows.value=v.items||[];total.value=v.total||0}
 catch(e){if(token===epoch&&e.name!=='AbortError'){error.value=e.message;total.value=0}}
 finally{if(token===epoch)loading.value=false}
}
watch([search,application,environment],()=>{controller?.abort();epoch++;rows.value=[];page.value=1;loading.value=true;clearTimeout(timer);timer=setTimeout(load,250)})
function turn(offset){page.value+=offset;load()}
onMounted(async()=>{load();try{const r=await fetch('api/environments'),v=await r.json();if(!destroyed&&r.ok)environments.value=v}catch{}})
onBeforeUnmount(()=>{destroyed=true;epoch++;controller?.abort();clearTimeout(timer)})
</script>
<template>
 <section class="space-y-4" aria-label="选择文档接口">
  <div class="flex items-center justify-between gap-2"><h3 class="font-medium">从接口文档选择接口</h3><Button variant="ghost" size="sm" :disabled="opening" @click="emit('close')">返回配置</Button></div>
  <Field><FieldLabel>搜索接口</FieldLabel><Input v-model="search" :disabled="opening" placeholder="功能、路径、方法或分组" aria-label="选择文档接口搜索" @keydown.enter="load"/></Field>
  <div class="grid min-w-0 gap-3"><Field v-if="!service"><FieldLabel>调用应用</FieldLabel><OptionPicker v-model="application" :options="applications" all-label="全部应用" aria-label="文档接口应用筛选" :disabled="opening"/></Field><Field><FieldLabel>文档环境</FieldLabel><OptionPicker v-model="environment" :options="[{value:'__all',label:'全部环境'},{value:'__unassigned',label:'未区分环境（历史）'},...environments.map(e=>({value:e.name,label:e.display_name||e.name}))]" aria-label="文档接口环境筛选" :disabled="opening"/></Field></div>
  <div class="flex items-center justify-between gap-2 text-xs text-muted-foreground"><span>共 {{total}} 个接口 · 各环境最新文档</span><Button size="sm" variant="outline" :disabled="loading||opening" @click="load">刷新</Button></div>
  <div v-if="loading||opening" class="flex items-center gap-2 py-2 text-sm" role="status"><Spinner/>{{opening?'正在读取接口定义':'正在检索接口'}}</div>
  <div v-if="error" role="alert" class="space-y-2 rounded-lg border p-3 text-sm"><p>{{error}}</p><Button size="sm" variant="outline" @click="load">重试</Button></div>
  <div v-else class="space-y-2"><Button v-for="row in rows" :key="row.snapshot_id+':'+row.id" variant="outline" class="h-auto w-full min-w-0 flex-col items-start gap-2 whitespace-normal p-3 text-left" :disabled="opening||loading" :aria-label="'选择接口 '+row.service+' '+row.method+' '+row.path+' 快照 '+row.snapshot_id" @click="emit('choose',row)"><span class="flex min-w-0 items-start gap-2"><Badge variant="secondary">{{row.method}}</Badge><span class="break-words font-medium">{{row.summary||row.path}}</span></span><code class="max-w-full break-all text-xs text-muted-foreground">{{row.path}}</code><span class="max-w-full break-words text-xs text-muted-foreground">{{row.display_name||row.service}} · {{row.environment||'未区分环境'}} · 快照 #{{row.snapshot_id}}</span></Button><p v-if="!loading&&!rows.length" class="py-6 text-center text-sm text-muted-foreground">没有匹配接口，请调整筛选或先导入接口文档</p></div>
  <div class="flex items-center justify-end gap-2"><Button variant="outline" size="sm" :disabled="loading||opening||page===1" @click="turn(-1)">上一页</Button><span class="text-sm">{{page}} / {{Math.max(1,Math.ceil(total/10))}}</span><Button variant="outline" size="sm" :disabled="loading||opening||page*10>=total" @click="turn(1)">下一页</Button></div>
 </section>
</template>
