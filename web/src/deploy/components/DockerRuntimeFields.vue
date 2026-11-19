<script setup>


import {computed} from 'vue'

const props=defineProps({config:{type:Object,required:true}})
const mounts=computed(()=>{try{const value=JSON.parse(props.config.mounts||'[]');return Array.isArray(value)?value:[]}catch{return []}})
const invalid=computed(()=>{try{return !Array.isArray(JSON.parse(props.config.mounts||'[]'))}catch{return true}})
function update(index,key,value){const rows=mounts.value.map(row=>({...row}));rows[index][key]=value;props.config.mounts=JSON.stringify(rows)}
function add(){props.config.mounts=JSON.stringify([...mounts.value,{source:'',target:'',read_only:true}])}
function remove(index){props.config.mounts=JSON.stringify(mounts.value.filter((_,i)=>i!==index))}



import {Button,Checkbox,Field,FieldLabel,Input,Textarea} from '../../shared/shadcn-ui.js'
</script>
<template><div class="fld"><label>网络模式 / Docker 网络名</label><Input v-model="config.network" placeholder="bridge（默认）、host 或已有网络名"/><small class="help">host 模式共享宿主机端口，不能同时配置端口映射；不会停止其他 Java 进程。</small></div><div v-if="config.network==='host'" class="fld"><label>宿主机监听端口（必填）</label><Input v-model="config.host_ports" placeholder="8889,20881"/><small class="help">用于发布前检查占用，不修改程序的实际监听端口。</small></div><div class="fld"><label>目录 / 文件挂载</label><template v-if="!invalid"><div v-for="(row,i) in mounts" :key="i" class="mount-row"><Field ><FieldLabel as="div">目标机源路径</FieldLabel><Input :model-value="row.source" placeholder="/data/models/bge-v1" @update:model-value="update(i,'source',$event)"/></Field><Field ><FieldLabel as="div">容器内路径</FieldLabel><Input :model-value="row.target" placeholder="/models/bge" @update:model-value="update(i,'target',$event)"/></Field><label class="mount-readonly"><Checkbox :model-value="row.read_only" @update:model-value="update(i,'read_only',$event===true)"/>只读</label><Button @click="remove(i)" variant="outline" size="default" type="button">移除</Button></div><Button @click="add" variant="outline" size="default" type="button">添加挂载</Button></template><template v-else><small role="alert">挂载配置格式无效，请修正 JSON 后保存。</small><Textarea v-model="config.mounts" :rows="({minRows:(3),maxRows:Math.max((3),16)}).minRows||4"/></template><small class="help">源路径位于实际部署目标机，须提前存在并可读取。不会自动创建目录、下载模型或分发文件。</small></div><div class="two"><div class="fld"><label>启动检查超时（秒）</label><Input v-model="config.startup_timeout_seconds" placeholder="120"/></div><div class="fld"><label>最短稳定观察（秒）</label><Input v-model="config.startup_observe_seconds" placeholder="15"/></div></div><small class="help">无 HTTP 检查时仍检查退出、OOM 和重启循环。启动观察通过不等于业务就绪，建议配置容器专属健康检查。</small></template>
<style scoped>.mount-row{display:grid;grid-template-columns:minmax(0,1fr) minmax(0,1fr) auto auto;gap:8px;align-items:end;margin-bottom:8px}.mount-row label{min-width:0}.mount-readonly{display:flex;align-items:center;gap:4px;min-height:34px}@media(max-width:640px){.mount-row{grid-template-columns:minmax(0,1fr) auto}.mount-row>label:not(.mount-readonly){grid-column:1/-1}}</style>
