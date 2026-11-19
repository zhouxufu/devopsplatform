<script setup>

import {computed} from 'vue'
const props=defineProps({nodes:{type:Array,default:()=>[]}})
const operations=computed(()=>props.nodes.filter(n=>['script','verify','extension'].includes(n.kind)||n.monitor))

import {ref as _viewRef} from 'vue'
const _view1=_viewRef([])


import {Accordion,AccordionContent,AccordionItem,AccordionTrigger} from '../../shared/shadcn-ui.js'
</script>
<template><Accordion v-if="operations.length" class="operation-preview platform-collapse" type="multiple" :model-value="(_view1||[]).map(String)" @update:model-value="_view1=$event"><AccordionItem :value="String(&quot;panel&quot;)"><AccordionTrigger>插件、脚本与验证快照 · {{operations.length}} 项</AccordionTrigger><AccordionContent force-mount v-show="!(!(_view1||[]).map(String).includes(String(&quot;panel&quot;)))"><article v-for="n in operations" :key="n.id"><h4>{{n.name}} · {{n.service||'本次交付'}}</h4><template v-if="n.extension"><p>插件：{{n.extension.provider}} · {{n.extension.provider_version}} · {{n.extension.executor}}</p><p v-if="n.extension.provider_digest">安装包：{{n.extension.provider_digest}}</p><p v-for="task in n.extension.documents||[]" :key="task.application_id">{{task.service}} · 制品 #{{task.artifact_id}} · Commit {{task.commit.slice(0,12)}} · {{task.plan.output_file}}</p><p v-if="n.extension.parameters">参数：{{JSON.stringify(n.extension.parameters)}}</p></template><template v-if="n.script"><p>执行主机：{{n.script.host}} · {{n.script.target||'预检时解析'}} · 超时 {{n.script.timeout_seconds}} 秒</p><pre>{{n.script.content}}</pre><p>参数：{{JSON.stringify(n.script.arguments||[])}}</p><small>内容和目标随执行单冻结；非零退出、超时或中断不自动重试。</small></template><template v-if="n.verification"><p>{{n.verification.mode==='manual'?'人工验证（非机器测试证明）':'HTTP 验证'}}</p><p>{{n.verification.mode==='manual'?(n.approvers||[]).join('、'):n.verification.url}}</p><small>绑定本次应用及制品摘要，不以普通 HTTP 200 或人工点击替代自动测试证明。</small></template><template v-if="n.monitor"><p>指标来源：{{n.monitor.url}}</p><p>预热 {{n.monitor.warmup_seconds}} 秒 · 窗口 {{n.monitor.window_seconds}} 秒 · 最低请求 {{n.monitor.min_requests}} · 错误率 ≤ {{n.monitor.max_error_rate}} · P95 ≤ {{n.monitor.max_p95_ms}} ms · 连续 {{n.monitor.consecutive}} 次</p><small>超时 {{n.monitor.timeout_seconds}} 秒；无数据、过期、批次身份不符均保持暂停。</small></template></article></AccordionContent></AccordionItem></Accordion></template>
<style scoped>.operation-preview{margin:12px 0}article{padding:8px 0;border-top:1px solid var(--border);margin-top:8px}h4{margin:6px 0}p,small{font-size:.75rem;overflow-wrap:anywhere}small{color:var(--muted-foreground)}pre{white-space:pre-wrap;overflow-wrap:anywhere;max-height:280px;overflow:auto;padding:10px;background:var(--panel2);border-radius:var(--radius)}</style>
