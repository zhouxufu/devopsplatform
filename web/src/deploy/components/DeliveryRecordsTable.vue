<script setup>
import { computed, inject, nextTick, onBeforeUnmount, ref, watch } from 'vue';
import { ElButton, ElInput, ElDropdown, ElDropdownMenu, ElDropdownItem } from '../../shared/components/ui.js';
import Sel from '../../shared/components/Sel.vue';
const props=defineProps({type:{type:String,required:true},active:Boolean,me:{type:Object,default:()=>({})},services:{type:Array,default:()=>[]},canManage:{type:Function,required:true}});
const emit=defineEmits(['view','edit','version','action','preview','deliver','create','work']);
const api=inject('api'),query=ref(''),status=ref(''),kind=ref(''),service=ref(''),owner=ref(''),association=ref('');
const items=ref([]),owners=ref([]),page=ref(1),pageSize=ref(20),total=ref(0),loading=ref(false),error=ref(''),ownersError=ref(''),selected=ref([]),scroll=ref(null);
let sequence=0,timer,loaded=false,position=null;
const isVersion=computed(()=>props.type==='versions');
const endpoint=computed(()=>isVersion.value?'release-versions':'changes');
const pages=computed(()=>Math.max(1,Math.ceil(total.value/pageSize.value)));
const filtered=computed(()=>!!(query.value||status.value||kind.value||service.value||owner.value||association.value));
const mineAvailable=computed(()=>!!props.me.username&&props.me.username!=='local'&&props.me.auth_enabled!==false);
const mine=computed(()=>mineAvailable.value&&owner.value===props.me.username);
const ownerOptions=computed(()=>[...new Set([...owners.value,...(owner.value?[owner.value]:[])])].map(value=>({value,label:value})));
const statusOptions=computed(()=>isVersion.value?[{value:'draft',label:'草稿'},{value:'ready',label:'已冻结'},{value:'completed',label:'已完成'},{value:'canceled',label:'已取消'}]:[{value:'draft',label:'草稿'},{value:'ready',label:'已就绪'}]);
const kindOptions=[{value:'code',label:'代码分支'},{value:'artifact',label:'应用制品'},{value:'mysql',label:'SQL / 数据同步'},{value:'nacos',label:'Nacos 配置'},{value:'redis',label:'Redis 命令'},{value:'manual',label:'人工操作'}];
const kindLabel=v=>v.data_sync?'数据同步':v.kind==='mysql'?'SQL 变更':kindOptions.find(k=>k.value===v.kind)?.label||v.kind;
const statusLabel=v=>statusOptions.value.find(s=>s.value===v.status)?.label||v.status;
const date=value=>value?new Date(value*1000).toLocaleString('zh-CN',{month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit',hour12:false}):'—';
const allSelected=computed(()=>!!items.value.length&&selected.value.length===items.value.length);
const canEdit=item=>props.canManage(item)&&item.status==='draft'&&!item.frozen&&!item.data_sync;
function selectRow(item,checked){selected.value=checked?[...new Set([...selected.value,item.id])]:selected.value.filter(id=>id!==item.id)}
function selectPage(checked){selected.value=checked?items.value.map(v=>v.id):[]}
function clearSelection(){selected.value=[]}
async function loadPage(){
 clearTimeout(timer);loaded=true;const request=++sequence;loading.value=true;error.value='';items.value=[];clearSelection();
 const params=new URLSearchParams({page:String(page.value),page_size:String(pageSize.value),q:query.value,status:status.value,kind:kind.value,service:service.value,owner:owner.value,association:association.value});
 try{const result=await api(`api/${endpoint.value}/page?${params}`);if(request!==sequence)return;
  if(!Array.isArray(result?.items)||!Number.isInteger(result.total)||!Number.isInteger(result.page))throw new Error('列表响应格式错误，请刷新重试');
  items.value=result.items;total.value=result.total;page.value=result.page;
 }catch(e){if(request===sequence){error.value=e.message;total.value=0}}
 finally{if(request===sequence)loading.value=false}
}
async function loadOwners(){try{const result=await api(`api/${endpoint.value}/owners`);if(!Array.isArray(result))throw new Error();owners.value=result;ownersError.value=''}catch{ownersError.value='创建人列表加载失败，可刷新重试'}}
function refresh(){if(!loaded&&!props.active)return;loadPage();loadOwners()}
function goto(value){page.value=value;loadPage();if(scroll.value)scroll.value.scrollTop=0}
function clearFilters(){query.value='';status.value='';kind.value='';service.value='';owner.value='';association.value=''}
function rememberPosition(){position={top:scroll.value?.scrollTop||0,left:scroll.value?.scrollLeft||0,outer:scroll.value?.closest('main')?.scrollTop||0}}
async function restorePosition(){if(!position||!props.active)return;await nextTick();if(scroll.value){scroll.value.scrollTop=position.top;scroll.value.scrollLeft=position.left;const main=scroll.value.closest('main');if(main)main.scrollTop=position.outer}}
function command(op,item){if(op==='version')emit('version',item);else if(op==='preview')emit('preview',item);else emit('action',{item,op})}
watch(query,()=>{if(!loaded)return;page.value=1;sequence++;items.value=[];clearSelection();loading.value=true;clearTimeout(timer);timer=setTimeout(loadPage,300)});
watch([status,kind,service,owner,association,pageSize],()=>{if(!loaded)return;page.value=1;loadPage()});
watch(()=>props.active,active=>{if(active&&!loaded){loadPage();loadOwners()}else if(active)restorePosition()},{immediate:true});
onBeforeUnmount(()=>{sequence++;clearTimeout(timer)});
defineExpose({refresh,rememberPosition,restorePosition,clearSelection});
</script>
<template>
 <section class="delivery-list" :aria-label="isVersion?'发布版本列表':'变更列表'">
  <div class="filters">
   <ElInput v-model="query" class="search" :placeholder="isVersion?'搜索版本号、标题或说明':'搜索标题、应用或引用'" :aria-label="isVersion?'搜索发布版本':'搜索变更'" clearable />
   <Sel v-if="!isVersion" v-model="kind" :options="kindOptions" all-label="全部类型" width="160px" aria-label="变更类型" />
   <Sel v-model="status" :options="statusOptions" all-label="全部状态" width="130px" aria-label="记录状态" />
   <Sel v-model="service" :options="services.map(v=>({value:v.name,label:v.display_name||v.name}))" filterable all-label="全部应用" width="170px" aria-label="筛选应用" />
   <Sel v-model="owner" :options="ownerOptions" filterable all-label="全部创建人" width="150px" aria-label="筛选创建人" />
   <Sel v-if="!isVersion" v-model="association" :options="[{value:'linked',label:'关联需求 / BUG'},{value:'independent',label:'独立变更'}]" all-label="全部关联" width="160px" aria-label="关联关系" />
   <ElButton :type="mine?'primary':'default'" :disabled="!mineAvailable" :title="mineAvailable?'按当前账号筛选创建人':'登录后使用；也可直接选择创建人'" @click="owner=mine?'':me.username">只看我的</ElButton>
   <ElButton v-if="filtered" text @click="clearFilters">清空筛选</ElButton><ElButton :loading="loading" @click="refresh">刷新</ElButton>
  </div>
  <p v-if="ownersError" class="hint" role="alert">{{ownersError}}</p>
  <div v-if="selected.length&&!isVersion" class="selection" role="status">已选择本页 {{selected.length}} 项<ElButton size="small" type="primary" @click="emit('version',items.filter(v=>selected.includes(v.id)))">批量纳入版本</ElButton><ElButton size="small" text @click="clearSelection">取消选择</ElButton></div>
  <div ref="scroll" class="table-scroll" tabindex="0" :aria-label="isVersion?'版本表格，可滚动':'变更表格，可滚动'" :aria-busy="loading">
   <table :class="['records-table',{versions:isVersion}]">
    <colgroup><col v-if="!isVersion" class="select-col" /><col class="title-col" /><col v-if="!isVersion" class="kind-col" /><col :class="isVersion?'scope-col':'app-col'" /><col v-if="!isVersion" class="linked-col" /><col class="status-col" /><col class="owner-col" /><col class="date-col" /><col class="actions-col" /></colgroup>
    <thead><tr><th v-if="!isVersion" scope="col"><input type="checkbox" :checked="allSelected" :indeterminate="selected.length>0&&!allSelected" :disabled="loading||!items.length" aria-label="选择本页全部变更" @change="selectPage($event.target.checked)" /></th><th scope="col">{{isVersion?'版本 / 名称':'变更标题'}}</th><th v-if="!isVersion" scope="col">类型</th><th scope="col">{{isVersion?'交付范围':'应用'}}</th><th v-if="!isVersion" scope="col">关联需求 / BUG</th><th scope="col">{{isVersion?'版本状态':'状态'}}</th><th scope="col">创建人</th><th scope="col">更新时间</th><th scope="col" class="fixed-actions">操作</th></tr></thead>
    <tbody><tr v-for="item in items" :key="item.id">
     <td v-if="!isVersion"><input type="checkbox" :checked="selected.includes(item.id)" :aria-label="`选择变更 ${item.title}`" @change="selectRow(item,$event.target.checked)" /></td>
     <td><button class="title-link" :title="isVersion?`${item.version} · ${item.title}`:item.title" @click="emit('view',item)">{{isVersion?item.version:item.title}}</button><small :title="isVersion?item.title:''">{{isVersion?item.title:`#${item.id}`}}</small></td>
     <td v-if="!isVersion"><span class="tag">{{kindLabel(item)}}</span></td>
     <td v-if="isVersion" :title="'应用数包含代码、配置等所有关联应用；交付结果按环境查看'">{{item.application_count}} 应用 · {{item.work_item_count}} 工作项 · {{item.change_count}} 变更</td><td v-else :title="item.service">{{item.service||'—'}}</td>
     <td v-if="!isVersion"><template v-if="item.work_items?.length"><button class="related-link" :title="item.work_items.map(w=>w.title).join('、')" @click="emit('work',item.work_items[0])">{{item.work_items[0].external_id||`#${item.work_items[0].id}`}} · {{item.work_items[0].title}}</button><small v-if="item.work_items.length>1">另 {{item.work_items.length-1}} 项 · 详情查看全部</small></template><span v-else class="muted">独立变更</span></td>
     <td><span class="status" :class="item.status">{{statusLabel(item)}}</span><small v-if="item.frozen">随版本冻结</small></td><td :title="item.owner">{{item.owner||'—'}}</td><td :title="item.updated_at?new Date(item.updated_at*1000).toLocaleString('zh-CN'):''">{{date(item.updated_at)}}</td>
     <td class="fixed-actions"><div class="row-actions"><button @click="emit('view',item)">查看</button><button v-if="canEdit(item)" @click="emit('edit',item)">{{isVersion?'编辑范围':'编辑'}}</button><button v-else-if="isVersion&&['ready','completed'].includes(item.status)" @click="emit('deliver',item)">发起交付</button>
      <ElDropdown trigger="click" @command="command($event,item)"><button class="more" :aria-label="`${item.title}的更多操作`">更多 ▾</button><template #dropdown><ElDropdownMenu>
       <ElDropdownItem v-if="!isVersion" command="version">纳入版本</ElDropdownItem>
       <ElDropdownItem v-if="!isVersion&&['mysql','nacos','redis'].includes(item.kind)" command="preview">{{item.kind==='mysql'?'SQL 预检':item.kind==='nacos'?'Nacos 差异预览':'Redis 命令预检'}}</ElDropdownItem>
       <ElDropdownItem v-if="canManage(item)&&item.status==='draft'&&!item.frozen" command="ready">{{isVersion?'冻结并标记就绪':'标记就绪'}}</ElDropdownItem>
       <ElDropdownItem v-if="!isVersion&&canManage(item)&&item.status==='ready'&&!item.frozen" command="reopen">退回草稿</ElDropdownItem>
       <ElDropdownItem v-if="canManage(item)&&item.status==='draft'&&!item.frozen" command="delete">删除草稿</ElDropdownItem>
       <ElDropdownItem v-if="isVersion&&(!canManage(item)||item.status!=='draft')" disabled>其他操作见版本详情</ElDropdownItem>
      </ElDropdownMenu></template></ElDropdown>
     </div></td>
    </tr><tr v-if="!items.length"><td :colspan="isVersion?6:9" class="empty-row"><span v-if="loading" role="status">正在加载…</span><template v-else-if="error"><p role="alert">{{error}}</p><ElButton @click="refresh">重试</ElButton></template><template v-else-if="filtered"><p>没有符合筛选条件的记录</p><ElButton @click="clearFilters">清空筛选</ElButton></template><template v-else><p>尚无{{isVersion?'发布版本':'变更'}}</p><ElButton @click="emit('create')">{{isVersion?'创建版本草稿':'新建变更'}}</ElButton></template></td></tr></tbody>
   </table>
  </div>
  <div class="pagination"><span role="status">{{loading?'查询中…':`共 ${total} 条`}}</span><Sel v-model="pageSize" :options="[20,50,100].map(value=>({value,label:`${value} 条 / 页`}))" width="130px" aria-label="每页条数" /><div class="page-navigation" aria-label="列表分页"><ElButton size="small" :disabled="loading||page<=1" @click="goto(1)">首页</ElButton><ElButton size="small" :disabled="loading||page<=1" @click="goto(page-1)">上一页</ElButton><span>{{page}} / {{pages}} 页</span><ElButton size="small" :disabled="loading||page>=pages" @click="goto(page+1)">下一页</ElButton><ElButton size="small" :disabled="loading||page>=pages" @click="goto(pages)">末页</ElButton></div></div>
 </section>
</template>
<style scoped>
.delivery-list{min-width:0}.filters{display:flex;align-items:center;flex-wrap:wrap;gap:8px;padding:8px;margin-bottom:10px;border:1px solid var(--border);border-radius:12px;background:var(--panel)}.search{width:260px;max-width:100%}.hint{font-size:12px;color:var(--muted);margin:0 0 8px}.selection{display:flex;gap:12px;align-items:center;margin:0 0 10px;padding:8px 12px;background:var(--brand-soft);border-radius:8px;font-size:12px}
.table-scroll{overflow:auto;max-height:calc(100vh - 230px);min-height:180px;border:1px solid var(--border);border-radius:12px;background:var(--panel)}.records-table{width:100%;min-width:1280px;table-layout:fixed;border-collapse:separate;border-spacing:0;font-size:13px;line-height:1.4}.records-table.versions{min-width:950px}.records-table th,.records-table td{text-align:left;padding:9px 12px;border-bottom:1px solid var(--border);overflow:hidden;text-overflow:ellipsis;white-space:nowrap;background:var(--panel)}.records-table th{position:sticky;top:0;background:var(--panel2);color:var(--muted);font-size:12px;font-weight:600;z-index:2}.records-table tbody tr:hover td{background:var(--panel2)}.records-table tbody tr:last-child td{border-bottom:0}
.select-col{width:40px}.title-col{width:auto}.kind-col{width:125px}.app-col{width:145px}.scope-col{width:230px}.linked-col{width:220px}.status-col{width:105px}.owner-col{width:100px}.date-col{width:145px}.actions-col{width:175px}.records-table small{display:block;color:var(--muted);font-size:11px;margin-top:4px;overflow:hidden;text-overflow:ellipsis}.title-link,.related-link{display:block;max-width:100%;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;border:0;padding:0;background:none;color:var(--text);font-size:13px;cursor:pointer;text-align:left}.title-link{font-weight:600}.title-link:hover,.related-link:hover{color:var(--brand)}.tag,.status{font-size:11px;border-radius:5px;padding:3px 6px;background:var(--panel2);color:var(--muted)}.status.ready{color:var(--brand)}.status.completed{color:var(--ok)}.muted{color:var(--muted)}
.records-table .fixed-actions{position:sticky;right:0;border-left:1px solid var(--border);z-index:1}.records-table th.fixed-actions{z-index:3}.row-actions{display:flex;align-items:center;gap:12px}.row-actions button{border:0;padding:4px 0;background:none;color:var(--brand);font-size:12px;cursor:pointer;white-space:nowrap}.row-actions button:hover{text-decoration:underline}.row-actions :deep(.el-dropdown){line-height:1.4}.records-table .empty-row{text-align:center;height:160px;color:var(--muted)}.empty-row p{margin:0 0 14px}.pagination{display:flex;align-items:center;gap:16px;flex-wrap:wrap;padding:14px 0;font-size:12px;color:var(--muted)}.page-navigation{margin-left:auto;display:flex;align-items:center;gap:10px}.title-link:focus-visible,.related-link:focus-visible,.row-actions button:focus-visible,.table-scroll:focus-visible,input:focus-visible{outline:2px solid var(--brand);outline-offset:2px}input[type=checkbox]{accent-color:var(--brand);width:15px;height:15px;cursor:pointer}
@media(max-width:700px){.search{width:100%}.table-scroll{max-height:60vh}.records-table .fixed-actions{position:static}.page-navigation{margin-left:0;flex-wrap:wrap;gap:8px}.selection{flex-wrap:wrap}}
</style>
