<script setup>
import {computed,nextTick,onBeforeUnmount,onMounted,ref,watch} from 'vue'
import {ResizablePanelGroup,ResizablePanel,ResizableHandle} from '@/components/ui/resizable'
const props=defineProps({canvasOnly:Boolean,inspectorOnly:Boolean})
const root=ref(null),canvas=ref(null),inspector=ref(null),width=ref(1000)
const direction=computed(()=>width.value<800?'vertical':'horizontal')
const inspectorMinimum=computed(()=>direction.value==='horizontal'?Math.min(45,360/width.value*100):25)
let observer,lastSize=40
function resized(size){if(size>0&&!props.canvasOnly&&!props.inspectorOnly)lastSize=size}
async function sync(){await nextTick();if(props.canvasOnly){canvas.value?.expand();inspector.value?.collapse()}else if(props.inspectorOnly){inspector.value?.expand();canvas.value?.collapse()}else{canvas.value?.expand();inspector.value?.expand();inspector.value?.resize(lastSize)}}
function layoutChanged(sizes){if(props.canvasOnly&&sizes[1]>0||props.inspectorOnly&&sizes[0]>0)sync()}
watch(()=>[props.canvasOnly,props.inspectorOnly],sync)
onMounted(()=>{observer=new ResizeObserver(([entry])=>width.value=entry.contentRect.width);observer.observe(root.value);sync()})
onBeforeUnmount(()=>observer?.disconnect())
</script>
<template>
 <div ref="root" class="case-workspace" :data-direction="direction">
  <ResizablePanelGroup :direction="direction" :keyboard-resize-by="3" @layout="layoutChanged">
   <ResizablePanel ref="canvas" :collapsible="inspectorOnly" :collapsed-size="0" :order="0" :default-size="60" :min-size="25" class="min-h-0 min-w-0"><div class="case-canvas-pane"><slot name="canvas"/></div></ResizablePanel>
   <ResizableHandle v-show="!canvasOnly&&!inspectorOnly" with-handle class="case-resize-handle" :aria-label="direction==='horizontal'?'调整节点配置面板宽度':'调整节点配置面板高度'" title="拖动调整配置区域，或使用方向键调整"/>
   <ResizablePanel ref="inspector" :order="1" :default-size="40" :min-size="inspectorMinimum" :max-size="inspectorOnly?100:70" :collapsible="canvasOnly" :collapsed-size="0" class="min-h-0 min-w-0" @resize="resized"><div v-show="!canvasOnly" class="case-inspector-pane"><slot name="inspector"/></div></ResizablePanel>
  </ResizablePanelGroup>
 </div>
</template>
<style scoped>
.case-workspace{height:100%;min-height:0;min-width:0}.case-canvas-pane{display:flex;flex-direction:column;height:100%;min-height:0;min-width:0;overflow:hidden}.case-inspector-pane{height:100%;min-width:0;overflow:auto;padding:4px 8px 12px 12px}.case-resize-handle{width:10px;margin-inline:3px;background:transparent}.case-resize-handle:after{width:14px}.case-resize-handle:hover,.case-resize-handle:focus-visible{background:var(--muted)}.case-resize-handle :deep(>div){height:36px;background:var(--muted-foreground)}.case-workspace[data-direction=vertical] .case-resize-handle{width:100%;height:10px;margin:3px 0}.case-workspace[data-direction=vertical] .case-inspector-pane{padding:8px 4px}
</style>
