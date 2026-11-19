<script setup>
import {ref} from 'vue'
import {Button} from '@/components/ui/button'
import {toast} from '../../shared/composables/toast.js'
const props=defineProps({artifact:Object,disabled:Boolean})
const loading=ref(false)
async function download(){
 if(loading.value||props.disabled||!props.artifact?.id)return
 loading.value=true
 const current=props.artifact,url='api/artifacts/'+encodeURIComponent(current.id)+'/download'
 const controller=new AbortController(),timer=setTimeout(()=>controller.abort(),30000)
 try{
  // Preflight errors stay beside the original workflow. The actual file uses
  // the browser's streaming download, rather than buffering a large JAR in JS.
  const r=await fetch(url,{method:'HEAD',signal:controller.signal})
  if(!r.ok)throw Error(r.status===409?'制品不可下载：请核对构建状态、存储和摘要':r.status===401?'登录已过期，请重新登录后下载':'制品暂不可下载（'+r.status+'）')
  if(props.artifact?.id!==current.id)return
  const a=document.createElement('a');a.href=url;a.download='';a.click()
 }catch(e){toast(e.name==='AbortError'?'下载检查超时，请重试':e.message,'error')}
 finally{clearTimeout(timer);loading.value=false}
}
</script>
<template><Button v-if="artifact&&artifact.kind!=='oci-image'" variant="outline" size="sm" :disabled="disabled||loading" @click="download">{{loading?'检查中…':'下载制品'}}</Button></template>
