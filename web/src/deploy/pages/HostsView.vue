<script setup>
import DataTable from "../../shared/components/DataTable.vue"

import {useTableLayout} from '../../shared/composables/tableLayout.js'
const {pinTableActions}=useTableLayout()



import {ref, onMounted} from 'vue'


import {toast} from '../../shared/composables/toast.js'

const hosts = ref([]), environments = ref([]), testState = ref({})
const show = ref(false), editing = ref(null)
const form = ref({})

async function load() { [hosts.value, environments.value] = await Promise.all([fetch('api/hosts').then(r => r.json()), fetch('api/environments').then(r => r.json())]) }
onMounted(load)

function open(h) {
  editing.value = h || null
  form.value = h ? { ...h, password: '' } : { name: '', environment: environments.value[0]?.name || '', addr: '', user: 'root', key_path: '', password: '', host_key: '' }
  show.value = true
}
async function save() {
  if (!form.value.name || !form.value.environment || !form.value.addr) return toast('名称、环境和地址必填')
  const r = await fetch('api/hosts', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(form.value) })
  if (r.ok) { show.value = false; load() } else toast((await r.json().catch(()=>({}))).error || '保存失败', 'error')
}
async function del(h) {
  if (!await confirmAction(`删除主机「${h.name}」？`)) return
  await fetch(`api/hosts/${h.name}`, { method: 'DELETE' }); load()
}
async function test(h) {
  testState.value[h.name] = 'testing'
  const r = await fetch(`api/hosts/${h.name}/test`, { method: 'POST' }).then(x => x.json())
  testState.value[h.name] = r.ok ? 'ok' : 'fail'
}

import OptionPicker from '../../shared/components/OptionPicker.vue'



import {Button,Dialog,DialogContent,DialogDescription,DialogFooter,DialogHeader,DialogTitle,DropdownMenu,DropdownMenuContent,DropdownMenuItem,DropdownMenuTrigger,Empty,EmptyContent,EmptyDescription,EmptyHeader,Input,Table,TableBody,TableCell,TableHead,TableHeader,TableRow} from '../../shared/shadcn-ui.js'

