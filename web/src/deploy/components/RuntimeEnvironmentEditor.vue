<script setup>



import {nextTick, ref} from 'vue'
import {editRuntimeVariable} from '../runtimeEnvironment.js'
const props = defineProps({draft: {type: Object, required: true}})
const editor = ref(null)
async function edit(key) {
  const next = editRuntimeVariable(props.draft.runtime_env_text, key)
  props.draft.runtime_env_text = next.text
  await nextTick()
  focusControl(editor.value)
  const textarea=editor.value?.$el||editor.value
  textarea?.setSelectionRange?.(next.start, next.end)
}
defineExpose({focus: () => focusControl(editor.value)})
function toggleDelete(key) {
  const keys = props.draft.runtime_env_delete_keys || []
  props.draft.runtime_env_delete_keys = keys.includes(key) ? keys.filter(value => value !== key) : [...keys, key]
}

import {focusControl} from '../../shared/focusControl.js'


import {Button,Checkbox,Textarea} from '../../shared/shadcn-ui.js'
</script>

<template><div class="runtime-variables"><div v-if="draft.runtime_env_keys?.length" class="saved-variables"><span>已保存 {{draft.runtime_env_keys.length}} 项 · 值已隐藏</span><ul><li v-for="key in draft.runtime_env_keys" :key="key" :class="{removed: draft.runtime_env_clear || draft.runtime_env_delete_keys?.includes(key)}"><code>{{key}}</code><Button :aria-label="`修改 ${key}`" @click="edit(key)" variant="outline" size="default" type="button" :disabled="(draft.runtime_env_clear || draft.runtime_env_delete_keys?.includes(key))">修改</Button><Button :aria-label="`${draft.runtime_env_delete_keys?.includes(key)?'撤销删除':'删除'} ${key}`" @click="toggleDelete(key)" variant="outline" size="default" type="button" :disabled="(draft.runtime_env_clear)">{{draft.runtime_env_delete_keys?.includes(key)?'撤销删除':'删除'}}</Button></li></ul></div><label for="runtime-variable-patch">新增或更新变量（含敏感变量）</label><Textarea ref="editor" v-model="draft.runtime_env_text" :disabled="draft.runtime_env_clear" placeholder="SPRING_PROFILES_ACTIVE=test&#10;NACOS_SERVER_ADDR=192.168.0.10:8848" v-bind="{&quot;id&quot;:&quot;runtime-variable-patch&quot;,&quot;aria-describedby&quot;:&quot;runtime-variable-help&quot;}" :rows="({minRows:(5),maxRows:Math.max((5),16)}).minRows||4"/><small id="runtime-variable-help">每行 KEY=value。同名更新，未填写的已有变量保留；KEY= 表示空值，不代表删除。变量加密保存，值不回显。</small><label v-if="draft.runtime_env_keys?.length" class="clear-variables"><Checkbox v-model="draft.runtime_env_clear"/>清空全部已保存变量（保存时确认）</label></div></template>

<style scoped>.runtime-variables{display:flex;flex-direction:column;gap:8px;min-width:0}.runtime-variables>label{font-weight:600}.runtime-variables small,.saved-variables>span{color:var(--muted-foreground);font-size:.75rem;line-height:1.6}.saved-variables ul{list-style:none;margin:8px 0;padding:0;display:flex;gap:8px;flex-wrap:wrap}.saved-variables li{display:flex;align-items:center;gap:8px;max-width:100%;border:1px solid var(--border);border-radius:var(--radius);padding:0 10px;background:var(--panel)}.saved-variables code{overflow-wrap:anywhere}.saved-variables .removed code{text-decoration:line-through;color:var(--muted-foreground)}.runtime-variables>.clear-variables{display:flex;justify-content:flex-start;align-items:center;gap:8px;min-height:44px;font-weight:400;color:var(--error);cursor:pointer}</style>
<style scoped></style>
