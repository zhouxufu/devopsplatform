<script setup>
import {ref,onMounted,onBeforeUnmount,watch} from 'vue'
import {basicSetup} from 'codemirror'
import {EditorState,Compartment,Prec} from '@codemirror/state'
import {EditorView,keymap,placeholder} from '@codemirror/view'
import {indentWithTab} from '@codemirror/commands'
import {sql,MySQL} from '@codemirror/lang-sql'
import {syntaxHighlighting} from '@codemirror/language'
import {sqlTokenHighlighter} from '../sqlHighlight.js'
import '../sqlHighlight.css'
import {autocompletion} from '@codemirror/autocomplete'
const props=defineProps({modelValue:{type:String,default:''},disabled:Boolean,script:Boolean,inputId:String,label:{type:String,default:''},kind:{type:String,default:'dbms'},suggestions:{type:Array,default:()=>[]}})
const emit=defineEmits(['update:modelValue','execute']),mount=ref(null)
const access=new Compartment(),language=new Compartment(),completion=new Compartment()
let view
const quote=value=>'`'+String(value).replaceAll('`','``')+'`'
function selection(){if(!view)return props.modelValue;const {from,to}=view.state.selection.main;return from===to?view.state.doc.toString():view.state.sliceDoc(from,to)}
function execute(){if(props.script)return false;if(!props.disabled)emit('execute',selection());return true}
function insert(value){if(!view||props.disabled)return;view.dispatch(view.state.replaceSelection(value));focusControl(view)}
function format(){const parts=props.modelValue.split(/('(?:''|[^'])*'|`[^`]*`|"(?:\\.|[^"\\])*")/g);emit('update:modelValue',parts.map((s,i)=>i%2?s:s.replace(/\s+/g,' ').replace(/\b(SELECT|FROM|LEFT JOIN|RIGHT JOIN|INNER JOIN|JOIN|WHERE|GROUP BY|ORDER BY|LIMIT|OFFSET|HAVING|UPDATE|SET|VALUES)\b/gi,v=>'\n'+v.toUpperCase())).join('').trim())}
function completions(){return autocompletion({override:[context=>{const word=context.matchBefore(/[\w`]+/);if(!word&&!context.explicit)return null;return {from:word?.from??context.pos,options:props.suggestions.map(name=>({label:String(name),type:'property',apply:quote(name)}))}}]})}
const enabled=()=>[EditorState.readOnly.of(props.disabled),EditorView.editable.of(!props.disabled)]
const syntax=()=>props.kind==='dbms'?sql({dialect:MySQL}):[]
onMounted(()=>{
 view=new EditorView({parent:mount.value,state:EditorState.create({doc:props.modelValue,extensions:[basicSetup,Prec.highest(keymap.of([...(props.script?[]:[{key:'Mod-Enter',run:execute}]),indentWithTab])),access.of(enabled()),language.of(syntax()),props.script?syntaxHighlighting(sqlTokenHighlighter):[],completion.of(completions()),EditorView.lineWrapping,placeholder(props.script?'填写 SQL 脚本…':props.kind==='dbms'?'编写查询 SQL…':'输入只读 Redis 命令，每行一条'),EditorView.contentAttributes.of({...(props.inputId?{id:props.inputId}:{}),'aria-label':props.label||(props.script?'SQL 脚本':props.kind==='dbms'?'查询 SQL':'查询命令（每行一条）'),'aria-multiline':'true'}),EditorView.updateListener.of(update=>{if(update.docChanged)emit('update:modelValue',update.state.doc.toString())}),EditorView.theme({'&':{height:'240px',fontSize:'.875rem',color:'var(--foreground)',backgroundColor:'var(--background)'},'.cm-scroller':{overflow:'auto',fontFamily:'var(--mono)'},'.cm-content':{padding:'12px 0',caretColor:'var(--foreground)'},'.cm-gutters':{backgroundColor:'var(--muted)',color:'var(--muted-foreground)',borderColor:'var(--border)'},'.cm-activeLine,.cm-activeLineGutter':{backgroundColor:'var(--accent)'},'.cm-cursor':{borderLeftColor:'var(--foreground)'},'.cm-selectionBackground, &.cm-focused .cm-selectionBackground':{backgroundColor:'var(--accent)'},'.cm-tooltip':{backgroundColor:'var(--popover)',color:'var(--popover-foreground)',borderColor:'var(--border)'},'&.cm-focused':{outline:'none'}})]})})
})
watch(()=>props.modelValue,value=>{if(view&&value!==view.state.doc.toString()){const cursor=Math.min(view.state.selection.main.head,value.length);view.dispatch({changes:{from:0,to:view.state.doc.length,insert:value},selection:{anchor:cursor}})}})
watch(()=>props.disabled,()=>view?.dispatch({effects:access.reconfigure(enabled())}))
watch(()=>props.kind,()=>view?.dispatch({effects:language.reconfigure(syntax())}))
watch(()=>props.suggestions,()=>view?.dispatch({effects:completion.reconfigure(completions())}),{deep:true})
onBeforeUnmount(()=>view?.destroy())
defineExpose({focus:()=>focusControl(view),execute,insert})

import {focusControl} from '../focusControl.js'


import {Button} from '../shadcn-ui.js'
</script>
<template><section class="query-code min-w-0 space-y-2" :class="{'sql-highlight':script}"><div v-if="!script" class="flex flex-wrap items-center gap-2"><span class="text-xs text-muted-foreground">{{kind==='dbms'?'SQL':'Redis'}} · Ctrl / ⌘ + Enter</span><Button v-if="kind==='dbms'" class="ml-auto" variant="ghost" size="sm" :disabled="disabled" @click="format">格式化</Button></div><div ref="mount" class="code-surface overflow-hidden rounded-lg border focus-within:ring-2 focus-within:ring-ring/50"/><div v-if="suggestions.length" class="completion flex flex-wrap gap-1"><Button v-for="field in suggestions" :key="field" variant="ghost" size="xs" :disabled="disabled" @click="insert(quote(field))">{{field}}</Button></div></section></template>
