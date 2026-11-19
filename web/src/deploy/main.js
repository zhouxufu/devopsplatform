import { installPlatformAuthGuard } from '../shared/platformAuth.js'
import {createApp} from '../shared/app.js'
import { createRouter, createWebHashHistory } from 'vue-router'
import App from './App.vue'
import ServicesView from './pages/ServicesView.vue'
import DetailView from './pages/DetailView.vue'
import RunView from './pages/RunView.vue'
import PluginsView from './pages/PluginsView.vue'
import ChangesView from './pages/ChangesView.vue'
import OrchestrationView from './pages/OrchestrationView.vue'

import { initTheme } from '../shared/composables/theme.js'
initTheme()
installPlatformAuthGuard()

const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    { path: '/', component: ServicesView },
    { path: '/plugins', component: PluginsView },
    { path: '/changes', component: ChangesView },
    { path: '/orchestration', component: OrchestrationView },
    { path: '/svc/:name/:tab?', component: DetailView },
    { path: '/run/:id', component: RunView },
  ],
})
createApp(App).use(router).mount('#app')
