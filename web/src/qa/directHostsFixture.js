import {h,provide,ref} from 'vue'
import {createRouter,createWebHashHistory,RouterView} from 'vue-router'
import {createApp} from '../shared/app.js'
import {initTheme,setTheme} from '../shared/composables/theme.js'
import Detail from '../deploy/pages/DetailView.vue'
initTheme();const params=new URLSearchParams(location.search);if(params.get('theme'))setTheme({mode:params.get('theme')})
const api=async(url,opt)=>{const r=await fetch('/'+url.replace(/^\//,''),opt),v=await r.json();if(!r.ok)throw Error(v.error||'Fixture failed');return v}
const router=createRouter({history:createWebHashHistory(),routes:[{path:'/',redirect:'/svc/orders/environment'},{path:'/svc/:name/:tab?',component:Detail}]})
const app=createApp({setup(){provide('api',api);provide('globalEnvironment',ref('test'));return()=>h('main',{class:'min-w-0 mx-auto max-w-[1480px] p-6'},h(RouterView))}})
app.use(router);app.mount('#app')
