<script setup>
import {ElInput} from '../../shared/components/ui.js';
defineProps({model:{type:Object,required:true},disabled:Boolean});
</script>
<template>
  <label class="span" data-field="commands">Redis 命令（每行一条）<ElInput v-model="model.commands" type="textarea" :rows="7" :disabled="disabled" placeholder="TYPE menu:tree&#10;TTL menu:tree&#10;UNLINK menu:tree&#10;EXPIRE menu:session 600" /></label>
  <p class="hint span">支持 UNLINK、HDEL、EXPIRE/PEXPIRE、PERSIST 及基础检查命令。仅允许资源前缀内的精确 Key，不扩展通配符。保存不执行，审核按目标环境和入口策略决定；生产保护环境始终需要审核。非事务、无自动恢复；请勿粘贴密钥。</p>
</template>
<style scoped>.hint{color:var(--muted);font-size:12px;line-height:1.6;overflow-wrap:anywhere}label{min-width:0;grid-column:1/-1}.span{grid-column:1/-1}</style>
