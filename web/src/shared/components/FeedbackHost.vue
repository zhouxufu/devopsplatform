<script setup>
import {AlertDialog,AlertDialogContent,AlertDialogHeader,AlertDialogTitle,AlertDialogDescription,AlertDialogFooter,AlertDialogCancel,AlertDialogAction} from '@/components/ui/alert-dialog'
import {Toaster} from '@/components/ui/sonner'
import {pendingConfirmation,finishConfirmation} from '../shadcnFeedback.js'
import {ref,watch} from 'vue'
import {Field,FieldLabel,Textarea} from '../shadcn-ui.js'
const input=ref('')
watch(pendingConfirmation,()=>input.value='')
import {themeDark} from '../composables/theme.js'
</script>
<template><Toaster :theme="themeDark?'dark':'light'" position="top-right" close-button/><AlertDialog :open="!!pendingConfirmation"><AlertDialogContent @escape-key-down="finishConfirmation(false)"><AlertDialogHeader><AlertDialogTitle>{{pendingConfirmation?.title}}</AlertDialogTitle><AlertDialogDescription class="whitespace-pre-wrap leading-6">{{pendingConfirmation?.content}}</AlertDialogDescription></AlertDialogHeader><Field v-if="pendingConfirmation?.options.input"><FieldLabel for="confirmation-input">说明</FieldLabel><Textarea id="confirmation-input" v-model="input" autofocus :rows="3"/></Field><AlertDialogFooter><AlertDialogCancel @click="finishConfirmation(false)">{{pendingConfirmation?.options.cancelButtonText||'取消'}}</AlertDialogCancel><AlertDialogAction @click="finishConfirmation(true,pendingConfirmation?.options.input?input:true)">{{pendingConfirmation?.options.confirmButtonText||'确认'}}</AlertDialogAction></AlertDialogFooter></AlertDialogContent></AlertDialog></template>
