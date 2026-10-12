<script setup>
// 对外 API 与旧版一致：:show :title :wide @close + 默认插槽
import { computed } from 'vue'
import { ElDialog } from 'element-plus'
import 'element-plus/dist/index.css'
import 'element-plus/theme-chalk/dark/css-vars.css'
const props = defineProps({ title: String, show: Boolean, wide: Boolean, extraWide: Boolean, busy: Boolean, width: String })
const emit = defineEmits(['close'])
const visible = computed({
  get: () => props.show,
  set: v => { if (!v && props.show && !props.busy) emit('close') },
})
</script>
<template>
  <ElDialog v-model="visible" :title="title" :width="width || (extraWide ? 'min(1060px, 92vw)' : wide ? 'min(720px, 92vw)' : 'min(520px, 92vw)')" :close-on-click-modal="!busy" :close-on-press-escape="!busy" :show-close="!busy" append-to-body align-center>
    <slot />
  </ElDialog>
</template>
