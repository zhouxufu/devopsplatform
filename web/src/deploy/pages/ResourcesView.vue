<script setup>
import { computed, ref, onMounted, watch } from 'vue'
import Modal from '../../shared/components/Modal.vue'
import Sel from '../../shared/components/Sel.vue'
import HelpTip from '../../shared/components/HelpTip.vue'
import { ElButton, ElInput, ElInputNumber } from '../../shared/components/ui.js'
import { toast } from '../../shared/composables/toast.js'
import { MULTILINE_MODES, collectEditor, collectMultiline, selectCollectApplication } from '../agentCollection.js'
import EnvironmentsView from './EnvironmentsView.vue'

const props=defineProps({plugin:{type:String,default:'deploy'},directAPI:Boolean})
const resourceURL=url=>url.startsWith('api/')?(props.directAPI?url:`/api/resources/${url.slice(4)}`):url
const tab=ref(''),connections=ref([]),hosts=ref([]),groups=ref([]),environments=ref([]),variables=ref([]),runners=ref([]),audit=ref([]),agentStats=ref([]),applications=ref([])
const showConnection=ref(false),showHost=ref(false),showGroup=ref(false),showVariables=ref(false),showRunner=ref(false),showAgent=ref(false),showApplication=ref(false)
const connectionForm=ref({}),hostForm=ref({}),editingHost=ref(null),groupForm=ref({}),variableForm=ref({}),runnerForm=ref({}),testState=ref({}),hostTestState=ref({})
const applicationForm=ref({name:''})
const hostKeyScanning=ref(false),pendingHostKey=ref(null)
const connectionKeyScanning=ref(false),pendingConnectionHostKey=ref(null)
const agentHost=ref(null),agentForm=ref({}),agentSaving=ref(false),agentApplying=ref(false),agentRemoteState=ref({}),agentInstallState=ref({})
const KINDS={git:'Git 代码凭据',maven:'Maven 私服',docker:'Docker Registry','aliyun-sae':'阿里云 SAE',mysql:'MySQL 变更数据库',zentao:'禅道 16.0（需求来源）'}
const ALL_RESOURCE_TABS={connections:['connections','🔌','连接'],environments:['environments','🌐','环境'],hosts:['hosts','🖥','主机'],groups:['groups','▦','主机组'],variables:['variables','⌘','变量组'],runners:['runners','⚙','构建 Runner'],audit:['audit','◷','审计日志']}
const RESOURCE_SCOPES={deploy:['connections','environments','hosts','groups','variables','runners','audit'],logs:['hosts','environments','audit'],monitor:['hosts','environments','audit'],devtools:['environments','audit']}
const PLUGIN_LABELS={deploy:'部署平台',logs:'日志查询',monitor:'监控大盘',devtools:'AI 开发工具'}
const resourceTabs=computed(()=>(RESOURCE_SCOPES[props.plugin]||['environments','hosts','audit']).map(key=>ALL_RESOURCE_TABS[key]))
const pluginLabel=computed(()=>PLUGIN_LABELS[props.plugin]||'当前插件')
const showAgentResources=computed(()=>props.plugin==='logs')
const hostPurpose=computed(()=>props.plugin==='logs'?'管理日志采集主机，并安装或调整采集 Agent':props.plugin==='monitor'?'管理机器监控所依赖的主机身份和 SSH 连接':'主机归属唯一环境，应用发布时通过主机组选择目标')
watch(resourceTabs,tabs=>{if(!tabs.some(item=>item[0]===tab.value))tab.value=tabs[0]?.[0]||''},{immediate:true})
async function json(url,opt){const r=await fetch(resourceURL(url),opt);const body=await r.json().catch(()=>({}));if(!r.ok)throw new Error(body.error||'请求失败');return body}
const asArray=value=>Array.isArray(value)?value:[]
async function loadAgentStats(){const data=await json('/api/plugins/logs/api/stats').catch(()=>({agents:[]}));agentStats.value=asArray(data?.agents)}
async function load(){
  const result=await Promise.all(['api/connections','api/hosts','api/host-groups','api/environments','api/variable-groups','api/runners','api/audit?limit=100','/api/registry/applications'].map(x=>props.directAPI&&x==='/api/registry/applications'?Promise.resolve([]):json(x)))
  ;[connections.value,hosts.value,groups.value,environments.value,variables.value,runners.value,audit.value,applications.value]=result.map(asArray)
  loadAgentStats()
}
onMounted(load)
const post=(url,body)=>json(url,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)})
async function remove(url,label){if(!confirm(`删除「${label}」？`))return;try{await json(url,{method:'DELETE'});toast('已删除','success');load()}catch(e){toast(e.message,'error')}}
function editConnection(v){
  const config={database:'',tls:'false',product_ids:'',bug_status:'closed',baseline:'',sync_minutes:'0',settings_path:'',repository_id:'',mirror_of:'*',region:'cn-hangzhou',oss_bucket:'',oss_endpoint:'',oss_prefix:'logplatform/releases',security_token:'',auth_mode:'https',known_hosts:'',ssh_fingerprint:'',...(v?.config||{})}
  connectionForm.value=v?{...v,username:'',password:'',private_key:'',passphrase:'',config}:{name:'',kind:'maven',endpoint:'',username:'',password:'',private_key:'',passphrase:'',config}
  pendingConnectionHostKey.value=null;connectionKeyScanning.value=false;showConnection.value=true
}
const gitAuthMode=computed({get:()=>connectionForm.value.config?.auth_mode||'https',set:value=>{
  if(!connectionForm.value.config)connectionForm.value.config={}
  if(connectionForm.value.config.auth_mode!==value){connectionForm.value.endpoint='';connectionForm.value.username=value==='ssh'?'git':'';connectionForm.value.password='';connectionForm.value.private_key='';connectionForm.value.passphrase='';connectionForm.value.config.known_hosts='';connectionForm.value.config.ssh_fingerprint='';pendingConnectionHostKey.value=null}
  connectionForm.value.config.auth_mode=value
}})
function updateConnectionEndpoint(value){
  const previous=(connectionForm.value.endpoint||'').trim();connectionForm.value.endpoint=value
  if(connectionForm.value.kind==='git'&&gitAuthMode.value==='ssh'&&previous&&previous!==value.trim()){connectionForm.value.config.known_hosts='';connectionForm.value.config.ssh_fingerprint='';pendingConnectionHostKey.value=null}
}
async function scanConnectionHostKey(){
  const endpoint=(connectionForm.value.endpoint||'').trim();if(!endpoint)return toast('请先填写 SSH 服务地址','warning')
  connectionKeyScanning.value=true;pendingConnectionHostKey.value=null
  try{const result=await post('api/connections/scan-key',{endpoint});pendingConnectionHostKey.value={...result,requested_endpoint:endpoint}}
  catch(e){toast(e.message,'error')}finally{connectionKeyScanning.value=false}
}
function acceptConnectionHostKey(){
  const result=pendingConnectionHostKey.value
  if(!result||result.requested_endpoint!==(connectionForm.value.endpoint||'').trim()){pendingConnectionHostKey.value=null;return toast('SSH 服务地址已改变，请重新获取指纹','warning')}
  connectionForm.value.config.known_hosts=result.known_hosts;connectionForm.value.config.ssh_fingerprint=result.fingerprint;pendingConnectionHostKey.value=null;toast('Git 服务器指纹已确认','success')
}
async function saveConnection(){
  const f=connectionForm.value
  if(!/^[\p{L}\p{N}][\p{L}\p{N}_.-]{0,63}$/u.test((f.name||'').trim()))return toast('连接名称支持中文、字母、数字、点、短横线和下划线，长度 1-64','warning')
  if(f.kind!=='aliyun-sae'&&!(f.endpoint||'').trim())return toast('请填写服务地址','warning')
  if(f.kind==='git'&&gitAuthMode.value==='ssh'&&!f.config?.ssh_fingerprint)return toast('请先获取并确认 Git 服务器 SSH 主机指纹','warning')
  if(f.kind==='git'&&gitAuthMode.value==='ssh'&&!f.secret_configured&&!(f.private_key||'').trim())return toast('请填写 SSH 私钥','warning')
  try{await post('api/connections',f);showConnection.value=false;toast('连接已保存','success');load()}catch(e){toast(e.message,'error')}
}
async function testConnection(v){testState.value[v.name]='testing';try{const r=await post(`api/connections/${v.name}/test`,{});testState.value[v.name]=r.ok?'ok':'fail';if(!r.ok)toast(r.error||r.status,'error')}catch(e){testState.value[v.name]='fail';toast(e.message,'error')}}
function editHost(v){editingHost.value=v||null;hostForm.value=v?{...v,password:''}:{name:'',environment:environments.value[0]?.name||'',addr:'',user:'root',key_path:'',password:'',host_key:''};hostKeyScanning.value=false;pendingHostKey.value=null;showHost.value=true}
function updateHostAddress(value){const previous=(hostForm.value.addr||'').trim();hostForm.value.addr=value;pendingHostKey.value=null;if(previous&&previous!==value.trim()&&hostForm.value.host_key)hostForm.value.host_key=''}
async function scanHostKey(){
  const addr=(hostForm.value.addr||'').trim()
  if(!addr)return toast('请先填写主机地址','warning')
  hostKeyScanning.value=true
  pendingHostKey.value=null
  try{const result=await post('api/hosts/scan-key',{addr});pendingHostKey.value={...result,requested_addr:addr}}
  catch(e){toast(e.message,'error')}
  finally{hostKeyScanning.value=false}
}
function acceptHostKey(){
  const result=pendingHostKey.value
  if(!result||result.requested_addr!==(hostForm.value.addr||'').trim()){pendingHostKey.value=null;return toast('主机地址已改变，请重新获取指纹','warning')}
  hostForm.value.host_key=result.fingerprint
  pendingHostKey.value=null
  toast('主机指纹已确认','success')
}
async function saveHost(){
  const f=hostForm.value
  if(!f.name||!f.environment||!f.addr)return toast('名称、环境和地址必填','warning')
  if(!/^[\p{L}\p{N}][\p{L}\p{N}_.-]{0,63}$/u.test(f.name.trim()))return toast('主机名称支持中文、字母、数字、点、短横线和下划线，长度 1-64','warning')
  try{await post('api/hosts',f);showHost.value=false;toast('主机已保存','success');load()}catch(e){toast(e.message,'error')}
}
async function testHost(v){hostTestState.value[v.name]='testing';try{const r=await post(`api/hosts/${v.name}/test`,{});hostTestState.value[v.name]=r.ok?'ok':'fail';if(!r.ok)toast(r.error||'主机不可达','error')}catch(e){hostTestState.value[v.name]='fail';toast(e.message,'error')}}
function centralAgent(h){return agentStats.value.find(a=>a.id===`${h.environment}/${h.name}`)}
function agentState(h){const live=centralAgent(h),remote=agentRemoteState.value[h.name];if(live?.online)return {cls:'st-ok',text:'● 在线'};if(remote?.installed&&remote?.active)return {cls:'st-run',text:'◐ 已启动，待上报'};if(remote?.installed)return {cls:'st-fail',text:'○ 服务已停止'};if(remote?.status==='not-installed')return {cls:'dim',text:'未安装'};if(live)return {cls:'dim',text:'○ 离线'};return {cls:'dim',text:'未检测'}}
async function checkAgent(h){agentRemoteState.value[h.name]={status:'checking'};try{agentRemoteState.value[h.name]=await json(`api/hosts/${h.name}/agent/status`)}catch(e){agentRemoteState.value[h.name]={status:'error'};toast(e.message,'error')}}
async function openAgentConfig(h){
  const host=location.hostname||'127.0.0.1'
  agentHost.value=h
  try{
    const [cfg,status]=await Promise.all([json(`api/hosts/${h.name}/agent/config`),json(`api/hosts/${h.name}/agent/status`).catch(()=>null)])
    if(status)agentRemoteState.value[h.name]=status
    const safeCfg=cfg&&typeof cfg==='object'?cfg:{}
    const savedCollect=asArray(safeCfg.collect).filter(Boolean)
    agentForm.value={...safeCfg,server_url:safeCfg.server_url||`http://${host}:9801`,monitor_url:safeCfg.monitor_url||`http://${host}:9802`,run_user:safeCfg.run_user||'logagent',run_group:safeCfg.run_group||'logagent',spool_max_mb:safeCfg.spool_max_mb||1024,collect:(savedCollect.length?savedCollect:[{application_id:'',service:'',paths:['/var/log/app/*.log'],multiline:''}]).map(collectEditor)}
    showAgent.value=true
  }catch(e){toast(e.message,'error')}
}
function addCollect(){if(!Array.isArray(agentForm.value.collect))agentForm.value.collect=[];agentForm.value.collect.push(collectEditor({application_id:'',service:''}))}
function editApplication(){applicationForm.value={name:''};showApplication.value=true}
async function saveApplication(){
  const name=applicationForm.value.name.trim()
  if(!name)return toast('填写应用名称','warning')
  try{
    const app=await json('/api/registry/applications/observe',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({name,source:'manual'})})
    if(!applications.value.some(item=>item.application_id===app.application_id))applications.value.push(app)
    showApplication.value=false
    if(agentForm.value.collect?.length){const item=agentForm.value.collect.find(c=>!c.application_id)||agentForm.value.collect[agentForm.value.collect.length-1];selectCollectApplication(item,app)}
    toast('平台应用已创建','success')
  }catch(e){toast(e.message,'error')}
}
function removeCollect(i){const collect=asArray(agentForm.value.collect);if(collect.length>1)collect.splice(i,1);agentForm.value.collect=collect}
function agentInstalled(h){return !!(centralAgent(h)||agentRemoteState.value[h.name]?.installed)}
function agentConfigBody(){
  const f=agentForm.value
  if(!f.server_url)return {error:'日志接收地址必填'}
  if((f.collect||[]).some(c=>c.multiline_mode==='custom'&&!collectMultiline(c)))return {error:'自定义模式需要填写首行正则；无需合并请选择单行日志'}
  const collect=asArray(f.collect).filter(Boolean).map(c=>{const app=asArray(applications.value).find(a=>a.application_id===c.application_id);return {application_id:c.application_id||'',service:app?.name||c.service||'',paths:typeof c.paths_text==='string'?c.paths_text.split('\n').map(x=>x.trim()).filter(Boolean):[],multiline:collectMultiline(c)}})
  if(!collect.length||collect.some(c=>!c.application_id||!c.paths.length))return {error:'每个采集项都要选择平台应用并填写日志路径'}
  return {body:{server_url:f.server_url,monitor_url:f.monitor_url,run_user:f.run_user,run_group:f.run_group,spool_max_mb:f.spool_max_mb,collect}}
}
async function saveAgentConfig(action='save'){
  const prepared=agentConfigBody();if(prepared.error)return toast(prepared.error,'warning')
  action==='save'?agentSaving.value=true:agentApplying.value=true
  try{
    const saved=await json(`api/hosts/${agentHost.value.name}/agent/config`,{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify(prepared.body)})
    agentForm.value={...(saved||{}),collect:asArray(saved?.collect).map(collectEditor)}
    if(action==='apply'){const applied=await post(`api/hosts/${agentHost.value.name}/agent/config/apply`,{});agentForm.value={...agentForm.value,...applied};agentRemoteState.value[agentHost.value.name]={installed:true,active:true,status:'active'};toast('采集配置已保存并下发','success')}
    else if(action==='install'){const result=await post(`api/hosts/${agentHost.value.name}/agent/install`,{});agentRemoteState.value[agentHost.value.name]={installed:true,active:true,status:'active',version:result.version};agentForm.value={...agentForm.value,synchronized:true,applied_at:Math.floor(Date.now()/1000)};toast('Agent 已安装并应用采集配置','success');loadAgentStats()}
    else toast('期望配置已保存，尚未下发','success')
  }catch(e){toast(e.message,'error')}
  finally{agentSaving.value=false;agentApplying.value=false}
}
async function installAgentForHost(h){
  agentInstallState.value[h.name]='installing'
  try{const result=await post(`api/hosts/${h.name}/agent/install`,{});agentRemoteState.value[h.name]={installed:true,active:true,status:'active',version:result.version};toast('Agent 已安装并应用期望配置','success');loadAgentStats()}
  catch(e){toast(e.message,'error');if(e.message.includes('采集配置'))openAgentConfig(h)}
  finally{agentInstallState.value[h.name]=''}
}
function editGroup(v){groupForm.value=v?{...JSON.parse(JSON.stringify(v)),_existing:true}:{name:'',environment:environments.value[0]?.name||'',description:'',hosts:[],_existing:false};showGroup.value=true}
function toggleHost(name){const a=groupForm.value.hosts??=[];const i=a.indexOf(name);i>=0?a.splice(i,1):a.push(name)}
async function saveGroup(){try{await post('api/host-groups',groupForm.value);showGroup.value=false;toast('主机组已保存','success');load()}catch(e){toast(e.message,'error')}}
function editVariables(v){variableForm.value={name:v?.name||'',description:v?.description||'',raw:'',keys:v?.keys||[]};showVariables.value=true}
async function saveVariables(){const values={};for(const line of variableForm.value.raw.split('\n')){if(!line.trim())continue;const i=line.indexOf('=');if(i<1)return toast(`变量格式错误：${line}`,'error');values[line.slice(0,i).trim()]=line.slice(i+1)};try{await post('api/variable-groups',{name:variableForm.value.name,description:variableForm.value.description,values});showVariables.value=false;toast('变量已加密保存','success');load()}catch(e){toast(e.message,'error')}}
function editRunner(v){runnerForm.value=v?{...v}:{name:'local-build',kind:'local',labels:'local',enabled:true,max_parallel:1};showRunner.value=true}
async function saveRunner(){try{await post('api/runners',runnerForm.value);showRunner.value=false;toast('Runner 已保存','success');load()}catch(e){toast(e.message,'error')}}
const fmt=t=>new Date(t*1000).toLocaleString('zh-CN',{hour12:false})
const envName=name=>environments.value.find(e=>e.name===name)?.display_name||name
const envProtected=name=>!!environments.value.find(e=>e.name===name)?.protected
</script>
<template><div>
  <div class="page-head"><h2>资源中心</h2><span class="plugin-scope">{{pluginLabel}}</span><HelpTip :text="`当前仅展示${pluginLabel}会使用的资源；切换顶部插件工作区后，分类会同步变化。资源数据仍由平台统一管理。`" /></div>
  <div class="resource-layout">
  <nav class="resource-nav"><button v-for="t in resourceTabs" :key="t[0]" type="button" :class="{active:tab===t[0]}" @click="tab=t[0]"><span>{{t[1]}}</span><b>{{t[2]}}</b></button></nav>
  <div class="resource-content">

  <EnvironmentsView v-if="tab==='environments'" embedded />

  <section v-if="tab==='connections'">
    <div class="section-head"><div><h3>资源连接</h3><p>账号密码加密保存；编辑时留空表示保留原凭据</p></div><ElButton type="primary" native-type="button" size="small" @click="editConnection()">＋ 新建连接</ElButton></div>
    <div class="resource-grid"><article v-for="v in connections" :key="v.name" class="resource-card"><span class="icon">{{v.kind==='maven'?'Ⓜ':v.kind==='docker'?'🐳':v.kind==='aliyun-sae'?'☁':'⌘'}}</span><div class="grow"><b>{{v.name}}</b><small>{{KINDS[v.kind]}}{{v.kind==='git'?' · '+(v.config?.auth_mode==='ssh'?'SSH 私钥':'HTTPS / PAT'):''}}</small><code>{{v.kind==='aliyun-sae' ? `${v.config?.region} · ${v.config?.oss_bucket||'未配置 OSS'}` : v.endpoint}}</code></div><span :class="v.secret_configured?'st-ok':'st-run'">{{v.secret_configured?'凭据已配置':'无凭据'}}</span><div class="actions"><button class="op op-btn" @click="testConnection(v)">{{testState[v.name]==='testing'?'测试中…':testState[v.name]==='ok'?'✓ 可连接':testState[v.name]==='fail'?'✗ 重试':'测试'}}</button><button class="op op-btn" @click="editConnection(v)">编辑</button><button class="op op-btn del" @click="remove(`api/connections/${v.name}`,v.name)">删除</button></div></article></div>
    <div v-if="!connections.length" class="onboarding"><span>🔌</span><h3>连接构建与云资源</h3><p>按实际需要添加 Git、Maven 私服、镜像仓库或 SAE；没有使用的资源无需配置。</p><ElButton type="primary" size="small" @click="editConnection()">新建第一个连接</ElButton></div>
  </section>

  <section v-if="tab==='hosts'">
    <div class="section-head"><div><h3>主机</h3><p>{{hostPurpose}}</p></div><ElButton type="primary" native-type="button" size="small" @click="editHost()">＋ 新增主机</ElButton></div>
    <div v-if="hosts.length" class="table-scroll"><table><thead><tr><th>名称</th><th>所属环境</th><th>地址</th><th>用户</th><th>凭据</th><th>连通性</th><th v-if="showAgentResources">采集 Agent</th><th>操作</th></tr></thead><tbody><tr v-for="h in hosts" :key="h.name"><td><b>{{h.name}}</b></td><td>{{envName(h.environment)}}</td><td><code>{{h.addr}}</code></td><td>{{h.user}}</td><td class="dim">{{h.key_path?'私钥':h.credential_configured?'密码（已保存）':'默认私钥'}}</td><td><span :class="hostTestState[h.name]==='ok'?'st-ok':hostTestState[h.name]==='fail'?'st-fail':hostTestState[h.name]==='testing'?'st-run':'dim'">{{hostTestState[h.name]==='ok'?'✓ 可达':hostTestState[h.name]==='fail'?'✗ 不通':hostTestState[h.name]==='testing'?'测试中…':'未测'}}</span></td><td v-if="showAgentResources"><span :class="agentState(h).cls">{{agentRemoteState[h.name]?.status==='checking'?'检测中…':agentState(h).text}}</span></td><td><button class="op op-btn" @click="testHost(h)">SSH 测试</button><template v-if="showAgentResources"><button class="op op-btn" @click="checkAgent(h)">检测 Agent</button><button class="op op-btn" @click="openAgentConfig(h)">采集配置</button><button class="op op-btn" :disabled="agentInstallState[h.name]==='installing'" @click="installAgentForHost(h)">{{agentInstallState[h.name]==='installing'?'处理中…':agentInstalled(h)?'升级 Agent':'安装 Agent'}}</button></template><button class="op op-btn" @click="editHost(h)">编辑</button><button class="op op-btn del" @click="remove(`api/hosts/${h.name}`,h.name)">删除</button></td></tr></tbody></table></div>
    <div v-else class="onboarding"><span>🖥</span><h3>{{plugin==='logs'?'添加日志采集主机':plugin==='monitor'?'添加监控主机':'添加发布主机'}}</h3><p>{{plugin==='deploy'?'先指定主机所属环境，再把同环境主机加入主机组。使用 SAE 时可以跳过此步骤。':'主机是 Agent 安装和采集配置的目标，先选择所属环境再建立 SSH 连接。'}}</p><ElButton type="primary" size="small" @click="editHost()">新增主机</ElButton></div>
  </section>

  <section v-if="tab==='groups'">
    <div class="section-head"><div><h3>环境主机组</h3><p>主机和主机组都归属唯一环境，测试资源不能绑定到生产环境</p></div><div><ElButton size="small" @click="tab='hosts'">管理主机</ElButton><ElButton type="primary" native-type="button" size="small" @click="editGroup()">＋ 新建主机组</ElButton></div></div>
    <div class="resource-grid"><article v-for="v in groups" :key="v.name" class="resource-card"><span class="icon">🖥</span><div class="grow"><b>{{v.name}}</b><small>{{envProtected(v.environment)?'🔒 ':''}}{{envName(v.environment)}} · {{v.description||'未填写说明'}}</small><span>{{v.hosts.join('、')||'空主机组'}}</span></div><b>{{v.hosts.length}} 台</b><div class="actions"><button class="op op-btn" @click="editGroup(v)">编辑</button><button class="op op-btn del" @click="remove(`api/host-groups/${v.name}`,v.name)">删除</button></div></article></div>
    <div v-if="!groups.length" class="onboarding"><span>▦</span><h3>创建环境主机组</h3><p>主机组是应用发布的目标，不需要在发布单里逐台填写主机。</p><ElButton type="primary" size="small" :disabled="!hosts.length" @click="editGroup()">{{hosts.length?'新建主机组':'请先添加主机'}}</ElButton></div>
  </section>

  <section v-if="tab==='variables'">
    <div class="section-head"><div><h3>变量组</h3><p>构建变量与密钥统一加密，应用按需引用</p></div><ElButton type="primary" native-type="button" size="small" @click="editVariables()">＋ 新建变量组</ElButton></div>
    <table><thead><tr><th>变量组</th><th>说明</th><th>变量键</th><th>更新时间</th><th>操作</th></tr></thead><tbody><tr v-for="v in variables" :key="v.name"><td><b>{{v.name}}</b></td><td>{{v.description||'—'}}</td><td><code>{{v.keys.join(', ')||'—'}}</code></td><td>{{fmt(v.updated_at)}}</td><td><button class="op op-btn" @click="editVariables(v)">编辑</button><button class="op op-btn del" @click="remove(`api/variable-groups/${v.name}`,v.name)">删除</button></td></tr></tbody></table>
  </section>

  <section v-if="tab==='runners'">
    <div class="section-head"><div><h3>构建 Runner</h3><p>构建任务运行位置；当前版本提供安全隔离的本地 Runner 接口</p></div><ElButton type="primary" native-type="button" size="small" @click="editRunner()">＋ 新建 Runner</ElButton></div>
    <table><thead><tr><th>Runner</th><th>类型</th><th>标签</th><th>并发</th><th>状态</th><th>操作</th></tr></thead><tbody><tr v-for="v in runners" :key="v.name"><td><b>{{v.name}}</b></td><td>{{v.kind}}</td><td>{{v.labels}}</td><td>{{v.max_parallel}}</td><td :class="v.enabled?'st-ok':'st-fail'">{{v.enabled?'可用':'停用'}}</td><td><button class="op op-btn" @click="editRunner(v)">配置</button></td></tr></tbody></table>
  </section>

  <section v-if="tab==='audit'"><div class="section-head"><div><h3>审计日志</h3><p>记录资源配置、构建触发、审批、发布和回滚操作</p></div></div><table><thead><tr><th>时间</th><th>操作者</th><th>动作</th><th>资源</th><th>标识</th></tr></thead><tbody><tr v-for="v in audit" :key="v.id"><td>{{fmt(v.created_at)}}</td><td>{{v.actor}}</td><td>{{v.action}}</td><td>{{v.resource_type}}</td><td><code>{{v.resource_id}}</code></td></tr></tbody></table></section>
  </div></div>

  <Modal :show="showConnection" title="资源连接" wide @close="showConnection=false">
    <div class="fld"><label>连接名称</label><ElInput v-model="connectionForm.name" placeholder="例如 云效代码凭据" /><small class="help">用于在应用中选择这份凭据，支持中文；名称中不要包含空格或斜杠。</small></div>
    <div class="fld"><label>类型</label><Sel v-model="connectionForm.kind" :disabled="!!connectionForm.updated_at" :options="Object.entries(KINDS).map(([value,label])=>({value,label}))" width="100%" /></div>
    <div v-if="connectionForm.kind==='git'" class="fld"><label>认证方式</label><Sel v-model="gitAuthMode" :options="[{value:'https',label:'HTTPS + 用户名 / PAT'},{value:'ssh',label:'SSH 私钥'}]" width="100%" /><small class="help">认证方式必须与应用中的仓库地址一致；SSH 地址形如 git@codeup.aliyun.com:组织/仓库.git。</small></div>
    <div v-if="connectionForm.kind!=='aliyun-sae'" class="fld">
      <label>{{connectionForm.kind==='git'&&gitAuthMode==='ssh'?'SSH 服务地址':'服务地址'}}</label>
      <ElInput :model-value="connectionForm.endpoint" :placeholder="connectionForm.kind==='mysql'?'192.168.0.32:3306':connectionForm.kind==='zentao'?'http://192.168.0.5/zentao':connectionForm.kind==='git'?(gitAuthMode==='ssh'?'codeup.aliyun.com:22':'https://codeup.aliyun.com'):connectionForm.kind==='docker'?'https://registry.example.com':'https://packages.example.com/repository/maven-public'" @update:model-value="updateConnectionEndpoint" />
      <small v-if="connectionForm.kind==='git'" class="help">只填写 Git 服务，不填写具体仓库路径；HTTPS 可省略协议，SSH 可省略默认端口 22。</small>
    </div>
    <template v-if="connectionForm.kind==='mysql'"><div class="two"><div class="fld"><label>数据库</label><ElInput v-model="connectionForm.config.database" placeholder="例如 estate_ai" /></div><div class="fld"><label>连接加密</label><Sel v-model="connectionForm.config.tls" :options="[{value:'true',label:'TLS（校验证书）'},{value:'false',label:'内网明文连接'}]" width="100%" /></div></div><small class="help">每个环境使用独立连接和最小权限账号；预检不会试执行 DDL。账号需可读取目标表结构。</small></template>
    <template v-if="connectionForm.kind==='zentao'"><div class="two"><div class="fld"><label>Bug 产品 ID（逗号分隔）</label><ElInput v-model="connectionForm.config.product_ids" placeholder="1,2；仅同步用户可留空" /></div><div class="fld"><label>更新或关闭日期基线</label><ElInput v-model="connectionForm.config.baseline" placeholder="YYYY-MM-DD；留空扫描历史" /></div></div><div class="fld"><label>默认 Bug 状态（手动同步可覆盖）</label><Sel v-model="connectionForm.config.bug_status" :options="[{value:'closed',label:'已关闭'},{value:'active',label:'激活（未解决）'},{value:'resolved',label:'已解决（未关闭）'},{value:'unclosed',label:'未关闭（激活 + 已解决）'},{value:'all',label:'全部状态'}]" width="100%" /></div><div class="fld"><label>Bug 自动同步周期（分钟，0 关闭）</label><ElInput v-model="connectionForm.config.sync_minutes" /></div><small class="help">默认同步已关闭 Bug；自动同步沿用这里的状态与日期，手动同步可单次覆盖。适配开源 16.0，会话由平台维护。只读同步 Bug 和用户，不回写禅道；用户同步入口在成员管理。同步用户需要组织/用户列表查看权限，地址须包含实际安装子路径。</small></template>
    <template v-if="connectionForm.kind==='maven'"><div class="two"><div class="fld"><label>仓库 ID</label><ElInput v-model="connectionForm.config.repository_id" placeholder="默认使用连接名称" /></div><div class="fld"><label>mirrorOf</label><ElInput v-model="connectionForm.config.mirror_of" placeholder="*" /></div></div><div class="fld"><label>已有 settings.xml（可选）</label><ElInput v-model="connectionForm.config.settings_path" placeholder="留空由平台安全生成" /><small class="help">留空时自动生成引用环境变量的 settings.xml，不把密码写入文件。</small></div></template>
    <template v-if="connectionForm.kind==='aliyun-sae'"><div class="two"><div class="fld"><label>地域</label><ElInput v-model="connectionForm.config.region" placeholder="cn-hangzhou" /></div><div class="fld"><label>SAE Endpoint（可选）</label><ElInput v-model="connectionForm.endpoint" placeholder="留空自动生成" /></div></div><div class="two"><div class="fld"><label>OSS Bucket（JAR 必填）</label><ElInput v-model="connectionForm.config.oss_bucket" /></div><div class="fld"><label>OSS Endpoint（可选）</label><ElInput v-model="connectionForm.config.oss_endpoint" placeholder="留空按地域生成" /></div></div><div class="fld"><label>OSS 对象前缀</label><ElInput v-model="connectionForm.config.oss_prefix" placeholder="logplatform/releases" /></div><div class="fld"><label>STS SecurityToken（临时凭据时填写）</label><ElInput v-model="connectionForm.config.security_token" type="password" show-password placeholder="长期 AccessKey 留空" /></div></template>
    <template v-if="connectionForm.kind==='git'&&gitAuthMode==='ssh'">
      <div class="fld"><label>SSH 用户</label><ElInput v-model="connectionForm.username" placeholder="git" /><small class="help">云效、GitLab、GitHub 等代码托管通常使用 git；这不是控制台登录账号。</small></div>
      <div class="fld"><label>SSH 私钥</label><ElInput v-model="connectionForm.private_key" type="textarea" :rows="7" placeholder="-----BEGIN OPENSSH PRIVATE KEY-----&#10;..." /><small class="help">{{connectionForm.secret_configured?'留空表示保留原私钥。':'粘贴与代码托管平台公钥对应的私钥。'}}私钥加密保存，构建时写入临时目录并在步骤结束后删除。</small></div>
      <div class="fld"><label>私钥口令（可选）</label><ElInput v-model="connectionForm.passphrase" type="password" show-password :placeholder="connectionForm.secret_configured?'留空表示保留':'私钥未加密则留空'" /></div>
      <div class="fld"><label>SSH 主机指纹</label><div class="fingerprint-row"><ElInput :model-value="connectionForm.config.ssh_fingerprint" readonly placeholder="尚未确认" /><ElButton :loading="connectionKeyScanning" :disabled="connectionKeyScanning||!connectionForm.endpoint" @click="scanConnectionHostKey">{{connectionKeyScanning?'获取中…':'获取指纹'}}</ElButton></div><small class="help">平台主动读取服务器公钥；确认后严格校验，不会关闭 StrictHostKeyChecking。</small>
        <div v-if="pendingConnectionHostKey" class="fingerprint-confirm"><b>确认 Git 服务器身份</b><dl><div><dt>目标</dt><dd><code>{{pendingConnectionHostKey.addr}}</code></dd></div><div><dt>算法</dt><dd><code>{{pendingConnectionHostKey.algorithm}}</code></dd></div><div><dt>SHA256 指纹</dt><dd><code>{{pendingConnectionHostKey.fingerprint}}</code></dd></div></dl><p>请与云效或服务器管理员公布的 SSH 指纹核对。确认后，该连接只信任这把服务器公钥。</p><div><ElButton size="small" @click="pendingConnectionHostKey=null">取消</ElButton><ElButton type="primary" size="small" @click="acceptConnectionHostKey">确认并使用</ElButton></div></div>
      </div>
    </template>
    <div v-else class="two"><div class="fld"><label>{{connectionForm.kind==='aliyun-sae'?'AccessKey ID':connectionForm.kind==='git'?'用户名 / 邮箱':'用户名'}}</label><ElInput v-model="connectionForm.username" placeholder="留空保留" /></div><div class="fld"><label>{{connectionForm.kind==='aliyun-sae'?'AccessKey Secret':connectionForm.kind==='git'?'个人访问令牌（PAT）':'密码 / Token'}}</label><ElInput v-model="connectionForm.password" type="password" show-password placeholder="留空保留" /></div></div>
    <small v-if="connectionForm.kind==='git'&&gitAuthMode==='https'" class="help">HTTPS 模式填写代码托管账号与个人访问令牌（PAT），不会读取本机 SSH 私钥。</small>
    <div class="modal-actions"><ElButton type="primary" @click="saveConnection">加密保存</ElButton></div>
  </Modal>
  <Modal :show="showHost" :title="editingHost?'编辑主机':'新增主机'" @close="showHost=false"><div class="fld"><label>名称</label><ElInput v-model="hostForm.name" :disabled="!!editingHost" placeholder="例如 住房宝测试" /><small class="help">支持中文、字母、数字、点、短横线和下划线；保存后名称不可修改。</small></div><div class="fld"><label>所属环境</label><Sel v-model="hostForm.environment" :options="environments.map(e=>({value:e.name,label:(e.protected?'🔒 ':'')+e.display_name}))" width="100%" /><small class="help">加入主机组或应用环境后，所属环境将被锁定。</small></div><div class="fld"><label>地址（host[:port]）</label><ElInput :model-value="hostForm.addr" @update:model-value="updateHostAddress" /></div><div class="two"><div class="fld"><label>用户</label><ElInput v-model="hostForm.user" /></div><div class="fld"><label>私钥路径</label><ElInput v-model="hostForm.key_path" placeholder="留空使用默认私钥" /></div></div><div class="fld"><label>密码（可选）</label><ElInput v-model="hostForm.password" type="password" show-password placeholder="编辑时留空表示保留" /></div><div class="fld"><label>SSH 主机指纹</label><div class="fingerprint-row"><ElInput v-model="hostForm.host_key" placeholder="SHA256:…" /><ElButton :loading="hostKeyScanning" :disabled="hostKeyScanning||!hostForm.addr" @click="scanHostKey">{{hostKeyScanning?'获取中…':'获取指纹'}}</ElButton></div><small class="help">平台仅读取目标 SSH 公钥；请核对后确认。留空时仍会校验平台用户的 known_hosts。</small><div v-if="pendingHostKey" class="fingerprint-confirm"><b>确认目标主机身份</b><dl><div><dt>目标</dt><dd><code>{{pendingHostKey.addr}}</code></dd></div><div><dt>算法</dt><dd><code>{{pendingHostKey.algorithm}}</code></dd></div><div><dt>SHA256 指纹</dt><dd><code>{{pendingHostKey.fingerprint}}</code></dd></div></dl><p>请与服务器管理员或服务器控制台显示的指纹核对。确认后，平台才会保存并信任它。</p><div><ElButton size="small" @click="pendingHostKey=null">取消</ElButton><ElButton type="primary" size="small" @click="acceptHostKey">确认并使用</ElButton></div></div></div><div class="modal-actions"><ElButton type="primary" @click="saveHost">保存主机</ElButton></div></Modal>
  <Modal :show="showAgent" :title="`日志采集配置 · ${agentHost?.name||''}`" wide @close="showAgent=false">
    <div :class="['agent-config-state',agentForm.token_available?'ok':'error']">
      <b>{{agentForm.token_available?'✓ 上报凭据由平台统一管理':'平台未配置上报凭据'}}</b>
      <span>{{agentForm.token_available?'保存或下发时无需再次填写 Token。':'请先为平台进程配置 DEVOPSPLATFORM_DATA_TOKEN。'}}</span>
    </div>
    <div v-if="agentForm.configured" class="sync-line">
      <span :class="agentForm.synchronized?'st-ok':'st-run'">{{agentForm.synchronized?'✓ 已与目标主机同步':'● 有尚未下发的配置'}}</span>
      <small v-if="agentForm.updated_at">更新于 {{fmt(agentForm.updated_at)}}</small>
    </div>
    <div class="two"><div class="fld"><label>日志接收地址</label><ElInput v-model="agentForm.server_url" placeholder="http://平台地址:9801" /></div><div class="fld"><label>机器监控地址（可选）</label><ElInput v-model="agentForm.monitor_url" placeholder="http://平台地址:9802" /></div></div>
    <small class="help">远端主机必须能访问上述地址；不要填写 127.0.0.1，除非平台就在该主机。</small>
    <div class="two"><div class="fld"><label>运行用户</label><ElInput v-model="agentForm.run_user" /></div><div class="fld"><label>运行用户组</label><ElInput v-model="agentForm.run_group" /></div></div>
    <div class="fld"><label>本地断点缓冲（MB）</label><ElInputNumber v-model="agentForm.spool_max_mb" :min="64" :max="10240" /></div>
    <div class="collect-head"><div><b>日志采集项</b><small>应用来源于统一应用目录，保存稳定 application_id</small></div><span><ElButton size="small" @click="editApplication">＋ 新建应用</ElButton><ElButton size="small" :disabled="!applications.length" @click="addCollect">＋ 添加采集项</ElButton></span></div>
    <div v-if="!applications.length" class="install-note">当前没有平台应用。可以直接新建日志应用，后续接入部署或 OTel 时继续沿用同一个应用身份。</div>
    <div v-for="(c,i) in agentForm.collect" :key="i" class="collect-card">
      <div class="collect-title"><b>采集项 {{i+1}}</b><button v-if="agentForm.collect.length>1" class="op del" @click="removeCollect(i)">删除</button></div>
      <div class="fld"><label>平台应用</label><Sel :model-value="c.application_id" @update:model-value="id=>selectCollectApplication(c,applications.find(a=>a.application_id===id))" :options="applications.map(a=>({value:a.application_id,label:`${a.name} · ${(a.sources||[]).map(s=>s==='logs'?'日志':s==='otel'?'OTel':s==='deploy'?'部署':'手动').join(' + ')||'待接入'}`}))" placeholder="选择应用" filterable width="100%" /></div>
      <div class="fld"><label>日志绝对路径（每行一个，支持 glob）</label><ElInput v-model="c.paths_text" type="textarea" :rows="2" placeholder="/opt/apps/order/logs/*.log" /></div>
      <div class="fld"><label>日志合并方式</label><Sel v-model="c.multiline_mode" :options="MULTILINE_MODES" width="100%" />
        <small v-if="c.multiline_mode==='single'" class="help">每行独立上报。Java 异常会被拆成多条，建议选择“Java 异常堆栈合并”。JSON 每行一条的日志可保留此模式。</small>
        <small v-else-if="c.multiline_mode==='java'" class="help">适用于日期或时间开头的日志，如 2026-09-16 10:24:37.025、2026-09-16T10:24:37 或 10:24:37。后续异常、at、Caused by 等行合并到上一条，同一条日志沿用首行级别；下一时间戳开始新日志。</small>
        <template v-else><ElInput v-model="c.multiline" placeholder="例如 ^\d{4}-\d{2}-\d{2}" /><small class="help">匹配表示一条新日志开始，不匹配的后续行并入上一条。请按文件中真实的首行格式填写，不需要 /…/ 包裹。</small></template>
      </div>
    </div>
    <small class="help">合并规则在“保存并下发”后生效，无需重装 Agent；仅影响后续采集，已入库的拆行日志不会自动重组。合并后的日志可在搜索结果中点击展开完整堆栈。</small>
    <div class="modal-actions"><ElButton @click="showAgent=false">关闭</ElButton><ElButton :loading="agentSaving" :disabled="agentSaving||agentApplying||!applications.length" @click="saveAgentConfig('save')">仅保存期望配置</ElButton><ElButton v-if="agentHost" type="primary" :loading="agentApplying" :disabled="agentSaving||agentApplying||!applications.length||!agentForm.token_available" @click="saveAgentConfig(agentInstalled(agentHost)?'apply':'install')">{{agentInstalled(agentHost)?'保存并下发':'保存并安装 Agent'}}</ElButton></div>
  </Modal>
  <Modal :show="showApplication" title="新建平台应用" @close="showApplication=false"><div class="fld"><label>应用名称</label><ElInput v-model="applicationForm.name" placeholder="例如 estate-ai-seek" @keydown.enter="saveApplication" /><small class="help">创建后可直接配置日志采集；以后接入构建部署或 OTel 时选择同一个应用。</small></div><div class="modal-actions"><ElButton @click="showApplication=false">取消</ElButton><ElButton type="primary" @click="saveApplication">创建</ElButton></div></Modal>
  <Modal :show="showGroup" title="环境主机组" @close="showGroup=false"><div class="fld"><label>主机组标识</label><ElInput v-model="groupForm.name" :disabled="groupForm._existing" /></div><div class="fld"><label>所属环境</label><Sel :model-value="groupForm.environment" :disabled="groupForm._existing" :options="environments.map(e=>({value:e.name,label:(e.protected?'🔒 ':'')+e.display_name}))" width="100%" @update:model-value="v=>{groupForm.environment=v;groupForm.hosts=[]}" /></div><div class="fld"><label>说明</label><ElInput v-model="groupForm.description" /></div><div class="fld"><label>选择同环境主机</label><div class="pick-list"><label v-for="h in hosts.filter(h=>h.environment===groupForm.environment)" :key="h.name" :class="{checked:groupForm.hosts?.includes(h.name)}"><input type="checkbox" :checked="groupForm.hosts?.includes(h.name)" @change="toggleHost(h.name)"/><b>{{h.name}}</b><small>{{h.addr}}</small></label></div><small v-if="!hosts.some(h=>h.environment===groupForm.environment)" class="help">该环境还没有主机，请先创建主机并指定环境。</small></div><div class="modal-actions"><ElButton type="primary" @click="saveGroup">保存</ElButton></div></Modal>
  <Modal :show="showVariables" title="变量组" @close="showVariables=false"><div class="fld"><label>变量组标识</label><ElInput v-model="variableForm.name" /></div><div class="fld"><label>说明</label><ElInput v-model="variableForm.description" /></div><div v-if="variableForm.keys?.length" class="existing">已保存：<code>{{variableForm.keys.join(', ')}}</code>；不填写变量可只修改说明。</div><div class="fld"><label>变量（每行 KEY=value）</label><ElInput v-model="variableForm.raw" type="textarea" :rows="7" placeholder="JAVA_HOME=/opt/jdk8&#10;REPO_TOKEN=secret" /></div><div class="modal-actions"><ElButton type="primary" @click="saveVariables">加密保存</ElButton></div></Modal>
  <Modal :show="showRunner" title="构建 Runner" @close="showRunner=false"><div class="fld"><label>Runner 标识</label><ElInput v-model="runnerForm.name" /></div><div class="fld"><label>标签</label><ElInput v-model="runnerForm.labels" placeholder="jdk8,docker" /></div><div class="fld"><label>最大并发</label><ElInputNumber v-model="runnerForm.max_parallel" :min="1" :max="32" /></div><label><input v-model="runnerForm.enabled" type="checkbox"/> 可用</label><div class="modal-actions"><ElButton type="primary" @click="saveRunner">保存</ElButton></div></Modal>
