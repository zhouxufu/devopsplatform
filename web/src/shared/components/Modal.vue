<script setup>
// 对外 API 与旧版一致：:show :title :wide @close + 默认插槽
import { computed } from 'vue'
import { ElDialog } from 'element-plus'
import 'element-plus/dist/index.css'
import 'element-plus/theme-chalk/dark/css-vars.css'
const props = defineProps({ title: String, show: Boolean, wide: Boolean, extraWide: Boolean })
const emit = defineEmits(['close'])
const visible = computed({
  get: () => props.show,
  set: v => { if (!v) emit('close') },
})
</script>
<template>
  <ElDialog v-model="visible" :title="title" :width="extraWide ? 'min(960px, 92vw)' : wide ? 'min(560px, 92vw)' : 'min(480px, 92vw)'" append-to-body align-center>
    <slot />
  </ElDialog>
</template>
