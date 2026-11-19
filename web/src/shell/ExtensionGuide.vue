<script setup>

import {ref, computed} from 'vue'
import {marked} from 'marked'
import DOMPurify from 'dompurify'
import protocol from '../../../deploy/PLUGIN-PROTOCOL.md?raw'
import source from '../../../examples/capability-plugin/main.go?raw'
import build from '../../../examples/capability-plugin/build.sh?raw'
const topic=ref('start')
const sections={start:'快速开始',all:'完整协议',splunk:'Splunk 配置',workflow:'编排与文档插件'}
const content=computed(()=>topic.value==='start'?protocol.slice(protocol.indexOf('## 从零定义一个插件'),protocol.indexOf('## Splunk 安装后怎么配置')):topic.value==='workflow'?protocol.slice(protocol.indexOf('## 编排节点扩展 workflow.node.v1')):topic.value==='splunk'?protocol.slice(protocol.indexOf('## Splunk 安装后怎么配置'),protocol.indexOf('## 边界')):protocol)
const html=computed(()=>DOMPurify.sanitize(marked.parse(content.value)))
const download=value=>'data:text/plain;charset=utf-8,'+encodeURIComponent(value)



import {Button} from '../shared/shadcn-ui.js'
</script>
<template><section aria-label="插件开发规范" class="plugin-guide"><div class="guide-navigation"><Button v-for="(label,key) in sections" :key="key" :aria-pressed="topic===key" :class="{active:topic===key}" @click="topic=key" :variant="({primary:'default',secondary:'outline',text:'ghost',outline:'outline',dashed:'outline'})['secondary']||'outline'" :size="({small:'sm',mini:'xs',large:'lg',medium:'default'})['default']||'default'" :type="'button'">{{label}}</Button></div><div class="guide-downloads"><a :href="download(protocol)" download="PLUGIN-PROTOCOL.md">下载协议文档</a><a :href="download(source)" download="main.go">下载示例源码 main.go</a><a :href="download(build)" download="build.sh">下载打包脚本 build.sh</a></div><p class="guide-hint">示例两份文件放在同一目录，运行 bash build.sh 即可生成安装包。示例只做回显，不连接业务系统。</p><article v-html="html" class="protocol-content"></article></section></template>
<style scoped>.plugin-guide{min-width:0}.guide-navigation,.guide-downloads{display:flex;flex-wrap:wrap;gap:10px;margin-bottom:12px}.guide-downloads a{color:var(--brand);text-decoration:underline;font-size:.875rem}.guide-hint{font-size:.75rem;color:var(--muted-foreground)}.protocol-content{line-height:1.7;overflow-wrap:anywhere}.protocol-content :deep(pre){overflow:auto;padding:14px;border:1px solid var(--border);border-radius:var(--radius);background:var(--panel2);line-height:1.5}.protocol-content :deep(code){font-family:var(--mono);font-size:.75rem}.protocol-content :deep(table){display:block;overflow:auto;border-collapse:collapse;font-size:.875rem}.protocol-content :deep(th),.protocol-content :deep(td){padding:8px;border:1px solid var(--border);text-align:left}.protocol-content :deep(h2){margin-top:24px;font-size:1.25rem}.protocol-content :deep(h3){margin-top:20px;font-size:1.125rem}</style>
