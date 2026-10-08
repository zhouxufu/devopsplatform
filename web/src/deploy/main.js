import { createApp } from 'vue'
import { createRouter, createWebHashHistory } from 'vue-router'
import App from './App.vue'
import ServicesView from './pages/ServicesView.vue'
import DetailView from './pages/DetailView.vue'
import RunView from './pages/RunView.vue'
import PluginsView from './pages/PluginsView.vue'
import ChangesView from './pages/ChangesView.vue'
import OrchestrationView from './pages/OrchestrationView.vue'
import '../shared/theme.css'
import { initTheme } from '../shared/composables/theme.js'
initTheme()

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
