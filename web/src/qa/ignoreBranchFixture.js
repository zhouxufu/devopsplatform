// Disposable local browser fixture; no real application actions.
import {h} from 'vue'
import {createRouter,createWebHashHistory} from 'vue-router'
import {createApp} from '../shared/app.js'
import {initTheme} from '../shared/composables/theme.js'
import Table from '../deploy/components/BuildRecordsTable.vue'
initTheme()
const app=createApp({render:()=>h('main',{class:'w-full min-w-0 p-6'},h(Table,{service:'orders',kind:'artifacts',me:{username:'alice',role:'admin'}}))})
app.use(createRouter({history:createWebHashHistory(),routes:[{path:'/',component:{render:()=>null}}]}))
app.mount('#app')
