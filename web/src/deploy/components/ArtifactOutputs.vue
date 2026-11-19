<script setup>
import {copyText} from '../../shared/clipboard.js'
import {Button} from '@/components/ui/button'
import {Badge} from '@/components/ui/badge'
import ArtifactDownloadButton from './ArtifactDownloadButton.vue'
defineProps({artifacts:{type:Array,default:()=>[]},primaryId:[Number,String],disabled:Boolean,publishable:Boolean,primaryLabel:{type:String,default:'主制品'}})
const emit=defineEmits(['publish'])
const labels={'java-bundle':'Java 整包',jar:'Java JAR','oci-image':'Docker 镜像','docker-image-archive':'镜像离线包','static-archive':'静态资源包'}
function copy(value){return copyText(value,{successMessage:'已复制镜像地址',failureMessage:'复制失败，可选中地址手动复制'})}
</script>
<template><div class="grid min-w-0 gap-3"><section v-for="a in artifacts" :key="a.id" class="min-w-0 rounded-lg border p-4"><div class="flex flex-wrap items-center justify-between gap-3"><div class="flex min-w-0 flex-wrap items-center gap-2"><b>{{labels[a.kind]||a.kind}}</b><Badge variant="secondary">#{{a.id}}</Badge><Badge v-if="Number(a.id)===Number(primaryId)" variant="outline">{{primaryLabel}}</Badge><span class="text-sm text-muted-foreground">{{a.version}} · 构建 #{{a.source_id||'—'}}</span></div><div class="flex flex-wrap gap-2"><ArtifactDownloadButton :artifact="a" :disabled="disabled"/><Button v-if="publishable" variant="outline" size="sm" :disabled="disabled" @click="emit('publish',a)">发布此制品</Button><Button v-if="a.kind==='oci-image'" variant="outline" size="sm" @click="copy(a.uri)">复制镜像地址</Button></div></div><code class="mt-3 block break-all text-sm">{{a.digest}}</code><code v-if="a.kind==='oci-image'" class="mt-2 block break-all text-sm text-muted-foreground">{{a.uri}}</code><p v-if="a.metadata?.compiled_artifact_id" class="mt-2 text-sm text-muted-foreground">镜像编译输入：JAR #{{a.metadata.compiled_artifact_id}}</p><p v-if="a.metadata?.related_artifact_id" class="mt-2 text-sm text-muted-foreground">同次构建产出：JAR #{{a.metadata.related_artifact_id}}（未证明镜像包含关系）</p></section></div></template>
