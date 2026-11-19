import {h,provide,ref} from 'vue'
import {createRouter,createWebHashHistory,RouterView} from 'vue-router'
import {createApp} from '../shared/app.js'
import {initTheme,setTheme} from '../shared/composables/theme.js'
import Detail from '../deploy/pages/DetailView.vue'
import Run from '../deploy/pages/RunView.vue'
import Orchestration from '../deploy/pages/OrchestrationView.vue'
initTheme();const qaMode=new URLSearchParams(location.search).get('theme');if(qaMode)setTheme({mode:qaMode})
const api=async(url,opt)=>{const r=await fetch('/'+url.replace(/^\//,''),opt),body=await r.json();if(!r.ok)throw Error(body.error||'Fixture read failed');return body}
const router=createRouter({history:createWebHashHistory(),routes:[{path:'/',redirect:'/orchestration?run=9'},{path:'/service/:name/:tab?',alias:'/svc/:name/:tab?',component:Detail},{path:'/run/:id',component:Run},{path:'/orchestration',component:Orchestration}]})
const app=createApp({setup(){provide('api',api);provide('globalEnvironment',ref('prod'));return()=>h('main',{class:'mx-auto w-full max-w-[1440px] min-w-0 p-6'},h(RouterView))}})
app.use(router);app.mount('#app')
