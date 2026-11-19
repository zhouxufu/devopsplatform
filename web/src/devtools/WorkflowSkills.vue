<script setup>
import {ref,onMounted,watch} from 'vue'
import {Badge,Button,Card,CardHeader,CardTitle,CardDescription,CardContent,CardFooter,Empty,EmptyHeader,EmptyDescription,Field,FieldLabel,Spinner,Textarea} from '../shared/shadcn-ui.js'
import OptionPicker from '../shared/components/OptionPicker.vue'
import {skillInstallPrompt} from './skillInstallation.js'
import {toast} from '../shared/composables/toast.js'
import {copyText} from '../shared/clipboard.js'
const skills=ref([]),loading=ref(false),failed=ref(false)
const scope=ref('project'),instruction=ref('')
watch(scope,()=>{instruction.value=''})
async function copyInstruction(items,mode='install'){
 try{instruction.value=skillInstallPrompt(items,{scope:scope.value,mode});await copyText(instruction.value)}
 catch(e){toast(e.message,'error')}
}
async function load(){
 if(loading.value)return
 loading.value=true;failed.value=false;instruction.value=''
 try{
  const response=await fetch('api/skills'),body=await response.json()
  if(!response.ok||!Array.isArray(body))throw Error(body.error||'读取 Skill 失败')
  skills.value=body
 }catch(e){failed.value=true;toast(e.message,'error')}finally{loading.value=false}
}
onMounted(load)
</script>
<template>
 <section class="mt-6 space-y-4" aria-labelledby="workflow-skills-title">
  <div class="flex flex-wrap items-end justify-between gap-3"><div><h3 id="workflow-skills-title" class="text-base font-semibold">配套工作流 Skill</h3><p class="mt-1 text-sm text-muted-foreground">复制指令到 Codex，通过现有 MCP 安装或更新。</p></div><Field class="w-full sm:w-52"><FieldLabel>安装范围</FieldLabel><OptionPicker v-model="scope" :options="[{value:'project',label:'仅当前项目（推荐）'},{value:'user',label:'个人全部项目'}]"/></Field></div>
  <div v-if="skills.length&&!loading" class="flex flex-wrap gap-2"><Button @click="copyInstruction(skills)">复制全部安装指令</Button><Button variant="outline" @click="copyInstruction(skills,'update')">复制全部更新指令</Button><Button variant="ghost" @click="load">重新读取版本</Button></div>
  <div v-if="loading" class="flex items-center gap-2 py-4" role="status"><Spinner/>正在读取 Skill</div>
  <Empty v-else-if="!skills.length"><EmptyHeader><EmptyDescription>{{failed?'未能读取 Skill':'暂未提供 Skill'}}</EmptyDescription></EmptyHeader><Button variant="outline" @click="load">重新读取</Button></Empty>
  <div v-else class="grid gap-4 md:grid-cols-2">
   <Card v-for="skill in skills" :key="skill.id" class="min-w-0">
    <CardHeader><CardTitle class="flex flex-wrap items-center gap-2">{{skill.name}}<Badge variant="secondary">{{skill.version}}</Badge></CardTitle><CardDescription>{{skill.description}}</CardDescription></CardHeader>
    <CardContent class="min-w-0 space-y-4">
     <div class="flex flex-wrap items-center gap-2 text-sm"><span class="text-muted-foreground">完整流程所需权限</span><Badge v-for="scope in skill.scopes" :key="scope" variant="outline">{{scope}}</Badge></div>
     <div class="rounded-md bg-muted p-3 text-sm break-words [overflow-wrap:anywhere]">{{skill.prompt}}</div>
     <p v-if="!skill.compatible" class="text-sm text-muted-foreground">当前 MCP 缺少所需工具，请更新平台后使用。</p>
    </CardContent>
    <CardFooter class="mt-auto flex flex-wrap gap-2"><Button @click="copyInstruction([skill])">复制安装指令</Button><Button variant="outline" @click="copyInstruction([skill],'update')">复制更新指令</Button><Button variant="ghost" as-child><a :href="'api/skills/'+encodeURIComponent(skill.id)+'/download'">下载 ZIP</a></Button><Button variant="ghost" @click="copyText(skill.prompt)">复制调用示例</Button></CardFooter>
   </Card>
  </div>
  <Card>
   <CardHeader><CardTitle>安装和更新</CardTitle></CardHeader>
   <CardContent class="space-y-3 text-sm leading-6">
    <ol class="list-decimal space-y-1 pl-5"><li>先连接平台 MCP；新增工具未出现时重新连接。</li><li>选择范围，复制指令并粘贴到 Codex。首次安装选择安装指令；更新指令只更新已有 Skill。</li><li>Codex 检查本地内容后安装并回读验证；冲突会保留原文件，更新会备份旧版本。</li></ol>
    <p class="text-muted-foreground">本页只显示平台提供的版本，无法直接读取电脑上的安装状态。安装不增加令牌权限；文件验证后仍未出现时，新开会话或重启 Codex。也可下载 ZIP 手动安装到项目或个人的 .agents/skills/。</p>
    <Field v-if="instruction"><FieldLabel for="skill-install-instruction">本次指令（可手动复制）</FieldLabel><Textarea id="skill-install-instruction" :model-value="instruction" readonly :rows="6" class="max-h-56 resize-y font-mono text-xs"/></Field>
    <a href="https://learn.chatgpt.com/docs/build-skills" target="_blank" rel="noopener noreferrer" class="underline underline-offset-4">查看官方安装说明</a>
   </CardContent>
  </Card>
 </section>
</template>
