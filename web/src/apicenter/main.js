import {createApp} from '../shared/app.js'
import {createRouter,createWebHashHistory} from 'vue-router'
import {installPlatformAuthGuard} from '../shared/platformAuth.js'
import {initTheme} from '../shared/composables/theme.js'

import App from './Entry.vue'
initTheme();installPlatformAuthGuard()
const router=createRouter({history:createWebHashHistory(),routes:[{path:'/:pathMatch(.*)*',component:App}]})
const app=createApp(App).use(router)
router.isReady().then(()=>app.mount('#app'))
