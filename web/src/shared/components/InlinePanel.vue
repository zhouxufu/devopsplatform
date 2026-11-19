<script setup>defineProps({show:Boolean})</script>
<template><section v-if="show" class="inline-panel"><slot/><div v-if="$slots.footer" class="inline-footer"><slot name="footer"/></div></section></template>
<style scoped>.inline-panel{min-width:0}.inline-footer{margin-top:16px;padding:12px 0;border-top:1px solid var(--border)}.inline-footer :deep(.footer){display:flex;gap:8px;align-items:center;flex-wrap:wrap}.inline-footer :deep(.footer>span){margin-right:auto}</style>
