<script setup>
import { computed } from 'vue'
import {Button} from '../../shared/shadcn-ui.js'
const emit=defineEmits(['edit','order'])
const props=defineProps({canEdit:Function,disabled:Boolean,steps:{type:Array,default:()=>[]}})
const hasDependencies=computed(()=>props.steps.some(step=>step.items?.some(item=>item.prerequisites?.length)))
const labels={planned:'计划执行',blocked:'待补齐',applied:'已有回执，跳过',not_applicable:'本环境无需执行'}
</script>
<template>
  <section v-if="steps.length" aria-label="本环境发布清单" class="space-y-2">
    <div class="flex flex-wrap items-center gap-3"><h4 class="font-medium">本环境发布清单</h4><Button size="sm" variant="outline" :disabled="disabled" @click="emit('order')">调整步骤顺序</Button></div>
    <p class="text-sm text-muted-foreground">{{hasDependencies?'以下顺序已按登记的前置变更校验。':'当前沿用流程顺序。需要先后执行的变更可在编辑时登记前置项。'}}</p>
    <ol class="space-y-3">
      <li v-for="(step,index) in steps" :key="step.node_id">
        <strong class="text-sm">{{index+1}}. {{step.name}}</strong>
        <ul class="space-y-1 pl-4 text-sm">
          <li v-for="item in step.items" :key="item.id">
            #{{item.id}} {{item.title}} · {{labels[item.status]}}
            <span v-if="item.reason" class="text-muted-foreground">（{{item.reason}}）</span>
            <Button v-if="canEdit?.(item.id)&&!['applied','not_applicable'].includes(item.status)" size="sm" variant="link" :disabled="disabled" @click="emit('edit',item.id,item.status!=='blocked')">{{item.status==='blocked'?'补充这项':'查看或调整前置关系'}}</Button>
            <div v-for="dep in item.prerequisites" :key="dep.change_id" class="text-muted-foreground">先完成 #{{dep.change_id}}：{{dep.reason}}</div>
          </li>
        </ul>
      </li>
    </ol>
  </section>
</template>
