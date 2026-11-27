<script setup>
import { computed, ref } from 'vue'
import { useMediaQuery } from '@vueuse/core'
import { Badge, Button, Card, CardHeader, CardTitle, CardContent, Input, SidebarMenu, SidebarMenuButton, SidebarMenuItem } from '../../shared/shadcn-ui.js'
const props=defineProps({steps:Array, index:Number, label:Function, statusOf:Function, duration:Function})
const emit=defineEmits(['select'])
const query=ref(''), failures=ref(false), mobileOpen=ref(false)
const long=computed(()=>props.steps.length>8)
const mobile=useMediaQuery('(max-width: 767px)')
const inlineDetail=computed(()=>mobile.value && !long.value && !!props.steps[props.index])
const visible=computed(()=>props.steps.map((step,index)=>({step,index})).filter(({step})=>(!failures.value||['failed','interrupted','uncertain','needs_review'].includes(props.statusOf(step))) && [step.name,step.service,step.node_id].join(' ').toLowerCase().includes(query.value.trim().toLowerCase())))
function select(index){emit('select',index);mobileOpen.value=false}
</script>
<template>
  <Card class="min-w-0 self-start gap-3">
    <CardHeader class="flex flex-row items-center justify-between"><CardTitle>执行步骤 <span class="text-muted-foreground">{{steps.length}}</span></CardTitle><Button v-if="long" type="button" size="sm" variant="ghost" class="md:hidden" :aria-expanded="mobileOpen" @click="mobileOpen=!mobileOpen">{{mobileOpen?'收起':'选择步骤'}}</Button></CardHeader>
    <CardContent :class="['min-w-0 space-y-2', long && !mobileOpen && 'hidden md:block']">
      <div v-if="long" class="flex flex-wrap items-center gap-2"><Input v-model="query" aria-label="搜索应用或步骤" placeholder="搜索应用或步骤"/><Button size="sm" variant="outline" :aria-pressed="failures" @click="failures=!failures">{{failures?'显示全部':'只看异常'}}</Button></div>
      <SidebarMenu aria-label="选择执行步骤" :class="long && 'max-h-[50dvh] overflow-y-auto overscroll-contain'">
        <SidebarMenuItem v-for="{step,index:i} in visible" :key="step.node_id+':'+(step.service||'')"><SidebarMenuButton class="h-auto min-h-10 items-start whitespace-normal" :is-active="index===i" :aria-expanded="mobile && !long ? index===i : undefined" @click="select(i)"><div class="min-w-0 break-words"><div>{{i+1}}. {{step.name}}</div><small class="text-muted-foreground">{{label(step)}} {{duration(step)}}</small></div></SidebarMenuButton>
          <slot v-if="inlineDetail && index===i"/>
        </SidebarMenuItem>
      </SidebarMenu>
      <p v-if="!visible.length" role="status">没有匹配的步骤</p>
    </CardContent>
  </Card>
  <slot v-if="!inlineDetail"/>
</template>
