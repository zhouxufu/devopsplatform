<script setup>
import {computed,onMounted,onBeforeUnmount,ref,watch} from 'vue'

import PageSurface from './PageSurface.vue'
import PageHeader from './PageHeader.vue'
import ApplicationCenterView from '../pages/ApplicationCenterView.vue'
import ResourcesView from '../../deploy/pages/ResourcesView.vue'
import ExtensionManager from '../../shell/ExtensionManager.vue'
import {usePlatformCapabilities} from '../composables/platformCapabilities.js'
import {themeDark,themeMode,themeTypography,themeDesign,themeColorway,themeDensity,themeSyncPayload} from '../composables/theme.js'

import {toast} from '../composables/toast.js'
const props=defineProps({view:String,currentUser:Object,directAPI:Boolean,active:{type:Boolean,default:true}})
const emit=defineEmits(['state','open','switch'])
const {mcpAvailable}=usePlatformCapabilities()
const me=ref(null),frame=ref(null),state=ref({dirty:false,busy:false})
const admin=computed(()=>(props.currentUser||me.value)?.role==='admin')
watch(state,value=>emit('state',value),{deep:true,immediate:true})
watch(()=>props.view,()=>state.value={dirty:false,busy:false})
function syncTheme(){frame.value?.contentWindow?.postMessage(themeSyncPayload(),location.origin)}
watch([themeDark,themeMode,themeTypography,themeDesign,themeColorway,themeDensity],syncTheme)
function message(event){
 if(event.origin!==location.origin||event.source!==frame.value?.contentWindow)return
 const data=event.data
 if(data?.type==='workspace.dirty')state.value={dirty:data.dirty===true,busy:data.busy===true}
 if(data?.type==='platform.open'&&['apps','resources','plugins'].includes(data.view))emit('switch',data.view)
 if(data?.type==='platform.auth.required'&&window.parent!==window)window.parent.postMessage(data,location.origin)
}
async function canLeave(){if(state.value.busy){toast('操作正在进行，请等待结果后关闭','warning');return false}return !state.value.dirty||await confirmAction('存在未保存的编辑内容，离开将放弃这些内容。已保存配置不受影响。','放弃未保存的编辑？',{confirmButtonText:'放弃并关闭',cancelButtonText:'继续编辑'})}
onMounted(()=>{if(!props.currentUser)fetch('/api/me').then(r=>r.ok?r.json():null).then(v=>me.value=v).catch(()=>{});window.addEventListener('message',message)})
onBeforeUnmount(()=>window.removeEventListener('message',message))
defineExpose({canLeave})

import {confirmAction} from '../shadcnFeedback.js'


import {Empty,EmptyContent,EmptyDescription,EmptyHeader} from '../shadcn-ui.js'
</script>
<template><div v-if="view==='mcp'" class="platform-mcp-content"><iframe v-if="mcpAvailable" ref="frame" title="MCP 访问管理" src="/api/plugins/devtools/ui?panel=1" @load="syncTheme"/><Empty v-else><EmptyHeader><EmptyDescription>{{"MCP 服务尚未启用"}}</EmptyDescription></EmptyHeader></Empty></div><PageSurface v-else class="platform-content-surface"><ApplicationCenterView v-if="view==='apps'" @state="state=$event"/><ResourcesView v-else-if="view==='resources'" plugin="platform" :directAPI="directAPI" @state="state=$event"/><template v-else-if="view==='plugins'&&admin"><PageHeader title="插件管理" help="插件独立安装、配置和启停；安装不会自动启用。管理员管理插件，成员按各能力授权使用。"/><ExtensionManager embedded :show="active" @state="state=$event"/></template><Empty v-else><EmptyHeader><EmptyDescription>{{"此页面不可用或没有访问权限"}}</EmptyDescription></EmptyHeader></Empty></PageSurface></template>
<style scoped>.platform-mcp-content{height:100%;min-height:0;background:var(--panel)}.platform-mcp-content>iframe{display:block;width:100%;height:100%;border:0}.platform-content-surface{min-height:100%}</style>
