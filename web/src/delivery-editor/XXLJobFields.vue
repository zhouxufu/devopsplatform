<script setup>
import { computed } from 'vue'
import OptionPicker from '../shared/components/OptionPicker.vue'
import { Accordion, AccordionContent, AccordionItem, AccordionTrigger, Field, FieldLabel, Input, Textarea } from '../shared/shadcn-ui.js'

const props = defineProps({
  model: { type: Object, required: true },
  disabled: Boolean,
  fieldId: { type: String, required: true },
})
const config = computed(() => props.model.config || {})
const hasConfig = computed(() => ['create', 'update'].includes(props.model.action))
const choices = {
  action: [['create', '创建（初始停用）'], ['update', '修改配置'], ['enable', '启用'], ['disable', '停用']],
  scheduleType: [['CRON', 'Cron'], ['FIX_RATE', '固定频率'], ['NONE', '仅手动触发']],
  misfireStrategy: [['DO_NOTHING', '忽略'], ['FIRE_ONCE_NOW', '立即补跑一次']],
  executorRouteStrategy: [['FIRST', '第一个'], ['LAST', '最后一个'], ['ROUND', '轮询'], ['RANDOM', '随机'], ['CONSISTENT_HASH', '一致性哈希'], ['LEAST_FREQUENTLY_USED', '最不经常使用'], ['LEAST_RECENTLY_USED', '最近最少使用'], ['FAILOVER', '故障转移'], ['BUSYOVER', '忙碌转移'], ['SHARDING_BROADCAST', '分片广播']],
  executorBlockStrategy: [['SERIAL_EXECUTION', '单机串行'], ['DISCARD_LATER', '丢弃后续调度'], ['COVER_EARLY', '覆盖之前调度']],
}
const options = type => choices[type].map(([value, label]) => ({ value, label }))
const basicFields = [{ key: 'jobDesc', label: '任务标题' }, { key: 'executorHandler', label: 'Handler' }, { key: 'author', label: '负责人' }]
const advancedFields = [{ key: 'alarmEmail', label: '告警邮箱' }, { key: 'executorTimeout', label: '超时秒数', type: 'number' }, { key: 'executorFailRetryCount', label: '失败重试次数', type: 'number' }]
const strategies = [{ key: 'executorRouteStrategy', label: '路由策略' }, { key: 'executorBlockStrategy', label: '阻塞策略' }, { key: 'misfireStrategy', label: '过期策略' }]
function setAction(action) {
  props.model.action = action
  if (action !== 'enable') delete props.model.created_by_change_id
  if (action === 'create') {
    delete props.model.target_title
    delete props.model.target_handler
  }
  if (action === 'enable' || action === 'disable') delete props.model.config
}
function setConfig(key, value, numeric = false) {
  const next = { ...config.value }
  if (value === undefined || value === null || (numeric && value === '')) delete next[key]
  else next[key] = numeric ? Number(value) : String(value)
  props.model.config = next
}
</script>

