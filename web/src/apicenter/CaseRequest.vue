<script setup>
import {ref} from 'vue'
import RequestEditor from './RequestEditor.vue'
defineProps({step:{type:Object,required:true},available:{type:Array,default:()=>[]},disabled:Boolean})
const editor=ref(null)
defineExpose({focus:(field,index)=>editor.value?.focus(field,index)})
</script>
<template><RequestEditor ref="editor" :editor="step" :available="available" :disabled="disabled"/></template>
