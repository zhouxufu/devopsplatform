<script setup>
import {Empty,EmptyMedia,EmptyHeader,EmptyTitle,EmptyDescription,EmptyContent,Spinner} from '../shadcn-ui.js'
defineProps({title:String,message:String,loading:Boolean,error:Boolean})
</script>
<template><Empty :role="error?'alert':'status'" :aria-busy="loading"><EmptyMedia v-if="loading"><Spinner/></EmptyMedia><EmptyHeader><EmptyTitle>{{title}}</EmptyTitle><EmptyDescription v-if="message">{{message}}</EmptyDescription></EmptyHeader><EmptyContent v-if="$slots.default" class="flex flex-wrap justify-center gap-2"><slot/></EmptyContent></Empty></template>
