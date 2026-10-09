<script setup>
import { computed, inject, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { ElButton, ElInput } from '../../shared/components/ui.js';
import Sel from '../../shared/components/Sel.vue';
import HelpTip from '../../shared/components/HelpTip.vue';
const props=defineProps({me:{type:Object,default:()=>({})},canManage:{type:Function,required:true}});
const emit=defineEmits(['view','edit','delivery','version','create']);
const api=inject('api');
const query=ref(''),status=ref(''),kind=ref(''),person=ref(''),personRole=ref('related');
const items=ref([]),people=ref([]),page=ref(1),pageSize=ref(20),total=ref(0),loading=ref(false),error=ref(''),peopleError=ref('');
let sequence=0,searchTimer;
const pages=computed(()=>Math.max(1,Math.ceil(total.value/pageSize.value)));
const filtered=computed(()=>!!(query.value||status.value||kind.value||person.value));
const canUseMine=computed(()=>!!props.me.username&&props.me.username!=='local'&&props.me.auth_enabled!==false);
const mine=computed(()=>canUseMine.value&&person.value===props.me.username);
const personOptions=computed(()=>[...new Set([...people.value,...(person.value?[person.value]:[])])].map(value=>({value,label:value})));
const roles=[{value:'related',label:'相关人员'},{value:'assigned_to',label:'指派给'},{value:'resolved_by',label:'由谁解决'},{value:'opened_by',label:'由谁创建'},{value:'closed_by',label:'由谁关闭'},{value:'owner',label:'手工需求创建人'}];
const statuses=[{value:'open',label:'进行中（未关闭）'},{value:'active',label:'激活'},{value:'resolved',label:'已解决'},{value:'closed',label:'已关闭'}];
const statusLabel=item=>({active:'激活',resolved:'已解决',closed:'已关闭'}[item.external_status]||{open:'进行中',closed:'已关闭'}[item.status]||item.status);
const account=value=>value==='closed'?'已关闭':value||'—';
const date=value=>value?new Date(value*1000).toLocaleString('zh-CN',{month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit',hour12:false}):'—';
const missingPeople=computed(()=>items.value.some(v=>v.source?.startsWith('zentao:')&&![v.assigned_to,v.opened_by,v.resolved_by,v.closed_by].some(Boolean)));
async function loadPage(){
 clearTimeout(searchTimer);
 const request=++sequence;
 loading.value=true;error.value='';items.value=[];
 const params=new URLSearchParams({page:String(page.value),page_size:String(pageSize.value),q:query.value,status:status.value,kind:kind.value,person:person.value,person_role:personRole.value});
 try{
  const result=await api(`api/work-items/page?${params}`);
  if(request!==sequence)return;
  if(!Array.isArray(result?.items)||!Number.isInteger(result.total))throw new Error('需求列表响应格式错误，请刷新页面');
  items.value=result.items;total.value=result.total;page.value=result.page;
 }catch(e){if(request===sequence){error.value=e.message;total.value=0}}
 finally{if(request===sequence)loading.value=false}
}
async function loadPeople(){try{const result=await api('api/work-items/people');if(!Array.isArray(result))throw new Error('人员响应格式错误');people.value=result;peopleError.value=''}catch(e){people.value=[];peopleError.value='人员列表加载失败，请刷新重试'}}
function refresh(){loadPage();loadPeople()}
function goto(value){page.value=value;loadPage()}
function showMine(){personRole.value='related';person.value=mine.value?'':props.me.username}
function clearFilters(){query.value='';status.value='';kind.value='';person.value='';personRole.value='related'}
watch(query,()=>{page.value=1;sequence++;loading.value=true;items.value=[];clearTimeout(searchTimer);searchTimer=setTimeout(loadPage,300)});
watch([status,kind,person,personRole,pageSize],()=>{page.value=1;loadPage()});
onMounted(refresh);
onBeforeUnmount(()=>{sequence++;clearTimeout(searchTimer)});
defineExpose({refresh});
</script>
<template>
 <section class="work-list" aria-label="需求与 BUG 列表">
  <div class="work-filters">
   <ElInput v-model="query" class="work-search" placeholder="搜索标题、描述或外部编号" aria-label="搜索需求与 BUG" clearable />
   <Sel v-model="kind" :options="[{value:'requirement',label:'需求'},{value:'bug',label:'BUG'}]" all-label="全部类型" width="120px" aria-label="需求类型" />
   <Sel v-model="status" :options="statuses" all-label="全部状态" width="180px" aria-label="需求状态" />
   <div class="person-filters">
    <Sel v-model="personRole" :options="roles" width="150px" aria-label="人员关系" />
    <Sel v-model="person" :options="personOptions" filterable all-label="全部人员" width="220px" aria-label="筛选人员" />
    <HelpTip text="相关人员包含禅道的指派人、创建人、解决人、关闭人；手工需求按创建人筛选。平台同步人不算 Bug 负责人。只看我的使用当前登录账号，请与禅道账号保持一致。历史 Bug 需要重新同步才能补齐人员字段。" />
   </div>
   <ElButton :type="mine?'primary':'default'" :disabled="!canUseMine" :title="canUseMine?'按当前登录账号筛选相关记录':'登录后使用；当前可直接选择禅道账号'" @click="showMine">只看我的</ElButton>
   <ElButton v-if="filtered" text @click="clearFilters">清空筛选</ElButton>
   <ElButton :loading="loading" @click="refresh">刷新</ElButton><HelpTip v-if="missingPeople" text="部分历史 Bug 暂无人员信息，重新同步来源平台后补齐。人员筛选不会包含这些历史记录。" />
  </div>
  <p v-if="peopleError" class="list-hint" role="alert">{{peopleError}}</p>

  <div class="work-table-scroll" tabindex="0" aria-label="需求表格，可横向滚动" :aria-busy="loading">
   <table class="work-table">
    <colgroup><col class="type-col" /><col class="number-col" /><col class="title-col" /><col class="status-col" /><col class="person-col" /><col class="person-col" /><col class="date-col" /><col class="actions-col" /></colgroup>
    <thead><tr><th scope="col">类型</th><th scope="col">编号</th><th scope="col">标题</th><th scope="col">状态</th><th scope="col">指派给</th><th scope="col">由谁解决</th><th scope="col">同步 / 更新时间</th><th scope="col" class="fixed-actions">操作</th></tr></thead>
    <tbody>
     <tr v-for="item in items" :key="item.id">
      <td><span class="kind-tag" :class="item.kind">{{item.kind==='bug'?'BUG':'需求'}}</span></td>
      <td><span class="work-number" :title="item.source">{{item.external_id||`#${item.id}`}}</span><small>{{item.external_id?'禅道 / 外部':'手工'}}</small></td>
      <td><button class="work-title" :title="item.title" @click="emit('view',item)">{{item.title}}</button><small :title="item.source">{{item.source?.startsWith('zentao:')?item.source.slice(7):item.source==='manual'?`创建人：${item.owner||'—'}`:item.source}}</small></td>
      <td><span class="work-status" :class="item.external_status||item.status">{{statusLabel(item)}}</span></td>
      <td :title="item.assigned_to||'暂无人员信息'">{{account(item.assigned_to)}}</td>
      <td :title="item.resolved_by||'暂无人员信息'">{{account(item.resolved_by)}}</td>
      <td :title="item.updated_at?new Date(item.updated_at*1000).toLocaleString('zh-CN'):''">{{date(item.updated_at)}}</td>
      <td class="fixed-actions"><div class="row-actions"><button @click="emit('view',item)">查看</button><button @click="emit('delivery',item)">交付内容</button><button @click="emit('version',item)">纳入版本</button><button v-if="canManage(item)" @click="emit('edit',item)">编辑</button></div></td>
     </tr>
     <tr v-if="!items.length"><td colspan="8" class="table-empty">
      <span v-if="loading" role="status">正在加载…</span>
      <template v-else-if="error"><p role="alert">{{error}}</p><ElButton @click="refresh">重试</ElButton></template>
      <template v-else-if="filtered"><p>没有符合筛选条件的需求或 Bug</p><ElButton @click="clearFilters">清空筛选</ElButton></template>
      <template v-else><p>尚无需求与 Bug</p><ElButton @click="emit('create')">新建需求</ElButton></template>
     </td></tr>
    </tbody>
   </table>
  </div>
  <div class="work-pagination">
   <span role="status">{{loading?'查询中…':`共 ${total} 条`}}</span>
   <Sel v-model="pageSize" :options="[20,50,100].map(value=>({value,label:`${value} 条 / 页`}))" width="130px" aria-label="每页条数" />
   <div class="page-navigation" aria-label="需求列表分页">
    <ElButton size="small" :disabled="loading||page<=1" @click="goto(1)">首页</ElButton>
    <ElButton size="small" :disabled="loading||page<=1" @click="goto(page-1)">上一页</ElButton>
    <span>{{page}} / {{pages}} 页</span>
    <ElButton size="small" :disabled="loading||page>=pages" @click="goto(page+1)">下一页</ElButton>
    <ElButton size="small" :disabled="loading||page>=pages" @click="goto(pages)">末页</ElButton>
   </div>
  </div>
 </section>
</template>
<style scoped>
.work-list{min-width:0}.work-filters{display:flex;align-items:center;flex-wrap:wrap;gap:10px;padding:8px;margin-bottom:10px;border:1px solid var(--border);border-radius:12px;background:var(--panel)}
.work-search{width:280px;max-width:100%}.person-filters{display:flex;align-items:center;gap:8px;flex-wrap:wrap}.list-hint{font-size:12px;color:var(--muted);margin:0 0 10px}
.work-table-scroll{overflow:auto;max-height:calc(100vh - 230px);min-height:180px;border:1px solid var(--border);border-radius:12px;background:var(--panel)}
.work-table{width:100%;min-width:1150px;table-layout:fixed;border-collapse:separate;border-spacing:0;font-size:13px;line-height:1.4}.work-table th,.work-table td{text-align:left;padding:8px 14px;border-bottom:1px solid var(--border);overflow:hidden;text-overflow:ellipsis;white-space:nowrap;background:var(--panel)}
.work-table th{position:sticky;top:0;background:var(--panel2);color:var(--muted);font-size:12px;font-weight:600;z-index:2}.work-table tbody tr:hover td{background:var(--panel2)}.work-table tbody tr:last-child td{border-bottom:0}
.type-col{width:72px}.number-col{width:100px}.title-col{width:auto}.status-col{width:90px}.person-col{width:120px}.date-col{width:140px}.actions-col{width:230px}
.work-table small{display:block;color:var(--muted);font-size:11px;margin-top:5px;overflow:hidden;text-overflow:ellipsis}.work-title{display:block;max-width:100%;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;border:0;padding:0;background:none;color:var(--text);font-weight:600;font-size:13px;cursor:pointer;text-align:left}.work-title:hover{color:var(--brand)}
.kind-tag,.work-status{font-size:11px;border-radius:5px;padding:4px 6px;background:var(--panel2);color:var(--muted)}.kind-tag.bug{color:var(--brand);background:var(--brand-soft)}.work-status.closed{color:var(--ok)}.work-status.resolved{color:var(--brand)}.work-status.active{color:var(--warn)}
.work-table .fixed-actions{position:sticky;right:0;border-left:1px solid var(--border);z-index:1}.work-table th.fixed-actions{z-index:3}.row-actions{display:flex;gap:12px}.row-actions button{padding:3px 0;border:0;background:none;color:var(--brand);font-size:12px;cursor:pointer;white-space:nowrap}.row-actions button:hover{text-decoration:underline}
.work-table .table-empty{text-align:center;height:160px;color:var(--muted)}.table-empty p{margin:0 0 14px}.work-pagination{display:flex;align-items:center;gap:16px;flex-wrap:wrap;padding:14px 0;font-size:12px;color:var(--muted)}.page-navigation{margin-left:auto;display:flex;align-items:center;gap:10px}.work-title:focus-visible,.row-actions button:focus-visible,.work-table-scroll:focus-visible{outline:2px solid var(--brand);outline-offset:3px}
@media(max-width:700px){.work-search{width:100%}.work-filters{padding:12px}.work-table-scroll{max-height:60vh}.work-table .fixed-actions{position:static}.page-navigation{margin-left:0;flex-wrap:wrap;gap:8px}.person-filters{width:100%}}
</style>
