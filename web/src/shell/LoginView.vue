<script setup>
import {onMounted,ref,nextTick} from 'vue'
import {BoxesIcon} from '@lucide/vue'
const props=defineProps({username:String,password:String,busy:Boolean,error:String,expired:Boolean,preserveWorkspace:Boolean})
const emit=defineEmits(['update:username','update:password','submit'])
const usernameInput=ref(null),passwordInput=ref(null)
onMounted(()=>nextTick(()=>{const input=props.expired?passwordInput.value:usernameInput.value;input?.$el?.focus()}))

import {Button,Card,CardContent,CardDescription,CardHeader,CardTitle,Field,FieldLabel,Input,Spinner} from '../shared/shadcn-ui.js'
</script>
<template><main class="fixed inset-0 z-[5000] flex min-h-svh flex-col items-center justify-center gap-7 overflow-auto bg-muted/30 p-5" aria-labelledby="login-title" @keydown.esc.stop><div class="flex items-center gap-2 text-sm font-semibold"><span class="flex size-8 items-center justify-center rounded-lg bg-primary text-primary-foreground"><BoxesIcon class="size-4"/></span>DevOps 控制台</div><Card class="w-full max-w-sm"><CardHeader><CardTitle id="login-title" class="text-xl">登录 DevOps 控制台</CardTitle><CardDescription>{{expired?'登录已过期，请重新登录':'研发与应用交付'}}</CardDescription></CardHeader><CardContent><form class="space-y-5" @submit.prevent="emit('submit')"><Field><FieldLabel for="platform-username">用户名</FieldLabel><Input id="platform-username" ref="usernameInput" :model-value="username" autocomplete="username" required :disabled="busy" placeholder="请输入用户名" @update:model-value="emit('update:username',$event)"/></Field><Field><FieldLabel for="platform-password">密码</FieldLabel><Input id="platform-password" ref="passwordInput" :model-value="password" type="password" autocomplete="current-password" required :disabled="busy" placeholder="请输入密码" @update:model-value="emit('update:password',$event)"/></Field><p v-if="expired" role="status" class="text-xs leading-5 text-muted-foreground">{{preserveWorkspace?'当前工作区和草稿保留，提交操作不会自动重试。':'登录后返回原页面，提交操作不会自动重试。'}}</p><p v-if="error" role="alert" class="text-sm text-destructive">{{error}}</p><Button type="submit" class="w-full" :disabled="busy"><Spinner v-if="busy"/>登录</Button></form></CardContent></Card></main></template>
