<script setup>
import {computed,ref,watch,onMounted,onBeforeUnmount} from 'vue'
import {ContextMenu,ContextMenuTrigger,ContextMenuContent,ContextMenuItem,ContextMenuSeparator,DropdownMenu,DropdownMenuTrigger,DropdownMenuContent,DropdownMenuItem,DropdownMenuSeparator,Button,Badge} from '../shared/shadcn-ui.js'
import {ContextMenuLabel,ContextMenuShortcut} from '@/components/ui/context-menu'
import HelpTip from '../shared/components/HelpTip.vue'
import {maxCaseSteps} from './caseModel.js'
import {flowReferences,edgeKey} from './testFlowModel.js'
import {Maximize2Icon,Minimize2Icon,UnplugIcon,PlusIcon,MinusIcon,Trash2Icon,ChevronDownIcon} from '@lucide/vue'
const props=defineProps({steps:{type:Array,default:()=>[]},selected:{type:String,default:''},disabled:Boolean,readonly:Boolean,graph:{type:Object,default:null},layout:{type:Object,default:null},expandable:Boolean,fullscreen:Boolean,fullscreenBusy:Boolean})
const emit=defineEmits(['select','inspect','connect','disconnect','add','remove','layout','toggle-fullscreen','add-document'])
const zoom=ref(1),pan=ref({x:20,y:28}),positions=ref({}),linkFrom=ref(''),linkBranch=ref(''),viewport=ref(null)
const selectedEdge=ref(''),menuNode=ref(''),menuEdge=ref(null),menuPoint=ref(null),spaceDown=ref(false)
const keys=computed(()=>props.steps.map((s,i)=>s.id||'legacy-'+i))
const refs=computed(()=>flowReferences(props.steps))
const status=s=>({pending:'未执行',running:'执行中',success:'通过',failed:'失败',canceled:'已取消',unknown:'待核对',skipped:'跳过'}[s]||'待执行')
const marker='flow-arrow-'+Math.random().toString(36).slice(2)
let drag=null
watch(keys,ids=>{const next={};ids.forEach((id,i)=>next[id]=positions.value[id]||{x:i*270,y:0});positions.value=next;if(!ids.includes(linkFrom.value))linkFrom.value=''}, {immediate:true})
function point(index){return positions.value[keys.value[index]]||{x:index*270,y:0}}
const edgeList=computed(()=>props.graph?.edges||props.steps.slice(1).map((s,i)=>({from:keys.value[i],to:keys.value[i+1]})))
const activeEdge=computed(()=>edgeList.value.find(e=>edgeKey(e)===selectedEdge.value))
const nodeName=id=>props.steps.find(s=>s.id===id)?.name||id
const edgeName=edge=>nodeName(edge.from)+' → '+nodeName(edge.to)+(edge.branch==='true'?'（真）':edge.branch==='false'?'（假）':'')
watch(edgeList,()=>{if(!activeEdge.value)selectedEdge.value=''}, {deep:true})
const edges=computed(()=>edgeList.value.map(e=>{const a=positions.value[e.from],b=positions.value[e.to];if(!a||!b)return null;return {...e,d:`M ${a.x+230} ${a.y+62} C ${a.x+255} ${a.y+62}, ${b.x-30} ${b.y+62}, ${b.x-7} ${b.y+62}`,x:(a.x+b.x+230)/2,y:(a.y+b.y)/2+54}}).filter(Boolean))
function remember(){if(!props.readonly)emit('layout',{positions:JSON.parse(JSON.stringify(positions.value)),zoom:zoom.value,pan:{...pan.value}})}
function applyLayout(value){if(value&&Object.keys(value.positions||{}).length){positions.value=Object.fromEntries(keys.value.map(id=>[id,value.positions[id]||positions.value[id]]));zoom.value=value.zoom;pan.value={...value.pan}}}
watch(()=>props.layout,applyLayout,{deep:true})
function finishDrag(){const changed=drag?.changed;drag=null;if(changed)remember()}

