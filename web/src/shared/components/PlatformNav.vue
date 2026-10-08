<script setup>
const emit = defineEmits(['open'])
function open(view) {
  if (window.parent !== window) {
    window.parent.postMessage({ type: 'platform.open', view }, location.origin)
    return
  }
  emit('open', view)
}
</script>

<template>
  <div class="platform-nav">
    <span>平台</span>
    <button type="button" @click="open('apps')">📦<i>应用</i></button>
    <button type="button" @click="open('resources')">🖥<i>资源</i></button>
  </div>
</template>

<style scoped>
.platform-nav{display:flex;flex-direction:column;gap:4px;width:100%;padding-top:7px;margin-top:3px;border-top:1px solid var(--border)}
.platform-nav>span{display:none;padding:0 9px;color:var(--dim);font-size:9px}
.platform-nav button{display:flex;flex-direction:column;align-items:center;gap:3px;padding:8px 0;width:100%;border:0;border-radius:8px;background:transparent;color:var(--muted);font:inherit;font-size:10px;cursor:pointer}
.platform-nav button i{font-style:normal}.platform-nav button:hover{color:var(--text);background:var(--panel2)}
</style>
