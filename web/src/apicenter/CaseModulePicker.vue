<script setup>
import {computed,ref} from 'vue'
import {CheckIcon,ChevronRightIcon,ChevronsUpDownIcon} from '@lucide/vue'
import {TreeItem,TreeRoot} from 'reka-ui'
import {Popover,PopoverContent,PopoverTrigger} from '@/components/ui/popover'
import {Button,Input} from '../shared/shadcn-ui.js'
import {searchModuleTree} from './collectionModel.js'

const props=defineProps({modelValue:String,items:{type:Array,required:true}})
const emit=defineEmits(['update:modelValue'])
const open=ref(false),query=ref(''),expanded=ref([]),searchCollapsed=ref([])
const all={value:'',label:'全部模块'}
const active=computed(()=>({value:props.modelValue||''}))
const searching=computed(()=>!!query.value.trim())
const matches=computed(()=>searchModuleTree(props.items,query.value))
const items=computed(()=>searching.value?matches.value.items:[all,...props.items])
const visibleBranches=computed(()=>searching.value?matches.value.expanded.filter(value=>!searchCollapsed.value.includes(value)):expanded.value)

function changeOpen(value){
 open.value=value
 if(!value)return
 updateQuery('')
 const parts=(props.modelValue||'').split('/')
 const ancestors=parts.slice(0,-1).map((_,index)=>parts.slice(0,index+1).join('/'))
 expanded.value=[...new Set([...expanded.value,...ancestors])]
}
function updateQuery(value){
 query.value=value
 searchCollapsed.value=[]
}
function updateExpanded(values){
 if(searching.value)searchCollapsed.value=matches.value.expanded.filter(value=>!values.includes(value))
 else expanded.value=values
}
function toggleBranch(value,event){
 const visible=visibleBranches.value
 updateExpanded(visible.includes(value)?visible.filter(key=>key!==value):[...visible,value])
 event.currentTarget.closest('[role=treeitem]')?.focus()
}
function select(node){
 emit('update:modelValue',node.value)
 changeOpen(false)
}
</script>

<template>
 <Popover :open="open" @update:open="changeOpen">
  <PopoverTrigger as-child>
   <Button type="button" variant="outline" aria-label="用例模块筛选" class="w-45 min-w-0 justify-between" :title="modelValue||all.label">
    <span class="truncate">{{modelValue||all.label}}</span>
    <ChevronsUpDownIcon class="size-4 shrink-0 text-muted-foreground"/>
   </Button>
  </PopoverTrigger>
  <PopoverContent align="start" aria-label="用例模块筛选" class="w-76">
   <Input :model-value="query" aria-label="搜索用例模块" placeholder="搜索模块" @update:model-value="updateQuery"/>
   <p v-if="!items.length" class="px-2 py-1.5 text-sm text-muted-foreground">没有匹配的模块</p>
   <TreeRoot v-else v-slot="{flattenItems}" :items="items" :get-key="node=>node.value" :model-value="active" :expanded="visibleBranches" aria-label="用例模块" class="max-h-60 overflow-auto" @update:expanded="updateExpanded">
    <TreeItem v-for="row in flattenItems" :key="row._id" v-slot="{isExpanded,isSelected}" v-bind="row.bind" :aria-label="row.value.label" :title="row.value.value||all.label"
     class="flex min-h-8 min-w-0 cursor-pointer items-center gap-1 rounded px-2 py-0.5 text-sm outline-none hover:bg-accent focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring aria-selected:bg-accent"
     :style="{paddingInlineStart:`${8+(row.level-1)*16}px`}" @select.prevent="select(row.value)" @toggle="event=>{if(event.detail.originalEvent.type==='click')event.preventDefault()}">
     <button v-if="row.hasChildren" type="button" tabindex="-1" class="grid size-6 shrink-0 place-items-center rounded text-muted-foreground hover:bg-background hover:text-foreground" :aria-label="(isExpanded?'收起 ':'展开 ')+row.value.label" :aria-expanded="isExpanded" @mousedown.prevent @click.stop="toggleBranch(row._id,$event)">
      <ChevronRightIcon class="size-4" :class="{'rotate-90':isExpanded}"/>
     </button>
     <span v-else class="size-6 shrink-0" aria-hidden="true"/>
     <span class="min-w-0 flex-1 truncate">{{row.value.label}}</span>
     <CheckIcon v-if="isSelected" class="size-4 shrink-0" aria-hidden="true"/>
    </TreeItem>
   </TreeRoot>
  </PopoverContent>
 </Popover>
</template>
