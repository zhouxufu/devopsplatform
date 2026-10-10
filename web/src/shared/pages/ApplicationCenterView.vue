<script setup>
import PageHeader from "../components/PageHeader.vue"
import { computed, onMounted, ref } from 'vue'
import Modal from '../components/Modal.vue'
import HelpTip from '../components/HelpTip.vue'
import UiIcon from '../components/UiIcon.vue'
import { ElButton, ElInput } from '../components/ui.js'
import { toast } from '../composables/toast.js'

const applications=ref([]),loading=ref(true),keyword=ref(''),showCreate=ref(false),name=ref('')
const sourceLabel={deploy:'部署',logs:'日志',otel:'OTel',manual:'手动'}
const filtered=computed(()=>{const q=keyword.value.trim().toLocaleLowerCase();return q?applications.value.filter(a=>a.name.toLocaleLowerCase().includes(q)):applications.value})
async function load(){loading.value=true;try{const r=await fetch('/api/registry/applications');applications.value=r.ok?(await r.json()||[]):[]}finally{loading.value=false}}
async function createApplication(){
  const value=name.value.trim();if(!value)return toast('填写应用名称','warning')
  const r=await fetch('/api/registry/applications/observe',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({name:value,source:'manual'})})
  const body=await r.json().catch(()=>({}))
  if(!r.ok)return toast(body.error||'创建失败','error')
  showCreate.value=false;name.value='';toast('应用已创建','success');load()
}
onMounted(load)
</script>

<template><div class="application-center">
<PageHeader title="应用中心" help="应用是日志、监控、构建和部署共享的稳定身份。数据可以先从任意入口接入，后续仍关联到同一个 application_id。"><template #actions><ElButton type="primary" @click="showCreate=true">＋ 新建应用</ElButton></template></PageHeader>
  <div class="toolbar"><ElInput v-model="keyword" placeholder="搜索应用名称" clearable /></div>
  <div v-if="loading" class="empty">正在加载…</div>
  <div v-else-if="filtered.length" class="app-grid"><article v-for="app in filtered" :key="app.application_id"><div class="app-title"><span><UiIcon name="apps" :size="22"/></span><div><b>{{app.name}}</b><code>{{app.application_id}}</code></div></div><div class="sources"><span v-for="source in app.sources||[]" :key="source">{{sourceLabel[source]||source}}</span><em v-if="!(app.sources||[]).length">待接入</em></div><small>{{app.status==='managed'?'已纳入平台管理':'由数据上报自动发现'}}</small></article></div>
  <div v-else class="empty"><UiIcon name="apps" :size="32"/><b>{{keyword?'没有匹配的应用':'还没有应用'}}</b><p v-if="!keyword">可以先创建应用，也可以等待日志或 OTel 首次上报后自动发现。</p></div>
  <Modal :show="showCreate" title="新建平台应用" @close="showCreate=false"><div class="fld"><label>应用名称</label><ElInput v-model="name" placeholder="例如 estate-ai-seek" @keydown.enter="createApplication" /><small>后续日志、OTel 和部署都通过这个名称关联到同一个 application_id。</small></div><div class="actions"><ElButton @click="showCreate=false">取消</ElButton><ElButton type="primary" @click="createApplication">创建</ElButton></div></Modal>
</div></template>

<style scoped>
.application-center{min-width:0}.page-head{display:flex;align-items:center;gap:12px;margin-bottom:14px}.page-head>div{flex:1}.title-row{display:flex;align-items:center;gap:7px}.page-head h2{margin:0;font-size:20px}.toolbar{max-width:420px;margin-bottom:14px}.app-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(300px,1fr));gap:10px}.app-grid article{padding:14px;border:1px solid var(--border);border-radius:9px;background:var(--panel)}.app-title{display:flex;gap:9px;align-items:center}.app-title>span{display:grid;place-items:center;width:34px;height:34px;border-radius:7px;background:var(--panel2)}.app-title div{display:flex;flex-direction:column;min-width:0}.app-title code{margin-top:2px;color:var(--dim);font-size:9px;overflow:hidden;text-overflow:ellipsis}.sources{display:flex;gap:5px;margin:12px 0 7px}.sources span,.sources em{padding:2px 7px;border-radius:10px;background:color-mix(in srgb,var(--brand) 12%,var(--panel2));color:var(--brand);font-size:10px;font-style:normal}.sources em{color:var(--dim);background:var(--panel2)}article>small{color:var(--muted)}.empty{min-height:260px;display:flex;flex-direction:column;align-items:center;justify-content:center;color:var(--muted);gap:7px}.empty>span{font-size:30px}.empty p{margin:0}.actions{display:flex;justify-content:flex-end;gap:8px;margin-top:16px}.fld small{display:block;margin-top:5px;color:var(--muted)}
</style>

<style scoped>
.page-head{margin-bottom:20px}.page-head h2{font-size:22px}.app-grid{grid-template-columns:repeat(auto-fill,minmax(min(100%,300px),1fr));gap:16px}.app-grid article{padding:20px;border-radius:12px;box-shadow:var(--shadow-sm)}.app-title>span{width:44px;height:44px;flex-shrink:0;border-radius:12px;background:var(--brand-soft);color:var(--brand-ink)}.app-title b{font-size:15px;overflow-wrap:anywhere}.app-title code{font-size:11px}.sources span,.sources em{font-size:11px}.empty{background:var(--panel);border:1px solid var(--border);border-radius:12px}.empty>.ui-icon{color:var(--brand-ink);margin-bottom:10px}.empty>b{font-size:16px}
</style>
