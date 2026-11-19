<script setup>
import {computed} from 'vue'
import {highlightSQL} from '../sqlHighlight.js'
import '../sqlHighlight.css'
const props=defineProps({content:{type:String,default:''},label:{type:String,default:'SQL 脚本'}})
const html=computed(()=>highlightSQL(props.content))
</script>
<template><pre class="sql-code sql-highlight" tabindex="0" role="region" :aria-label="label"><code v-html="html"/></pre></template>
<style scoped>
.sql-code{min-width:0;max-width:100%;max-height:26rem;overflow:auto;margin:0;border:1px solid var(--border);border-radius:var(--radius);background:var(--muted);color:var(--foreground);padding:1rem;white-space:pre;tab-size:2;font: .8125rem/1.7 var(--mono)}
.sql-code:focus-visible{outline:2px solid var(--ring);outline-offset:2px}
.sql-code code{font:inherit;white-space:inherit}
</style>
