<script setup>
import {passwordError} from '../shared/passwordPolicy.js'
import {confirmAction,toast} from '../shared/shadcnFeedback.js'
import {PlusIcon,RefreshCwIcon,Trash2Icon} from '@lucide/vue'
import ContextHelp from '../shared/components/ContextHelp.vue'


import {computed, ref, watch} from 'vue'
import OptionPicker from '../shared/components/OptionPicker.vue'


const props = defineProps({ show: Boolean, current: { type: Object, default: () => ({}) } })
const emit = defineEmits(['close'])
const dutyOptions=[{value:'frontend',label:'前端'},{value:'backend',label:'后端'},{value:'test',label:'测试'},{value:'product',label:'产品'}]
const dutyLabel=values=>(values||[]).map(v=>dutyOptions.find(o=>o.value===v)?.label||v).join('、')||'未设置'
const saving=ref(false),reading=ref(false)
const users = ref([]), editing = ref(null), editorOpen = ref(false), form = ref({}), error = ref('')
const links = ref({}), syncFilter=ref('')
const syncOpen = ref(false), connections = ref([]), connection = ref(''), directory = ref(null), selected = ref([]), syncing = ref(false), syncError = ref(''), syncResult = ref('')

const memberQuery=ref('')
const visibleUsers=computed(()=>users.value.filter(user=>[user.username,user.display_name].some(v=>String(v||'').toLowerCase().includes(memberQuery.value.trim().toLowerCase()))))
const visibleCandidates=computed(()=>(directory.value?.users||[]).filter(u=>!syncFilter.value||[u.account,u.name,u.username].some(v=>String(v||'').toLowerCase().includes(syncFilter.value.toLowerCase()))))
function linkOptions(u){return users.value.filter(user=>!user.source&&!Object.entries(links.value).some(([id,username])=>id!==u.id&&username===user.username)).map(user=>({value:user.username,label:`${user.display_name}（${user.username}）${user.role==='admin'?' · 管理员':''}${user.enabled?'':' · 停用'}`}))}
function chooseLink(u,username){if(username){links.value[u.id]=username;if(!selected.value.includes(u.id))selected.value.push(u.id)}else{delete links.value[u.id];if(u.action==='skip')selected.value=selected.value.filter(id=>id!==u.id)}}
async function openSync() {
  if (syncing.value) return
  links.value={};syncFilter.value='';syncOpen.value = true; syncError.value = ''; syncResult.value = ''; directory.value = null; selected.value = []; connections.value=[];connection.value=''
  try { await load();connections.value = (await api('/api/resources/connections')).filter(c => c.kind === 'zentao'); connection.value = connections.value[0]?.name || '' }
  catch (e) { syncError.value = e.message }
}
function resetPreview() { links.value={};syncFilter.value='';directory.value = null; selected.value = []; syncResult.value = ''; syncError.value = '' }
async function syncUsers(apply = false) {
  if (syncing.value) return
  syncing.value = true; syncError.value = ''; syncResult.value = ''
  try {
    const data = await api(apply ? '/api/users/sync' : '/api/users/sync-preview', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ connection: connection.value, ids: apply ? selected.value : [], expected_source: apply ? directory.value?.source : '', links: apply ? Object.fromEntries(Object.entries(links.value).filter(([id])=>selected.value.includes(id))) : {} }) })
    directory.value = data
    if (apply) {
      const rows = data.users.filter(u => selected.value.includes(u.id))
      syncResult.value = `同步完成：新增 ${rows.filter(u => u.action === 'create').length}，更新 ${rows.filter(u => u.action === 'update').length}，关联 ${rows.filter(u => u.action === 'link').length}，跳过 ${rows.filter(u => u.action === 'skip').length}。新成员请编辑并设置平台密码后启用。`
      selected.value = []; links.value={};await load()
    } else { links.value={};selected.value = data.users.filter(u => u.action !== 'skip').map(u => u.id) }
  } catch (e) { syncError.value = e.message }
  finally { syncing.value = false }
}

async function api(url, options) {
  const response = await fetch(url, options)
  const body = await response.json().catch(() => ({}))
  if (!response.ok) throw new Error(body.error || '请求失败')
  return body
}
async function load() {reading.value=true;try{users.value = await api('/api/users')}finally{reading.value=false}}
function open(user) {
  editing.value = user || null
  form.value = user ? { ...user, password: '' } : { username: '', display_name: '', role: 'member', password: '', enabled: true, duties: [] }
  error.value = ''
  editorOpen.value = true
}
async function save() {
  if(saving.value)return
  error.value = passwordError(form.value.password,{creating:!editing.value})
  if(error.value)return
  saving.value=true
  try {
    const path = editing.value ? `/api/users/${encodeURIComponent(editing.value.username)}` : '/api/users'
    await api(path, { method: editing.value ? 'PUT' : 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(form.value) })
    editorOpen.value = false
    editing.value = null
    await load()
  } catch (e) { error.value = e.message }finally{saving.value=false}
}
async function remove(user) {
  if (!await confirmAction(`删除用户「${user.display_name || user.username}」？`,'删除成员')) return
  try { await api(`/api/users/${encodeURIComponent(user.username)}`, { method: 'DELETE' }); await load() } catch (e) { toast(e.message,'error') }
}
watch(() => props.show, value => {
  if (value) { memberQuery.value=''; editing.value = null; load().catch(e => { error.value = e.message }) }
  else { editorOpen.value = false; syncOpen.value = false; editing.value = null; form.value = {} }
})

