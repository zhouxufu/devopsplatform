<script setup>
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { ElSelect, ElOption, ElButton } from 'element-plus'
// repositories: [{service}] for saved applications; git_url/connection may
// explicitly override them while editing a draft (including empty connection).
const props=defineProps({modelValue:{type:String,default:''},repositories:{type:Array,default:()=>[]},disabled:Boolean,placeholder:{type:String,default:'筛选或输入分支；留空使用默认分支'}})
const emit=defineEmits(['update:modelValue'])
const branches=ref([]),defaults=ref([]),loading=ref(false),attempted=ref(false),error=ref(''),synced=ref(false)
const scope=computed(()=>JSON.stringify(props.repositories))
const selector=ref(null)
let revision=0,controller
function invalidate(){revision++;controller?.abort();loading.value=false;branches.value=[];defaults.value=[];attempted.value=false;synced.value=false;error.value=''}
watch(scope,invalidate)
onBeforeUnmount(invalidate)
const options=computed(()=>props.modelValue&&!branches.value.includes(props.modelValue)?[props.modelValue,...branches.value]:branches.value)
const ready=computed(()=>props.repositories.length>0&&props.repositories.every(r=>r.git_url?.trim()||r.service))
async function sync(){
 if(props.disabled||loading.value||!ready.value)return
 const current=++revision;controller?.abort();controller=new AbortController();const signal=controller.signal
 loading.value=true;attempted.value=true;error.value='';synced.value=false
 try{
  if(props.repositories.length>20)throw new Error('一次最多核对 20 个应用，请拆分代码内容')
  const result=[]
  // Limit discovery concurrency; check every credential/repository instead of
  // silently checking only the first application in a multi-service change.
  for(let i=0;i<props.repositories.length;i+=3){
   result.push(...await Promise.all(props.repositories.slice(i,i+3).map(async r=>{
    const response=await fetch('api/git/branches',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(r),signal})
    const data=await response.json().catch(()=>({}))
    if(!response.ok)throw new Error(`${r.service?`${r.service}：`:''}${data.error||'分支同步失败'}`)
    if(!Array.isArray(data.branches))throw new Error('分支同步响应格式错误')
    return data
   })))
  }
  if(current!==revision)return
  const lists=result.map(r=>new Set(r.branches.map(b=>b.name)))
  branches.value=[...lists[0]].filter(name=>lists.every(list=>list.has(name))).sort()
  defaults.value=[...new Set(result.map(r=>r.default_branch).filter(Boolean))]
  synced.value=true
 }catch(e){if(current===revision&&e.name!=='AbortError')error.value=e.message}
 finally{if(current===revision)loading.value=false}
}
function opened(value){if(value&&!attempted.value)sync()}
// Element Plus can leave a newly created option without a keyboard highlight
// when the remote list is empty or just being replaced. Confirm a typed new
// branch explicitly, without overriding a different option chosen by arrows.
function confirmManual(event){
 if(props.disabled||event.isComposing||event.target?.getAttribute('role')!=='combobox')return
 const query=(event.target.value||'').trim()
 if(!query||branches.value.includes(query))return
 const active=document.getElementById(event.target.getAttribute('aria-activedescendant')||'')
 if(active?.textContent?.trim()&&active.textContent.trim()!==query)return
 event.preventDefault();event.stopPropagation();emit('update:modelValue',query);selector.value?.blur()
}
</script>
<template>
 <div class="branch-field" @keydown.enter.capture="confirmManual">
  <div class="branch-controls">
   <ElSelect ref="selector" :model-value="modelValue||''" :disabled="disabled" filterable allow-create default-first-option clearable :placeholder="placeholder" @update:model-value="emit('update:modelValue',$event||'')" @visible-change="opened">
    <ElOption v-for="branch in options" :key="branch" :value="branch" :label="branch" />
   </ElSelect>
   <ElButton :disabled="disabled||!ready" :loading="loading" @click="sync">同步分支</ElButton>
  </div>
  <small v-if="loading">正在同步远程分支…仍可手填分支后按回车确认。</small>
  <small v-else-if="error" class="branch-error" role="alert">{{error}}；当前输入已保留，可修改凭据后重试。</small>
  <small v-else-if="!ready">{{repositories.length?'请先填写代码仓库':'先选择关联应用，再同步分支'}}；也可手填后按回车确认。</small>
  <small v-else-if="synced">已同步 {{branches.length}} 个{{repositories.length>1?'共同':''}}分支<template v-if="defaults.length"> · 默认：{{defaults.join(' / ')}}</template><template v-if="modelValue&&!branches.includes(modelValue)"> · 当前分支不在同步列表，请核对</template>。手填后按回车确认。</small>
  <small v-else>读取远程分支，不拉取代码；手填后按回车确认。同步由平台执行。</small>
 </div>
</template>
<style scoped>
.branch-field{display:grid;gap:6px;min-width:0}.branch-controls{display:flex;gap:8px;min-width:0}.branch-controls>.el-select{flex:1;min-width:0}.branch-controls>.el-button{flex-shrink:0}.branch-field small{color:var(--muted);font-size:12px;line-height:1.5;overflow-wrap:anywhere}.branch-field .branch-error{color:var(--red,#e55)}
</style>
