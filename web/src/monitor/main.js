import { installPlatformAuthGuard } from '../shared/platformAuth.js'
import {createApp} from '../shared/app.js'
import { createRouter, createWebHashHistory } from 'vue-router'
import App from './App.vue'
import AlertsView from './pages/AlertsView.vue'
import OverviewView from './pages/OverviewView.vue'
import MachinesView from './pages/MachinesView.vue'
import ApiView from './pages/ApiView.vue'
import JvmView from './pages/JvmView.vue'
import TraceView from './pages/TraceView.vue'
import RulesView from './pages/RulesView.vue'
import ArthasView from './pages/ArthasView.vue'

import { initTheme } from '../shared/composables/theme.js'
initTheme()
installPlatformAuthGuard()

const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    { path: '/', component: OverviewView },
    { path: '/alerts', component: AlertsView },
    { path: '/machines', component: MachinesView },
    { path: '/api', component: ApiView },
    { path: '/jvm', component: JvmView },
    { path: '/arthas', component: ArthasView },
    { path: '/trace', component: TraceView },
    { path: '/rules', component: RulesView },
  ],
})
createApp(App).use(router).mount('#app')
