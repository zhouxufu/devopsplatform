<script setup>
import RichDescription from '../../deploy/components/RichDescription.vue'
defineProps({content:{type:String,default:''},label:{type:String,default:'变更说明'}})
</script>
<template><section class="change-markdown min-w-0" :aria-label="label"><RichDescription :content="content" format="markdown" :title="label" compact full/></section></template>
<style scoped>
.change-markdown :deep(.description-body){font-size:.875rem;line-height:1.7;min-width:0}
</style>
