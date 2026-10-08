<script setup>
import { ref } from 'vue'
defineProps({
  text: { type: String, required: true },
  align: { type: String, default: 'left' },
})
const open = ref(false)
</script>

<template>
  <button class="help-tip" :class="`align-${align}`" type="button" :aria-label="text" :aria-expanded="open"
    @mouseenter="open = true" @mouseleave="open = false" @focus="open = true" @click.stop="open = true" @blur="open = false">
    ?
    <span v-if="open" class="help-popover" role="tooltip">{{ text }}</span>
  </button>
</template>

<style scoped>
.help-tip{position:relative;display:inline-grid;place-items:center;width:16px;height:16px;padding:0;border:1px solid var(--border);border-radius:50%;background:transparent;color:var(--muted);font-size:10px;font-weight:600;line-height:1;cursor:pointer;outline:none}
.help-tip:hover,.help-tip:focus-visible,.help-tip[aria-expanded="true"]{border-color:var(--brand);color:var(--brand)}
.help-popover{position:absolute;z-index:50;top:24px;left:-8px;width:max-content;max-width:520px;max-height:calc(100vh - 120px);overflow:auto;padding:10px 12px;border:1px solid var(--border);border-radius:7px;background:var(--panel);box-shadow:0 8px 24px rgba(0,0,0,.24);color:var(--text);font-size:11px;font-weight:400;line-height:1.55;white-space:pre-line;text-align:left;cursor:default}
.help-popover:before{content:'';position:absolute;top:-5px;left:10px;width:8px;height:8px;border-left:1px solid var(--border);border-top:1px solid var(--border);background:var(--panel);transform:rotate(45deg)}
.align-right .help-popover{left:auto;right:-8px}
.align-right .help-popover:before{left:auto;right:10px}
@media(max-width:600px){
  .help-popover{position:fixed;top:12px;left:12px!important;right:12px!important;width:auto;max-width:none;max-height:calc(100vh - 24px)}
  .help-popover:before{display:none}
}
</style>