function fit(){
 const width=viewport.value?.clientWidth||700,height=viewport.value?.clientHeight||390,points=Object.values(positions.value)
 if(!points.length)return
 const left=Math.min(...points.map(p=>p.x)),top=Math.min(...points.map(p=>p.y)),right=Math.max(...points.map(p=>p.x+230)),bottom=Math.max(...points.map(p=>p.y+150))
 zoom.value=Math.max(.4,Math.min(1,(width-40)/(right-left),(height-56)/(bottom-top)))
 pan.value={x:(width-(right-left)*zoom.value)/2-left*zoom.value,y:(height-(bottom-top)*zoom.value)/2-top*zoom.value}
}
function arrange(){const cols=Math.max(1,Math.floor(((viewport.value?.clientWidth||700)-40)/270));positions.value=Object.fromEntries(keys.value.map((id,i)=>[id,{x:(i%cols)*270,y:Math.floor(i/cols)*190}]));fit();remember()}
function start(event,id){
 if(props.disabled||![0,1].includes(event.button))return
 const panning=event.button===1||spaceDown.value
 if(!panning&&event.target.closest('button,input,textarea,a,[role="button"]'))return
 event.preventDefault();viewport.value?.focus({preventScroll:true});selectedEdge.value=''
 if(panning)id=null
 else if(id)emit('select',keys.value.indexOf(id));else{emit('select',-1);clearConnection()}
 drag={id,changed:false,x:event.clientX,y:event.clientY,origin:{...(id?positions.value[id]:pan.value)}};event.currentTarget.setPointerCapture(event.pointerId)
}
function move(event){if(!drag)return;if(Math.abs(event.clientX-drag.x)+Math.abs(event.clientY-drag.y)>1)drag.changed=true;const scale=drag.id?zoom.value:1,p={x:drag.origin.x+(event.clientX-drag.x)/scale,y:drag.origin.y+(event.clientY-drag.y)/scale};if(drag.id)positions.value={...positions.value,[drag.id]:p};else pan.value=p}
function canvasPoint(event){const r=viewport.value.getBoundingClientRect();return {x:(event.clientX-r.left-pan.value.x)/zoom.value,y:(event.clientY-r.top-pan.value.y)/zoom.value}}
function openMenu(event){menuNode.value=event.target.closest('[data-node-id]')?.dataset.nodeId||'';const key=event.target.closest('[data-edge-key]')?.dataset.edgeKey;menuEdge.value=edgeList.value.find(e=>edgeKey(e)===key)||null;menuPoint.value=canvasPoint(event);if(menuNode.value){selectedEdge.value='';emit('select',keys.value.indexOf(menuNode.value))}else if(menuEdge.value)selectEdge(menuEdge.value)}
function addAt(kind){emit('add',kind,menuPoint.value)}
function inspect(index){emit('select',index);emit('inspect',index)}
function doubleClick(event){const node=event.target.closest('[data-node-id]');if(node){inspect(keys.value.indexOf(node.dataset.nodeId));return}if(!props.readonly&&!props.disabled&&!event.target.closest('[data-edge-key]'))emit('add','http',canvasPoint(event))}
function wheel(event){if(props.disabled)return;if(event.ctrlKey||event.metaKey){const before=zoom.value,p=canvasPoint(event);zoom.value=Math.max(.4,Math.min(1.6,before*Math.exp(-event.deltaY*.002)));pan.value={x:pan.value.x+p.x*(before-zoom.value),y:pan.value.y+p.y*(before-zoom.value)}}else pan.value={x:pan.value.x-(event.shiftKey?event.deltaY:event.deltaX),y:pan.value.y-(event.shiftKey?event.deltaX:event.deltaY)};remember()}
function clearConnection(){linkFrom.value='';linkBranch.value=''}
function selectEdge(edge){if(props.disabled||props.readonly)return;selectedEdge.value=edgeKey(edge);clearConnection()}
function disconnect(){if(props.disabled||props.readonly||!activeEdge.value)return;emit('disconnect',{...activeEdge.value});selectedEdge.value=''}
function keydown(event){
 if(event.target.closest('input,textarea,select,[contenteditable="true"],[role="menu"]'))return
 const index=keys.value.indexOf(props.selected)
 if(event.key==='Escape'&&(linkFrom.value||selectedEdge.value)){event.preventDefault();event.stopPropagation();clearConnection();selectedEdge.value=''}
 else if(['Delete','Backspace'].includes(event.key)&&!props.disabled&&!props.readonly){if(activeEdge.value){event.preventDefault();event.stopPropagation();disconnect()}else if(index>=0&&props.steps.length>1){event.preventDefault();event.stopPropagation();emit('remove',index)}}
 else if(event.key==='Enter'&&event.target===viewport.value&&index>=0){event.preventDefault();inspect(index)}
 else if(event.code==='Space'&&event.target===viewport.value){event.preventDefault();spaceDown.value=true}
 else if(event.target===viewport.value&&['+','=','-','0'].includes(event.key)){event.preventDefault();if(event.key==='0'){fit();remember()}else zoomBy(event.key==='-'?-.1:.1)}
}
function connect(id,branch=''){selectedEdge.value='';if(!linkFrom.value){linkFrom.value=id;linkBranch.value=branch;return}const from=linkFrom.value,selectedBranch=linkBranch.value;clearConnection();if(from!==id)emit('connect',from,id,selectedBranch)}
function zoomBy(amount){zoom.value=Math.max(.4,Math.min(1.6,Math.round((zoom.value+amount)*10)/10));remember()}
onMounted(()=>{if(Object.keys(props.layout?.positions||{}).length)applyLayout(props.layout);else fit()})
onBeforeUnmount(()=>drag=null)
</script>
<template>
 <section class="test-flow" :class="{'flow-expanded':fullscreen}" aria-label="接口测试画布" @keydown="keydown">
  <div class="flow-toolbar"><div class="flex flex-wrap items-center gap-2"><Badge variant="secondary">{{graph?'图执行':'顺序执行'}} · {{steps.length}} 个节点</Badge><HelpTip text="单击选中，双击节点打开配置，双击空白添加接口。右键打开快捷菜单；Delete/Backspace 删除选中节点或连线，Esc 取消连线。拖动空白/鼠标中键/空格拖动平移，滚轮平移，Ctrl/⌘+滚轮缩放，0 适应视图。"/><span v-if="linkFrom" class="text-sm" role="status">{{nodeName(linkFrom)}}{{linkBranch==='true'?'（真）':linkBranch==='false'?'（假）':''}}：请选择后继节点<Button type="button" variant="ghost" size="sm" @click="clearConnection">取消连线</Button></span></div><div class="flow-tools">