<template>
  <Field data-field="xxl_job">
    <FieldLabel as="div">任务操作</FieldLabel>
    <OptionPicker :model-value="model.action" :options="options('action')" :disabled="disabled" @update:model-value="setAction"/>
  </Field>
  <Field v-if="model.action === 'enable'">
    <FieldLabel :for="fieldId+'-creation'">前置创建变更编号</FieldLabel>
    <Input :id="fieldId+'-creation'" :model-value="model.created_by_change_id" type="number" min="1" :disabled="disabled" placeholder="启用已有任务时留空"
      @update:model-value="value => model.created_by_change_id=Number(value)||undefined"/>
    <p v-if="model.created_by_change_id" class="text-xs text-muted-foreground">在「前置变更」中添加此创建变更。若启用依赖新版服务，也添加对应的代码变更。</p>
  </Field>
  <template v-if="model.action !== 'create' && !model.created_by_change_id">
    <Field>
      <FieldLabel :for="fieldId+'-target'">现有任务标题</FieldLabel>
      <Input :id="fieldId+'-target'" v-model="model.target_title" maxlength="255" :disabled="disabled"/>
    </Field>
    <Field>
      <FieldLabel :for="fieldId+'-handler'">现有 Handler</FieldLabel>
      <Input :id="fieldId+'-handler'" v-model="model.target_handler" maxlength="255" :disabled="disabled"/>
    </Field>
  </template>
  <template v-if="hasConfig">
    <p v-if="model.action==='update'" class="text-xs text-muted-foreground md:col-span-2">仅填写本次需要调整的字段。参数留空可清空，使用「保留原值」可撤销这项修改。</p>
    <Field v-for="{key,label} in basicFields" :key="key" :data-field="key">
      <FieldLabel :for="fieldId+'-'+key">{{ label }}</FieldLabel>
      <Input :id="fieldId+'-'+key" :model-value="config[key]" :placeholder="model.action==='update'?'保留原值':undefined" :disabled="disabled"
        @update:model-value="value => setConfig(key,value || undefined)"/>
    </Field>
    <Field data-field="scheduleType">
      <FieldLabel as="div">调度方式</FieldLabel>
      <OptionPicker :model-value="config.scheduleType" :options="options('scheduleType')" :all-label="model.action==='update'?'保留原值':'请选择'" :disabled="disabled"
        @update:model-value="value => setConfig('scheduleType',value || undefined)"/>
    </Field>
    <Field v-if="config.scheduleType !== 'NONE'" data-field="scheduleConf">
      <FieldLabel :for="fieldId+'-schedule'">{{ config.scheduleType === 'FIX_RATE' ? '间隔秒数' : '调度规则' }}</FieldLabel>
      <Input :id="fieldId+'-schedule'" :model-value="config.scheduleConf" :placeholder="model.action==='update'?'保留原值':'0 0 2 * * ?'" :disabled="disabled"
        @update:model-value="value => setConfig('scheduleConf',value || undefined)"/>
    </Field>
    <Field class="md:col-span-2">
      <FieldLabel :for="fieldId+'-params'">任务参数</FieldLabel>
      <Textarea :id="fieldId+'-params'" :model-value="config.executorParam" rows="2" maxlength="512" :placeholder="model.action==='update'?'保留原值':undefined" :disabled="disabled"
        @update:model-value="value => setConfig('executorParam',value)"/>
      <button v-if="model.action==='update' && 'executorParam' in config" type="button" :disabled="disabled" class="self-start text-sm underline underline-offset-4" @click="setConfig('executorParam',undefined)">保留原值</button>
    </Field>
    <Accordion type="single" collapsible class="md:col-span-2">
      <AccordionItem value="advanced">
        <AccordionTrigger>执行策略与告警</AccordionTrigger>
        <AccordionContent class="grid gap-3 md:grid-cols-2">
          <Field v-for="{key,label} in strategies" :key="key">
            <FieldLabel as="div">{{ label }}</FieldLabel>
            <OptionPicker :model-value="config[key]" :options="options(key)" :all-label="model.action==='update'?'保留原值':'使用默认值'" :disabled="disabled"
              @update:model-value="value => setConfig(key,value || undefined)"/>
          </Field>
          <Field v-for="{key,label,type} in advancedFields" :key="key">
            <FieldLabel :for="fieldId+'-'+key">{{ label }}</FieldLabel>
            <Input :id="fieldId+'-'+key" :model-value="config[key]" :type="type || 'text'" min="0" :disabled="disabled"
              @update:model-value="value => setConfig(key,value,type==='number')"/>
            <button v-if="model.action==='update' && key in config" type="button" :disabled="disabled" class="self-start text-sm underline underline-offset-4" @click="setConfig(key,undefined)">保留原值</button>
          </Field>
        </AccordionContent>
      </AccordionItem>
    </Accordion>
  </template>
</template>
