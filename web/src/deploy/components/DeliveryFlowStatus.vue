<script setup>
import {computed} from 'vue'
import {deliveryFlow} from '../deliveryFlow.js'
import {Badge} from '../../shared/shadcn-ui.js'
const props=defineProps({run:Object,phase:Number,error:String})
const flow=computed(()=>deliveryFlow(props.run,props.phase||1))
</script>
<template>
<section class="mb-5 min-w-0 space-y-3 border-b pb-4" aria-label="交付工作台进度">
 <div class="flex flex-wrap items-center justify-between gap-3"><div class="flex flex-wrap items-center gap-3"><h3 class="text-sm font-semibold">交付进度</h3><Badge :variant="['blocked','failed'].includes(run?.status)?'destructive':'secondary'">{{flow.label}}</Badge></div><span class="text-xs text-muted-foreground" role="status">{{error||(run?'状态自动更新，无需切换页面':'当前为交付草稿')}}</span></div>
 <ol class="grid grid-cols-5 gap-3 text-sm"><li v-for="(stage,i) in flow.stages" :key="stage" :aria-current="i===flow.index?'step':undefined" :class="i===flow.index?'font-semibold text-foreground':'text-muted-foreground'"><span class="mr-2 tabular-nums">{{i+1}}</span>{{stage}}</li></ol>
 <p class="text-sm leading-6">{{flow.next}}</p><p v-if="run?.blocked_reason" class="text-sm text-destructive">{{run.blocked_reason}}</p>
</section>
</template>