<div class="flow-zoom" role="group" aria-label="画布缩放">
<Button type="button" variant="ghost" size="icon-sm" @click="zoomBy(-.1)" aria-label="缩小画布"><MinusIcon/></Button>
<span class="text-xs tabular-nums">{{Math.round(zoom*100)}}%</span>
<Button type="button" variant="ghost" size="icon-sm" @click="zoomBy(.1)" aria-label="放大画布"><PlusIcon/></Button>
<Button type="button" variant="ghost" size="sm" @click="fit();remember()">适应视图</Button>
</div>
<DropdownMenu v-if="!readonly"><DropdownMenuTrigger as-child><Button type="button" variant="outline" size="sm" :disabled="disabled||steps.length>=maxCaseSteps"><PlusIcon/>添加节点<ChevronDownIcon/></Button></DropdownMenuTrigger><DropdownMenuContent align="end"><DropdownMenuItem @select="emit('add-document')">从接口文档添加</DropdownMenuItem><DropdownMenuItem @select="emit('add','http')">HTTP 请求节点</DropdownMenuItem><DropdownMenuItem @select="emit('add','dubbo')">Dubbo 调用节点</DropdownMenuItem><DropdownMenuSeparator/><DropdownMenuItem v-for="kind in [{id:'condition',name:'条件分支'},{id:'parallel',name:'并行开始'},{id:'join',name:'汇合'}]" :key="kind.id" @select="emit('add',kind.id)">{{kind.name}}</DropdownMenuItem></DropdownMenuContent></DropdownMenu>
<Button v-if="!readonly" type="button" variant="ghost" size="icon-sm" aria-label="删除选中节点" title="删除选中节点（Delete）" :disabled="disabled||steps.length<=1||!selected" @click="emit('remove',keys.indexOf(selected))"><Trash2Icon/></Button>
<Button v-if="expandable" type="button" variant="outline" size="sm" :disabled="fullscreenBusy" :aria-label="fullscreen?'退出画布全屏':'进入画布全屏'" @click="emit('toggle-fullscreen')"><Minimize2Icon v-if="fullscreen"/><Maximize2Icon v-else/>{{fullscreen?'退出画布全屏':'全屏画布'}}</Button>
<slot name="tools"/>
</div></div>
  <ContextMenu><ContextMenuTrigger as-child><div ref="viewport" class="flow-viewport" tabindex="0" aria-label="画布操作区域" @keyup.space="spaceDown=false" @blur="spaceDown=false" @contextmenu="openMenu" @dblclick="doubleClick" @wheel.prevent="wheel" @pointerdown.self="start($event,null)" @pointermove="move" @pointerup="finishDrag" @pointercancel="finishDrag">
  <div v-if="activeEdge&&!readonly" class="flow-edge-actions" role="group" aria-label="选中连线操作"><span class="min-w-0 flex-1 break-words text-sm" role="status">{{edgeName(activeEdge)}}</span><Button type="button" variant="outline" size="sm" :disabled="disabled" @click="disconnect"><UnplugIcon/>断开连线</Button><Button type="button" variant="ghost" size="sm" @click="selectedEdge=''">取消选择</Button></div>
   <div class="flow-plane" :style="{transform:`translate(${pan.x}px,${pan.y}px) scale(${zoom})`}">
    <svg class="flow-edges" :aria-hidden="readonly||undefined"><defs><marker :id="marker" markerWidth="7" markerHeight="7" refX="6" refY="3.5" orient="auto"><path d="M0 0 L7 3.5 L0 7" fill="currentColor"/></marker></defs><g v-for="edge in edges" :key="edgeKey(edge)" :data-edge-key="edgeKey(edge)" class="flow-edge" :class="{'flow-edge-selected':selectedEdge===edgeKey(edge)}" :role="readonly?undefined:'button'" :tabindex="readonly||disabled?-1:0" :aria-label="'选择连线 '+edgeName(edge)" :aria-pressed="selectedEdge===edgeKey(edge)" @pointerdown.stop @click.stop="selectEdge(edge)" @focus="selectEdge(edge)" @keydown.enter.prevent.stop="selectEdge(edge)" @keydown.space.prevent.stop="selectEdge(edge)"><path class="flow-edge-hit" :d="edge.d" fill="none" vector-effect="non-scaling-stroke"/><path class="flow-edge-line" :d="edge.d" fill="none" vector-effect="non-scaling-stroke" :marker-end="`url(#${marker})`"/><text v-if="edge.branch" :x="edge.x" :y="edge.y" fill="currentColor">{{edge.branch==='true'?'真':'假'}}</text></g></svg>
    <div v-for="(step,i) in steps" :key="keys[i]" :data-node-id="keys[i]" class="flow-node" :class="{'flow-selected':!selectedEdge&&selected===keys[i],'flow-link-source':linkFrom===keys[i]}" :style="{left:point(i).x+'px',top:point(i).y+'px'}" @pointerdown="start($event,keys[i])">
     <div class="flex items-center justify-between gap-2"><Badge variant="secondary">{{({condition:'条件',parallel:'并行开始',join:'汇合'})[step.kind]||step.request?.method||'GET'}}</Badge><span class="text-xs text-muted-foreground">{{i+1}} / {{steps.length}}</span></div>
     <Button type="button" variant="ghost" class="flow-node-name" :aria-pressed="selected===keys[i]" @click="emit('select',i);emit('inspect',i)">{{step.name||'未命名接口'}}</Button>
     <span v-if="step.service" class="text-xs text-muted-foreground truncate" :title="step.service">{{step.service}}</span><code class="flow-path">{{step.kind==='condition'?step.condition?.variable:['parallel','join'].includes(step.kind)?'':step.request?.path||'/'}}</code>
     <div class="flex flex-wrap items-center justify-between gap-1"><Badge :variant="['failed','unknown'].includes(step.status)?'destructive':'secondary'">{{status(step.status)}}</Badge><span class="text-xs text-muted-foreground">{{step.request?.assertions?.length||0}} 项断言</span><template v-if="!readonly"><Button v-if="linkFrom&&linkFrom!==keys[i]" data-port type="button" variant="outline" size="sm" :disabled="disabled" :aria-label="'接入节点 '+(step.name||i+1)" @click="connect(keys[i])">接到此处</Button><template v-else><Button v-for="branch in step.kind==='condition'?['true','false']:['']" :key="branch" data-port type="button" :variant="linkFrom===keys[i]?'default':'outline'" size="sm" :disabled="disabled" :aria-label="'连接节点 '+(step.name||i+1)+(branch==='true'?' 真':branch==='false'?' 假':'')" @click="connect(keys[i],branch)">{{branch==='true'?'真':branch==='false'?'假':'连接'}}</Button></template></template></div>
    </div>
   </div>
  </div>
  </ContextMenuTrigger><ContextMenuContent class="w-52">
   <template v-if="menuEdge&&!readonly"><ContextMenuLabel>连线</ContextMenuLabel><ContextMenuItem :disabled="disabled" @select="selectEdge(menuEdge);disconnect()">断开连线<ContextMenuShortcut>Del</ContextMenuShortcut></ContextMenuItem></template>
   <template v-else-if="menuNode"><ContextMenuLabel>{{nodeName(menuNode)}}</ContextMenuLabel><ContextMenuItem @select="inspect(keys.indexOf(menuNode))">查看节点配置<ContextMenuShortcut>Enter</ContextMenuShortcut></ContextMenuItem><template v-if="!readonly"><ContextMenuItem v-for="branch in steps[keys.indexOf(menuNode)]?.kind==='condition'?['true','false']:['']" :key="branch" :disabled="disabled" @select="connect(menuNode,branch)">从此节点连接{{branch==='true'?'（真）':branch==='false'?'（假）':''}}</ContextMenuItem><ContextMenuItem v-if="linkFrom&&linkFrom!==menuNode" :disabled="disabled" @select="connect(menuNode)">连接到此节点</ContextMenuItem><ContextMenuSeparator/><ContextMenuItem :disabled="disabled||steps.length<=1" @select="emit('remove',keys.indexOf(menuNode))">删除节点<ContextMenuShortcut>Del</ContextMenuShortcut></ContextMenuItem></template></template>
   <template v-else-if="!readonly"><ContextMenuLabel>添加节点</ContextMenuLabel><ContextMenuItem v-for="kind in ['http','dubbo','condition','parallel','join']" :key="kind" :disabled="disabled||steps.length>=maxCaseSteps" @select="addAt(kind)">{{({http:'HTTP 请求',dubbo:'Dubbo 调用',condition:'条件分支',parallel:'并行开始',join:'汇合'})[kind]}}</ContextMenuItem></template>
   <ContextMenuSeparator/><ContextMenuItem @select="fit();remember()">适应视图<ContextMenuShortcut>0</ContextMenuShortcut></ContextMenuItem><ContextMenuItem :disabled="disabled" @select="arrange()">自动排列节点</ContextMenuItem><ContextMenuItem v-if="linkFrom||selectedEdge" @select="clearConnection();selectedEdge=''">取消选择<ContextMenuShortcut>Esc</ContextMenuShortcut></ContextMenuItem>
  </ContextMenuContent></ContextMenu>
  <div v-if="refs.length" class="flow-bindings text-xs text-muted-foreground"><span v-for="r in refs" :key="r.from+r.to+r.name">{{steps.find(s=>s.id===r.from)?.name}} → {{steps.find(s=>s.id===r.to)?.name}}：<code v-text="'{{step.'+r.name+'}}'"/></span></div>
 </section>