</div></template>
<style scoped>
.page-head{display:flex;align-items:center;gap:7px;margin-bottom:12px}.page-head h2{font-size:20px}.resource-layout{display:grid;grid-template-columns:148px minmax(0,1fr);gap:18px}.resource-nav{display:flex;position:sticky;top:0;align-self:start;flex-direction:column;gap:3px;padding:5px;border:1px solid var(--border);border-radius:9px;background:var(--panel)}.resource-nav button{display:flex;align-items:center;gap:8px;width:100%;padding:9px 10px;border:0;border-radius:6px;background:transparent;color:var(--muted);text-align:left;cursor:pointer}.resource-nav button:hover{background:var(--panel2);color:var(--text)}.resource-nav button.active{background:color-mix(in srgb,var(--brand) 12%,var(--panel2));color:var(--brand)}.resource-nav button span{width:18px;text-align:center}.resource-nav button b{font-size:11px}.resource-content{min-width:0}.section-head p{color:var(--muted);font-size:11px}.section-head{display:flex;align-items:center;margin-bottom:12px}.section-head h3{font-size:15px}.section-head>button,.section-head>div:last-child:not(:first-child){margin-left:auto}.resource-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(360px,1fr));gap:10px}.resource-card{display:flex;align-items:flex-start;gap:10px;padding:14px;background:var(--panel);border:1px solid var(--border);border-radius:9px}.icon{display:grid;place-items:center;width:34px;height:34px;border-radius:7px;background:var(--panel2);font-size:18px}.grow{display:flex;flex-direction:column;min-width:0;flex:1}.grow small{color:var(--muted)}.grow code,.grow span{font-size:10px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}.resource-card>.st-ok,.resource-card>.st-run{font-size:10px}.actions{width:100%;border-top:1px solid var(--border);padding-top:8px;margin-top:10px;margin-left:-44px;padding-left:44px;align-self:flex-end}.two{display:grid;grid-template-columns:1fr 1fr;gap:10px}.modal-actions{display:flex;justify-content:flex-end;margin-top:15px}.pick-list{display:grid;grid-template-columns:1fr 1fr;gap:7px}.pick-list label{display:grid;grid-template-columns:auto 1fr;align-items:center;column-gap:6px;border:1px solid var(--border);padding:8px;border-radius:7px}.pick-list label.checked{border-color:var(--brand)}.pick-list input{grid-row:1/3}.pick-list small{color:var(--muted);font-size:9px}.existing{background:var(--panel2);padding:8px;border-radius:6px;margin-bottom:10px;font-size:10px;color:var(--muted)}
.onboarding{display:flex;min-height:240px;flex-direction:column;align-items:center;justify-content:center;padding:32px;border:1px dashed var(--border);border-radius:10px;color:var(--muted);text-align:center}.onboarding>span{font-size:30px}.onboarding h3{margin:8px 0 3px;color:var(--text);font-size:15px}.onboarding p{max-width:480px;margin-bottom:15px;font-size:12px}.dim{color:var(--dim)}
.install-note{padding:9px 11px;margin-bottom:12px;border-radius:7px;background:var(--panel2);color:var(--muted);font-size:11px}.collect-head,.collect-title{display:flex;align-items:center;justify-content:space-between}.collect-head{margin:14px 0 7px}.collect-card{padding:10px 11px;margin-bottom:9px;border:1px solid var(--border);border-radius:8px;background:var(--panel2)}.collect-card .fld:last-child{margin-bottom:0}.install-success{display:flex;flex-direction:column;gap:2px;padding:10px;margin-top:10px;border:1px solid color-mix(in srgb,var(--ok) 45%,var(--border));border-radius:8px;color:var(--ok)}.install-success span,.install-success code{font-size:10px;color:var(--muted)}
.agent-config-state{display:flex;flex-direction:column;gap:2px;padding:10px 11px;margin-bottom:10px;border:1px solid var(--border);border-radius:8px;background:var(--panel2);font-size:11px}.agent-config-state span{color:var(--muted)}.agent-config-state.ok b{color:var(--ok)}.agent-config-state.error b{color:var(--error)}.sync-line{display:flex;align-items:center;justify-content:space-between;margin-bottom:11px;font-size:10px}.sync-line small,.collect-head small{display:block;color:var(--muted);font-weight:400}.collect-head>div{display:flex;flex-direction:column;gap:1px}
.fingerprint-row{display:grid;grid-template-columns:minmax(0,1fr) auto;gap:8px}.fingerprint-confirm{margin-top:9px;padding:11px;border:1px solid color-mix(in srgb,var(--brand) 45%,var(--border));border-radius:8px;background:color-mix(in srgb,var(--brand) 6%,var(--panel))}.fingerprint-confirm dl{margin:8px 0;display:grid;gap:5px}.fingerprint-confirm dl>div{display:grid;grid-template-columns:74px minmax(0,1fr);gap:7px}.fingerprint-confirm dt{color:var(--muted);font-size:10px}.fingerprint-confirm dd{margin:0;min-width:0}.fingerprint-confirm code{display:block;overflow-wrap:anywhere;font-size:10px}.fingerprint-confirm p{margin:7px 0;color:var(--muted);font-size:10px}.fingerprint-confirm>div:last-child{display:flex;justify-content:flex-end;gap:7px}
.resource-card{display:grid;grid-template-columns:34px minmax(0,1fr) auto;align-items:start}
.resource-card .grow{min-width:0;flex:none}.resource-card .grow>b,.resource-card .grow>small{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.resource-card .actions{grid-column:2/-1;display:flex;align-items:center;gap:4px;width:auto;min-width:0;margin:2px 0 0;padding:8px 0 0;align-self:auto}
@media(max-width:760px){.resource-layout{display:block}.resource-nav{position:static;flex-direction:row;overflow-x:auto;margin-bottom:14px}.resource-nav button{flex:0 0 auto;width:auto}.resource-grid{grid-template-columns:1fr}.resource-card .actions{grid-column:1/-1}}
.plugin-scope{padding:3px 8px;border-radius:10px;background:color-mix(in srgb,var(--brand) 12%,var(--panel2));color:var(--brand);font-size:10px}
</style>
