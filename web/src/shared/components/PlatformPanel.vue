<script setup>
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import ApplicationCenterView from '../pages/ApplicationCenterView.vue'
import ResourcesView from '../../deploy/pages/ResourcesView.vue'
import UiIcon from './UiIcon.vue'
const props = defineProps({
  view: { type: String, default: '' },
  plugin: { type: String, default: 'deploy' },
  directAPI: Boolean,
})
const emit = defineEmits(['close', 'switch'])
const dialog = ref(null)
let returnFocus
watch(() => !!props.view, async open => {
  if (open) {
    returnFocus = document.activeElement
    await nextTick()
    dialog.value?.querySelector('.close')?.focus()
  } else if (returnFocus?.isConnected) returnFocus.focus()
})
function onKeydown(event) {
  if (!props.view) return
  // Nested Element Plus dialogs manage their own keyboard focus / Escape.
  if ([...document.querySelectorAll('.el-dialog')].some(el => el.getClientRects().length)) return
  if (event.key === 'Escape') { event.preventDefault(); emit('close') }
  if (event.key === 'Tab') {
    const controls = [...(dialog.value?.querySelectorAll('button:not(:disabled),a[href],input:not(:disabled),select:not(:disabled),textarea:not(:disabled),[tabindex="0"]') || [])].filter(el => el.getClientRects().length)
    const first = controls[0], last = controls.at(-1)
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus() }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus() }
  }
}
onMounted(() => window.addEventListener('keydown', onKeydown))
onBeforeUnmount(() => window.removeEventListener('keydown', onKeydown))
</script>

<template>
  <div v-if="view" class="platform-layer">
    <button class="backdrop" type="button" aria-label="关闭公共中心并返回原页面" @click="$emit('close')"></button>
    <aside ref="dialog" class="platform-panel" role="dialog" aria-modal="true" :aria-label="view==='apps'?'应用中心':'资源中心'">
      <header>
        <div class="identity"><b>平台公共中心</b><small>关闭后返回原工作位置</small></div>
        <nav><button type="button" :class="{active:view==='apps'}" @click="$emit('switch','apps')"><UiIcon name="apps" :size="16"/> 应用中心</button><button type="button" :class="{active:view==='resources'}" @click="$emit('switch','resources')"><UiIcon name="resources" :size="16"/> 资源中心</button></nav>
        <button class="close" type="button" aria-label="关闭并返回原页面" title="关闭并返回原页面" @click="$emit('close')">×</button>
      </header>
      <main><ApplicationCenterView v-if="view==='apps'"/><ResourcesView v-else-if="view==='resources'" :plugin="plugin" :directAPI="directAPI"/></main>
    </aside>
  </div>
</template>

<style scoped>
.platform-layer{position:absolute;inset:0;z-index:90;pointer-events:none}.backdrop{position:absolute;inset:48px 0 0;border:0;background:rgba(0,0,0,.42);backdrop-filter:blur(1px);pointer-events:auto;cursor:default}.platform-panel{position:absolute;left:50%;top:calc(50% + 24px);width:min(1120px,calc(100% - 64px));height:min(820px,calc(100% - 88px));transform:translate(-50%,-50%);display:flex;flex-direction:column;overflow:hidden;border:1px solid var(--border);border-radius:12px;background:var(--bg);box-shadow:0 24px 70px rgba(0,0,0,.38);pointer-events:auto;animation:panel-in .16s ease-out}
.platform-panel>header{height:48px;display:flex;align-items:center;padding:0 14px;border-bottom:1px solid var(--border);background:var(--panel);flex-shrink:0}.identity{display:flex;align-items:baseline;gap:8px}.identity b{font-size:13px}.identity small{color:var(--dim);font-size:10px}.platform-panel nav{display:flex;align-self:stretch;margin-left:28px}.platform-panel nav button{padding:0 14px;border:0;border-bottom:2px solid transparent;background:transparent;color:var(--muted);cursor:pointer}.platform-panel nav button:hover{color:var(--text);background:var(--panel2)}.platform-panel nav button.active{color:var(--brand);border-bottom-color:var(--brand);font-weight:600}.platform-panel>header .close{margin-left:auto;width:30px;height:30px;border:0;border-radius:6px;background:transparent;color:var(--muted);font-size:20px;cursor:pointer}.platform-panel>header .close:hover{background:var(--panel2);color:var(--text)}
.platform-panel>main{flex:1;min-width:0;overflow:auto;padding:14px 16px}
@keyframes panel-in{from{transform:translate(-50%,-48%) scale(.99);opacity:.82}to{transform:translate(-50%,-50%) scale(1);opacity:1}}
@media(max-width:720px){.identity{display:none}.platform-panel{width:calc(100% - 24px);height:calc(100% - 72px)}.platform-panel nav{margin-left:0}.platform-panel nav button{padding:0 10px}}
.backdrop{inset:56px 0 0;background:rgb(8 18 33 / 46%);backdrop-filter:blur(4px)}.platform-panel{background:var(--bg);border-radius:16px;box-shadow:var(--shadow-float)}.platform-panel>header{height:60px;padding:0 20px}.identity b{font-size:14px}.identity small{font-size:11px}.platform-panel nav button{display:flex;align-items:center;gap:8px;font-size:13px}.platform-panel>main{padding:24px}.platform-panel>header .close{width:36px;height:36px}
</style>
