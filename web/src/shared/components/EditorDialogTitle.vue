<script setup>


defineProps({title:String,fullscreen:Boolean,busy:Boolean,expandLabel:String,collapseLabel:String})
defineEmits(['toggle'])

import {Maximize2Icon as IconFullscreen,Minimize2Icon as IconFullscreenExit} from '@lucide/vue'


import {Button} from '../shadcn-ui.js'
</script>
<template><div class="editor-dialog-title"><span :title="title">{{title}}</span><Button :disabled="busy" :aria-label="fullscreen?(collapseLabel||'退出全屏编辑'):(expandLabel||'全屏编辑')" :title="fullscreen?(collapseLabel||'退出全屏'):(expandLabel||'全屏')" @click="$emit('toggle')" variant="ghost" size="sm" type="button"><IconFullscreenExit v-if="fullscreen"/><IconFullscreen v-else/>{{fullscreen?(collapseLabel||'还原'):(expandLabel||'全屏')}}</Button></div></template>
<style scoped>.editor-dialog-title{display:flex;align-items:center;gap:12px;min-width:0;width:100%;padding-right:30px;box-sizing:border-box}.editor-dialog-title>span{flex:1;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;text-align:left}</style>
