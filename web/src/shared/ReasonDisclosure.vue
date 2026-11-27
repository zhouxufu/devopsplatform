<script setup>
import { ref } from 'vue'
import { Button } from './shadcn-ui.js'

defineProps({ required: Boolean, filled: Boolean })
const expanded = ref(false)
</script>

<template>
  <slot v-if="required || expanded || filled" />
  <Button v-else type="button" variant="ghost" size="sm" class="justify-self-start" @click="expanded = true">添加说明（选填）</Button>
</template>
