<script setup>

import {ref} from 'vue'


const props=defineProps({item:Object,model:Object,disabled:Boolean,expanded:Boolean})
const editing=ref(props.expanded||false)
const label=w=>w?.release_required===true?'需上线':w?.release_required===false?'无需上线':'上线待确认'
function change(value){props.model.use_rule=value==='rule';props.model.release_required=value==='yes'?true:value==='no'?false:null}

import OptionPicker from '../../shared/components/OptionPicker.vue'



import {Accordion,AccordionContent,AccordionItem,AccordionTrigger,Field,FieldLabel,Input} from '../../shared/shadcn-ui.js'
</script>
<template><div class="release-intent-fields"><div class="intent-summary"><span>{{label(item)}}<small v-if="item?.release_origin"> · {{item.release_origin==='manual'?'人工调整':item.release_origin==='rule'?'规则识别':'待确认'}}</small></span></div><small v-if="item?.release_reason" class="r-muted">{{item.release_reason}}</small><Accordion class="platform-collapse" type="multiple" :model-value="(editing?['intent']:[]||[]).map(String)" @update:model-value="(keys=>editing=keys.length>0)($event)"><AccordionItem :disabled="disabled" :value="String(&quot;intent&quot;)"><AccordionTrigger>人工调整</AccordionTrigger><AccordionContent force-mount v-show="!(!(editing?['intent']:[]||[]).map(String).includes(String(&quot;intent&quot;)))"><div class="intent-edit"><Field ><FieldLabel as="div">生产交付判定</FieldLabel><OptionPicker :model-value="model.use_rule?'rule':model.release_required===true?'yes':model.release_required===false?'no':''" :options="[{value:'rule',label:'恢复自动规则'},{value:'yes',label:'需上线'},{value:'no',label:'无需上线'}]" all-label="保持当前判定" :disabled="disabled" :style="{width:'100%',maxWidth:'100%'}" @update:model-value="change"/></Field><Field v-if="model.release_required!==null&&model.release_required!==undefined&&!model.use_rule"><FieldLabel as="div">调整原因</FieldLabel><Input v-model="model.no_release_reason" :disabled="disabled" placeholder="调整原因（选填）"/></Field></div></AccordionContent></AccordionItem></Accordion></div></template>
<style scoped>.release-intent-fields{grid-column:1/-1;display:grid;gap:6px}.r-muted{color:var(--muted-foreground)}.intent-edit{display:grid;grid-template-columns:minmax(0,1fr) minmax(0,1fr);gap:12px}.intent-edit label{display:grid;gap:6px}@media(max-width:600px){.intent-edit{grid-template-columns:1fr}}</style>
