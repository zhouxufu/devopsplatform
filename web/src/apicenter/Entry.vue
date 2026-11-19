<script setup>
import {useRoute} from 'vue-router'
import Documents from './App.vue'
import Testing from './TestConsole.vue'
import TestCases from './TestCases.vue'
const route=useRoute()
</script>
<template><TestCases v-if="route.path==='/test-cases'"/><Testing v-else-if="['/testing','/test-execution'].includes(route.path)"/><Documents v-else/></template>
