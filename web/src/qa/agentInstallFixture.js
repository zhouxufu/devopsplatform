import {h,provide} from 'vue'
import {createMemoryHistory,createRouter,RouterView} from 'vue-router'
import {createApp} from '../shared/app.js'
import {initTheme,setTheme} from '../shared/composables/theme.js'
import StatusView from '../logs/pages/StatusView.vue'
initTheme();setTheme({mode:new URLSearchParams(location.search).get('theme')||'light'})
const api=async(url,opt)=>{const r=await fetch('/'+url.replace(/^\//,''),opt),v=await r.json();if(!r.ok)throw Error(v.error||'Fixture failed');return v}
const router=createRouter({history:createMemoryHistory(),routes:[{path:'/',component:StatusView}]})
const app=createApp({setup(){provide('api',api);return()=>h('main',{class:'min-w-0 p-6'},h(RouterView))}})
app.use(router);app.mount('#app')