import {confirmAction} from "../../shared/shadcnFeedback.js"
</script>
<template><div><div style="display:flex;margin-bottom:12px"><Button style="margin-left:auto" @click="open()" variant="default" size="sm" type="button">＋ 新增主机</Button></div><div class="console-table min-w-0 max-w-full"><div class="min-w-0 max-w-full" :style="{maxHeight:({x:1190}).y?({x:1190}).y+'px':undefined}"><DataTable class="[&_[data-slot=table-container]]:rounded-lg [&_[data-slot=table-container]]:border" :rows="(((hosts)||[]).map((h,rowIndex)=>({...h,__rowKey:h.name}))||[])" :columns="[{id:'c0',label:&quot;名称&quot;,size:220,minSize:64,required:true,defaultHidden:false,flex:true,selection:false,pin:'left'},{id:'c1',label:&quot;所属环境&quot;,size:160,minSize:64,required:false,defaultHidden:false,flex:false,selection:false,pin:false},{id:'c2',label:&quot;地址&quot;,size:220,minSize:64,required:false,defaultHidden:false,flex:false,selection:false,pin:false},{id:'c3',label:&quot;用户&quot;,size:120,minSize:64,required:false,defaultHidden:false,flex:false,selection:false,pin:false},{id:'c4',label:&quot;凭据&quot;,size:120,minSize:64,required:false,defaultHidden:false,flex:false,selection:false,pin:false},{id:'c5',label:&quot;连通性&quot;,size:130,minSize:64,required:false,defaultHidden:false,flex:false,selection:false,pin:false},{id:'c6',label:&quot;操作&quot;,size:128,minSize:64,required:true,defaultHidden:false,flex:false,selection:false,pin:'right'}]" table-key="deploy-pages-HostsView-0" :row-key="(record,rowIndex)=>typeof (&quot;__rowKey&quot;)==='function'?(&quot;__rowKey&quot;)(record):record[&quot;__rowKey&quot;]??rowIndex"><template #header-c0>名称</template><template #cell-c0="{record,rowIndex}"><div class="min-w-0 break-words" :class="{truncate:!!(true)}"><template v-for="{record:h,rowIndex:rowIndex} in [{record,rowIndex}]"><div>{{ h.name }}</div></template></div></template><template #header-c1>所属环境</template><template #cell-c1="{record,rowIndex}"><div class="min-w-0 break-words" :class="{truncate:!!(true)}"><template v-for="{record:h,rowIndex:rowIndex} in [{record,rowIndex}]"><div><b>{{environments.find(e=>e.name===h.environment)?.display_name||h.environment}}</b></div></template></div></template><template #header-c2>地址</template><template #cell-c2="{record,rowIndex}"><div class="min-w-0 break-words" :class="{truncate:!!(true)}"><template v-for="{record:h,rowIndex:rowIndex} in [{record,rowIndex}]"><div style="font-family:var(--mono)">{{ h.addr }}</div></template></div></template><template #header-c3>用户</template><template #cell-c3="{record,rowIndex}"><div class="min-w-0 break-words" :class="{truncate:!!(true)}"><template v-for="{record:h,rowIndex:rowIndex} in [{record,rowIndex}]"><div>{{ h.user }}</div></template></div></template><template #header-c4>凭据</template><template #cell-c4="{record,rowIndex}"><div class="min-w-0 break-words" :class="{truncate:!!(true)}"><template v-for="{record:h,rowIndex:rowIndex} in [{record,rowIndex}]"><div style="color:var(--dim)">{{ h.key_path ? '私钥' : h.credential_configured ? '密码（已保存）' : '默认私钥' }}</div></template></div></template><template #header-c5>连通性</template><template #cell-c5="{record,rowIndex}"><div class="min-w-0 break-words" :class="{truncate:!!(true)}"><template v-for="{record:h,rowIndex:rowIndex} in [{record,rowIndex}]"><div><span v-if="testState[h.name] === 'ok'" class="st-ok">✓ 可达</span><span v-else-if="testState[h.name] === 'fail'" class="st-fail">✗ 不通</span><span v-else-if="testState[h.name] === 'testing'" class="st-run">测试中…</span><span v-else style="color:var(--dim)">未测</span></div></template></div></template><template #header-c6>操作</template><template #cell-c6="{record,rowIndex}"><div class="min-w-0 break-words" :class="{truncate:!!(false)}"><template v-for="{record:h,rowIndex:rowIndex} in [{record,rowIndex}]"><div class="cell-actions"><Button @click="open(h)" variant="ghost" size="sm" type="button">编辑</Button><DropdownMenu><DropdownMenuTrigger as-child><Button variant="ghost" size="sm" type="button">更多</Button></DropdownMenuTrigger><DropdownMenuContent align="end"><DropdownMenuItem @select="test(h)">测试</DropdownMenuItem><DropdownMenuItem @select="del(h)">删除</DropdownMenuItem></DropdownMenuContent></DropdownMenu></div></template></div></template><template #empty><Empty><EmptyHeader><EmptyDescription>暂无主机</EmptyDescription></EmptyHeader></Empty></template></DataTable></div></div><Dialog :open="show" @update:open="async v=>{if(!v){show = false}}"><DialogContent class="flex max-h-[92dvh] flex-col gap-0 overflow-hidden p-0 sm:max-w-[var(--editor-width)]" :class="false?'inset-0 h-dvh max-h-dvh w-screen max-w-none translate-x-0 translate-y-0 rounded-none sm:max-w-none':''" :style="{'--editor-width':typeof (&quot;min(520px, 92vw)&quot;)==='number'?(&quot;min(520px, 92vw)&quot;)+'px':(&quot;min(520px, 92vw)&quot;)}" :show-close-button="true" @interact-outside="e=>{e.preventDefault()}"><DialogHeader class="shrink-0 border-b p-5 pr-12"><DialogTitle>{{editing ? '编辑主机' : '新增主机'}}</DialogTitle><DialogDescription class="sr-only">详情与操作</DialogDescription></DialogHeader><div class="min-h-0 flex-1 overflow-auto p-5"><div class="fld"><label>名称</label><Input v-model="form.name" :disabled="!!editing"/></div><div class="fld"><label>所属环境</label><OptionPicker v-model="form.environment" :options="environments.map(e=>({value:e.name,label:(e.protected?'🔒 ':'')+e.display_name}))" :style="{width:'100%',maxWidth:'100%'}"/><small style="color:var(--muted-foreground)">未被引用时可调整；加入主机组或应用环境后由服务端锁定。</small></div><div class="fld"><label>地址（host[:port]）</label><Input v-model="form.addr"/></div><div class="fld"><label>用户</label><Input v-model="form.user"/></div><div class="fld"><label>私钥路径（留空用 ~/.ssh/id_rsa）</label><Input v-model="form.key_path"/></div><div class="fld"><label>或密码</label><Input v-model="form.password" type="password"/></div><div class="fld"><label>SSH 主机指纹（推荐，SHA256:…）</label><Input v-model="form.host_key" placeholder="留空则校验 ~/.ssh/known_hosts"/></div></div><DialogFooter><Button @click="save" variant="default" size="default" type="button">保存</Button></DialogFooter></DialogContent></Dialog></div></template>
