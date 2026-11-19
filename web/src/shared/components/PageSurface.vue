<script setup>defineProps({fill:Boolean,flush:Boolean})</script>
<template><section data-slot="page-surface" class="min-w-0 bg-background text-foreground" :class="[fill?'flex h-full min-h-0 flex-col':'min-h-full',flush?'':'p-4 md:p-6']"><slot/></section></template>
