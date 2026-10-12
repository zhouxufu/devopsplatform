<script setup>
import { computed, onMounted, onBeforeUnmount, ref } from 'vue'
import UiIcon from './UiIcon.vue'

// A browser preference shared across workspace iframes. Switching the layout
// never replaces the workspace or its editing session.
const key = 'lp-sidebar-mode'
const readMode = () => { try { return localStorage.getItem(key) === 'pinned' } catch { return false } }
const pinned = ref(readMode()), open = ref(false), sidebar = ref(null)
const small = ref(false), mobile = window.matchMedia('(max-width: 760px)')
const fixed = computed(() => pinned.value && !small.value)
const expanded = computed(() => fixed.value || open.value)
let hovered = false, explicit = false
function setMode() {
  pinned.value = !pinned.value
  try { localStorage.setItem(key, pinned.value ? 'pinned' : 'drawer') } catch {}
  window.dispatchEvent(new Event('sidebar-mode-change'))
  open.value = true
}
function toggle() { explicit = !open.value; open.value = explicit }
function enter(event) { if (event.pointerType === 'mouse' && !small.value) { hovered = true; open.value = true } }
function focusIn(event) { if (!event.target.closest('.sidebar-toggle')) open.value = true }
function leave() { hovered = false; if (!explicit && !sidebar.value?.querySelector(':focus-visible')) open.value = false }
function focusOut(event) { if (!hovered && !explicit && !sidebar.value?.contains(event.relatedTarget)) open.value = false }
function close() { explicit = false; open.value = false }
function outside(event) { if (!sidebar.value?.contains(event.target)) close() }
function escape(event) { if (event.key === 'Escape' && !fixed.value && open.value) { close(); event.preventDefault() } }
function syncMode() { pinned.value = readMode() }
function storage(event) { if (event.key === key || event.key === null) syncMode() }
function resize() { small.value = mobile.matches; close() }
onMounted(() => {
  resize()
  mobile.addEventListener('change', resize)
  window.addEventListener('storage', storage)
  window.addEventListener('sidebar-mode-change', syncMode)
  window.addEventListener('blur', close)
  document.addEventListener('pointerdown', outside)
})
onBeforeUnmount(() => {
  mobile.removeEventListener('change', resize)
  window.removeEventListener('storage', storage)
  window.removeEventListener('sidebar-mode-change', syncMode)
  window.removeEventListener('blur', close)
  document.removeEventListener('pointerdown', outside)
})
</script>

<template>
  <header ref="sidebar" class="workspace-sidebar" :class="{ 'is-open': expanded, 'is-pinned': fixed }"
    :data-mode="fixed ? 'pinned' : 'drawer'" @pointerenter="enter" @pointerleave="leave"
    @focusin="focusIn" @focusout="focusOut" @keydown="escape">
    <div class="sidebar-controls">
      <button type="button" class="sidebar-toggle" :aria-expanded="expanded" :disabled="fixed"
        :aria-label="expanded ? '收起导航抽屉' : '展开导航抽屉'" :title="fixed ? '导航已固定' : '展开 / 收起导航'" @click="toggle"><UiIcon name="sidebar"/></button>
      <button v-if="expanded && !small" type="button" class="sidebar-pin" :aria-pressed="fixed"
        :aria-label="fixed ? '切换抽屉导航' : '固定导航'" :title="fixed ? '取消固定，使用抽屉模式' : '固定导航，常驻显示'" @click="setMode"><UiIcon name="pin"/><span>{{fixed ? '已固定' : '固定'}}</span></button>
    </div>
    <slot />
  </header>
</template>

<style scoped>
.sidebar-controls{display:flex;align-items:center;gap:4px;min-height:36px;flex-shrink:0}
.sidebar-controls button{display:flex;align-items:center;justify-content:center;gap:6px;height:36px;border:0;border-radius:7px;background:transparent;color:var(--muted);cursor:pointer;font:inherit;font-size:12px;padding:8px}
.sidebar-toggle{width:52px;flex-shrink:0}.sidebar-pin{flex:1;white-space:nowrap}.sidebar-pin[aria-pressed=true]{background:var(--brand-soft);color:var(--brand-ink)}
.sidebar-controls button:hover:not(:disabled){background:var(--panel2);color:var(--text)}.sidebar-controls button:disabled{cursor:default}
@media(pointer:coarse){.sidebar-controls button{min-height:44px}}
</style>
