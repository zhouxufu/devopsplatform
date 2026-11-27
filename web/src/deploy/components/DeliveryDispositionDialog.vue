<script setup>
import { ref } from 'vue'
import ReasonDisclosure from '../../shared/ReasonDisclosure.vue'
import { Button, Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter, Field, FieldLabel, Textarea } from '../../shared/shadcn-ui.js'
const props=defineProps({decision:Object,busy:Boolean})
const emit=defineEmits(['cancel','confirm'])
const note=ref('')
</script>
<template>
  <Dialog :open="true" @update:open="open=>{if(!open&&!busy)emit('cancel')}"><DialogContent>
    <DialogHeader><DialogTitle>{{decision.action==='skip'?'本次跳过':'我已手动执行'}} · {{decision.name}}</DialogTitle>
      <DialogDescription>{{decision.environment}} · {{decision.target}}</DialogDescription></DialogHeader>
    <p class="text-sm">{{decision.action==='skip'?'该节点保留在原位置，本单不会派发。':'请确认已在目标环境完成本次冻结内容，并核对结果。登记后，依赖此节点的步骤可以继续。'}}</p>
    <p v-if="decision.affected.length" class="max-h-28 overflow-y-auto text-sm">{{decision.action==='skip'?'以下后续步骤仍需处理':'关联后续步骤'}}：{{decision.affected.join('、')}}</p>
    <ReasonDisclosure :required="decision.required" :filled="!!note"><Field><FieldLabel for="disposition-comment">处理说明{{decision.required?'（必填）':'（选填）'}}</FieldLabel><Textarea id="disposition-comment" v-model="note" :maxlength="2000"/></Field></ReasonDisclosure>
    <DialogFooter><Button variant="outline" :disabled="busy" @click="emit('cancel')">取消</Button><Button :disabled="busy || (decision.required&&!note.trim())" @click="emit('confirm',note.trim())">{{decision.action==='skip'?'确认跳过':'登记人工完成'}}</Button></DialogFooter>
  </DialogContent></Dialog>
</template>