</template>
<style scoped>
.test-flow{min-width:0;border:1px solid var(--border);border-radius:var(--radius);overflow:hidden}.flow-toolbar{display:flex;align-items:center;justify-content:space-between;flex-wrap:wrap;gap:8px;padding:8px 12px;border-bottom:1px solid var(--border)}.flow-tools{display:flex;flex-wrap:wrap;align-items:center;gap:8px;margin-left:auto}.flow-zoom{display:flex;align-items:center;gap:2px;padding-right:8px;border-right:1px solid var(--border)}.flow-zoom>span{min-width:36px;text-align:center}.flow-viewport{position:relative;min-width:0;height:390px;overflow:hidden;touch-action:none;cursor:grab;background-color:var(--background);background-image:radial-gradient(var(--border) 1px,transparent 1px);background-size:18px 18px}.flow-plane{position:absolute;inset:0;transform-origin:0 0;pointer-events:none}.flow-edges{position:absolute;width:100%;height:100%;overflow:visible;color:var(--muted-foreground)}.flow-edge-hit{stroke:transparent;stroke-width:20;pointer-events:stroke;cursor:pointer}.flow-edge-line{stroke:currentColor;stroke-width:1.5;pointer-events:none}.flow-edge text{pointer-events:none}.flow-edge:hover,.flow-edge:focus-visible,.flow-edge-selected{color:var(--primary);outline:none}.flow-edge:hover .flow-edge-line,.flow-edge:focus-visible .flow-edge-line,.flow-edge-selected .flow-edge-line{stroke-width:3}.flow-edge-actions{position:absolute;z-index:3;top:10px;left:10px;right:10px;border:1px solid var(--border);border-radius:var(--radius);display:flex;align-items:center;flex-wrap:wrap;gap:8px;padding:8px 12px;border-bottom:1px solid var(--border);background:var(--muted)}.flow-expanded{display:flex;flex:1;flex-direction:column;min-height:0}.flow-expanded .flow-viewport{height:auto;flex:1;min-height:160px}.flow-link-source{outline:2px dashed var(--primary);outline-offset:3px}.flow-node{position:absolute;width:230px;min-height:130px;padding:12px;display:flex;flex-direction:column;gap:8px;background:var(--card);color:var(--card-foreground);border:1px solid var(--border);border-radius:var(--radius);pointer-events:auto;touch-action:none;cursor:grab}.flow-selected{outline:2px solid var(--ring);outline-offset:2px}.flow-node-name{justify-content:flex-start;white-space:normal;height:auto;min-height:32px;padding:0;text-align:left;overflow-wrap:anywhere}.flow-path{font-size:12px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;color:var(--muted-foreground)}.flow-bindings{display:flex;gap:12px;flex-wrap:wrap;padding:10px 12px;border-top:1px solid var(--border)}
</style>
