<script setup>

import {computed} from 'vue'
const props=defineProps({result:Object,disabled:Boolean}),emit=defineEmits(['load','inspect','clear'])
const scans=computed(()=>(props.result?.rows||[]).filter(r=>r.argv?.[0]==='SCAN'&&Array.isArray(r.result)&&Array.isArray(r.result[1])))
function next(row){const argv=[...row.argv];argv[1]=String(row.result[0]);emit('load',argv.map(a=>JSON.stringify(String(a))).join(' '))}



import {Button} from '../shadcn-ui.js'
</script>
<template><section v-if="scans.length" class="redis-read-results"><article v-for="(row,i) in scans" :key="i"><div class="scan-head"><b>Key 查找结果 · {{row.result[1].length}} 项</b><small>{{String(row.result[0])==='0'?'本轮扫描结束':'下一游标 '+row.result[0]}} · SCAN 可能返回重复项，空页不代表结束</small><Button @click="next(row)" variant="outline" size="default" type="button" :disabled="(disabled||String(row.result[0])==='0')">载入下一页命令（不执行）</Button></div><div class="keys"><div v-for="(key,j) in row.result[1]" :key="j"><code>{{key}}</code><Button @click="emit('inspect',String(key))" variant="outline" size="default" type="button" :disabled="(disabled)">查看类型 / TTL</Button><Button @click="emit('clear',String(key))" variant="outline" size="default" type="button" :disabled="(disabled)">新建清理草稿</Button></div><p v-if="!row.result[1].length">本页没有匹配 Key，可继续扫描。</p></div></article></section></template>
<style scoped>.redis-read-results{border-top:1px solid var(--border);margin-top:12px;padding-top:12px;min-width:0}.scan-head{display:flex;align-items:center;gap:12px;flex-wrap:wrap;font-size:.875rem}.scan-head small{flex:1;color:var(--muted-foreground)}.keys{max-height:300px;overflow:auto;margin-top:8px}.keys>div{display:flex;align-items:center;gap:10px;border-bottom:1px solid var(--border);padding:6px 0;flex-wrap:wrap}.keys code{flex:1;min-width:160px;overflow-wrap:anywhere}</style>
