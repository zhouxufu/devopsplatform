<script setup>
import { computed } from 'vue'
const props = defineProps({ ids: Array, nodes: Array, review: Boolean, limit: Number })
const names = computed(() => new Map((props.nodes || []).map(node => [node.id, node.name])))
</script>
<template>
  <section v-if="ids" class="my-3 min-w-0" aria-label="首批可执行节点">
    <h4 class="text-sm font-semibold">{{ ids.length > 1 ? '首批可并行派发' : '首批可派发' }}</h4>
    <ul v-if="ids.length" class="mt-1 flex flex-wrap gap-x-4 gap-y-1 text-sm"><li v-for="id in ids" :key="id">{{ names.get(id) || id }}</li></ul>
    <p v-else class="text-sm text-muted-foreground">没有可自动派发的节点，请查看执行顺序。</p>
    <p v-if="review || limit" class="text-sm text-muted-foreground"><span v-if="review">审批通过后可开始派发。</span><span v-if="limit">本单最多同时执行 {{ limit }} 个节点。</span></p>
  </section>
</template>
