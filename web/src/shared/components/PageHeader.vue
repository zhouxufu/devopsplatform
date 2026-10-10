<script setup>
import HelpTip from './HelpTip.vue'
defineProps({ title: String, help: String, headingTag: { type: String, default: 'h2' } })
</script>

<template>
  <div class="platform-page-header">
    <div class="page-header-title"><slot name="title"><component :is="headingTag">{{ title }}</component><HelpTip v-if="help" :text="help" /></slot></div>
    <div v-if="$slots.navigation" class="page-header-navigation"><slot name="navigation" /></div>
    <div v-if="$slots.actions" class="page-header-actions"><slot name="actions" /></div>
  </div>
</template>

<style scoped>
.platform-page-header{display:flex;align-items:center;flex-wrap:wrap;gap:8px 16px;min-height:44px;margin:0 0 12px;min-width:0}
.page-header-title{display:flex;align-items:center;gap:8px;min-width:0;max-width:100%;flex-shrink:0}
.page-header-title :deep(h2),.page-header-title :deep(h3){margin:0!important;font-size:20px!important;line-height:1.4}
.page-header-navigation{display:flex;align-items:center;flex-wrap:wrap;gap:8px;min-width:0;flex:1 1 auto}
.page-header-navigation :deep(nav){display:flex!important;align-items:center!important;flex-direction:row!important;flex-wrap:wrap;gap:4px!important;margin:0!important;padding:0!important;border:0!important;background:transparent!important}
.page-header-navigation :deep(nav button),.page-header-navigation :deep(nav a){display:inline-flex!important;align-items:center!important;justify-content:center!important;flex-direction:row!important;gap:6px!important;min-height:44px!important;padding:8px 12px!important;border:0!important;border-bottom:2px solid transparent!important;border-radius:6px 6px 0 0!important;background:transparent;color:var(--muted);font-size:13px!important;line-height:20px;white-space:nowrap;cursor:pointer;text-decoration:none}
.page-header-navigation :deep(nav .active){color:var(--brand-ink,var(--brand));background:var(--brand-soft);border-bottom-color:var(--brand)!important;font-weight:600}
.page-header-navigation :deep(nav button:hover),.page-header-navigation :deep(nav a:hover){background:var(--panel2)}
.page-header-actions{display:flex;align-items:center;flex-wrap:wrap;gap:8px;margin-left:auto;min-width:0}
.page-header-actions :deep(.el-button + .el-button){margin-left:0}
.page-header-actions :deep(.actions),.page-header-actions :deep(.buttons){display:flex;align-items:center;flex-wrap:wrap;gap:8px;margin:0!important;padding:0!important;border:0!important}
.page-header-navigation :deep(:focus-visible){outline:2px solid var(--brand);outline-offset:2px}
@media(max-width:760px){.platform-page-header{gap:8px 12px}.page-header-title :deep(h2),.page-header-title :deep(h3){font-size:18px!important}.page-header-navigation{order:3;flex-basis:100%}.page-header-navigation :deep(nav button),.page-header-navigation :deep(nav a){padding:8px 10px!important}.page-header-actions{flex-shrink:1}}
</style>
