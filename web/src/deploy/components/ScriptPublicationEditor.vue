<script setup>
import {Field,FieldLabel,FieldGroup} from '@/components/ui/field'
import {Input} from '@/components/ui/input'
import {Textarea} from '@/components/ui/textarea'
import OptionPicker from '../../shared/components/OptionPicker.vue'
import HelpTip from '../../shared/components/HelpTip.vue'
defineProps({config:{type:Object,required:true},runners:{type:Array,default:()=>[]},disabled:Boolean})
</script>
<template>
 <FieldGroup aria-label="脚本发布配置" class="min-w-0 gap-5">
  <div class="grid min-w-0 gap-4 sm:grid-cols-2">
   <Field><FieldLabel>执行 Runner<HelpTip text="在已登记的本机 Runner 上运行 Bash。只传入本次制品与应用变量，不继承平台进程凭据。"/></FieldLabel><OptionPicker v-model="config.script_runner" :disabled="disabled" :options="runners.filter(r=>r.enabled&&r.kind==='local').map(r=>({value:r.name,label:r.name}))" placeholder="选择执行 Runner" aria-label="脚本执行 Runner" width="100%"/></Field>
   <Field><FieldLabel>超时（秒）</FieldLabel><Input v-model="config.script_timeout_seconds" :disabled="disabled" inputmode="numeric" aria-label="脚本发布超时" placeholder="600"/></Field>
  </div>
  <Field><FieldLabel>发布脚本<HelpTip text='Bash 脚本。使用 "$LP_ARTIFACT_PATH" 读取本次制品，$LP_ARTIFACT_DIGEST 为摘要，$DELIVERY_ENVIRONMENT 为环境。向 "$LP_RESULT_FILE" 写入 JSON：{"status":"success","external_id":"外部执行编号","values":{}}。中断或缺少成功结果时须核对，不自动重跑。'/></FieldLabel><Textarea v-model="config.script" :disabled="disabled" rows="12" class="font-mono text-xs" aria-label="发布脚本" placeholder='set -Eeuo pipefail&#10;# 发布并验证本次制品，成功后写入结果文件'/></Field>
  <Field><FieldLabel>脚本参数（JSON 数组）<HelpTip text="每个字符串作为独立参数传入 $1、$2，不作为 Shell 命令拼接。凭据请放在应用变量中。"/></FieldLabel><Input v-model="config.script_arguments" :disabled="disabled" aria-label="发布脚本参数" placeholder='["作业标识"]' class="font-mono"/></Field>
  <Field><FieldLabel>并发保护标识（可选）<HelpTip text="默认按应用、环境互斥。多个应用操作同一外部任务时填写相同标识，避免同时发布；中断后保留保护，核对结果后解锁。"/></FieldLabel><Input v-model="config.script_concurrency_key" :disabled="disabled" aria-label="脚本并发保护标识" placeholder="默认按当前应用、环境保护"/></Field>
 </FieldGroup>
</template>
