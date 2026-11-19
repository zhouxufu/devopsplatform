<script setup>
import {onMounted,ref} from 'vue'
import {Button,Field,FieldDescription,FieldLabel,Textarea} from '../shared/shadcn-ui.js'
import {copyText} from '../shared/clipboard.js'
defineProps({secret:{type:String,required:true},name:{type:String,default:''}})
const secretField=ref(null)
onMounted(()=>{secretField.value?.$el?.focus()})
</script>
<template>
 <div class="min-w-0 space-y-5">
  <p class="text-sm">已为「{{name}}」创建访问令牌。请立即复制并保存，关闭后无法再次查看完整密钥。</p>
  <Field>
   <FieldLabel for="mcp-created-secret">完整访问令牌（仅显示一次）</FieldLabel>
   <Textarea id="mcp-created-secret" ref="secretField" :model-value="secret" readonly :rows="3" spellcheck="false" autocomplete="off" class="resize-none break-all font-mono text-sm" @click="$event.target.select()"/>
   <FieldDescription>列表只保留标识前缀，不能从中恢复密钥。</FieldDescription>
  </Field>
  <Button type="button" class="w-full sm:w-auto" @click="copyText(secret)">复制完整令牌</Button>
 </div>
</template>
