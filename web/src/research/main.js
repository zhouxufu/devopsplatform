import { installPlatformAuthGuard } from '../shared/platformAuth.js'
import {createApp} from '../shared/app.js'
import { createRouter, createWebHashHistory } from 'vue-router'
import App from './App.vue'
import Inbox from './pages/Inbox.vue'
import WorkItems from './pages/WorkItems.vue'
import WorkDetail from './pages/WorkDetail.vue'
import Settings from './pages/Settings.vue'
import Versions from '../deploy/pages/ChangesView.vue'

import { initTheme } from '../shared/composables/theme.js'
initTheme()
installPlatformAuthGuard()
const router=createRouter({history:createWebHashHistory(),routes:[{path:'/',component:Inbox},{path:'/work',component:WorkItems},{path:'/work/:id',component:WorkDetail},{path:'/versions',component:Versions,props:{scope:'versions'}},{path:'/settings',component:Settings}]})
createApp(App).use(router).mount('#app')
