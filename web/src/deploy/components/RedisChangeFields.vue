<script setup>
import {ElInput} from '../../shared/components/ui.js';
defineProps({model:{type:Object,required:true},disabled:Boolean});
</script>
<template>
  <label class="span">Redis 命令（每行一条）<ElInput v-model="model.commands" type="textarea" :rows="7" :disabled="disabled" placeholder="TYPE menu:tree&#10;TTL menu:tree&#10;UNLINK menu:tree&#10;EXPIRE menu:session 600" /></label>
  <p class="hint span">支持 PING、EXISTS、TYPE、TTL/PTTL、STRLEN/HLEN/LLEN/SCARD/ZCARD，以及 UNLINK、HDEL、EXPIRE/PEXPIRE、PERSIST。仅允许连接前缀内的精确 Key，不扩展通配符。含空格参数用引号；不支持 shell、SET、KEYS、FLUSH* 或 Lua。保存不执行；开发、测试、生产均须另一名环境审核人审核。无事务、无自动回滚，缓存应由业务重建。请勿粘贴密钥。</p>
</template>
<style scoped>.hint{color:var(--muted);font-size:12px;line-height:1.6;overflow-wrap:anywhere}label{min-width:0}</style>
