<script setup>
import {useRoute} from 'vue-router'
import Documents from './App.vue'
import ReportRecords from './ReportRecords.vue'
import Testing from './TestConsole.vue'
import TestCases from './TestCases.vue'
const route=useRoute()
</script>
<template><ReportRecords v-if="route.path==='/reports'"/><TestCases v-else-if="route.path==='/test-cases'"/><Testing v-else-if="['/testing','/test-execution'].includes(route.path)"/><Documents v-else/></template>