import {Button,Checkbox,Dialog,DialogContent,DialogDescription,DialogFooter,DialogHeader,DialogTitle,Empty,EmptyDescription,EmptyHeader,Field,FieldLabel,Input,Spinner,Table,TableBody,TableCell,TableHead,TableHeader,TableRow} from '../shared/shadcn-ui.js'
</script>

<template>
<Dialog :open="show" @update:open="v=>{if(!v&&!saving&&!syncing)emit('close')}"><DialogContent class="flex max-h-[90dvh] flex-col sm:max-w-5xl"><DialogHeader><DialogTitle>成员管理</DialogTitle><DialogDescription class="sr-only">平台成员、职能及来源账号同步</DialogDescription></DialogHeader><div class="flex items-center gap-3"><Input v-model="memberQuery" aria-label="搜索平台成员" placeholder="搜索姓名或账号" class="max-w-sm"/><span class="mr-auto shrink-0 text-xs text-muted-foreground">{{visibleUsers.length}} / {{users.length}} 人</span><Button variant="outline" :disabled="reading" @click="openSync">从禅道同步</Button><Button @click="open(null)"><PlusIcon class="size-4"/>添加成员</Button></div><div class="min-h-0 flex-1 overflow-hidden"><Table class="table-fixed" container-class="overflow-auto" :container-style="{maxHeight:'calc(90dvh - 160px)'}" :container-tabindex="0" container-label="平台成员列表"><TableHeader class="sticky top-0 z-10 bg-background"><TableRow><TableHead class="w-[40%]">用户</TableHead><TableHead class="w-[12%]">角色</TableHead><TableHead class="w-[20%]">职能</TableHead><TableHead class="w-[12%]">状态</TableHead><TableHead class="w-[16%] text-right">操作</TableHead></TableRow></TableHeader><TableBody><TableRow v-for="user in visibleUsers" :key="user.username"><TableCell><span class="font-medium">{{user.display_name||user.username}}</span><span class="block text-xs text-muted-foreground">{{user.username}}{{user.source?' · 来源平台':''}}</span></TableCell><TableCell>{{user.role==='admin'?'管理员':'成员'}}</TableCell><TableCell>{{dutyLabel(user.duties)}}</TableCell><TableCell>{{user.source&&!user.password_configured?'待设置密码':user.enabled?'启用':'停用'}}</TableCell><TableCell class="text-right"><Button variant="ghost" size="sm" @click="open(user)">编辑</Button><Button variant="ghost" size="sm" class="text-destructive" :disabled="user.username===current.username" @click="remove(user)">删除</Button></TableCell></TableRow></TableBody></Table><Empty v-if="!visibleUsers.length"><EmptyHeader><EmptyDescription>{{reading?'正在读取成员':memberQuery?'没有匹配的成员，可修改搜索条件':'暂无成员'}}</EmptyDescription></EmptyHeader></Empty></div><p v-if="error&&!editorOpen" role="alert" class="text-sm text-destructive">{{error}}</p></DialogContent></Dialog>
<Dialog :open="editorOpen" @update:open="v=>{if(!v&&!saving){editorOpen=false;form={};editing=null}}"><DialogContent class="sm:max-w-lg" :show-close-button="!saving" @escape-key-down="e=>{if(saving)e.preventDefault()}" @interact-outside="e=>{if(saving)e.preventDefault()}"><DialogHeader><DialogTitle>{{editing?'编辑成员':'添加成员'}}</DialogTitle><DialogDescription class="sr-only">编辑平台成员资料和登录设置</DialogDescription></DialogHeader><form class="space-y-4" @submit.prevent="save"><div class="grid items-start gap-4 sm:grid-cols-2"><Field><FieldLabel for="member-username">用户名</FieldLabel><Input id="member-username" v-model="form.username" :disabled="!!editing||saving" required/></Field><Field><FieldLabel for="member-name">姓名</FieldLabel><Input id="member-name" v-model="form.display_name" :disabled="saving"/></Field><Field><FieldLabel for="member-role">角色</FieldLabel><OptionPicker id="member-role" v-model="form.role" :disabled="saving||editing?.username===current.username" :options="[{value:'member',label:'成员'},{value:'admin',label:'管理员'}]"/></Field><Field><div class="flex items-center gap-1"><FieldLabel for="member-duties">职能</FieldLabel><ContextHelp text="可多选，仅标识前端、后端、测试、产品职能，不改变权限。"/></div><OptionPicker id="member-duties" v-model="form.duties" multiple :options="dutyOptions" :disabled="saving"/></Field></div><Field><FieldLabel for="member-password">{{editing?'新密码（留空不修改）':'初始密码'}}</FieldLabel><Input id="member-password" v-model="form.password" type="password" autocomplete="new-password" :disabled="saving" :required="!editing" placeholder="至少 6 位"/></Field><Field orientation="horizontal"><Checkbox id="member-enabled" v-model="form.enabled" :disabled="saving||editing?.username===current.username"/><FieldLabel for="member-enabled">启用账号</FieldLabel></Field><p v-if="error" role="alert" class="text-sm text-destructive">{{error}}</p><DialogFooter><Button variant="outline" type="button" :disabled="saving" @click="editorOpen=false;form={};editing=null">取消</Button><Button type="submit" :disabled="saving"><Spinner v-if="saving"/>保存</Button></DialogFooter></form></DialogContent></Dialog>
<Dialog :open="syncOpen" @update:open="v=>{if(!v&&!syncing)syncOpen=false}"><DialogContent class="flex max-h-[90dvh] flex-col sm:max-w-5xl" :show-close-button="!syncing" @escape-key-down="e=>{if(syncing)e.preventDefault()}" @interact-outside="e=>{if(syncing)e.preventDefault()}"><DialogHeader><DialogTitle class="flex items-center gap-1">从禅道同步成员<ContextHelp text="仅同步账号与姓名，不同步密码或禅道角色；新成员默认停用。关联仅建立身份映射，本次保留平台资料。已有关联不可在这里改绑；不会自动删除平台成员。"/></DialogTitle><DialogDescription class="sr-only">读取来源成员并预览，在确认同步所选后保存</DialogDescription></DialogHeader><div class="flex items-center gap-2"><div class="min-w-0 flex-1"><OptionPicker v-model="connection" :options="connections.map(c=>({value:c.name,label:c.name}))" :disabled="syncing" @change="resetPreview"/></div><Button variant="outline" :disabled="!connection||syncing" @click="syncUsers(false)"><Spinner v-if="syncing"/>读取用户</Button></div><p v-if="!connections.length" class="text-sm text-muted-foreground">尚未配置禅道连接，请先在资源中心添加。</p><Input v-if="directory" v-model="syncFilter" aria-label="筛选同步用户" placeholder="筛选来源账号、姓名或关联用户"/>
<div v-if="directory" class="min-h-0 overflow-auto"><Table class="table-fixed"><TableHeader><TableRow><TableHead>选择</TableHead><TableHead>来源账号</TableHead><TableHead>姓名</TableHead><TableHead>关联平台用户</TableHead><TableHead>处理方式</TableHead></TableRow></TableHeader><TableBody><TableRow v-for="user in visibleCandidates" :key="user.id"><TableCell><Checkbox :model-value="selected.includes(user.id)" :disabled="syncing||(user.action==='skip'&&!links[user.id])" :aria-label="'选择 '+user.account" @update:model-value="v=>{selected=v?[...new Set([...selected,user.id])]:selected.filter(id=>id!==user.id)}"/></TableCell><TableCell>{{user.account}}</TableCell><TableCell>{{user.name||user.account}}</TableCell><TableCell><span v-if="user.bound">{{user.username}}</span><OptionPicker v-else :model-value="links[user.id]||''" :options="linkOptions(user)" all-label="按原规则（不关联）" :aria-label="'关联平台用户 '+user.account" :disabled="syncing" @change="chooseLink(user,$event)"/></TableCell><TableCell>{{links[user.id]||user.action==='link'?'关联已有用户':user.action==='create'?'新增成员':user.action==='update'?'更新姓名':'跳过'}}<small class="block text-xs text-muted-foreground">{{links[user.id]?'仅建立关联，保留平台设置':user.reason}}</small></TableCell></TableRow></TableBody></Table></div><p v-if="syncError" role="alert" class="text-sm text-destructive">{{syncError}}</p><p v-if="syncResult" role="status" class="text-sm">{{syncResult}}</p><DialogFooter><Button variant="outline" :disabled="syncing" @click="syncOpen=false">关闭</Button><Button :disabled="syncing||!selected.length" @click="syncUsers(true)"><Spinner v-if="syncing"/>同步所选（{{selected.length}}）</Button></DialogFooter></DialogContent></Dialog>
</template>
