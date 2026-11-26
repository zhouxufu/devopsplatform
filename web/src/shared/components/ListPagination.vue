<script setup>
import {computed,watch} from 'vue'
import OptionPicker from './OptionPicker.vue'
import {Pagination,PaginationContent,PaginationPrevious,PaginationNext} from '../shadcn-ui.js'
import {listPageSizes,paginationState} from '../pagination.js'
const props=defineProps({page:{type:Number,default:1},pageSize:{type:Number,default:20},total:{type:Number,default:0},disabled:Boolean,partial:Boolean})
const emit=defineEmits(['update:page','update:page-size'])
const state=computed(()=>paginationState(props.total,props.page,props.pageSize))
function resize(value){if(!props.disabled&&listPageSizes.includes(value)&&value!==props.pageSize)emit('update:page-size',value)}
watch(()=>[props.total,props.disabled],()=>{if(!props.disabled&&state.value.page!==props.page)emit('update:page',state.value.page)})
</script>
<template>
 <div data-slot="list-pagination" class="flex w-full min-w-0 flex-wrap items-center justify-end gap-x-3 gap-y-2 py-4" :aria-busy="disabled">
  <span class="mr-auto text-sm text-muted-foreground">共 {{total}} 条<span v-if="partial">（部分清单）</span></span>
  <div class="flex items-center gap-2 text-sm"><span>每页</span><OptionPicker aria-label="每页条数" class="w-20" :model-value="state.size" :options="listPageSizes.map(value=>({value,label:String(value)}))" :disabled="disabled" @update:model-value="resize"/><span>条</span></div>
  <Pagination class="mx-0 w-auto" :page="page" :total="total" :items-per-page="state.size" :disabled="disabled" @update:page="value=>emit('update:page',value)"><PaginationContent><PaginationPrevious/><span class="px-2 text-sm" aria-live="polite">{{page}} / {{state.pages}}</span><PaginationNext/></PaginationContent></Pagination>
 </div>
</template>
